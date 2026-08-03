package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

func makeTierClip(t *testing.T, dir, name string, dur int) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=320x240:rate=10", dur),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=%d", dur),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v\n%s", err, raw)
	}
	return out
}

// cut_range removes the middle and rejoins — duration shrinks by the cut.
func TestCutRange(t *testing.T) {
	c := composeCtx(t)
	clip := makeTierClip(t, c.Project.Dir, "clip.mp4", 4)
	if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	res := r.Execute(c, "cut_range", []byte(`{"start":1,"end":2}`))
	if res.Err != nil {
		t.Fatalf("cut_range: %v", res.Err)
	}
	info, err := media.Probe(context.Background(), c.Project.Current)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration < 2.6 || info.Duration > 3.4 {
		t.Fatalf("duration after 1s cut from 4s clip = %.2fs, want ~3s", info.Duration)
	}
	if !info.HasAudio {
		t.Fatal("audio lost across the cut")
	}
}

// add_music lays a bed under a silent or voiced video and keeps duration.
func TestAddMusic(t *testing.T) {
	c := composeCtx(t)
	clip := makeTierClip(t, c.Project.Dir, "clip.mp4", 3)
	if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	// The "music": a second tone file.
	music := filepath.Join(c.Project.Dir, "music.wav")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "sine=frequency=220:duration=1", music)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("music: %v\n%s", err, raw)
	}

	r := NewRegistry()
	res := r.Execute(c, "add_music", []byte(`{"audio":"`+music+`","volume":0.3}`))
	if res.Err != nil {
		t.Fatalf("add_music: %v", res.Err)
	}
	if !strings.Contains(res.Summary, "ducked") {
		t.Fatalf("clip has audio — bed should duck, got %q", res.Summary)
	}
	info, err := media.Probe(context.Background(), c.Project.Current)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration < 2.5 || info.Duration > 3.5 {
		t.Fatalf("duration changed: %.2fs", info.Duration)
	}
	if !info.HasAudio {
		t.Fatal("no audio on output")
	}
}

// The storyboard lives in project state and the ledger.
func TestStoryboard(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()
	res := r.Execute(c, "storyboard", []byte(`{"scenes":[{"n":1,"intent":"hook","duration":3},{"n":2,"intent":"features","duration":5}]}`))
	if res.Err != nil {
		t.Fatalf("storyboard: %v", res.Err)
	}
	if len(c.Project.Scenes) != 2 {
		t.Fatalf("scenes = %d", len(c.Project.Scenes))
	}
	ledger := c.Project.Ledger(context.Background())
	if !strings.Contains(ledger, "Storyboard:") || !strings.Contains(ledger, "PLANNED") {
		t.Fatalf("ledger missing storyboard: %s", ledger)
	}

	clip := makeTierClip(t, c.Project.Dir, "scene1.mp4", 3)
	if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	res = r.Execute(c, "storyboard", []byte(`{"mark_rendered":{"n":1,"output":"a1"}}`))
	if res.Err != nil {
		t.Fatalf("mark_rendered: %v", res.Err)
	}
	if c.Project.Scenes[0].Output == "" {
		t.Fatal("scene 1 not marked rendered")
	}
	if !strings.Contains(c.Project.Ledger(context.Background()), "rendered → scene1.mp4") {
		t.Fatal("ledger missing rendered scene")
	}
}

// view_strip returns ONE storyboard sheet with labeled cells (browser path).
func TestViewStrip(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)
	// Two visually distinct halves so scene detection has a cut to find.
	clip := filepath.Join(c.Project.Dir, "two.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=red:duration=2:size=320x240:rate=10",
		"-f", "lavfi", "-i", "color=blue:duration=2:size=320x240:rate=10",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1[v]", "-map", "[v]",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", clip)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v\n%s", err, raw)
	}
	if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	res := r.Execute(c, "view_strip", []byte(`{}`))
	if res.Err != nil {
		t.Fatalf("view_strip: %v", res.Err)
	}
	if len(res.Frames) != 1 {
		t.Fatalf("want one sheet, got %d frames", len(res.Frames))
	}
	if !strings.Contains(res.Summary, "storyboard") {
		t.Fatalf("summary: %q", res.Summary)
	}
	info, err := media.Probe(context.Background(), res.Frames[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width < 300 {
		t.Fatalf("sheet too small: %dx%d", info.Width, info.Height)
	}
}

// list_files must surface material the user simply dropped in — the model
// cannot use add_music (or any user-supplied file) if it cannot find it.
func TestListFiles(t *testing.T) {
	c := composeCtx(t)
	clip := makeTierClip(t, c.Project.Dir, "clip.mp4", 2)
	if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(c.Project.Dir, "bed.wav")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "sine=frequency=220:duration=1", music)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("music: %v\n%s", err, raw)
	}

	res := NewRegistry().Execute(c, "list_files", []byte(`{}`))
	if res.Err != nil {
		t.Fatalf("list_files: %v", res.Err)
	}
	listing, _ := res.Data["files"].(string)
	if !strings.Contains(listing, "clip.mp4") || !strings.Contains(listing, "asset a1") {
		t.Fatalf("registered asset not marked: %s", listing)
	}
	if !strings.Contains(listing, "bed.wav") || !strings.Contains(listing, "unregistered") {
		t.Fatalf("dropped-in music not discoverable: %s", listing)
	}
	// Audio-only filter must exclude the video.
	res = NewRegistry().Execute(c, "list_files", []byte(`{"kind":"audio"}`))
	if l, _ := res.Data["files"].(string); strings.Contains(l, "clip.mp4") {
		t.Fatalf("kind filter ignored: %s", l)
	}
}
