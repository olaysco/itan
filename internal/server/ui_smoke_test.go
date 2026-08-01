package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/olaysco/itan/internal/browser"
	"github.com/olaysco/itan/internal/cli"
	"github.com/olaysco/itan/internal/media"
)

// TestUISmoke drives the real UI in a real Chromium: loads the page, opens
// the project switcher (asserting the dialog is actually inside the
// viewport — it once rendered off-screen), generates the demo clip, and
// removes it via the source card's ✕. Curl can't catch what this catches.
func TestUISmoke(t *testing.T) {
	chrome, err := browser.Find()
	if err != nil {
		t.Skip("no Chromium-family browser:", err)
	}
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}

	session, err := cli.NewSession(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(session).Handler())
	defer srv.Close()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.WindowSize(1600, 1000),
	)
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	ctx, cancelC := chromedp.NewContext(actx)
	defer cancelC()
	ctx, cancelT := context.WithTimeout(ctx, 60*time.Second)
	defer cancelT()

	// The remove flow uses confirm(); accept dialogs automatically.
	chromedp.ListenTarget(ctx, func(ev any) {
		if _, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			go func() {
				_ = chromedp.Run(ctx, page.HandleJavaScriptDialog(true))
			}()
		}
	})

	state := func() (assets, ops int, current string) {
		resp, err := http.Get(srv.URL + "/api/state")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var st struct {
			Assets  []any  `json:"assets"`
			Ops     []any  `json:"ops"`
			Current string `json:"current"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&st)
		return len(st.Assets), len(st.Ops), st.Current
	}

	var dlgVisible bool
	var pathVal string
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#projChip", chromedp.ByID),

		// Project switcher opens inside the viewport.
		chromedp.Click("#projChip", chromedp.ByID),
		chromedp.WaitVisible("#projDlg", chromedp.ByID),
		chromedp.Evaluate(`(()=>{const r=document.querySelector('#projDlg').getBoundingClientRect();
			return r.top>=0&&r.left>=0&&r.bottom<=innerHeight&&r.right<=innerWidth&&r.width>100})()`, &dlgVisible),

		// Typing a path — including "/" — must land in the field, not get
		// hijacked by the slash-focuses-chat shortcut.
		chromedp.Click("#projPath", chromedp.ByID),
		chromedp.SendKeys("#projPath", "/tmp/x", chromedp.ByID),
		chromedp.Value("#projPath", &pathVal, chromedp.ByID),
		chromedp.Click("#projWrap .scrimClear", chromedp.ByQuery),

		// Demo clip loads into the strip.
		chromedp.Click("#demoBtn", chromedp.ByID),
		chromedp.WaitVisible(".step .rmx", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !dlgVisible {
		t.Fatal("project dialog is not fully inside the viewport")
	}
	if pathVal != "/tmp/x" {
		t.Fatalf("path field got %q — slashes must not be hijacked by the chat shortcut", pathVal)
	}
	if a, _, _ := state(); a != 1 {
		t.Fatalf("after demo: %d assets, want 1", a)
	}

	// Make one edit via the gesture endpoint (no LLM) so the strip has a step.
	resp, err := http.Post(srv.URL+"/api/tool", "application/json",
		strings.NewReader(`{"name":"trim","args":{"start":0,"end":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("gesture trim: %d %s", resp.StatusCode, body)
	}
	_, opsN, curAfterTrim := state()
	if opsN != 1 || curAfterTrim == "" {
		t.Fatalf("after trim: ops=%d current=%q", opsN, curAfterTrim)
	}

	// ✕ on the newest edit step removes it and moves CURRENT back (undo
	// semantics). The step card is the last one in the strip.
	err = chromedp.Run(ctx,
		chromedp.Reload(),
		chromedp.WaitVisible(".step:last-child .rmx", chromedp.ByQuery),
		chromedp.Click(".step:last-child .rmx", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { a, o, _ := state(); return a == 1 && o == 0 })
	if _, _, cur := state(); strings.Contains(cur, "-trim") {
		t.Fatalf("current did not move back after step removal: %q", cur)
	}

	// ✕ on the source unregisters it (confirm auto-accepted); with nothing
	// left, the empty state returns.
	err = chromedp.Run(ctx,
		chromedp.Click(".step .rmx", chromedp.ByQuery),
		chromedp.WaitVisible("#empty", chromedp.ByID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a, o, _ := state(); a != 0 || o != 0 {
		t.Fatalf("after remove: assets=%d ops=%d, want 0/0", a, o)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}
