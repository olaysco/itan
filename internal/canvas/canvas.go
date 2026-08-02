// Package canvas renders self-contained HTML compositions to video — the
// native, Node-free take on the HTML-to-video idea (HyperFrames, Remotion):
// the real engine was always a browser plus ffmpeg, and Itan already has
// both, so Go drives Chrome directly over the DevTools Protocol.
//
// Determinism comes from stepping time by hand instead of recording a live
// page: an injected runtime pauses every animation the page declares (CSS
// animations/transitions and the Web Animations API) and seeks them to each
// frame's timestamp, and toggles `data-start`/`data-duration` element
// visibility — the same authoring idiom agents already know from the HTML
// video ecosystem. Each frame is screenshotted and ffmpeg encodes the
// sequence. Same input, same frames, same output.
//
// Supported in compositions: inline CSS/JS, CSS animations & transitions,
// Web Animations API, data-start/data-duration timing, data URIs and local
// file references. Not supported: external network resources (renders are
// offline by design) and rAF-driven animation libraries (their clocks don't
// seek); the compose tool's description steers the model accordingly.
package canvas

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/olaysco/itan/internal/browser"
	"github.com/olaysco/itan/internal/media"
)

// maxFrames bounds a render so a runaway duration/fps pair cannot fill the
// disk: 3600 frames is two minutes at 30fps.
const maxFrames = 3600

type Opts struct {
	HTML     string // complete, self-contained document
	Width    int    // canvas size (default 1920x1080)
	Height   int
	FPS      int     // default 30
	Duration float64 // seconds, required
	OutPath  string  // .mp4 destination
	// Scale supersamples the capture (default 2): the page renders at
	// Scale× device pixels and ffmpeg downscales with Lanczos, which is
	// what keeps text crisp through 4:2:0 encoding. 1–3.
	Scale int
}

// seekRuntime is injected once per render. __itanSeek(ms) makes the page
// show exactly the state it should have at that timestamp — CSS/WAAPI
// animations via currentTime, GSAP via its global timeline (GSAP animates
// with rAF + inline styles, so getAnimations() cannot see it).
const seekRuntime = `
if (window.gsap) { try { gsap.ticker.lagSmoothing(0); gsap.globalTimeline.pause(); } catch (e) {} }
window.__itanSeek = (ms) => {
  document.getAnimations().forEach(a => { try { a.pause(); a.currentTime = ms; } catch (e) {} });
  if (window.gsap) { try { gsap.globalTimeline.time(ms / 1000, false); } catch (e) {} }
  document.querySelectorAll('[data-start],[data-duration]').forEach(el => {
    const s = parseFloat(el.dataset.start || 0) * 1000;
    const d = el.dataset.duration ? parseFloat(el.dataset.duration) * 1000 : Infinity;
    el.style.visibility = (ms >= s && ms < s + d) ? '' : 'hidden';
  });
  return true;
};
document.documentElement.style.overflow = 'hidden';
true;
`

// Render turns opts.HTML into an MP4 at opts.OutPath.
func Render(ctx context.Context, opts Opts) error {
	if opts.Width <= 0 {
		opts.Width = 1920
	}
	if opts.Height <= 0 {
		opts.Height = 1080
	}
	if opts.FPS <= 0 {
		opts.FPS = 30
	}
	if opts.Duration <= 0 {
		return fmt.Errorf("compose needs a positive duration")
	}
	frames := int(opts.Duration*float64(opts.FPS) + 0.5)
	if frames < 1 {
		frames = 1
	}
	if frames > maxFrames {
		return fmt.Errorf("%d frames exceeds the render cap of %d (%.0fs at %dfps) — lower duration or fps", frames, maxFrames, opts.Duration, opts.FPS)
	}

	chrome, err := browser.Find()
	if err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "itan-canvas-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	htmlPath := filepath.Join(work, "composition.html")
	if err := os.WriteFile(htmlPath, []byte(injectGSAP(injectFonts(opts.HTML))), 0o600); err != nil {
		return err
	}

	scale := opts.Scale
	if scale <= 0 {
		scale = 2
	}
	if scale > 3 {
		scale = 3
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.WindowSize(opts.Width, opts.Height),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		// Compositions may embed project images (capture_page output) via
		// file:// paths from their own file:// document.
		chromedp.Flag("allow-file-access-from-files", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	cctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	cctx, cancelTimeout := context.WithTimeout(cctx, 5*time.Minute)
	defer cancelTimeout()

	awaitFonts := func(p *cdruntime.EvaluateParams) *cdruntime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}
	if err := chromedp.Run(cctx,
		chromedp.EmulateViewport(int64(opts.Width), int64(opts.Height), chromedp.EmulateScale(float64(scale))),
		chromedp.Navigate("file://"+htmlPath),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(`document.fonts.ready.then(() => true)`, nil, awaitFonts),
		chromedp.Evaluate(seekRuntime, nil),
	); err != nil {
		return fmt.Errorf("compose: page load: %w", err)
	}

	for i := 0; i < frames; i++ {
		ms := float64(i) / float64(opts.FPS) * 1000
		var shot []byte
		if err := chromedp.Run(cctx,
			chromedp.Evaluate(fmt.Sprintf("__itanSeek(%.3f)", ms), nil),
			chromedp.CaptureScreenshot(&shot),
		); err != nil {
			return fmt.Errorf("compose: frame %d/%d: %w", i+1, frames, err)
		}
		frame := filepath.Join(work, fmt.Sprintf("f_%05d.png", i))
		if err := os.WriteFile(frame, shot, 0o600); err != nil {
			return err
		}
	}

	// Lanczos downscale from the supersampled capture keeps text edges
	// crisp through 4:2:0; CRF 18 keeps them crisp through encoding.
	return media.Run(ctx,
		"-framerate", fmt.Sprintf("%d", opts.FPS),
		"-i", filepath.Join(work, "f_%05d.png"),
		"-vf", fmt.Sprintf("scale=%d:%d:flags=lanczos", opts.Width, opts.Height),
		"-c:v", "libx264", "-preset", "medium", "-crf", "18",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart",
		opts.OutPath)
}
