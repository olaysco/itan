package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/permission"
	"github.com/olaysco/itan/internal/provider"
)

// TestDoomLoopDetection: the third byte-identical tool call is refused with
// an error result instead of being executed.
func TestDoomLoopDetection(t *testing.T) {
	same := func() *provider.Response {
		return &provider.Response{
			Blocks:     []provider.Block{toolUse("t", "probe", `{"input":"nope.mp4"}`)},
			StopReason: "tool_use",
		}
	}
	fake := &scripted{responses: []*provider.Response{
		same(), same(), same(),
		{Blocks: []provider.Block{provider.TextBlock("stuck")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	var refusals int
	_, err := a.Run(context.Background(), "probe the missing file", func(e Event) {
		if e.Kind == "tool_end" && strings.Contains(e.Err, "refused") {
			refusals++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if refusals != 1 {
		t.Fatalf("expected exactly 1 doom-loop refusal, got %d", refusals)
	}
}

// TestPlanModeBlocksEdits: in plan mode a mutating tool call is denied with
// feedback, nothing is rendered, and the plan reminder rides the user message.
func TestPlanModeBlocksEdits(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "trim", `{"start":0,"end":1}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("Here is my plan: 1. trim")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	a.Gate.SetMode(permission.ModePlan)

	if _, err := a.Run(context.Background(), "trim it down", nil); err != nil {
		t.Fatal(err)
	}
	if len(proj.Ops) != 0 {
		t.Fatalf("plan mode rendered an edit: %+v", proj.Ops)
	}
	first := fake.requests[0].Messages[0].Blocks[0].Text
	if !strings.Contains(first, "<plan-mode>") {
		t.Error("plan-mode reminder missing from user message")
	}
	// The deny must reach the model as an error tool result with guidance.
	last := fake.requests[1].Messages
	found := false
	for _, m := range last {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" && b.IsError && strings.Contains(b.Content, "plan mode") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("plan-mode denial did not reach the model as feedback")
	}
}

// TestSessionPersistence: history and token counts survive a save/load cycle.
func TestSessionPersistence(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{provider.TextBlock("hello")}, StopReason: "end_turn", InputTokens: 11, OutputTokens: 7},
	}}
	a, proj := newTestAgent(t, fake)
	if _, err := a.Run(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSession(); err != nil {
		t.Fatal(err)
	}

	b := New(a.Cfg, fake, proj, a.Skills)
	ok, err := b.LoadSession()
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if len(b.History) != len(a.History) || b.InputTokens != 11 || b.OutputTokens != 7 {
		t.Fatalf("state lost: msgs=%d tokens=%d/%d", len(b.History), b.InputTokens, b.OutputTokens)
	}
	if _, err := os.Stat(filepath.Join(proj.Dir, ".itan", "session.json")); err != nil {
		t.Fatal("session file missing")
	}
}

// TestSafetyAskOnExportOverwrite: exporting over an existing file prompts
// even in auto mode, and the asker's denial reaches the model.
func TestSafetyAskOnExportOverwrite(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "export", `{"path":"final.mp4"}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("declined")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	// Pre-existing export target → safety tier.
	if err := os.WriteFile(filepath.Join(proj.Dir, "final.mp4"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sawSafety string
	a.Gate.SetAsker(func(r permission.Request) permission.Decision {
		sawSafety = r.Safety
		return permission.Decision{Action: permission.Deny, Feedback: "keep the old export"}
	})

	if _, err := a.Run(context.Background(), "export to final.mp4", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sawSafety, "overwrites existing file") {
		t.Fatalf("safety reason missing: %q", sawSafety)
	}
	if data, _ := os.ReadFile(filepath.Join(proj.Dir, "final.mp4")); string(data) != "old" {
		t.Fatal("file was overwritten despite denial")
	}
}

// TestRevertRewindsProjectAndConversation: two edit turns, then Revert(1)
// restores the edit stack, CURRENT, and history length to before the second
// request — while the rendered files stay on disk.
func TestRevertRewindsProjectAndConversation(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "trim", `{"start":0,"end":2}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("first trim done")}, StopReason: "end_turn"},
		{Blocks: []provider.Block{toolUse("t2", "trim", `{"start":0,"end":1}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("second trim done")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Run(context.Background(), "trim to 2s", nil); err != nil {
		t.Fatal(err)
	}
	afterFirst := proj.Current
	historyAfterFirst := len(a.History)
	if _, err := a.Run(context.Background(), "trim to 1s", nil); err != nil {
		t.Fatal(err)
	}
	if len(proj.Ops) != 2 || proj.Current == afterFirst {
		t.Fatalf("setup failed: ops=%d", len(proj.Ops))
	}
	secondRender := proj.Current

	cp, err := a.Revert(1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cp.Label, "trim to 1s") {
		t.Fatalf("wrong checkpoint: %q", cp.Label)
	}
	if len(proj.Ops) != 1 || proj.Current != afterFirst {
		t.Fatalf("project not rewound: ops=%d current=%s", len(proj.Ops), proj.Current)
	}
	if len(a.History) != historyAfterFirst {
		t.Fatalf("history not rewound: %d != %d", len(a.History), historyAfterFirst)
	}
	// Renders are immutable — the reverted output must still exist on disk.
	if _, err := os.Stat(secondRender); err != nil {
		t.Fatal("revert deleted a rendered file; it must not")
	}
}

// TestMaxTurnsNotice: exhausting the turn budget produces an honest notice
// instead of silent truncation.
func TestMaxTurnsNotice(t *testing.T) {
	var responses []*provider.Response
	for i := 0; i < 10; i++ {
		responses = append(responses, &provider.Response{
			Blocks:     []provider.Block{toolUse("t", "probe", `{}`)},
			StopReason: "tool_use",
		})
	}
	fake := &scripted{responses: responses}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	a.Cfg.Context.MaxTurns = 3

	reply, reason, err := a.RunWithReason(context.Background(), "loop forever", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reason != StopMaxTurns {
		t.Fatalf("reason = %s", reason)
	}
	if !strings.Contains(reply, "tool-call limit") {
		t.Fatalf("no honest notice in reply: %q", reply)
	}
}
