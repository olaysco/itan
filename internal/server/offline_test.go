package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The interface must not reach the network to look like itself. It used to
// pull its typefaces from Google Fonts while carrying the identical files in
// its own binary, so offline — or behind a firewall, or on a plane — the
// product fell back to system fonts.
func TestUIHasNoExternalResources(t *testing.T) {
	page, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// Any src/href on http(s). Data URIs and same-origin paths are fine.
	ext := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["'](https?://[^"']+)["']`)
	if m := ext.FindAllStringSubmatch(string(page), -1); len(m) > 0 {
		var urls []string
		for _, g := range m {
			urls = append(urls, g[1])
		}
		t.Errorf("the UI loads %d external resource(s): %s", len(urls), strings.Join(urls, ", "))
	}
	// url(...) inside CSS reaches the network just as readily.
	if strings.Contains(strings.ToLower(string(page)), "url(http") {
		t.Error("the UI has a CSS url() pointing at the network")
	}
}

// The fonts the page now asks for must actually be served.
func TestFontsAreServedFromTheBinary(t *testing.T) {
	s := testServer(t)
	h := s.Handler()
	for _, name := range []string{
		"BricolageGrotesque.ttf", "IBMPlexMono-Regular.ttf", "IBMPlexMono-Bold.ttf",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/fonts/"+name, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("/fonts/%s: HTTP %d", name, rec.Code)
			continue
		}
		body, _ := io.ReadAll(rec.Body)
		if len(body) < 10_000 {
			t.Errorf("/fonts/%s served only %d bytes", name, len(body))
		}
	}
	// Every @font-face the page declares must resolve.
	page, _ := uiFS.ReadFile("ui/index.html")
	for _, m := range regexp.MustCompile(`url\('(/fonts/[^']+)'\)`).FindAllStringSubmatch(string(page), -1) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", m[1], nil))
		if rec.Code != http.StatusOK {
			t.Errorf("the page asks for %s and the server answers %d", m[1], rec.Code)
		}
	}
}
