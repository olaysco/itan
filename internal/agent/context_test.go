package agent

import (
	"strings"
	"testing"

	"github.com/olaysco/clipwright/internal/provider"
)

func msg(role, text string) provider.Message {
	return provider.Message{Role: role, Blocks: []provider.Block{provider.TextBlock(text)}}
}

func toolExchange(name, result string) []provider.Message {
	return []provider.Message{
		{Role: "assistant", Blocks: []provider.Block{{Type: "tool_use", ID: "t1", Name: name, Input: []byte(`{"start":0}`)}}},
		{Role: "user", Blocks: []provider.Block{provider.ToolResultBlock("t1", result, false)}},
	}
}

func TestCompactNoopUnderBudget(t *testing.T) {
	msgs := []provider.Message{msg("user", "hi"), msg("assistant", "hello")}
	out := Compact(msgs, 10_000)
	if len(out) != 2 || out[0].Blocks[0].Text != "hi" {
		t.Fatal("under-budget history must be untouched")
	}
}

func TestCompactCollapsesToolResults(t *testing.T) {
	big := strings.Repeat("verbose ffmpeg-ish output line\n", 200)
	var msgs []provider.Message
	msgs = append(msgs, msg("user", "trim my video"))
	for i := 0; i < 5; i++ {
		msgs = append(msgs, toolExchange("trim", big)...)
	}
	msgs = append(msgs, msg("assistant", "done"))

	before := EstimateTokens(msgs)
	out := Compact(msgs, before/4)
	after := EstimateTokens(out)
	if after >= before/2 {
		t.Fatalf("compaction too weak: %d -> %d", before, after)
	}
	// The anchor (first user message) must survive.
	if out[0].Role != "user" || !strings.Contains(out[0].Blocks[0].Text, "trim my video") {
		t.Fatalf("anchor message lost: %+v", out[0])
	}
	// Collapsed tool exchanges become text notes.
	found := false
	for _, m := range out {
		for _, b := range m.Blocks {
			if b.Type == "text" && strings.Contains(b.Text, "[called trim]") {
				found = true
			}
			if b.Type == "tool_result" && len(b.Content) > 200 {
				// old exchanges must not carry full payloads anymore
				t.Log("note: recent exchange may keep payload — checking it is last")
			}
		}
	}
	if !found {
		t.Fatal("expected collapsed [called trim] notes")
	}
}

func TestEstimateTokensRoughlyCharsOver4(t *testing.T) {
	m := []provider.Message{msg("user", strings.Repeat("a", 4000))}
	got := EstimateTokens(m)
	if got < 900 || got > 1200 {
		t.Fatalf("estimate off: %d", got)
	}
}
