package tools

import (
	"os"
	"os/exec"
	"testing"
)

// TestViewFramesExtractsImages: real clip in, JPEG frames out, read-only.
func TestViewFramesExtractsImages(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()
	clip := c.Project.Dir + "/clip.mp4"
	mkClip(t, clip)
	if _, err := c.Project.AddAsset(c.Context, clip); err != nil {
		t.Fatal(err)
	}

	res := r.Execute(c, "view_frames", []byte(`{"times":[0.2,1.0,1.8]}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(res.Frames))
	}
	for _, f := range res.Frames {
		st, err := os.Stat(f.Path)
		if err != nil || st.Size() < 500 {
			t.Fatalf("frame %s missing or tiny: %v", f.Path, err)
		}
		if f.MediaType != "image/jpeg" {
			t.Fatalf("media type = %s", f.MediaType)
		}
	}
	if len(c.Project.Ops) != 0 {
		t.Fatal("view_frames must not commit ops")
	}
}

func mkClip(t *testing.T, path string) {
	t.Helper()
	if err := runFF("-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", path); err != nil {
		t.Fatalf("clip: %v", err)
	}
}

func runFF(args ...string) error {
	full := append([]string{"-y", "-v", "error"}, args...)
	cmd := exec.Command("ffmpeg", full...)
	return cmd.Run()
}
