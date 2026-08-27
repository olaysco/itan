package canvas

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/olaysco/itan/internal/browser"
)

// These tests measure the frame-indexed API against the authoring style it
// replaces. The engine renders by seeking, so anything that runs its own
// clock (rAF loops, and any library built on them) is rendered at whatever
// wall-clock moment the capture happens to reach — the limitation the package
// doc has always carried. A scene expressed as a pure function of frame has
// no clock to race: the tests below check it against analytic ground truth,
// under shuffled seek order, and across separate page loads, and measure the
// rAF equivalent the same way for comparison.

// probe loads html with the exact injections the render path uses, seeks the
// given frames in the given order, and evaluates expr after each seek. It
// returns the values in the order the frames were requested.
func probe(t *testing.T, html string, fps, total int, order []int, expr string) []float64 {
	t.Helper()
	chrome, err := browser.Find()
	if err != nil {
		t.Skip(err)
	}

	dir := t.TempDir()
	page := filepath.Join(dir, "scene.html")
	if err := os.WriteFile(page, []byte(injectFrameAPI(injectGSAP(injectFonts(html)))), 0o600); err != nil {
		t.Fatal(err)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		browser.AllocatorOptions(chrome, 640, 360,
			chromedp.Flag("hide-scrollbars", true),
		)...)
	defer cancelAlloc()
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	ctx, cancelTimeout := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTimeout()

	awaitFonts := func(p *cdruntime.EvaluateParams) *cdruntime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(640, 360),
		chromedp.Navigate("file://"+page),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(`document.fonts.ready.then(() => true)`, nil, awaitFonts),
		chromedp.Evaluate(fmt.Sprintf("window.__itanFPS=%d;window.__itanTotalFrames=%d;true", fps, total), nil),
		chromedp.Evaluate(seekRuntime, nil),
	); err != nil {
		t.Fatalf("probe: page load: %v", err)
	}

	out := make([]float64, len(order))
	for i, f := range order {
		ms := float64(f) / float64(fps) * 1000
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(fmt.Sprintf("__itanSeek(%.6f)", ms), nil),
			chromedp.Evaluate(expr, &out[i]),
		); err != nil {
			t.Fatalf("probe: frame %d: %v", f, err)
		}
	}
	return out
}

// A box driven purely by frame number. left is read back from layout, so the
// measurement is what actually rendered, not what the script intended.
const frameScene = `<!DOCTYPE html><html><head><style>
  body{margin:0;background:#0b0b0d;height:100vh;overflow:hidden}
  .box{position:absolute;top:100px;left:0;width:60px;height:60px;background:#E86C1A}
</style></head><body><div class="box"></div>
<script>
  const box = document.querySelector('.box');
  itan.frame(({frame}) => {
    box.style.left = interpolate(frame, [0, 30], [0, 300], {easing: 'out'}) + 'px';
  });
</script></body></html>`

// The same motion written the way a model reaches for by default: a rAF loop
// off the wall clock.
const rafScene = `<!DOCTYPE html><html><head><style>
  body{margin:0;background:#0b0b0d;height:100vh;overflow:hidden}
  .box{position:absolute;top:100px;left:0;width:60px;height:60px;background:#E86C1A}
</style></head><body><div class="box"></div>
<script>
  const box = document.querySelector('.box');
  const t0 = performance.now();
  (function loop(){
    const t = Math.min(1, (performance.now() - t0) / 1000);
    box.style.left = (300 * (1 - Math.pow(1 - t, 3))) + 'px';
    requestAnimationFrame(loop);
  })();
</script></body></html>`

const readLeft = `parseFloat(getComputedStyle(document.querySelector('.box')).left)`

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// easeOut mirrors frameapi.js's default easing, so the Go side is an
// independent statement of the intended motion rather than a copy of the
// implementation's output.
func easeOut(t float64) float64 { return 1 - math.Pow(1-t, 3) }

func expectedLeft(frame int) float64 {
	t := math.Max(0, math.Min(1, float64(frame)/30))
	return 300 * easeOut(t)
}

func maxErr(got []float64, want func(int) float64, order []int) float64 {
	var worst float64
	for i, f := range order {
		if d := math.Abs(got[i] - want(f)); d > worst {
			worst = d
		}
	}
	return worst
}

