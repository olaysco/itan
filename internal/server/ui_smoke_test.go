package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

	opts := browser.AllocatorOptions(chrome, 1600, 1000)
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
		chromedp.WaitVisible(".step .rmx.rm", chromedp.ByQuery),
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
		chromedp.WaitVisible(".step:last-child .rmx.undo", chromedp.ByQuery),
		chromedp.Click(".step:last-child .rmx.undo", chromedp.ByQuery),
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
		chromedp.Click(".step .rmx.rm", chromedp.ByQuery),
		chromedp.WaitVisible("#empty", chromedp.ByID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a, o, _ := state(); a != 0 || o != 0 {
		t.Fatalf("after remove: assets=%d ops=%d, want 0/0", a, o)
	}
}

// TestProjectSwitchCritical drives project switching in a real browser and
// verifies it at the level that matters: which folder files actually land
// in, whose state each view shows, and what survives a reload.
func TestProjectSwitchCritical(t *testing.T) {
	chrome, err := browser.Find()
	if err != nil {
		t.Skip("no Chromium-family browser:", err)
	}
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}

	dirA := t.TempDir()
	dirB := filepath.Join(t.TempDir(), "projB") // created by the switcher itself
	session, err := cli.NewSession(dirA, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(session).Handler())
	defer srv.Close()

	opts := browser.AllocatorOptions(chrome, 1600, 1000)
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	ctx, cancelC := chromedp.NewContext(actx)
	defer cancelC()
	ctx, cancelT := context.WithTimeout(ctx, 90*time.Second)
	defer cancelT()

	chipText := func() string {
		var s string
		if err := chromedp.Run(ctx, chromedp.Text("#projChip", &s, chromedp.ByID)); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// Load project A and put a demo clip in it.
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#projChip", chromedp.ByID),
		chromedp.Click("#demoBtn", chromedp.ByID),
		chromedp.WaitVisible(".step", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirA, ".itan", "uploads", "demo.mp4")); err != nil {
		t.Fatalf("demo clip not in project A: %v", err)
	}

	// Switch to a NEW project B via the path field.
	err = chromedp.Run(ctx,
		chromedp.Click("#projChip", chromedp.ByID),
		chromedp.WaitVisible("#projDlg", chromedp.ByID),
		chromedp.SendKeys("#projPath", dirB, chromedp.ByID),
		chromedp.Click("#projOpen", chromedp.ByID),
		chromedp.WaitVisible("#empty", chromedp.ByID), // B is empty: empty state must return
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := chipText(); !strings.Contains(got, "projB") {
		t.Fatalf("chip shows %q after switching to projB", got)
	}

	// The switcher must mark B active now.
	var activeName string
	err = chromedp.Run(ctx,
		chromedp.Click("#projChip", chromedp.ByID),
		chromedp.WaitVisible("#projDlg", chromedp.ByID),
		chromedp.Poll(`(function(){const r=[...document.querySelectorAll('#projList .mrow')].find(r=>r.textContent.includes('✓'));return r?r.querySelector('.nm').textContent:false})()`,
			&activeName, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Click("#projWrap .scrimClear", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	if activeName != "projB" {
		t.Fatalf("switcher marks %q active, want projB", activeName)
	}

	// Work done now must land in B, not A.
	err = chromedp.Run(ctx,
		chromedp.Click("#demoBtn", chromedp.ByID),
		chromedp.WaitVisible(".step", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirB, ".itan", "uploads", "demo.mp4")); err != nil {
		t.Fatalf("demo clip not in project B after switch: %v", err)
	}

	// Reload: the server keeps the switched project; the page must follow.
	err = chromedp.Run(ctx,
		chromedp.Reload(),
		chromedp.WaitVisible("#projChip", chromedp.ByID),
		chromedp.WaitVisible(".step", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := chipText(); !strings.Contains(got, "projB") {
		t.Fatalf("chip shows %q after reload, want projB", got)
	}

	// Switch back to A: its clip must still be there.
	err = chromedp.Run(ctx,
		chromedp.Click("#projChip", chromedp.ByID),
		chromedp.WaitVisible("#projDlg", chromedp.ByID),
		chromedp.Evaluate(`[...document.querySelectorAll('#projList .mrow')].find(r=>!r.textContent.includes('✓')&&r.querySelector('.via').textContent.includes(`+"`"+filepath.Base(dirA)+"`"+`))?.click()`, nil),
		chromedp.WaitVisible(".step", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := stateOf(t, srv.URL)
	if a != 1 {
		t.Fatalf("project A lost its asset after round-trip: %d", a)
	}
}

func stateOf(t *testing.T, base string) (assets, ops int, current string) {
	t.Helper()
	resp, err := http.Get(base + "/api/state")
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

// Files dropped anywhere in the window must register — the chat column had
// no handler, which is exactly where people drop. Images and audio must be
// accepted too, and must not hijack CURRENT.
func TestDropAnywhereAcceptsAllMedia(t *testing.T) {
	chrome, err := browser.Find()
	if err != nil {
		t.Skip("no Chromium-family browser:", err)
	}
	if !media.Available() {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	session, err := cli.NewSession(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(session).Handler())
	defer srv.Close()

	png := filepath.Join(dir, "logo.png")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "color=orange:size=64x64:d=1",
		"-frames:v", "1", png).CombinedOutput(); err != nil {
		t.Fatalf("png: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(png)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)

	opts := browser.AllocatorOptions(chrome, 1600, 1000)
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	ctx, cancelC := chromedp.NewContext(actx)
	defer cancelC()
	ctx, cancelT := context.WithTimeout(ctx, 90*time.Second)
	defer cancelT()

	dropOnChat := `(() => {
	  const bin=atob("` + b64 + `"); const arr=new Uint8Array(bin.length);
	  for(let i=0;i<bin.length;i++)arr[i]=bin.charCodeAt(i);
	  const dt=new DataTransfer(); dt.items.add(new File([arr],'logo.png',{type:'image/png'}));
	  Object.defineProperty(dt,'types',{value:['Files']});
	  const el=document.querySelector('#chatcol');
	  el.dispatchEvent(new DragEvent('dragover',{dataTransfer:dt,bubbles:true,cancelable:true}));
	  el.dispatchEvent(new DragEvent('drop',{dataTransfer:dt,bubbles:true,cancelable:true}));
	  return 1;
	})()`

	var inputsFree bool
	var accept string
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#projChip", chromedp.ByID),
		// the pickers must never live inside the empty-state subtree, which
		// is display:none once footage loads
		chromedp.Evaluate(`(()=>{const a=document.querySelector('#fileInput'),b=document.querySelector('#assetInput');
			return !!a&&!!b&&!a.closest('#empty')&&!b.closest('#empty')})()`, &inputsFree),
		chromedp.Evaluate(`document.querySelector('#assetInput').accept`, &accept),
		chromedp.Evaluate(dropOnChat, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !inputsFree {
		t.Fatal("file pickers live inside the hidden empty-state subtree")
	}
	if !strings.Contains(accept, "image/") || !strings.Contains(accept, "audio/") {
		t.Fatalf("asset import must accept images and audio, got %q", accept)
	}
	waitFor(t, func() bool { a, _, _ := stateOf(t, srv.URL); return a == 1 })
	if _, _, cur := stateOf(t, srv.URL); cur != "" {
		t.Fatalf("an image must not become the working video: %q", cur)
	}
}
