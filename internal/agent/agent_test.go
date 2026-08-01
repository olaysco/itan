package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestVisionRouting: with a vision route configured, the turn after
// view_frames (whose result carries frames) goes to the vision provider,
// and the main model never receives image blocks.
func TestVisionRouting(t *testing.T) {
	main := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "view_frames", `{"times":[0.5]}`)}, StopReason: "tool_use"},
	}}
	eyes := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{provider.TextBlock("Looks right.")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, main)
	a.vision, a.visionModel = eyes, "fake-vl"
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	reply, err := a.Run(context.Background(), "check the frame", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Looks right." {
		t.Fatalf("reply = %q", reply)
	}
	if main.calls != 1 || eyes.calls != 1 {
		t.Fatalf("calls: main=%d vision=%d", main.calls, eyes.calls)
	}
	vreq := eyes.requests[0]
	if vreq.Model != "fake-vl" {
		t.Fatalf("vision request used model %q", vreq.Model)
	}
	if !hasImages(vreq.Messages[len(vreq.Messages)-1]) {
		t.Fatal("vision request lost the frames")
	}
	for _, req := range main.requests {
		for _, m := range req.Messages {
			if hasImages(m) {
				t.Fatal("main model received image blocks")
			}
		}
	}
}

// Two identical failures of one tool must block a third call — a dead
// endpoint doesn't heal because the model varies the arguments, and every
// futile retry costs a full model round-trip.
func TestDoomRepeatedFailureBlocking(t *testing.T) {
	d := doomDetector{}
	d.noteFailure("transcribe", "whisper unreachable")
	if _, ok := d.blocked("transcribe"); ok {
		t.Fatal("one failure must not block")
	}
	d.noteFailure("transcribe", "whisper unreachable")
	msg, ok := d.blocked("transcribe")
	if !ok || !strings.Contains(msg, "unreachable") {
		t.Fatalf("two identical failures must block: %q %v", msg, ok)
	}
	if _, ok := d.blocked("probe"); ok {
		t.Fatal("other tools must stay callable")
	}
	// Different error texts are progress, not a loop — don't block.
	d.noteFailure("probe", "ffprobe a: exit status 1")
	d.noteFailure("probe", "ffprobe b: exit status 1")
	if _, ok := d.blocked("probe"); ok {
		t.Fatal("distinct errors must not block")
	}
}

// A model that ends its turn with no reply at all gets pushed back exactly
// once — models stall mid-task, and accepting the empty turn strands the
// user with nothing.
func TestEmptyTurnNudge(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: nil, StopReason: "end_turn"}, // the stall
		{Blocks: []provider.Block{provider.TextBlock("finished after nudge")}, StopReason: "end_turn"},
	}}
	a, _ := newTestAgent(t, fake)
	reply, err := a.Run(context.Background(), "do the thing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "finished after nudge" {
		t.Fatalf("reply = %q", reply)
	}
	if fake.calls != 2 {
		t.Fatalf("calls = %d, want 2 (stall + nudged retry)", fake.calls)
	}
	second := fake.requests[1]
	last := second.Messages[len(second.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Blocks[0].Text, "ended your turn") {
		t.Fatalf("nudge message missing: %+v", last)
	}
	// The stalled assistant turn must not enter history as an empty message.
	prev := second.Messages[len(second.Messages)-2]
	if prev.Role != "assistant" || len(prev.Blocks) == 0 {
		t.Fatalf("empty assistant message reached history: %+v", prev)
	}
}

// A response that is ALL chain-of-thought (reasoning hit the token cap) gets
// the targeted push-back: act, don't re-derive.
func TestReasoningOnlyStallNudge(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: nil, StopReason: "max_tokens", Reasoning: "very long plan..."},
		{Blocks: []provider.Block{provider.TextBlock("acted")}, StopReason: "end_turn"},
	}}
	a, _ := newTestAgent(t, fake)
	reply, err := a.Run(context.Background(), "make the video", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "acted" {
		t.Fatalf("reply = %q", reply)
	}
	second := fake.requests[1]
	last := second.Messages[len(second.Messages)-1]
	if !strings.Contains(last.Blocks[0].Text, "internal reasoning") {
		t.Fatalf("reasoning-stall nudge missing: %+v", last)
	}
}

// finishReply must never claim success the model didn't state.
func TestFinishReplyHonest(t *testing.T) {
	a := &Agent{}
	if got := a.finishReply("all trimmed", "x"); got != "all trimmed" {
		t.Fatalf("real reply mangled: %q", got)
	}
	if got := a.finishReply("", "trimmed to 0.5s–10.1s"); !strings.Contains(got, "trimmed to 0.5s–10.1s") || !strings.Contains(got, "without a summary") {
		t.Fatalf("empty reply must report the last real step: %q", got)
	}
	if got := a.finishReply("", ""); strings.Contains(got, "Done") {
		t.Fatalf("must not fabricate Done: %q", got)
	}
}

// Frames must not outlive the request that looked at them: a new Run starts
// with all previous images stripped from history.
func TestStaleFramesDroppedAcrossRuns(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "view_frames", `{"times":[0.5]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("looked")}, StopReason: "end_turn"},
		{Blocks: []provider.Block{provider.TextBlock("second run")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Run(context.Background(), "look at it", nil); err != nil {
		t.Fatal(err)
	}
	// Within run 1, the follow-up request must carry the frames.
	withFrames := fake.requests[1]
	found := false
	for _, m := range withFrames.Messages {
		if hasImages(m) {
			found = true
		}
	}
	if !found {
		t.Fatal("frames missing within the run that created them")
	}

	if _, err := a.Run(context.Background(), "now trim it", nil); err != nil {
		t.Fatal(err)
	}
	secondRun := fake.requests[2]
	for _, m := range secondRun.Messages {
		if hasImages(m) {
			t.Fatal("stale frames re-sent in a later run")
		}
	}
}

func TestStripImages(t *testing.T) {
	withImg := func() provider.Message {
		b := provider.ToolResultBlock("t1", "frames", false)
		b.Images = []provider.Image{{MediaType: "image/jpeg", Data: "eA=="}}
		return provider.Message{Role: "user", Blocks: []provider.Block{b}}
	}
	msgs := []provider.Message{withImg(), provider.UserText("hi"), withImg()}

	kept := stripImages(msgs, true)
	if hasImages(kept[0]) || !hasImages(kept[2]) {
		t.Fatal("keepLast must strip all but the final message")
	}
	if !strings.Contains(kept[0].Blocks[0].Content, "no longer attached") {
		t.Fatal("stale-frame note missing")
	}
	if !hasImages(msgs[0]) {
		t.Fatal("stripImages mutated the original history")
	}
	if all := stripImages(msgs, false); hasImages(all[2]) {
		t.Fatal("full strip left images on the final message")
	}
}
