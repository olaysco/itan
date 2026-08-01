// Package agent is the harness: the loop that turns a natural-language
// request into a sequence of tool calls against the working video.
//
// Hardening (patterns adopted from Claude Code and opencode):
//   - static system prompt + delta ledger reminders (prompt-cache hygiene)
//   - permission gate with plan mode and a bypass-immune safety tier
//   - doom-loop detection (identical repeated tool calls are refused)
//   - retry-with-backoff surfaced as UI events, honoring Retry-After
//   - parallel execution of concurrency-safe tool batches, ordered results
//   - oversized tool results spill to disk, readable back via read_text
//   - session persistence for resume, and model-based /compact
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/permission"
	"github.com/olaysco/itan/internal/provider"
	"github.com/olaysco/itan/internal/skills"
	"github.com/olaysco/itan/internal/tools"
	"github.com/olaysco/itan/internal/voice"
)

// Event streams progress to the CLI/UI while a request runs.
type Event struct {
	Kind     string        `json:"kind"` // "text" | "text_delta" | "tool_start" | "tool_end" | "retry" | "permission"
	Text     string        `json:"text,omitempty"`
	Tool     string        `json:"tool,omitempty"`
	Args     string        `json:"args,omitempty"`
	Summary  string        `json:"summary,omitempty"`
	Output   string        `json:"output,omitempty"`
	Err      string        `json:"err,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

// StopReason is the typed terminal state of a Run.
type StopReason string

const (
	StopEndTurn  StopReason = "end_turn"
	StopMaxTurns StopReason = "max_turns"
	StopError    StopReason = "error"
)

type Agent struct {
	Cfg      *config.Config
	Provider provider.Provider
	Project  *media.Project
	Registry *tools.Registry
	Skills   *skills.Set
	TTS      voice.TTS
	STT      voice.STT
	Gate     *permission.Gate

	// History persists across Run calls within a session.
	History []provider.Message

	// Session token accounting (for /cost-style reporting).
	InputTokens, OutputTokens int

	system       string          // static system prompt, built once
	lastLedger   string          // last <project-state> sent to the model
	activeSkills map[string]bool // skill bodies already injected this session
	spillSeq     int
	checkpoints  []Checkpoint // turn snapshots for /revert
}

// Checkpoint captures project + conversation state at the start of a user
// request, enabling multi-turn revert. Renders are immutable numbered files,
// so restoring the ledger (not the media bytes) is sufficient to time-travel.
type Checkpoint struct {
	Label      string         `json:"label"`
	At         time.Time      `json:"at"`
	Assets     []media.Asset  `json:"assets"`
	Ops        []media.EditOp `json:"ops"`
	Current    string         `json:"current"`
	HistoryLen int            `json:"history_len"`
}

const maxCheckpoints = 50

func New(cfg *config.Config, p provider.Provider, proj *media.Project, sk *skills.Set) *Agent {
	return &Agent{
		Cfg:          cfg,
		Provider:     p,
		Project:      proj,
		Registry:     tools.NewRegistry(),
		Skills:       sk,
		TTS:          voice.TTSFromConfig(cfg),
		STT:          voice.STTFromConfig(cfg),
		Gate:         permission.NewGate(permission.Mode(cfg.Mode), cfg.Permissions, nil),
		system:       buildSystemPrompt(proj.Dir, sk),
		activeSkills: map[string]bool{},
	}
}

// ActiveSkills lists skills whose playbooks have been injected this session.
func (a *Agent) ActiveSkills() []string {
	names := make([]string, 0, len(a.activeSkills))
	for n := range a.activeSkills {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AdoptState carries conversation state over from a previous agent instance
// (model/config switches rebuild the agent but continue the session).
func (a *Agent) AdoptState(old *Agent) {
	if old == nil {
		return
	}
	a.History = old.History
	a.InputTokens, a.OutputTokens = old.InputTokens, old.OutputTokens
	a.checkpoints = old.checkpoints
	a.Gate.SetMode(old.Gate.Mode())
}

func (a *Agent) toolDefs() []provider.ToolDef {
	all := a.Registry.All()
	defs := make([]provider.ToolDef, len(all))
	for i, t := range all {
		defs[i] = provider.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	return defs
}

// composeUserMessage attaches volatile context (ledger delta, newly activated
// skill playbooks) to the outgoing user text as reminder blocks.
func (a *Agent) composeUserMessage(ctx context.Context, userMsg string) provider.Message {
	text := userMsg
	for _, sk := range a.Skills.Match(userMsg) {
		if !a.activeSkills[sk.Name] {
			a.activeSkills[sk.Name] = true
			text += reminder("skill-playbook name=\""+sk.Name+"\"", sk.Body)
		}
	}
	if a.Gate.Mode() == permission.ModePlan {
		text += reminder("plan-mode", "Plan mode is active. Do NOT call mutating tools; inspect with read-only tools if needed and reply with a concise numbered plan of the edits you propose.")
	}
	text += a.ledgerDelta(ctx)
	return provider.UserText(text)
}

// ledgerDelta returns a <project-state> reminder only when the ledger changed
// since it was last sent — the system prompt stays byte-stable and the model
// still always has fresh state.
func (a *Agent) ledgerDelta(ctx context.Context) string {
	ledger := a.Project.Ledger(ctx)
	if ledger == a.lastLedger {
		return ""
	}
	a.lastLedger = ledger
	return reminder("project-state", ledger)
}

// Run processes one user request to completion and returns the final reply.
func (a *Agent) Run(ctx context.Context, userMsg string, onEvent func(Event)) (string, error) {
	reply, _, err := a.RunWithReason(ctx, userMsg, onEvent)
	return reply, err
}

func (a *Agent) RunWithReason(ctx context.Context, userMsg string, onEvent func(Event)) (string, StopReason, error) {
	emit := func(e Event) {
		if onEvent != nil {
			onEvent(e)
		}
	}

	// Retries are visible state, not hidden sleeps.
	prov := provider.WithRetry(a.Provider, func(ri provider.RetryInfo) {
		emit(Event{
			Kind: "retry",
			Text: fmt.Sprintf("attempt %d/%d failed, retrying in %s", ri.Attempt, ri.Max, ri.Wait.Round(time.Second)),
			Err:  ri.Err.Error(),
		})
	})

	a.pushCheckpoint(userMsg)
	budget := int(float64(a.Cfg.Context.MaxTokens) * a.Cfg.Context.CompactAt)
	a.History = Compact(append(a.History, a.composeUserMessage(ctx, userMsg)), budget)

	tctx := &tools.Ctx{Context: ctx, Project: a.Project, Config: a.Cfg, TTS: a.TTS, STT: a.STT}
	var finalText strings.Builder
	doom := doomDetector{}

	for turn := 0; turn < a.Cfg.Context.MaxTurns; turn++ {
		streamed := false
		resp, err := prov.CompleteStream(ctx, provider.Request{
			Model:     a.Cfg.Model.ID,
			System:    a.system,
			Messages:  a.History,
			Tools:     a.toolDefs(),
			MaxTokens: 2048,
		}, func(d provider.Delta) {
			streamed = true
			emit(Event{Kind: "text_delta", Text: d.Text})
		})
		if err != nil {
			return "", StopError, err
		}
		a.InputTokens += resp.InputTokens
		a.OutputTokens += resp.OutputTokens

		a.History = append(a.History, provider.Message{Role: "assistant", Blocks: resp.Blocks})
		if text := resp.Text(); text != "" {
			finalText.Reset() // only the last assistant text is the reply
			finalText.WriteString(text)
			if !streamed { // deltas already carried this text live
				emit(Event{Kind: "text", Text: text})
			}
		}

		uses := resp.ToolUses()
		if resp.StopReason != "tool_use" || len(uses) == 0 {
			return a.finishReply(finalText.String()), StopEndTurn, nil
		}

		results := a.executeBatch(tctx, uses, &doom, emit)

		// Attach a ledger delta to the tool-result message when the edits
		// changed project state, so the model never works from stale facts.
		blocks := results
		if delta := a.ledgerDelta(ctx); delta != "" {
			blocks = append(blocks, provider.TextBlock(delta))
		}
		a.History = append(a.History, provider.Message{Role: "user", Blocks: blocks})
		a.History = Compact(a.History, budget)
	}

	// Out of turns: tell the model-facing transcript AND the user honestly,
	// instead of silently truncating the work.
	notice := "Reached the per-request tool-call limit (context.max_turns). Work may be incomplete; ask me to continue to keep going."
	a.History = append(a.History, provider.Message{Role: "assistant", Blocks: []provider.Block{provider.TextBlock(notice)}})
	reply := strings.TrimSpace(finalText.String())
	if reply == "" {
		reply = notice
	} else {
		reply += "\n\n" + notice
	}
	return reply, StopMaxTurns, nil
}

func (a *Agent) finishReply(text string) string {
	reply := strings.TrimSpace(text)
	if reply == "" {
		reply = "Done."
	}
	return reply
}

// --- tool execution --------------------------------------------------------

// doomDetector refuses the third consecutive byte-identical tool call — a
// stuck model burning turns and renders on the same failing thing.
type doomDetector struct {
	lastKey string
	repeats int
}

func (d *doomDetector) check(name string, input []byte) bool {
	key := name + "\x00" + string(input)
	if key == d.lastKey {
		d.repeats++
	} else {
		d.lastKey, d.repeats = key, 0
	}
	return d.repeats >= 2
}

// executeBatch runs one response's tool calls: consecutive concurrency-safe
// tools run in parallel goroutines, everything else runs serially. Results
// are always emitted in call order so the transcript stays deterministic.
func (a *Agent) executeBatch(tctx *tools.Ctx, uses []provider.Block, doom *doomDetector, emit func(Event)) []provider.Block {
	results := make([]provider.Block, len(uses))

	i := 0
	for i < len(uses) {
		// Extend a run of parallel-safe calls.
		j := i
		for j < len(uses) && a.isParallelSafe(uses[j].Name) {
			j++
		}
		if j > i+1 {
			var wg sync.WaitGroup
			for k := i; k < j; k++ {
				k := k
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[k] = a.executeOne(tctx, uses[k], doom, emit)
				}()
			}
			wg.Wait()
			i = j
			continue
		}
		results[i] = a.executeOne(tctx, uses[i], doom, emit)
		i++
	}
	return results
}

func (a *Agent) isParallelSafe(name string) bool {
	t, ok := a.Registry.Get(name)
	return ok && t.ConcurrencySafe
}

func (a *Agent) executeOne(tctx *tools.Ctx, use provider.Block, doom *doomDetector, emit func(Event)) provider.Block {
	emit(Event{Kind: "tool_start", Tool: use.Name, Args: string(use.Input)})

	if doom.check(use.Name, use.Input) {
		msg := "refused: this exact tool call was already made twice in a row with identical input. Change the arguments, use a different tool, or explain the blocker to the user."
		emit(Event{Kind: "tool_end", Tool: use.Name, Err: msg})
		return provider.ToolResultBlock(use.ID, "ERROR: "+msg, true)
	}

	if dec, req := a.checkPermission(use); dec.Action != permission.Allow {
		emit(Event{Kind: "permission", Tool: use.Name, Summary: "denied", Err: dec.Feedback})
		_ = req
		return provider.ToolResultBlock(use.ID, "DENIED: "+dec.Feedback, true)
	}

	started := time.Now()
	res := a.Registry.Execute(tctx, use.Name, use.Input)
	compact := res.Compact(a.Cfg.Context.ToolResultMaxChars)
	if spill := a.spillIfTruncated(use.Name, res); spill != "" {
		compact += "\n(full output spilled to " + spill + " — use read_text to view)"
	}

	ev := Event{
		Kind: "tool_end", Tool: use.Name,
		Summary: res.Summary, Output: res.Output,
		Duration: time.Since(started).Round(10 * time.Millisecond),
	}
	if res.Err != nil {
		ev.Err = res.Err.Error()
	}
	emit(ev)
	return provider.ToolResultBlock(use.ID, compact, res.Err != nil)
}

// checkPermission decodes the call, computes any safety-tier reason, and asks
// the gate.
func (a *Agent) checkPermission(use provider.Block) (permission.Decision, permission.Request) {
	args := map[string]any{}
	_ = json.Unmarshal(use.Input, &args)
	t, ok := a.Registry.Get(use.Name)
	req := permission.Request{
		Tool:     use.Name,
		Args:     args,
		Mutating: ok && (t.Mutating || use.Name == "export"),
		Safety:   a.safetyReason(use.Name, args),
	}
	return a.Gate.Check(req), req
}

// safetyReason flags the bypass-immune tier: writes that clobber files
// outside Itan's managed output directory.
func (a *Agent) safetyReason(tool string, args map[string]any) string {
	if tool != "export" {
		return ""
	}
	dest, _ := args["path"].(string)
	if dest == "" {
		dest = filepath.Join(a.Project.Dir, "itan-export.mp4")
	} else if !filepath.IsAbs(dest) {
		dest = filepath.Join(a.Project.Dir, dest)
	}
	if _, err := os.Stat(dest); err == nil {
		return "overwrites existing file " + dest
	}
	return ""
}

// spillIfTruncated writes the untruncated result to disk when the compact
// form lost data, so nothing is destroyed by the context cap.
func (a *Agent) spillIfTruncated(tool string, res tools.Result) string {
	if res.Err != nil {
		return ""
	}
	full := res.Full()
	if len(full) <= a.Cfg.Context.ToolResultMaxChars {
		return ""
	}
	dir := filepath.Join(a.Project.OutDir(), "tool-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	a.spillSeq++
	path := filepath.Join(dir, fmt.Sprintf("%03d-%s.txt", a.spillSeq, tool))
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return ""
	}
	return path
}

// --- turn snapshots / revert ----------------------------------------------

func (a *Agent) pushCheckpoint(label string) {
	if len(label) > 80 {
		label = label[:80] + "…"
	}
	cp := Checkpoint{
		Label:      label,
		At:         time.Now().UTC(),
		Assets:     append([]media.Asset(nil), a.Project.Assets...),
		Ops:        append([]media.EditOp(nil), a.Project.Ops...),
		Current:    a.Project.Current,
		HistoryLen: len(a.History),
	}
	a.checkpoints = append(a.checkpoints, cp)
	if len(a.checkpoints) > maxCheckpoints {
		a.checkpoints = a.checkpoints[len(a.checkpoints)-maxCheckpoints:]
	}
}

// Checkpoints lists turn snapshots, oldest first.
func (a *Agent) Checkpoints() []Checkpoint {
	return append([]Checkpoint(nil), a.checkpoints...)
}

// Revert restores project state and conversation to n user requests ago.
// Rendered files stay on disk (they're numbered and immutable), so this is
// instant and itself reversible by re-running the requests.
func (a *Agent) Revert(n int) (*Checkpoint, error) {
	if n < 1 {
		n = 1
	}
	if len(a.checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints to revert to")
	}
	if n > len(a.checkpoints) {
		n = len(a.checkpoints)
	}
	cp := a.checkpoints[len(a.checkpoints)-n]
	a.checkpoints = a.checkpoints[:len(a.checkpoints)-n]

	a.Project.Assets = append([]media.Asset(nil), cp.Assets...)
	a.Project.Ops = append([]media.EditOp(nil), cp.Ops...)
	a.Project.Current = cp.Current
	if err := a.Project.Save(); err != nil {
		return nil, err
	}
	if cp.HistoryLen <= len(a.History) {
		a.History = a.History[:cp.HistoryLen]
	}
	a.lastLedger = "" // force a fresh <project-state> next turn
	return &cp, nil
}

// --- session persistence ---------------------------------------------------

type sessionState struct {
	History      []provider.Message `json:"history"`
	Checkpoints  []Checkpoint       `json:"checkpoints,omitempty"`
	InputTokens  int                `json:"input_tokens"`
	OutputTokens int                `json:"output_tokens"`
	SavedAt      time.Time          `json:"saved_at"`
}

func (a *Agent) sessionPath() string {
	return filepath.Join(a.Project.Dir, ".itan", "session.json")
}

// SaveSession persists conversation history so `itan --continue` can resume
// after a restart or crash.
func (a *Agent) SaveSession() error {
	if err := os.MkdirAll(filepath.Dir(a.sessionPath()), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(sessionState{
		History: a.History, Checkpoints: a.checkpoints,
		InputTokens: a.InputTokens, OutputTokens: a.OutputTokens,
		SavedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(a.sessionPath(), data, 0o644)
}

// LoadSession restores a saved conversation. Returns false when none exists.
func (a *Agent) LoadSession() (bool, error) {
	data, err := os.ReadFile(a.sessionPath())
	if err != nil {
		return false, nil
	}
	var st sessionState
	if err := json.Unmarshal(data, &st); err != nil {
		return false, fmt.Errorf("corrupt session file %s: %w", a.sessionPath(), err)
	}
	a.History = st.History
	a.checkpoints = st.Checkpoints
	a.InputTokens, a.OutputTokens = st.InputTokens, st.OutputTokens
	a.lastLedger = "" // force a fresh <project-state> on the next turn
	return true, nil
}

// --- model-based compaction (/compact) -------------------------------------

// compactPrompt is the video-editing adaptation of Claude Code's structured
// summary schema. Verbatim quotes guard against drift in creative intent.
const compactPrompt = `Summarize this video-editing session for context compaction. Think through the transcript first, then output ONLY the summary with exactly these sections:

## Creative direction
Every instruction the user gave about the video, with short verbatim quotes for anything stylistic ("make it punchier") so intent cannot drift.
## Timeline state
What the working video is now: dimensions, duration, the sequence of edits applied and why.
## Assets
Source files and important generated files (transcripts, tts audio, spilled outputs) with their paths.
## Failures and fixes
Renders or tools that failed, why, and what worked instead.
## Pending work
What the user asked for that is not done yet, plus the agreed next step if any.`

// CompactNow replaces history with a model-written summary plus a fresh
// ledger. Used by /compact; the deterministic Compact() remains the automatic
// in-loop mechanism.
func (a *Agent) CompactNow(ctx context.Context) error {
	if len(a.History) == 0 {
		return fmt.Errorf("nothing to compact")
	}
	msgs := append([]provider.Message{}, a.History...)
	msgs = append(msgs, provider.UserText("Compact the session now as instructed."))
	resp, err := provider.WithRetry(a.Provider, nil).Complete(ctx, provider.Request{
		Model:     a.Cfg.Model.ID,
		System:    compactPrompt,
		Messages:  msgs,
		MaxTokens: 2048,
	})
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(resp.Text())
	if summary == "" {
		return fmt.Errorf("model returned an empty summary")
	}
	a.History = []provider.Message{provider.UserText(
		"[Session restored from compaction. Summary of everything before this point:]\n" + summary)}
	a.lastLedger = ""
	a.activeSkills = map[string]bool{}
	return nil
}

// CostLine reports session token usage for /cost.
func (a *Agent) CostLine() string {
	return fmt.Sprintf("session tokens — in: %d, out: %d (model %s/%s)",
		a.InputTokens, a.OutputTokens, a.Cfg.Model.Provider, a.Cfg.Model.ID)
}
