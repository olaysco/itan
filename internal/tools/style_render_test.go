package tools

import (
	"strings"
	"testing"
)

// The claim is that a scene inherits the project's design without asking for
// it, and can still override any rule. Both halves are checked in a real
// render: the kit paints the ground, and the scene's own colour wins where
// it disagrees.
func TestStyleKitReachesTheRenderedScene(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	c := composeCtx(t)
	r := NewRegistry()

	res := r.Execute(c, "style_kit", []byte(`{"brief":"Kit test.",
	  "css":"body{background:#123456}\n.tile{width:100px;height:100px;background:#22D07A}"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}

	// The scene never mentions a background or a tile colour — it relies on
	// the kit entirely.
	res = r.Execute(c, "compose", []byte(`{"html":"<html><head></head><body><div class=\"tile\"></div></body></html>",
	  "duration":0.4,"width":160,"height":120,"fps":5,"scale":1}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	src, _ := res.Data["html"].(string)
	body := readFile(t, src)
	// The saved source is what the model wrote; the kit is injected at render
	// time, so the source must stay clean and re-editable.
	if strings.Contains(body, "data-itan-kit") {
		t.Error("the injected kit was written into the saved composition source")
	}
	inherited, _ := res.Data["file"].(string)
	if !frameHasColour(t, inherited, 0x12, 0x34, 0x56) {
		t.Error("the kit's ground colour never reached the frame")
	}
	if !frameHasColour(t, inherited, 0x22, 0xD0, 0x7A) {
		t.Error("the kit's .tile rule never reached the frame")
	}

	// Now a scene that disagrees: its own style must win.
	res = r.Execute(c, "compose", []byte(`{"html":"<html><head><style>body{background:#FF0000}</style></head><body></body></html>",
	  "duration":0.4,"width":160,"height":120,"fps":5,"scale":1}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	overridden, _ := res.Data["file"].(string)
	if !frameHasColour(t, overridden, 0xFF, 0x00, 0x00) {
		t.Error("the scene could not override the kit — the kit is injected too late")
	}
	if frameHasColour(t, overridden, 0x12, 0x34, 0x56) {
		t.Error("the kit's ground survived a scene that overrode it")
	}
}
