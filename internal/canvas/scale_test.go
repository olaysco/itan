package canvas

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// Supersampling is the one knob that trades render time for sharpness, and
// on a slow machine it decides whether a 45s vertical piece takes 30 minutes
// or two hours. Whatever it is set to, the output dimensions must not move.
func TestScaleDoesNotChangeOutputDimensions(t *testing.T) {
	chromeOrSkip(t)
	const html = `<!DOCTYPE html><html><head><style>
	  body{margin:0;background:#101018;display:flex;align-items:center;justify-content:center;height:100vh}
	  h1{color:#fff;font:800 60px sans-serif}
	</style></head><body><h1>scale</h1></body></html>`

	dir := t.TempDir()
	// 0 means "engine default"; 9 is out of range and must clamp rather than
	// try to screenshot a 2880x2160 page.
	for _, scale := range []int{0, 1, 2, 9} {
		out := filepath.Join(dir, "s.mp4")
		if err := Render(context.Background(), Opts{
			HTML: html, Width: 320, Height: 240, FPS: 5, Duration: 0.4,
			OutPath: out, Scale: scale,
		}); err != nil {
			t.Fatalf("scale=%d: %v", scale, err)
		}
		info, err := media.Probe(context.Background(), out)
		if err != nil {
			t.Fatalf("scale=%d: %v", scale, err)
		}
		if info.Width != 320 || info.Height != 240 {
			t.Errorf("scale=%d produced %dx%d, want 320x240", scale, info.Width, info.Height)
		}
	}
}
