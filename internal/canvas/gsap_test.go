package canvas

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A GSAP-driven scene must animate under the frame stepper (GSAP uses rAF +
// inline styles, invisible to document.getAnimations) and stay deterministic
// across renders — the same contract the golden test enforces for CSS.
const gsapHTML = `<!DOCTYPE html><html><head><style>
  body{margin:0;background:#101014;height:100vh;display:flex;align-items:center;justify-content:center;overflow:hidden}
  .box{width:120px;height:120px;background:#E86C1A;border-radius:16px}
  h1{position:absolute;color:#fff;font:700 40px monospace;opacity:0}
</style></head><body><div class="box"></div><h1>ìtàn</h1>
<script>
  const tl = gsap.timeline();
  tl.fromTo('.box',{x:-140,rotation:-12},{x:140,rotation:12,duration:1.6,ease:'power3.out'})
    .to('h1',{opacity:1,duration:0.6,ease:'power2.out'},0.4);
</script></body></html>`

func TestGSAPSceneDeterministic(t *testing.T) {
	chromeOrSkip(t)
	ctx := context.Background()
	dir := t.TempDir()

	render := func(name string) string {
		out := filepath.Join(dir, name)
		if err := Render(ctx, Opts{
			HTML: gsapHTML, Width: 320, Height: 240, FPS: 10, Duration: 2, OutPath: out,
		}); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		return out
	}
	a := render("a.mp4")
	b := render("b.mp4")

	for _, ts := range []float64{0.8, 1.9} {
		fa := extractFrame(t, a, ts)
		fb := extractFrame(t, b, ts)
		if d := meanAbsDiff(fa, fb); d > 1.0 {
			t.Errorf("gsap frame at %.1fs differs between renders: %.2f", ts, d)
		}
	}
	f0 := extractFrame(t, a, 0.05)
	f1 := extractFrame(t, a, 1.9)
	if d := meanAbsDiff(f0, f1); d < 1.0 {
		t.Errorf("gsap animation did not advance (diff %.2f) — the seek runtime is not driving the timeline", d)
	}
}

// Compositions that never mention gsap must not carry the library.
func TestGSAPInjectedOnlyWhenReferenced(t *testing.T) {
	plain := injectGSAP("<html><head></head><body><p>hi</p></body></html>")
	if strings.Contains(plain, "data-itan-gsap") {
		t.Fatal("gsap injected into a composition that never references it")
	}
	scenic := injectGSAP(`<html><head></head><body><script>gsap.to('.x',{x:1})</script></body></html>`)
	if !strings.Contains(scenic, "data-itan-gsap") || !strings.Contains(scenic, "SplitText") {
		t.Fatal("gsap+SplitText not injected for a gsap scene")
	}
}
