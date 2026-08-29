package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/provider"
)

// resultsSentBack returns every tool_result the model was handed, across all
// requests — that is the only channel a tool-activated playbook can arrive on.
func resultsSentBack(fake *scripted) []string {
	var out []string
	for _, req := range fake.requests {
		for _, m := range req.Messages {
			for _, b := range m.Blocks {
				if b.Type == "tool_result" {
					out = append(out, b.Content)
				}
			}
		}
	}
	return out
}

// "Make a short video about our company" names no craft term, so word triggers
// miss the motion-design playbook entirely and the model would design with no
// art direction. Calling storyboard is the signal that it is designing.
func TestStoryboardLoadsTheCraftPlaybook(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "storyboard",
			`{"scenes":[{"n":1,"intent":"open","say":"Here is the thing.","duration":3}]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("Storyboarded.")}, StopReason: "end_turn"},
	}}
	a, _ := newTestAgent(t, fake)

	if _, err := a.Run(context.Background(), "make a short video about our company", func(Event) {}); err != nil {
		t.Fatal(err)
	}

	var got string
	for _, r := range resultsSentBack(fake) {
		if strings.Contains(r, `skill-playbook name="motion-design"`) {
			got = r
		}
	}
	if got == "" {
		t.Fatal("storyboard did not deliver the motion-design playbook to the model")
	}
	// Not just the tag — the body the design depends on has to be in there.
	for _, want := range []string{"flat is a choice", "style brief", "ground", "atmosphere"} {
		if !strings.Contains(strings.ToLower(got), want) {
			t.Fatalf("playbook body missing %q", want)
		}
	}
}

// The body is large. Paying for it once per session is the point of
// progressive disclosure; paying per scene would be a context leak.
func TestCraftPlaybookLoadsOnlyOnce(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "storyboard",
			`{"scenes":[{"n":1,"intent":"open","say":"One.","duration":3}]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{toolUse("t2", "storyboard",
			`{"scenes":[{"n":1,"intent":"open","say":"Two.","duration":3}]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("Done.")}, StopReason: "end_turn"},
	}}
	a, _ := newTestAgent(t, fake)

	if _, err := a.Run(context.Background(), "make a short video about our company", func(Event) {}); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	n := 0
	for _, r := range resultsSentBack(fake) {
		if strings.Contains(r, `skill-playbook name="motion-design"`) && !seen[r] {
			seen[r] = true
			n++
		}
	}
	if n != 1 {
		t.Fatalf("playbook delivered in %d distinct tool results, want 1", n)
	}
}

// An edit that has nothing to do with design must not drag the playbook in.
func TestPlainEditDoesNotLoadTheCraftPlaybook(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "trim", `{"start":0,"end":1}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("Trimmed.")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Run(context.Background(), "keep only the first second", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	for _, r := range resultsSentBack(fake) {
		if strings.Contains(r, "skill-playbook") {
			t.Fatal("a trim pulled a design playbook into context")
		}
	}
}
