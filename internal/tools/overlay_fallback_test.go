package tools

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// The browser-rendered caption path must produce a valid video with the
// original dimensions and audio intact — it is what users get when their
// ffmpeg build ships without drawtext.
func TestOverlayTextBrowserFallback(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)

	clip := filepath.Join(c.Project.Dir, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v\n%s", err, out)
	}

	res := overlayTextViaBrowser(c, clip, "Text in → Frames out", "center", "#E85A4F", 40, 0.2, 1.5)
	if res.Err != nil {
		t.Fatalf("fallback: %v", res.Err)
	}
	if !strings.Contains(res.Summary, "browser-rendered") {
		t.Fatalf("summary should say the fallback was used: %q", res.Summary)
	}
	info, err := media.Probe(context.Background(), res.Output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("dims changed: %dx%d", info.Width, info.Height)
	}
	if !info.HasAudio {
		t.Fatal("audio track lost")
	}
}