// TestFrameAPIMatchesGroundTruth is the exactness measurement: every rendered
// frame must be the value the motion is defined to have at that frame.
func TestFrameAPIMatchesGroundTruth(t *testing.T) {
	chromeOrSkip(t)
	order := seq(31)
	got := probe(t, frameScene, 30, 31, order, readLeft)

	// getComputedStyle resolves to device-pixel precision, so the tolerance
	// is a rounding allowance, not a timing one.
	if e := maxErr(got, expectedLeft, order); e > 0.5 {
		t.Errorf("frame-indexed scene deviates from its definition by %.3fpx", e)
	} else {
		t.Logf("frame-indexed: max deviation from ground truth %.4fpx over %d frames", e, len(order))
	}

	rafGot := probe(t, rafScene, 30, 31, order, readLeft)
	rafErr := maxErr(rafGot, expectedLeft, order)
	t.Logf("rAF equivalent: max deviation from ground truth %.1fpx over %d frames", rafErr, len(order))
	if rafErr < 1 {
		t.Log("note: rAF scene happened to track the intended timing on this run; " +
			"it is not guaranteed to, which is the point")
	}
}

// TestFrameAPIRandomAccess: frame N must render the same whether it is
// reached going forward, backward, or out of order. A self-running timeline
// only approximates this; a pure function of frame guarantees it.
func TestFrameAPIRandomAccess(t *testing.T) {
	chromeOrSkip(t)
	forward := seq(31)
	// A fixed shuffle keeps the test deterministic while still walking the
	// timeline backwards and jumping across it.
	shuffled := []int{30, 3, 17, 0, 29, 8, 22, 1, 14, 30, 5, 11, 27, 2, 19, 9}

	fwd := probe(t, frameScene, 30, 31, forward, readLeft)
	shf := probe(t, frameScene, 30, 31, shuffled, readLeft)

	index := map[int]float64{}
	for i, f := range forward {
		index[f] = fwd[i]
	}
	for i, f := range shuffled {
		if got, want := shf[i], index[f]; got != want {
			t.Errorf("frame %d renders %.4f out of order but %.4f in order — seek order leaks into output", f, got, want)
		}
	}
}

// TestFrameAPIStableAcrossLoads: two independent page loads must produce
// identical values. The rAF scene is measured the same way to show what the
// engine is actually protecting against.
func TestFrameAPIStableAcrossLoads(t *testing.T) {
	chromeOrSkip(t)
	order := seq(31)

	a := probe(t, frameScene, 30, 31, order, readLeft)
	b := probe(t, frameScene, 30, 31, order, readLeft)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("frame %d: %.4f vs %.4f across identical loads", order[i], a[i], b[i])
		}
	}

	ra := probe(t, rafScene, 30, 31, order, readLeft)
	rb := probe(t, rafScene, 30, 31, order, readLeft)
	var spread float64
	for i := range ra {
		if d := math.Abs(ra[i] - rb[i]); d > spread {
			spread = d
		}
	}
	t.Logf("run-to-run spread — frame-indexed: 0.0000px, rAF: %.1fpx", spread)
}

// TestSpringIsClosedForm: the spring must be exact at any frame without
// having stepped through the frames before it — that is what makes it safe
// to render frames in any order, or to re-render one frame alone.
func TestSpringIsClosedForm(t *testing.T) {
	chromeOrSkip(t)
	const scene = `<!DOCTYPE html><html><head><style>
  body{margin:0;background:#0b0b0d;height:100vh;overflow:hidden}
  .box{position:absolute;top:100px;left:0;width:60px;height:60px;background:#E86C1A}
</style></head><body><div class="box"></div>
<script>
  const box = document.querySelector('.box');
  itan.frame(({frame, fps}) => {
    box.style.left = (200 * spring({frame, fps, config:{damping: 12, stiffness: 120}})) + 'px';
  });
</script></body></html>`

	// Same damped-harmonic solution, stated independently in Go.
	want := func(frame int) float64 {
		const damping, stiffness, mass = 12.0, 120.0, 1.0
		tt := float64(frame) / 30
		w0 := math.Sqrt(stiffness / mass)
		zeta := damping / (2 * math.Sqrt(stiffness*mass))
		wd := w0 * math.Sqrt(1-zeta*zeta)
		return 200 * (1 - math.Exp(-zeta*w0*tt)*(math.Cos(wd*tt)+(zeta*w0/wd)*math.Sin(wd*tt)))
	}

	// Reached only by jumping straight to each frame: no simulation to
	// accumulate error, so late frames are as accurate as early ones.
	order := []int{45, 2, 30, 7, 60, 15}
	got := probe(t, scene, 30, 61, order, readLeft)
	if e := maxErr(got, want, order); e > 0.5 {
		t.Errorf("spring deviates from the closed-form solution by %.3fpx", e)
	} else {
		t.Logf("spring: max deviation %.4fpx at frames %v (no stepping)", e, order)
	}
}

