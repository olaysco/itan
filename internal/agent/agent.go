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
	"encoding/base64"
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

	// Optional vision route (config model.vision): turns whose latest message
	// carries frames go to this provider; every other turn goes to the main
	// model with image blocks stripped, so a text-only brain never sees them.
	vision       provider.Provider
	visionModel  string
	visionErr    error
	visionWarned bool
	// blindModel latches once the active model has refused image input, so
	// the rest of the session skips attaching frames instead of rediscovering
	// it one expensive request at a time.
	blindModel bool
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
	vp, vm, verr := provider.VisionFromConfig(cfg)
	return &Agent{
		vision:       vp,
		visionModel:  vm,
		visionErr:    verr,
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

// CheckpointNow records a revert point for work initiated outside the chat
// loop (step edits, replays) so direct manipulation is as reversible as
// conversation.
func (a *Agent) CheckpointNow(label string) {
	a.pushCheckpoint(label)
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
	var summaryMu sync.Mutex
	var lastToolSummary string
	emit := func(e Event) {
		if e.Kind == "tool_end" && e.Err == "" && e.Summary != "" {
			summaryMu.Lock()
			lastToolSummary = e.Summary
			summaryMu.Unlock()
		}
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

	visionProv := prov
	if a.vision != nil {
		visionProv = provider.WithRetry(a.vision, func(ri provider.RetryInfo) {
			emit(Event{
				Kind: "retry",
				Text: fmt.Sprintf("vision attempt %d/%d failed, retrying in %s", ri.Attempt, ri.Max, ri.Wait.Round(time.Second)),
				Err:  ri.Err.Error(),
			})
		})
	}
	if a.visionErr != nil && !a.visionWarned {
		a.visionWarned = true
		emit(Event{Kind: "text", Text: "note: " + a.visionErr.Error() + " — frames go to the main model"})
	}

	a.pushCheckpoint(userMsg)
	// Frames from previous requests are stale working data, not context:
	// drop them so image tokens are paid only within the run that looked at
	// them, not on every turn for the rest of the session.
	a.History = stripImages(a.History, false)
	budget := int(float64(a.Cfg.Context.MaxTokens) * a.Cfg.Context.CompactAt)
	a.History = Compact(append(a.History, a.composeUserMessage(ctx, userMsg)), budget)

	tctx := &tools.Ctx{Context: ctx, Project: a.Project, Config: a.Cfg, TTS: a.TTS, STT: a.STT}
	var finalText strings.Builder
	doom := doomDetector{}
	nudged := false
	maxTok := a.replyMaxTokens()

	for turn := 0; turn < a.Cfg.Context.MaxTurns; turn++ {
		callProv, model, msgs := prov, a.Cfg.Model.ID, a.History
		sendingImages := false
		switch {
		case a.vision != nil && hasImages(a.History[len(a.History)-1]):
			// Fresh frames: route to the vision model, dropping stale
			// frames from earlier turns to keep the request lean.
			callProv, model, msgs = visionProv, a.visionModel, stripImages(a.History, true)
			sendingImages = true
		case a.vision != nil:
			// Text turn: the main brain may be text-only — strip images.
			msgs = stripImages(a.History, false)
		case a.blindModel:
			// This model already told us it cannot take images. Sending them
			// again would just buy the same 404 and another lost run.
			msgs = stripImages(a.History, false)
		default:
			sendingImages = hasImages(a.History[len(a.History)-1])
		}
		streamed := false
		thinkChars, thinkEmitted := 0, 0
		resp, err := callProv.CompleteStream(ctx, provider.Request{
			Model:     model,
			System:    a.system,
			Messages:  msgs,
			Tools:     a.toolDefs(),
			MaxTokens: maxTok,
		}, func(d provider.Delta) {
			if d.Text != "" {
				streamed = true
				emit(Event{Kind: "text_delta", Text: d.Text})
			}
			// Reasoning models think silently for a long time before any
			// visible output — surface it as activity, throttled, so the UI
			// never looks dead while the model works.
			if d.Thinking != "" {
				thinkChars += len(d.Thinking)
				if thinkChars-thinkEmitted >= 400 {
					thinkEmitted = thinkChars
					emit(Event{Kind: "thinking", Text: fmt.Sprintf("~%d words", thinkChars/5)})
				}
			}
		})
		if err != nil {
			// A model that cannot see is not a reason to throw away a run
			// that has already rendered everything. Drop the pictures, say
			// so plainly, and let the turn continue on text.
			if sendingImages && provider.ImageUnsupported(err) {
				a.blindModel = true
				emit(Event{Kind: "text", Text: "note: " + model + " cannot accept images, so the frames were not shown. " +
					"Continuing without them — route frames to a model that can see with `itan config set model.vision <provider>`."})
				a.History = blindFrames(a.History)
				continue
			}
			return "", StopError, err
		}
		a.InputTokens += resp.InputTokens
		a.OutputTokens += resp.OutputTokens

		blocks := resp.Blocks
		if len(blocks) == 0 { // an empty assistant message would corrupt the transcript for some APIs
			blocks = []provider.Block{provider.TextBlock("(no output)")}
		}
		a.History = append(a.History, provider.Message{Role: "assistant", Blocks: blocks})
		if text := resp.Text(); text != "" {
			finalText.Reset() // only the last assistant text is the reply
			finalText.WriteString(text)
			if !streamed { // deltas already carried this text live
				emit(Event{Kind: "text", Text: text})
			}
		}

		uses := resp.ToolUses()
		if resp.StopReason != "tool_use" || len(uses) == 0 {
			// Some models stall: they end the turn with no reply at all mid-task.
			// Push back exactly once instead of accepting the empty turn.
			if strings.TrimSpace(finalText.String()) == "" && !nudged {
				nudged = true
				push := "(You ended your turn without any reply. If the task is unfinished, continue it now with tool calls; otherwise summarize what changed in one short paragraph.)"
				if resp.Reasoning != "" && resp.StopReason == "max_tokens" {
					// The whole budget went to chain-of-thought. Triple the
					// budget for the retry so the thinker physically cannot
					// hit the same wall, and tell it to act, not re-derive.
					maxTok *= 3
					push = "(Your entire response was internal reasoning and hit the token limit before any visible output. Do NOT re-derive your plan — act on it immediately: emit the tool calls now, with minimal further deliberation.)"
				}
				a.History = append(a.History, provider.UserText(push))
				continue
			}
			reply := a.finishReply(finalText.String(), lastToolSummary)
			if strings.TrimSpace(finalText.String()) == "" {
				// The reply was synthesized, not spoken by the model — write
				// it into history so a restored chat shows the outcome
				// instead of an unanswered question.
				a.History = append(a.History, provider.Message{Role: "assistant", Blocks: []provider.Block{provider.TextBlock(reply)}})
			}
			return reply, StopEndTurn, nil
		}

		results := a.executeBatch(tctx, uses, &doom, emit)

		// Attach a ledger delta to the tool-result message when the edits
		// changed project state, so the model never works from stale facts.
		resultBlocks := results
		if delta := a.ledgerDelta(ctx); delta != "" {
			resultBlocks = append(resultBlocks, provider.TextBlock(delta))
		}
		a.History = append(a.History, provider.Message{Role: "user", Blocks: resultBlocks})
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

// hasImages reports whether any block in m carries frame attachments.
func hasImages(m provider.Message) bool {
	for _, b := range m.Blocks {
		if len(b.Images) > 0 {
			return true
		}
	}
	return false
}

// blindFrames strips frames from history and replaces them with a statement
// the model cannot misread. It has to say the frames were NOT seen: a note
// that merely says they are gone invites the model to review from memory and
// report on pictures it never had.
func blindFrames(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		if !hasImages(m) {
			out[i] = m
			continue
		}
		blocks := make([]provider.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if len(b.Images) > 0 {
				n := len(b.Images)
				b.Images = nil
				b.Content += fmt.Sprintf("\n[%d frame(s) could NOT be shown: this model does not accept images. "+
					"You have not seen them. Do not describe or judge them — say so and continue from what the "+
					"tools reported in text.]", n)
			}
			blocks[j] = b
		}
		out[i] = provider.Message{Role: m.Role, Blocks: blocks}
	}
	return out
}

// stripImages returns msgs with frame attachments removed — from every
// message, or from all but the final one when keepLast is set. A short note
// replaces dropped frames so the model knows to call view_frames again rather
// than trust a memory of them. History itself is never mutated.
func stripImages(msgs []provider.Message, keepLast bool) []provider.Message {
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		if (keepLast && i == len(msgs)-1) || !hasImages(m) {
			out[i] = m
			continue
		}
		blocks := make([]provider.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if len(b.Images) > 0 {
				b.Images = nil
				b.Content += "\n[frames no longer attached — call view_frames again to look]"
			}
			blocks[j] = b
		}
		out[i] = provider.Message{Role: m.Role, Blocks: blocks}
	}
	return out
}

// replyMaxTokens is the per-response cap; older configs without the field
// fall back to the default rather than a broken 0.
func (a *Agent) replyMaxTokens() int {
	if n := a.Cfg.Context.ReplyMaxTokens; n > 0 {
		return n
	}
	return 8192
}

// finishReply never fabricates success: when the model ends without a final
// text (some models skip it), the reply reports what verifiably happened
// instead of an unearned "Done."
func (a *Agent) finishReply(text, lastToolSummary string) string {
	if reply := strings.TrimSpace(text); reply != "" {
		return reply
	}
	if lastToolSummary != "" {
		return "The model ended without a summary — last completed step: " + lastToolSummary
	}
	return "The model ended without a reply and no edits were made."
}

// --- tool execution --------------------------------------------------------

// doomDetector refuses the third consecutive byte-identical tool call — a
// stuck model burning turns and renders on the same failing thing — and,
// separately, blocks any tool that has failed twice with the identical error
// this run: changing the arguments won't fix a dead endpoint or a missing
// binary, and each futile retry costs a full model round-trip. The mutex
// matters: concurrency-safe tool batches call this from parallel goroutines.
type doomDetector struct {
	mu       sync.Mutex
	lastKey  string
	repeats  int
	failures map[string]map[string]int // tool → error text → count
}

func (d *doomDetector) check(name string, input []byte) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := name + "\x00" + string(input)
	if key == d.lastKey {
		d.repeats++
	} else {
		d.lastKey, d.repeats = key, 0
	}
	return d.repeats >= 2
}

func (d *doomDetector) noteFailure(name, errText string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failures == nil {
		d.failures = map[string]map[string]int{}
	}
	if d.failures[name] == nil {
		d.failures[name] = map[string]int{}
	}
	d.failures[name][errText]++
}

// blocked reports the repeated error when name has already failed twice the
// same way this run.
func (d *doomDetector) blocked(name string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for msg, n := range d.failures[name] {
		if n >= 2 {
			return msg, true
		}
	}
	return "", false
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
	if failMsg, ok := doom.blocked(use.Name); ok {
		msg := "refused: " + use.Name + " already failed twice with the same error (" + failMsg + "). This is an environment problem that more attempts will not fix — work around it, or finish and tell the user exactly what to start or fix."
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
		doom.noteFailure(use.Name, res.Err.Error())
	}
	emit(ev)
	block := provider.ToolResultBlock(use.ID, compact, res.Err != nil)
	// No point base64-encoding megabytes of PNG for a model that has already
	// told us it cannot look at them.
	if !a.blindModel {
		block.Images = loadFrames(res.Frames)
	}
	return block
}

// loadFrames reads extracted frames off disk and base64-encodes them for the
// provider adapters. Unreadable frames are skipped rather than failing the
// whole result.
func loadFrames(frames []tools.FrameRef) []provider.Image {
	var images []provider.Image
	for _, f := range frames {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		images = append(images, provider.Image{
			MediaType: f.MediaType,
			Data:      base64.StdEncoding.EncodeToString(data),
		})
	}
	return images
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
	if a.vision != nil { // compaction runs on the main model, which may be text-only
		msgs = stripImages(msgs, false)
	}
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
