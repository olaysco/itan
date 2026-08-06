package tools

import (
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// compose must be able to ask for cheaper frames: without this the render
// cost is fixed, and a long vertical piece is impractical on a slow machine.
func TestComposeAcceptsScale(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)
	r := NewRegistry()

	res := r.Execute(c, "compose", []byte(`{"html":"<html><body style=\"background:#123\"></body></html>",
	  "duration":0.4,"width":320,"height":240,"fps":5,"scale":1}`))
	if res.Err != nil {
		t.Fatalf("compose with scale=1: %v", res.Err)
	}
	path, _ := res.Data["file"].(string)
	info, err := media.Probe(c.Context, path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("scale changed the delivered size: %dx%d", info.Width, info.Height)
	}

	tool, _ := r.Get("compose")
	props, _ := tool.Schema["properties"].(map[string]any)
	if _, ok := props["scale"]; !ok {
		t.Fatal("scale is not in the schema, so a model can never reach it")
	}
}
