package canvas

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// The golden-frame test locks in the determinism claim: the same composition
// must produce pixel-stable frames across renders. Rather than committing a
// binary golden PNG (which would couple CI to one Chrome build's font
// rasterization), it renders the SAME composition twice and requires the
// extracted mid-point frames to be near-identical — catching any drift from
// live clocks, animation timing, or nondeterministic capture, which is
// exactly what the seek-based engine exists to prevent.

const goldenHTML = `<!DOCTYPE html><html><head><style>
  body{margin:0;background:#0b2545;height:100vh;display:flex;align-items:center;justify-content:center}
  .card{width:200px;height:100px;background:#f2a444;border-radius:12px;animation:slide 2s linear both}
  @keyframes slide{from{transform:translateX(-120px) rotate(-8deg)}to{transform:translateX(120px) rotate(8deg)}}
  h1{position:absolute;color:#fff;font:700 34px monospace;animation:fade 2s ease both}
  @keyframes fade{from{opacity:0}to{opacity:1}}
</style></head><body><div class="card"></div><h1 data-start="0.5">ìtàn</h1></body></html>`

func chromeOrSkip(t *testing.T) {
	t.Helper()
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}
	if os.Getenv("ITAN_BROWSER") != "" {
		return
	}
	for _, cand := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if _, err := exec.LookPath(cand); err == nil {
			return
		}
	}
	if _, err := os.Stat("/opt/pw-browsers/chromium-1194/chrome-linux/chrome"); err == nil {
		t.Setenv("ITAN_BROWSER", "/opt/pw-browsers/chromium-1194/chrome-linux/chrome")
		return
	}
	t.Skip("no chromium available")
}

// extractFrame pulls a single frame at ts as raw RGB via ffmpeg, so the
// comparison is over decoded pixels, not encoder bytes.
func extractFrame(t *testing.T, video string, ts float64) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "frame.rgb")
	cmd := exec.Command("ffmpeg", "-y", "-ss", fmt.Sprintf("%.3f", ts), "-i", video,
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("frame extract: %v\n%s", err, raw)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// meanAbsDiff is the average per-channel absolute difference (0–255).
func meanAbsDiff(a, b []byte) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 255
	}
	var sum int64
	for i := range a {
		d := int64(a[i]) - int64(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(a))
}

func TestGoldenFrameDeterminism(t *testing.T) {
	chromeOrSkip(t)
	ctx := context.Background()
	dir := t.TempDir()

	render := func(name string) string {
		out := filepath.Join(dir, name)
		if err := Render(ctx, Opts{
			HTML: goldenHTML, Width: 320, Height: 240, FPS: 10, Duration: 2, OutPath: out,
		}); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		return out
	}
	a := render("a.mp4")
	b := render("b.mp4")

	// Compare the mid-animation frame — where a drifting clock would show
	// first — and the final frame. Threshold 1.0/255 mean absolute diff
	// tolerates codec noise while failing on any visible timing shift
	// (a mistimed slide moves the card several pixels ≈ diff >> 1).
	for _, ts := range []float64{1.0, 1.9} {
		fa := extractFrame(t, a, ts)
		fb := extractFrame(t, b, ts)
		if d := meanAbsDiff(fa, fb); d > 1.0 {
			t.Errorf("frame at %.1fs differs between identical renders: mean abs diff %.2f (determinism broken)", ts, d)
		}
	}

	// And the animation must actually progress within one render — a frozen
	// page would trivially pass the identity check.
	f0 := extractFrame(t, a, 0.05)
	f1 := extractFrame(t, a, 1.9)
	if d := meanAbsDiff(f0, f1); d < 1.0 {
		t.Errorf("start and end frames nearly identical (diff %.2f) — the animation did not advance", d)
	}
}