// A scene callback that throws must not take the render down with it: the
// engine keeps stepping and the rest of the composition still renders.
func TestFrameAPIBrokenSceneDoesNotStall(t *testing.T) {
	chromeOrSkip(t)
	const scene = `<!DOCTYPE html><html><head><style>
  body{margin:0;background:#0b0b0d;height:100vh;overflow:hidden}
  .box{position:absolute;top:100px;left:0;width:60px;height:60px;background:#E86C1A}
</style></head><body><div class="box"></div>
<script>
  itan.frame(() => { missingGlobal.boom(); });
  const box = document.querySelector('.box');
  itan.frame(({frame}) => { box.style.left = interpolate(frame, [0, 10], [0, 100]) + 'px'; });
</script></body></html>`

	order := []int{0, 5, 10}
	got := probe(t, scene, 30, 11, order, readLeft)
	if got[2] <= got[0] {
		t.Fatalf("healthy scene stopped animating after a sibling threw: %v", got)
	}
}

// edgeEnergy is a sharpness proxy: RMS luma gradient over the region where
// the content actually is. Crisp type has high edge energy; type that was
// composed small and scaled up has low. Measuring the whole frame would
// average the answer away against the flat background, so it takes a crop.
func edgeEnergy(t *testing.T, pngPath string) float64 {
	t.Helper()
	img := decodePNG(t, pngPath)
	b := img.Bounds()
	// Centre crop: the middle half horizontally, middle 60% vertically —
	// where a centred composition puts its logo and type.
	x0, x1 := b.Min.X+b.Dx()/4, b.Min.X+3*b.Dx()/4
	y0, y1 := b.Min.Y+b.Dy()/5, b.Min.Y+4*b.Dy()/5
	lum := func(x, y int) float64 {
		r, g, bl, _ := img.At(x, y).RGBA()
		return 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
	}
	var sum float64
	var n int
	for y := y0 + 1; y < y1-1; y++ {
		for x := x0 + 1; x < x1-1; x++ {
			gx, gy := lum(x+1, y)-lum(x-1, y), lum(x, y+1)-lum(x, y-1)
			sum += gx*gx + gy*gy
			n++
		}
	}
	return math.Sqrt(sum / float64(n))
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// pixelDiff is the mean per-channel absolute difference between two frames.
func pixelDiff(t *testing.T, a, b string) float64 {
	t.Helper()
	ia, ib := decodePNG(t, a), decodePNG(t, b)
	if ia.Bounds() != ib.Bounds() {
		t.Fatalf("frame sizes differ: %v vs %v", ia.Bounds(), ib.Bounds())
	}
	var sum float64
	var n int
	for y := ia.Bounds().Min.Y; y < ia.Bounds().Max.Y; y++ {
		for x := ia.Bounds().Min.X; x < ia.Bounds().Max.X; x++ {
			r1, g1, b1, _ := ia.At(x, y).RGBA()
			r2, g2, b2, _ := ib.At(x, y).RGBA()
			sum += math.Abs(float64(r1>>8)-float64(r2>>8)) +
				math.Abs(float64(g1>>8)-float64(g2>>8)) +
				math.Abs(float64(b1>>8)-float64(b2>>8))
			n += 3
		}
	}
	return sum / float64(n)
}

// TestFullResolutionOutput renders at delivery resolution — the check whose
// absence let a soft, logo-less render ship. It asserts what that output
// failed: type stays crisp when the composition is authored at 1920x1080, a
// local logo file actually reaches the frame, and authoring the same scene at
// 720p and upscaling measurably does not hold up.
func TestFullResolutionOutput(t *testing.T) {
	chromeOrSkip(t)
	logo, err := filepath.Abs(filepath.Join("..", "..", "assets", "icon", "itan.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logo); err != nil {
		t.Skip("app icon not present")
	}

	// One composition, sized in fractions of the canvas, so the only
	// difference between renders is the resolution it was authored at.
	scene := func(w, h int, withLogo bool) string {
		img := ""
		if withLogo {
			img = fmt.Sprintf(`<img src="file://%s" alt="">`, logo)
		}
		return fmt.Sprintf(`<!DOCTYPE html><html><head><style>
  body{margin:0;background:#0b0b0d;width:%dpx;height:%dpx;overflow:hidden;
       display:flex;flex-direction:column;align-items:center;justify-content:center;gap:%dpx}
  img{width:%dpx;height:%dpx}
  h1{margin:0;color:#fff;font:700 %dpx 'Bricolage Grotesque',sans-serif;letter-spacing:-0.02em}
  p{margin:0;color:#8a8a94;font:500 %dpx 'IBM Plex Mono',monospace}
</style></head><body>
  %s
  <h1>ìtàn</h1><p>agentic video editing</p>
<script>
  const h = document.querySelector('h1');
  itan.frame(({frame}) => { h.style.opacity = interpolate(frame, [0, 8], [0, 1]); });
</script></body></html>`, w, h, h/18, h/6, h/6, h/9, h/34, img)
	}

	// ITAN_KEEP=<dir> leaves the rendered frames behind to be looked at —
	// the metric says "sharp", but a person still has to be able to check.
	dir := t.TempDir()
	if keep := os.Getenv("ITAN_KEEP"); keep != "" {
		dir = keep
	}
	// Every variant is delivered as 1080p; only the authoring canvas differs.
	shot := func(name string, w, h int, withLogo bool) string {
		video := filepath.Join(dir, name+".mp4")
		if err := Render(context.Background(), Opts{
			HTML: scene(w, h, withLogo), Width: w, Height: h, FPS: 24, Duration: 0.5, OutPath: video,
		}); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		frame := filepath.Join(dir, name+".png")
		cmd := exec.Command("ffmpeg", "-y", "-ss", "0.4", "-i", video, "-frames:v", "1",
			"-vf", "scale=1920:1080:flags=lanczos", frame)
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("extract %s: %v\n%s", name, err, raw)
		}
		return frame
	}

	native := shot("native1080", 1920, 1080, true)
	upscaled := shot("authored720", 1280, 720, true)

	sharp, soft := edgeEnergy(t, native), edgeEnergy(t, upscaled)

	// The reference for "blurry" is the failure that actually shipped: a
	// 1080p title card joined onto a small clip, which concat used to
	// resolve by scaling everything down to the small one. Round-tripping
	// through 320x180 reproduces it, and gives the sharpness check a
	// self-calibrating floor instead of a magic constant.
	degraded := filepath.Join(dir, "degraded.png")
	cmd := exec.Command("ffmpeg", "-y", "-i", native,
		"-vf", "scale=320:180:flags=lanczos,scale=1920:1080:flags=lanczos", degraded)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("degrade: %v\n%s", err, raw)
	}
	ruined := edgeEnergy(t, degraded)

	t.Logf("edge energy — 1920x1080: %.1f | authored 1280x720, delivered 1080p: %.1f (%.2fx) | forced through 320x180: %.1f (%.2fx)",
		sharp, soft, sharp/soft, ruined, sharp/ruined)
	if sharp < ruined*2 {
		t.Errorf("a native 1080p render (%.1f) is not meaningfully sharper than one squeezed through 320x180 (%.1f) — the engine is losing detail", sharp, ruined)
	}
	// Authoring smaller is nearly free because the engine supersamples 2x
	// before downscaling; if that ever stops being true, this notices.
	if sharp/soft > 1.5 {
		t.Errorf("authoring at 720p now costs %.2fx sharpness — supersampling is no longer covering it, and the guidance needs to say so", sharp/soft)
	}

	// The logo must actually be in the frame. Rendering the same scene with
	// the <img> removed and requiring the frames to differ is unambiguous: a
	// file:// image that silently failed to load would produce no difference.
	blank := shot("nologo1080", 1920, 1080, false)
	if d := pixelDiff(t, native, blank); d < 0.5 {
		t.Errorf("frame is unchanged with the logo removed (diff %.3f) — the file:// image never rendered", d)
	} else {
		t.Logf("local file:// logo contributes %.2f mean channel difference — it is in the frame", d)
	}
}

// The frame API travels with every composition — a scene that never uses it
// pays a few kilobytes, and one that does never has to import anything.
func TestFrameAPIAlwaysInjected(t *testing.T) {
	out := injectFrameAPI("<html><head><title>x</title></head><body></body></html>")
	if !strings.Contains(out, "data-itan-frameapi") {
		t.Fatal("frame API not injected")
	}
	if strings.Index(out, "data-itan-frameapi") > strings.Index(out, "<title>") {
		t.Fatal("frame API must be injected at the top of head, before scene scripts")
	}
	if !strings.Contains(injectFrameAPI("<div>headless fragment</div>"), "data-itan-frameapi") {
		t.Fatal("frame API not injected into a document without <head>")
	}
}
