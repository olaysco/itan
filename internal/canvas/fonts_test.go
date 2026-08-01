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
	// Headless documents without <head> still get fonts.
	if !strings.HasPrefix(injectFonts("<p>x</p>"), "<style data-itan-fonts>") {
		t.Fatal("no-head fallback broken")
	}
}
