package canvas

import (
	"strings"
	"testing"
)

// The real case: a composition that pulled GSAP from a CDN. It happened to
// survive because GSAP is bundled — any other library would have rendered a
// video where nothing moves, and the render would have reported success.
func TestStripExternalRemovesDeadSubresources(t *testing.T) {
	html := `<!DOCTYPE html><html><head>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">
<style>body{background:#111}</style>
</head><body><h1>hi</h1>
<script src="https://cdnjs.cloudflare.com/ajax/libs/gsap/3.12.2/gsap.min.js"></script>
<script>gsap.to('h1',{x:100})</script>
</body></html>`

	out, urls := StripExternal(html)
	if strings.Contains(out, "cdnjs.cloudflare.com") || strings.Contains(out, "fonts.googleapis.com") {
		t.Errorf("external tags survived:\n%s", out)
	}
	if !strings.Contains(out, "gsap.to('h1'") {
		t.Error("the scene's own inline script was removed")
	}
	if !strings.Contains(out, "background:#111") {
		t.Error("inline styles were removed")
	}
	if len(urls) != 2 {
		t.Fatalf("reported %d urls: %v", len(urls), urls)
	}
	note := ExternalNote(urls)
	for _, want := range []string{"2 external URLs", "cdnjs", "fonts.googleapis", "file:///", "gsap"} {
		if !strings.Contains(note, want) {
			t.Errorf("note is missing %q: %s", want, note)
		}
	}
}

// Local and inline references must be untouched: file:// is how a project's
// own logo and screenshots get into a composition.
func TestStripExternalLeavesLocalReferences(t *testing.T) {
	html := `<html><head><style>@font-face{src:url(data:font/woff2;base64,AAA)}</style></head>
<body><img src="file:///Users/x/logo.svg"><img src="data:image/png;base64,iVBOR">
<script>const a=1</script></body></html>`
	out, urls := StripExternal(html)
	if len(urls) != 0 {
		t.Errorf("local references reported as external: %v", urls)
	}
	if out != html {
		t.Error("a composition with no network references was modified")
	}
	if ExternalNote(nil) != "" {
		t.Error("a note was produced with nothing to report")
	}
}

// An external image cannot be stripped without changing the layout, but the
// author still has to be told it will not appear.
func TestStripExternalReportsImagesItCannotFix(t *testing.T) {
	html := `<html><body><img src="https://example.com/hero.jpg"></body></html>`
	out, urls := StripExternal(html)
	if len(urls) != 1 || urls[0] != "https://example.com/hero.jpg" {
		t.Fatalf("urls = %v", urls)
	}
	if !strings.Contains(out, "hero.jpg") {
		t.Error("the img tag was removed, which would silently change the layout")
	}
	if !strings.Contains(ExternalNote(urls), "1 external URL (") {
		t.Errorf("singular not handled: %s", ExternalNote(urls))
	}
}
