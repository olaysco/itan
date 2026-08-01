package tools

import (
	"context"
	"math"
	"os/exec"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

func TestXfadeOffsets(t *testing.T) {
	// Three clips of 4s, 6s, 5s with 0.5s crossfades:
	// offset1 = 4-0.5 = 3.5; running = 4+6-0.5 = 9.5; offset2 = 9.0.
	got := xfadeOffsets([]float64{4, 6, 5}, 0.5)
	want := []float64{3.5, 9.0}
	if len(got) != len(want) {
		t.Fatalf("offsets = %v", got)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("offset[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestConcatWithFadeTransition renders a real crossfaded join and checks the
// overlap math shows up in the output duration: d0 + d1 - td.
func TestConcatWithFadeTransition(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()

	mk := func(name, color string) string {
		path := c.Project.Dir + "/" + name
		cmd := exec.Command("ffmpeg", "-y",
			"-f", "lavfi", "-i", "color=c="+color+":duration=2:size=320x240:rate=10",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("clip: %v\n%s", err, out)
		}
		return path
	}
	a := mk("a.mp4", "red")
	b := mk("b.mp4", "blue")

	res := r.Execute(c, "concat", []byte(`{"inputs":["`+a+`","`+b+`"],"transition":"fade","transition_duration":0.5}`))
	if res.Err != nil {
		t.Fatalf("concat fade: %v", res.Err)
	}
	info, err := media.Probe(context.Background(), c.Project.Current)
	if err != nil {
		t.Fatal(err)
	}
	// 2 + 2 - 0.5 = 3.5s, allow encoder padding slack.
	if info.Duration < 3.2 || info.Duration > 3.8 {
		t.Fatalf("crossfaded duration = %.2fs, want ~3.5s", info.Duration)
	}
	if !info.HasAudio {
		t.Fatal("acrossfade lost the audio track")
	}
}

func TestConcatRejectsBadTransition(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()
	res := r.Execute(c, "concat", []byte(`{"inputs":["x.mp4","y.mp4"],"transition":"explode"}`))
	if res.Err == nil {
		t.Fatal("unknown transition must be rejected")
	}
}
