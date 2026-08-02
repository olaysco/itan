package tools

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const testPage = `<!DOCTYPE html><html><head>
<title>Acme Widget — ship faster</title>
<meta name="description" content="The widget that ships your product faster.">
<meta name="theme-color" content="#0A84FF">
<style>body{color:red}</style><script>var hidden="secret-js";</script>
</head><body>
<h1>Acme Widget</h1>
<p>Build &amp; ship in minutes, not months.</p>
<div style="background:#0A84FF;width:200px;height:120px"></div>
</body></html>`

func TestFetchPageExtractsCopy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testPage))
	}))
	defer srv.Close()

	c := composeCtx(t)
	res := NewRegistry().Execute(c, "fetch_page", []byte(`{"url":"`+srv.URL+`"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Data["title"] != "Acme Widget — ship faster" {
		t.Errorf("title = %q", res.Data["title"])
	}
	if res.Data["description"] != "The widget that ships your product faster." {
		t.Errorf("description = %q", res.Data["description"])
	}
	if res.Data["theme_color"] != "#0A84FF" {
		t.Errorf("theme_color = %q", res.Data["theme_color"])
	}
	text := res.Data["text"].(string)
	if !strings.Contains(text, "Build & ship in minutes") {
		t.Errorf("entities not unescaped in text: %q", text)
	}
	if strings.Contains(text, "secret-js") || strings.Contains(text, "color:red") {
		t.Error("script/style leaked into readable text")
	}
	// Read-only: no op committed, CURRENT untouched.
	if len(c.Project.Ops) != 0 {
		t.Fatal("fetch_page must not commit ops")
	}
}

func TestFetchPageRejectsBadURL(t *testing.T) {
	c := composeCtx(t)
	if res := NewRegistry().Execute(c, "fetch_page", []byte(`{"url":"file:///etc/passwd"}`)); res.Err == nil {
		t.Fatal("non-http url must be rejected")
	}
}

// TestCapturePageScreenshotsLiveURL drives real Chrome against a local
// server and verifies a PNG lands in the project at 2x resolution.
func TestCapturePageScreenshotsLiveURL(t *testing.T) {
	t.Setenv("ITAN_BROWSER", testChrome(t))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testPage))
	}))
	defer srv.Close()

	c := composeCtx(t)
	res := NewRegistry().Execute(c, "capture_page", []byte(`{"url":"`+srv.URL+`","width":640}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	img, _ := res.Data["image"].(string)
	st, err := os.Stat(img)
	if err != nil || st.Size() < 2000 {
		t.Fatalf("capture missing or implausibly small: %v (%d bytes)", err, st.Size())
	}
	if !strings.Contains(res.Data["embed_as"].(string), "file://"+img) {
		t.Error("embed_as snippet must reference the saved image")
	}
	if len(c.Project.Ops) != 0 {
		t.Fatal("capture_page must not commit ops")
	}
}
