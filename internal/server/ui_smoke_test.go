package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	assets := func() int {
		resp, err := http.Get(srv.URL + "/api/state")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var st struct {
			Assets []any `json:"assets"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&st)
		return len(st.Assets)
	}

	var dlgVisible bool
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#projChip", chromedp.ByID),

		// Project switcher opens inside the viewport.
		chromedp.Click("#projChip", chromedp.ByID),
		chromedp.WaitVisible("#projDlg", chromedp.ByID),
		chromedp.Evaluate(`(()=>{const r=document.querySelector('#projDlg').getBoundingClientRect();
			return r.top>=0&&r.left>=0&&r.bottom<=innerHeight&&r.right<=innerWidth&&r.width>100})()`, &dlgVisible),
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
	if got := assets(); got != 1 {
		t.Fatalf("after demo: %d assets, want 1", got)
	}

	// Remove the source via ✕ (confirm auto-accepted) → empty state returns.
	err = chromedp.Run(ctx,
		chromedp.Click(".step .rmx", chromedp.ByQuery),
		chromedp.WaitVisible("#empty", chromedp.ByID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := assets(); got != 0 {
		t.Fatalf("after remove: %d assets, want 0", got)
	}
}
