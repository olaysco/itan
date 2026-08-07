package tools

import (
	"image"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// frameHasColour extracts a frame and reports whether a meaningful share of
// it is the given colour, allowing for 4:2:0 chroma drift.
func frameHasColour(t *testing.T, video string, r, g, b uint8) bool {
	t.Helper()
	png := filepath.Join(t.TempDir(), "f.png")
	if raw, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-i", video, "-frames:v", "1", png).CombinedOutput(); err != nil {
		t.Fatalf("extract: %v\n%s", err, raw)
	}
	f, err := os.Open(png)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	hits := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if math.Abs(float64(cr>>8)-float64(r)) < 12 &&
				math.Abs(float64(cg>>8)-float64(g)) < 12 &&
				math.Abs(float64(cb>>8)-float64(b)) < 12 {
				hits++
			}
		}
	}
	// A tile is a small fraction of the canvas; a ground is most of it.
	return float64(hits)/float64(bounds.Dx()*bounds.Dy()) > 0.02
}
