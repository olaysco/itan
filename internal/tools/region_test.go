package tools

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

func TestClampRect(t *testing.T) {
	// All cases run against a 640x360 source frame.
	cases := []struct {
		name    string
		in      rect
		want    rect
		wantErr bool
	}{
		{"inside unchanged", rect{10, 20, 100, 80}, rect{10, 20, 100, 80}, false},
		{"clipped right/bottom", rect{600, 300, 100, 100}, rect{600, 300, 40, 60}, false},
		{"clipped left/top", rect{-20, -10, 100, 100}, rect{0, 0, 80, 90}, false},
		{"odd dims rounded even", rect{0, 0, 101, 81}, rect{0, 0, 100, 80}, false},
		{"fully outside right", rect{700, 100, 50, 50}, rect{}, true},
		{"fully outside left", rect{-100, -100, 50, 50}, rect{}, true},
		{"negative size", rect{0, 0, -5, 10}, rect{}, true},
		{"zero size", rect{0, 0, 0, 0}, rect{}, true},
		{"sliver after clamp", rect{639, 100, 100, 50}, rect{}, true},
	}
	for _, tc := range cases {
		got, err := clampRect(tc.in, 640, 360)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %+v", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: clampRect(%+v) = %+v, want %+v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestFitRectAspect(t *testing.T) {
	// A square rect in a 16:9 frame widens minimally and stays inside.
	got := fitRectAspect(rect{100, 100, 200, 200}, 640, 360)
	if a := float64(got.W) / float64(got.H); math.Abs(a-16.0/9) > 0.02 {
		t.Errorf("aspect not matched: %+v (ratio %.3f)", got, a)
	}
	if got.X < 0 || got.Y < 0 || got.X+got.W > 640 || got.Y+got.H > 360 {
		t.Errorf("rect escaped the frame: %+v", got)
	}
	if got.W%2 != 0 || got.H%2 != 0 {
		t.Errorf("dims not even: %+v", got)
	}
}

func TestBlurRegionGraph(t *testing.T) {
	whole := blurRegionGraph(rect{10, 20, 100, 80}, 15, 0, -1)
	for _, frag := range []string{"crop=100:80:10:20", "boxblur=15:2", "overlay=10:20"} {
		if !strings.Contains(whole, frag) {
			t.Errorf("missing %q in %q", frag, whole)
		}
	}
	if strings.Contains(whole, "enable") {
		t.Errorf("whole-video graph should have no enable clause: %q", whole)
	}

	ranged := blurRegionGraph(rect{10, 20, 100, 80}, 15, 1.5, 4)
	if !strings.Contains(ranged, `enable='between(t\,1.500\,4.000)'`) {
		t.Errorf("missing between clause in %q", ranged)
	}
	open := blurRegionGraph(rect{10, 20, 100, 80}, 15, 2, -1)
	if !strings.Contains(open, `enable='gte(t\,2.000)'`) {
		t.Errorf("missing gte clause in %q", open)
	}
}

func TestPixelateRegionGraph(t *testing.T) {
	g := pixelateRegionGraph(rect{0, 0, 120, 60}, 12, 0, -1)
	for _, frag := range []string{"crop=120:60:0:0", "scale=10:5", "scale=120:60:flags=neighbor", "overlay=0:0"} {
		if !strings.Contains(g, frag) {
			t.Errorf("missing %q in %q", frag, g)
		}
	}
}

func makeToolClip(t *testing.T, dir string) string {
	t.Helper()
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(dir, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test clip: %v\n%s", err, raw)
	}
	return out
}

// TestBlurRegionEndToEnd renders a real blur through the registry: the output
// exists, the op is committed with CURRENT advanced, and dims are unchanged.
func TestBlurRegionEndToEnd(t *testing.T) {
	dir := t.TempDir()
	clip := makeToolClip(t, dir)
	proj, err := media.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := proj.AddAsset(ctx, clip); err != nil {
		t.Fatal(err)
	}

	res := NewRegistry().Execute(&Ctx{Context: ctx, Project: proj}, "blur_region",
		json.RawMessage(`{"x":40,"y":30,"w":120,"h":90,"strength":10,"start":0.2,"end":1.5}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if _, err := os.Stat(res.Output); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if len(proj.Ops) != 1 || proj.Ops[0].Tool != "blur_region" {
		t.Fatalf("op not committed: %+v", proj.Ops)
	}
	if proj.Current != res.Output {
		t.Errorf("CURRENT not advanced: %s", proj.Current)
	}
	info, err := media.Probe(ctx, res.Output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Errorf("dims changed: %dx%d", info.Width, info.Height)
	}
}
