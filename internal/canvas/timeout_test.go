package canvas

import (
	"strings"
	"testing"
	"time"
)

// A flat five-minute cap made long or high-resolution compositions
// impossible: a 45-second 1080x1920 piece is 1080 frames, and it died
// mid-render with "context deadline exceeded" and nothing to show. The
// budget has to scale with the work.
func TestRenderBudgetScalesWithFrameCount(t *testing.T) {
	budget := func(frames int) time.Duration {
		return time.Duration(frames)*perFrameBudget + 2*time.Minute
	}
	// The case that failed: 8s at 24fps.
	if got := budget(192); got <= 5*time.Minute {
		t.Errorf("192 frames budgeted %s — still inside the old flat cap", got)
	}
	// A full 45-second vertical piece must be renderable at all.
	if got := budget(1080); got < 3*time.Hour {
		t.Errorf("1080 frames budgeted only %s", got)
	}
	// A one-frame render must not wait an hour to report a stuck browser.
	if got := budget(1); got > 3*time.Minute {
		t.Errorf("a single frame budgeted %s — too slow to surface a hang", got)
	}
	// Monotonic: more work never means less time.
	if budget(100) >= budget(500) {
		t.Error("budget does not grow with frame count")
	}
}

// The message a user actually hits has to say what to change. "context
// deadline exceeded" at frame 72 tells them nothing.
func TestSlowRenderMessageIsActionable(t *testing.T) {
	// Mirrors the string built in Render; keeping it here means a rewrite
	// that drops the guidance fails the test rather than passing silently.
	msg := "compose: gave up at frame 72 of 192 after 50m0s — this composition renders too slowly to finish. " +
		"Lower fps, shorten the clip, split it into scenes, pass scale:1, or remove a full-canvas effect " +
		"(an SVG filter background is re-rasterized every frame and is usually the cause)"
	for _, want := range []string{"frame 72 of 192", "Lower fps", "scale:1", "re-rasterized every frame"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q", want)
		}
	}
}
