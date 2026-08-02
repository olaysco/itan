package canvas

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/olaysco/itan/internal/browser"
)

// Snapshot renders an HTML document to a transparent PNG at exactly w×h.
// It is the rasterizer behind text overlays when the installed ffmpeg lacks
// drawtext — same embedded fonts as compose, composited later with ffmpeg's
// core overlay filter, which every build ships.
func Snapshot(ctx context.Context, html string, w, h int) ([]byte, error) {
	chrome, err := browser.Find()
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "itan-snap-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	page := filepath.Join(tmpDir, "snap.html")
	if err := os.WriteFile(page, []byte(injectFonts(html)), 0o600); err != nil {
		return nil, err
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.WindowSize(w, h),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("allow-file-access-from-files", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	cctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	cctx, cancelTimeout := context.WithTimeout(cctx, 60*time.Second)
	defer cancelTimeout()

	var shot []byte
	err = chromedp.Run(cctx,
		chromedp.EmulateViewport(int64(w), int64(h)),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return emulation.SetDefaultBackgroundColorOverride().
				WithColor(&cdp.RGBA{R: 0, G: 0, B: 0, A: 0}).Do(ctx)
		}),
		chromedp.Navigate("file://"+page),
		chromedp.Sleep(600*time.Millisecond), // fonts settle
		chromedp.CaptureScreenshot(&shot),
	)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	return shot, nil
}
