package canvas

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/olaysco/itan/internal/browser"
)

// CapturePage screenshots a live URL — the raw material for product-launch
// compositions (hero shots, UI panes) that compose can then embed and move.
// Unlike Render, this deliberately goes online: the user handed us the URL.
func CapturePage(ctx context.Context, url string, width int, fullPage bool, outPath string) (w, h int, err error) {
	if width <= 0 {
		width = 1440
	}
	height := width * 9 / 16

	chrome, err := browser.Find()
	if err != nil {
		return 0, 0, err
	}
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.WindowSize(width, height),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	cctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	cctx, cancelTimeout := context.WithTimeout(cctx, 90*time.Second)
	defer cancelTimeout()

	var shot []byte
	actions := []chromedp.Action{
		chromedp.EmulateViewport(int64(width), int64(height), chromedp.EmulateScale(2)),
		chromedp.Navigate(url),
		chromedp.Sleep(1800 * time.Millisecond), // let lazy content and fonts settle
	}
	if fullPage {
		actions = append(actions, chromedp.FullScreenshot(&shot, 92))
	} else {
		actions = append(actions, chromedp.CaptureScreenshot(&shot))
	}
	if err := chromedp.Run(cctx, actions...); err != nil {
		return 0, 0, fmt.Errorf("capture %s: %w", url, err)
	}
	if err := os.WriteFile(outPath, shot, 0o644); err != nil {
		return 0, 0, err
	}
	return width * 2, height * 2, nil // EmulateScale(2) doubles captured pixels
}
