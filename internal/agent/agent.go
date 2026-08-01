// Package agent is the harness: the loop that turns a natural-language
// request into a sequence of tool calls against the working video.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/olaysco/heydit/internal/config"
	"github.com/olaysco/heydit/internal/media"
	"github.com/olaysco/heydit/internal/provider"
	"github.com/olaysco/heydit/internal/skills"
	"github.com/olaysco/heydit/internal/tools"
	"github.com/olaysco/heydit/internal/voice"
)

// Event streams progress to the CLI/UI while a request runs.
type Event struct {
	Kind     string        `json:"kind"` // "text" | "tool_start" | "tool_end"
	Text     string        `json:"text,omitempty"`
	Tool     string        `json:"tool,omitempty"`
	Args     string        `json:"args,omitempty"`
	Summary  string        `json:"summary,omitempty"`
	Output   string        `json:"output,omitempty"`
	Err      string        `json:"err,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

type Agent struct {
	Cfg      *config.Config
	Provider provider.Provider
	Project  *media.Project
	Registry *tools.Registry
	Skills   *skills.Set
	TTS      voice.TTS
	STT      voice.STT

	// History persists across Run calls within a session.
	History []provider.Message

	// Session token accounting (for /cost-style reporting).
	InputTokens, OutputTokens int
}

func New(cfg *config.Config, p provider.Provider, proj *media.Project, sk *skills.Set) *Agent {
	return &Agent{
		Cfg:      cfg,
		Provider: p,
		Project:  proj,
		Registry: tools.NewRegistry(),
		Skills:   sk,
		TTS:      voice.TTSFromConfig(cfg),
		STT:      voice.STTFromConfig(cfg),
	}
}

const identity = `You are Heydit, an agentic video editor. You edit real video files by calling tools; you never merely describe hypothetical edits.

Rules:
- Work on the CURRENT working video; every mutating tool's output becomes the new CURRENT automatically. Chain tools for multi-part requests.
- Trust the project-state ledger below over anything remembered from earlier conversation.
- Metadata in the ledger is current — call probe only for files not listed there.
- To fix spoken grammar or wording: transcribe → correct the text yourself in-thought → tts the corrected text → replace_audio.
- Prefer dedicated tools; use render (custom ffmpeg filters) only when nothing else fits, with a clear note.
- If a request is impossible with the available tools, say so plainly and suggest the closest achievable edit.
- Be terse. One short paragraph at the end describing what changed — no play-by-play, no markdown headers.`

// systemPrompt assembles identity + live ledger + skill index (+ bodies of
// skills triggered by this message).
func (a *Agent) systemPrompt(ctx context.Context, userMsg string) string {
	var b strings.Builder
	b.WriteString(identity)
	b.WriteString("\n\n")
	b.WriteString(a.Project.Ledger(ctx))
	if idx := a.Skills.Index(); idx != "" {
		b.WriteString("\n")
		b.WriteString(idx)
	}
	for _, sk := range a.Skills.Match(userMsg) {
		b.WriteString("\n## Active skill: " + sk.Name + "\n")
		b.WriteString(sk.Body)
		b.WriteString("\n")
	}
	return b.String()
}

func (a *Agent) toolDefs() []provider.ToolDef {
	all := a.Registry.All()
	defs := make([]provider.ToolDef, len(all))
	for i, t := range all {
		defs[i] = provider.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	return defs
}

// Run processes one user request to completion and returns the final reply.
func (a *Agent) Run(ctx context.Context, userMsg string, onEvent func(Event)) (string, error) {
	emit := func(e Event) {
		if onEvent != nil {
			onEvent(e)
		}
	}

	budget := int(float64(a.Cfg.Context.MaxTokens) * a.Cfg.Context.CompactAt)
	a.History = Compact(append(a.History, provider.UserText(userMsg)), budget)

	tctx := &tools.Ctx{Context: ctx, Project: a.Project, Config: a.Cfg, TTS: a.TTS, STT: a.STT}
	var finalText strings.Builder

	for turn := 0; turn < a.Cfg.Context.MaxTurns; turn++ {
		resp, err := a.Provider.Complete(ctx, provider.Request{
			Model:     a.Cfg.Model.ID,
			System:    a.systemPrompt(ctx, userMsg),
			Messages:  a.History,
			Tools:     a.toolDefs(),
			MaxTokens: 2048,
		})
		if err != nil {
			return "", err
		}
		a.InputTokens += resp.InputTokens
		a.OutputTokens += resp.OutputTokens

		a.History = append(a.History, provider.Message{Role: "assistant", Blocks: resp.Blocks})
		if text := resp.Text(); text != "" {
			finalText.Reset() // only the last assistant text is the reply
			finalText.WriteString(text)
			emit(Event{Kind: "text", Text: text})
		}

		uses := resp.ToolUses()
		if resp.StopReason != "tool_use" || len(uses) == 0 {
			break
		}

		var results []provider.Block
		for _, use := range uses {
			emit(Event{Kind: "tool_start", Tool: use.Name, Args: string(use.Input)})
			started := time.Now()
			res := a.Registry.Execute(tctx, use.Name, use.Input)
			ev := Event{
				Kind: "tool_end", Tool: use.Name,
				Summary: res.Summary, Output: res.Output,
				Duration: time.Since(started).Round(10 * time.Millisecond),
			}
			if res.Err != nil {
				ev.Err = res.Err.Error()
			}
			emit(ev)
			results = append(results, provider.ToolResultBlock(
				use.ID,
				res.Compact(a.Cfg.Context.ToolResultMaxChars),
				res.Err != nil,
			))
		}
		a.History = append(a.History, provider.Message{Role: "user", Blocks: results})
		a.History = Compact(a.History, budget)
	}

	reply := strings.TrimSpace(finalText.String())
	if reply == "" {
		reply = "Done."
	}
	return reply, nil
}

// CostLine reports session token usage for /cost.
func (a *Agent) CostLine() string {
	return fmt.Sprintf("session tokens — in: %d, out: %d (model %s/%s)",
		a.InputTokens, a.OutputTokens, a.Cfg.Model.Provider, a.Cfg.Model.ID)
}
