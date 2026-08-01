package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/provider"
	"github.com/olaysco/itan/internal/skills"
)

// scripted is a fake Provider that replays canned responses and records the
// requests it saw — the whole harness runs against it without any network.
type scripted struct {
	responses []*provider.Response
	requests  []provider.Request
	calls     int
}

func (s *scripted) Name() string { return "scripted" }

func (s *scripted) Complete(_ context.Context, req provider.Request) (*provider.Response, error) {
	s.requests = append(s.requests, req)
	if s.calls >= len(s.responses) {
		return &provider.Response{Blocks: []provider.Block{provider.TextBlock("done")}, StopReason: "end_turn"}, nil
	}
	r := s.responses[s.calls]
	s.calls++
	return r, nil
}

func toolUse(id, name, args string) provider.Block {
	return provider.Block{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(args)}
}

func makeClip(t *testing.T, dir string) string {
	t.Helper()
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(dir, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test clip: %v\n%s", err, raw)
	}
	return out
}

func newTestAgent(t *testing.T, p provider.Provider) (*Agent, *media.Project) {
	t.Helper()
	dir := t.TempDir()
	proj, err := media.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	sk := skills.Load(cfg, dir)
	return New(cfg, p, proj, sk), proj
}

// TestAgentExecutesToolChain drives a real trim through the loop: the fake
// model asks for trim, the harness renders with real ffmpeg, commits the op,
// and feeds a compact result back.
func TestAgentExecutesToolChain(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "trim", `{"start":0,"end":1}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("Trimmed to the first second.")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	var events []Event
	reply, err := a.Run(context.Background(), "keep only the first second", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Trimmed to the first second." {
		t.Fatalf("reply = %q", reply)
	}
	if len(proj.Ops) != 1 || proj.Ops[0].Tool != "trim" {
		t.Fatalf("ops = %+v", proj.Ops)
	}
	if proj.Current == clip {
		t.Fatal("CURRENT did not advance to the rendered output")
	}
	info, err := media.Probe(context.Background(), proj.Current)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration > 1.6 {
		t.Fatalf("trim did not shorten: %.2fs", info.Duration)
	}

	// Tool result fed back to the model must be compact, not ffmpeg logs.
	last := fake.requests[len(fake.requests)-1]
	found := false
	for _, m := range last.Messages {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" {
				found = true
				if len(b.Content) > a.Cfg.Context.ToolResultMaxChars+20 {
					t.Fatalf("tool result too big: %d chars", len(b.Content))
				}
			}
		}
	}
	if !found {
		t.Fatal("no tool_result sent back to the model")
	}
}

// TestAgentToolErrorSurvives: a failing tool must return an error result to
// the model (is_error), not crash the loop.
func TestAgentToolErrorSurvives(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "trim", `{"start":0}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("There is no video loaded yet.")}, StopReason: "end_turn"},
	}}
	a, _ := newTestAgent(t, fake) // no asset added
	reply, err := a.Run(context.Background(), "trim it", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply == "" {
		t.Fatal("expected a reply after tool error")
	}
	sawError := false
	for _, m := range fake.requests[len(fake.requests)-1].Messages {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" && b.IsError {
				sawError = true
			}
		}
	}
	if !sawError {
		t.Fatal("tool error was not reported to the model")
	}
}

// TestPromptCacheHygiene: the system prompt is static (identity + skill
// index only) while volatile state — the project ledger and activated skill
// playbooks — arrives as reminder blocks on the user message. The ledger is
// only re-sent when it changes.
func TestPromptCacheHygiene(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{provider.TextBlock("plan follows")}, StopReason: "end_turn"},
		{Blocks: []provider.Block{provider.TextBlock("ok")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "make this a tiktok", nil); err != nil {
		t.Fatal(err)
	}

	sys := fake.requests[0].System
	if !contains(sys, "## Skills") {
		t.Error("system prompt missing skill index")
	}
	for _, leaked := range []string{"clip.mp4", "320x240"} {
		if contains(sys, leaked) {
			t.Errorf("volatile state %q leaked into the static system prompt", leaked)
		}
	}

	userText := fake.requests[0].Messages[0].Blocks[0].Text
	for _, want := range []string{"<project-state>", "clip.mp4", "<skill-playbook", "9:16"} {
		if !contains(userText, want) {
			t.Errorf("user message missing reminder content %q", want)
		}
	}

	// Second run with unchanged project state: no duplicate ledger, no
	// duplicate playbook.
	if _, err := a.Run(context.Background(), "another tiktok tweak please", nil); err != nil {
		t.Fatal(err)
	}
	second := fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Blocks[0].Text
	if contains(second, "<project-state>") {
		t.Error("unchanged ledger was re-sent — cache-busting")
	}
	if contains(second, "<skill-playbook") {
		t.Error("already-active skill body was re-injected")
	}

	// System prompt must be byte-identical across turns (cacheable prefix).
	if fake.requests[0].System != fake.requests[1].System {
		t.Error("system prompt changed between turns")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func ExampleEvent() {
	fmt.Println(Event{Kind: "tool_end", Tool: "trim", Summary: "trimmed"}.Tool)
	// Output: trim
}
