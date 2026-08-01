package canvas

import (
	"strings"
	"testing"
)

func TestFontInjection(t *testing.T) {
	css := fontFaceCSS()
	for _, family := range []string{"Bricolage Grotesque", "IBM Plex Mono"} {
		if !strings.Contains(css, "'"+family+"'") {
			t.Errorf("embedded @font-face missing %s", family)
		}
	}
	if !strings.Contains(css, "data:font/ttf;base64,") {
		t.Error("fonts must be inlined as data URIs (renders are offline)")
	}

	// Injection goes right after <head> so author styles can override.
	out := injectFonts("<html><head><style>h1{}</style></head><body></body></html>")
	head := strings.Index(out, "<head>")
	fonts := strings.Index(out, "data-itan-fonts")
	author := strings.Index(out, "h1{}")
	if !(head < fonts && fonts < author) {
		t.Fatal("font style must land after <head> and before author styles")
	}
	// Headless documents without <head> still get charset + fonts, in order.
	if !strings.HasPrefix(injectFonts("<p>x</p>"), `<meta charset="utf-8"><style data-itan-fonts>`) {
		t.Fatal("no-head fallback broken")
	}
}

func TestCharsetGuarantee(t *testing.T) {
	// No charset in the source → the engine must declare UTF-8 itself,
	// before the ASCII font block that would starve Chrome's sniffer.
	out := injectFonts("<html><head><style>h1{}</style></head><body>ìtàn →</body></html>")
	meta := strings.Index(out, `<meta charset="utf-8">`)
	fonts := strings.Index(out, "data-itan-fonts")
	if meta < 0 || meta > fonts {
		t.Fatal("charset meta must be injected before the font block")
	}
	// An author-declared charset is respected, not duplicated.
	src := `<html><head><meta charset="utf-8"></head><body></body></html>`
	if strings.Count(injectFonts(src), "charset") != 1 {
		t.Fatal("existing charset declaration must not be duplicated")
	}
}
