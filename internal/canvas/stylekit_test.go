package canvas

import (
	"strings"
	"testing"
)

// The kit has to land after the fonts (so it can name a family) and before
// the scene's own styles (so the scene can still override any rule).
func TestStyleKitInjectionOrder(t *testing.T) {
	html := `<!DOCTYPE html><html><head><style>h1{color:red}</style></head><body><h1>x</h1></body></html>`
	out := injectStyleKit(injectFonts(html), `h1{color:blue}`)

	fonts := strings.Index(out, "data-itan-fonts")
	kit := strings.Index(out, "data-itan-kit")
	scene := strings.Index(out, "h1{color:red}")
	if fonts < 0 || kit < 0 || scene < 0 {
		t.Fatalf("something is missing:\n%s", out)
	}
	if !(fonts < kit && kit < scene) {
		t.Errorf("order is fonts=%d kit=%d scene=%d; want fonts < kit < scene", fonts, kit, scene)
	}
}

func TestStyleKitAbsentIsUntouched(t *testing.T) {
	html := "<html><head></head><body></body></html>"
	for _, css := range []string{"", "   ", "\n\t "} {
		if got := injectStyleKit(html, css); got != html {
			t.Errorf("an empty kit modified the document: %s", got)
		}
	}
}

// A fragment with no <head> still gets the kit rather than silently losing it.
func TestStyleKitWithoutHead(t *testing.T) {
	out := injectStyleKit("<div>scene</div>", "body{margin:0}")
	if !strings.Contains(out, "data-itan-kit") || !strings.Contains(out, "<div>scene</div>") {
		t.Fatalf("kit lost on a headless fragment: %s", out)
	}
}
