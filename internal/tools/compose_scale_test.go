package tools

import (
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// The first version of this test asserted that "scale" was in the schema and
// that the render succeeded. Both passed while runCompose never read the
// argument at all — every composition rendered at scale 2, three times slower
// than asked for, silently. A test that cannot fail is not a test: this one
// checks the value the engine was actually given.
func TestComposeHonoursScale(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)
	r := NewRegistry()
	const html = `{"html":"<html><body style=\"background:#123\"></body></html>",
	  "duration":0.4,"width":320,"height":240,"fps":5`

	res := r.Execute(c, "compose", []byte(html+`,"scale":1}`))
	if res.Err != nil {
		t.Fatalf("scale=1: %v", res.Err)
	}
	if got := res.Data["scale"]; got != 1 {
		t.Errorf("asked for scale 1, the render used %v", got)
	}

	res = r.Execute(c, "compose", []byte(html+`,"scale":3}`))
	if res.Err != nil {
		t.Fatalf("scale=3: %v", res.Err)
	}
	if got := res.Data["scale"]; got != 3 {
		t.Errorf("asked for scale 3, the render used %v", got)
	}

	// Omitted means the engine's default, and the report must say so rather
	// than claiming 0.
	res = r.Execute(c, "compose", []byte(html+`}`))
	if res.Err != nil {
		t.Fatalf("default scale: %v", res.Err)
	}
	if got := res.Data["scale"]; got != 2 {
		t.Errorf("default scale reported as %v, want 2", got)
	}

	// Whatever the scale, the delivered size must not move.
	path, _ := res.Data["file"].(string)
	info, err := media.Probe(c.Context, path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("delivered %dx%d, want 320x240", info.Width, info.Height)
	}
}

// The cost of a render has to come back with it. A model that cannot see
// what a composition cost cannot make the next one cheaper.
func TestComposeReportsRenderCost(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)
	res := NewRegistry().Execute(c, "compose", []byte(`{"html":"<html><body></body></html>",
	  "duration":0.4,"width":320,"height":240,"fps":5,"scale":1}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	line, _ := res.Data["render"].(string)
	for _, want := range []string{"2 frames", "320x240", "scale 1", "s/frame"} {
		if !contains(line, want) {
			t.Errorf("render report %q is missing %q", line, want)
		}
	}
	if secs, ok := res.Data["seconds"].(float64); !ok || secs <= 0 {
		t.Errorf("render seconds not reported: %v", res.Data["seconds"])
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
