package agent

import (
	"fmt"

	"github.com/olaysco/heydit/internal/provider"
)

// EstimateTokens is a cheap, provider-agnostic token estimate (~4 chars/token
// for English + JSON). It only needs to be right within ~20% to steer
// compaction, so we avoid shipping a tokenizer per provider.
func EstimateTokens(msgs []provider.Message) int {
	chars := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			chars += len(b.Text) + len(b.Content) + len(b.Input) + len(b.Name) + 8
		}
	}
	return chars / 4
}

// Compact prunes history once it crosses the budget, deterministically and
// without an extra model call.
//
// Because the project ledger (re-injected into the system prompt every turn)
// carries the full editing state, old tool_use/tool_result exchanges are
// redundant — each pair is collapsed to a one-line note, oldest turns first,
// until the transcript fits. User/assistant text is kept (it holds intent),
// only dropping whole oldest exchanges as a last resort.
func Compact(msgs []provider.Message, budgetTokens int) []provider.Message {
	if EstimateTokens(msgs) <= budgetTokens {
		return msgs
	}

	out := append([]provider.Message(nil), msgs...)

	// Pass 1: collapse tool blocks, oldest first. Never touch the final
	// exchange — the model may still be reasoning about it.
	for i := 0; i < len(out)-2 && EstimateTokens(out) > budgetTokens; i++ {
		out[i] = collapseTools(out[i])
	}

	// Pass 2: drop whole oldest exchanges (keeping the very first user
	// message, which anchors the session goal).
	for len(out) > 3 && EstimateTokens(out) > budgetTokens {
		out = append(out[:1], out[2:]...)
	}
	return out
}

func collapseTools(m provider.Message) provider.Message {
	var blocks []provider.Block
	for _, b := range m.Blocks {
		switch b.Type {
		case "tool_use":
			blocks = append(blocks, provider.TextBlock(fmt.Sprintf("[called %s]", b.Name)))
		case "tool_result":
			note := firstLine(b.Content)
			if b.IsError {
				note = "error: " + note
			}
			blocks = append(blocks, provider.TextBlock("[tool result: "+note+"]"))
		default:
			blocks = append(blocks, b)
		}
	}
	return provider.Message{Role: m.Role, Blocks: mergeText(blocks)}
}

func mergeText(blocks []provider.Block) []provider.Block {
	var out []provider.Block
	for _, b := range blocks {
		if b.Type == "text" && len(out) > 0 && out[len(out)-1].Type == "text" {
			out[len(out)-1].Text += " " + b.Text
			continue
		}
		out = append(out, b)
	}
	return out
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
		if i > 120 {
			return s[:i] + "…"
		}
	}
	return s
}
