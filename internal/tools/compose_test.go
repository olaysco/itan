package tools

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
)

// testChrome resolves a Chromium binary for e2e composition tests, honoring
// ITAN_BROWSER and the Playwright cache used in CI sandboxes.
func testChrome(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ITAN_BROWSER"); p != "" {
		return p
	}
	for _, cand := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(cand); err == nil {
			return p
		}
	}
	if _, err := os.Stat("/opt/pw-browsers/chromium-1194/chrome-linux/chrome"); err == nil {
		return "/opt/pw-browsers/chromium-1194/chrome-linux/chrome"
	}
	t.Skip("no chromium available for compose e2e")
	return ""
}

func composeCtx(t *testing.T) *Ctx {
	t.Helper()
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	proj, err := media.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &Ctx{Context: context.Background(), Project: proj, Config: config.Default()}
}

// TestComposeRendersHTMLToAsset drives the full native pipeline: HTML in,
// Chrome frames, ffmpeg encode, asset registered with correct dimensions.
func TestComposeRendersHTMLToAsset(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)
	r := NewRegistry()

	html := `<!DOCTYPE html><html><head><style>
	  body{margin:0;background:#123;display:flex;align-items:center;justify-content:center;height:100vh}
	  h1{color:#fff;font:700 60px sans-serif;animation:in 1s ease both}
	  @keyframes in{from{opacity:0;transform:translateY(30px)}to{opacity:1}}
	</style></head><body><h1 data-start="0.2">Ship Faster</h1></body></html>`

	res := r.Execute(c, "compose", []byte(`{"html":`+jsonString(html)+`,"duration":1,"width":320,"height":240,"fps":8}`))
	if res.Err != nil {
		t.Fatalf("compose: %v", res.Err)
	}
	if len(c.Project.Assets) != 1 {
		t.Fatalf("expected 1 registered asset, got %d", len(c.Project.Assets))
	}
	a := c.Project.Assets[0]
	if a.Info.Width != 320 || a.Info.Height != 240 {
		t.Fatalf("asset dims = %dx%d", a.Info.Width, a.Info.Height)
	}
	if a.Info.Duration < 0.8 || a.Info.Duration > 1.4 {
		t.Fatalf("asset duration = %.2fs, want ~1s", a.Info.Duration)
	}
	// The HTML source must sit next to the render for later inspection.
	htmlPath := strings.TrimSuffix(a.Path, ".mp4") + ".html"
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatal("composition HTML not preserved next to the render")
	}
	// compose must NOT advance CURRENT (it creates material, not an edit)…
	if len(c.Project.Ops) != 0 {
		t.Fatalf("compose committed an op: %+v", c.Project.Ops)
	}
}

func TestComposeValidation(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()
	if res := r.Execute(c, "compose", []byte(`{"html":"","duration":3}`)); res.Err == nil {
		t.Fatal("empty html must fail")
	}
	if res := r.Execute(c, "compose", []byte(`{"html":"<p>x</p>","duration":0}`)); res.Err == nil {
		t.Fatal("zero duration must fail")
	}
	if res := r.Execute(c, "compose", []byte(`{"html":"<p>x</p>","duration":500}`)); res.Err == nil {
		t.Fatal("duration over cap must fail")
	}
}

// TestOverlayVideoComposites overlays a small clip onto a larger one and
// verifies dimensions survive and the op commits.
func TestOverlayVideoComposites(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()

	main := c.Project.Dir + "/main.mp4"
	small := c.Project.Dir + "/small.mp4"
	mk := func(path, size string) {
		cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size="+size+":rate=10",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("clip: %v\n%s", err, out)
		}
	}
	mk(main, "640x360")
	mk(small, "160x90")
	if _, err := c.Project.AddAsset(context.Background(), main); err != nil {
		t.Fatal(err)
	}

	res := r.Execute(c, "overlay_video", []byte(`{"overlay":"`+small+`","start":0.5,"end":1.5,"x":10,"y":10}`))
	if res.Err != nil {
		t.Fatalf("overlay_video: %v", res.Err)
	}
	if len(c.Project.Ops) != 1 {
		t.Fatal("overlay_video must commit an op")
	}
	info, err := media.Probe(context.Background(), c.Project.Current)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 640 || info.Height != 360 {
		t.Fatalf("dims changed: %dx%d", info.Width, info.Height)
	}
	if !info.HasAudio {
		t.Fatal("main audio track lost")
	}
}

// jsonString is a minimal JSON string encoder for test payloads.
func jsonString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
