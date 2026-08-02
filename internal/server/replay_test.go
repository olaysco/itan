package server

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/olaysco/itan/internal/cli"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/tools"
)

// The ledger is a replayable recipe: editing step 1's args re-runs it and
// replays step 2 from its recorded recipe on top of the new output.
func TestReplayFrom(t *testing.T) {
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=6:size=640x360:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clip)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v\n%s", err, raw)
	}

	session, err := cli.NewSession(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	s := New(session)

	// Step 1: trim to 4s (CURRENT). Step 2: crop square (chains off CURRENT).
	reg := tools.NewRegistry()
	tctx := &tools.Ctx{Context: context.Background(), Project: session.Project, Config: session.Cfg}
	if res := reg.Execute(tctx, "trim", json.RawMessage(`{"start":0,"end":4}`)); res.Err != nil {
		t.Fatalf("trim: %v", res.Err)
	}
	if res := reg.Execute(tctx, "crop", json.RawMessage(`{"aspect":"1:1"}`)); res.Err != nil {
		t.Fatalf("crop: %v", res.Err)
	}
	if len(session.Project.Ops) != 2 {
		t.Fatalf("ops = %d", len(session.Project.Ops))
	}

	// Edit step 1: trim to 2s instead. The crop must replay on the new cut.
	if err := s.replayFrom(context.Background(), session.Project.Ops[0].Seq, map[string]any{"end": 2.0}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(session.Project.Ops) != 2 {
		t.Fatalf("ops after replay = %d, want 2", len(session.Project.Ops))
	}
	info, err := media.Probe(context.Background(), session.Project.Current)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration < 1.5 || info.Duration > 2.5 {
		t.Fatalf("replayed duration = %.2fs, want ~2s", info.Duration)
	}
	if info.Width != info.Height {
		t.Fatalf("crop did not replay: %dx%d", info.Width, info.Height)
	}
}
