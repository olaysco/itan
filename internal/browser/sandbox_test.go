package browser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// fakeChrome writes an executable stub that prints msg on stderr and exits
// with code, standing in for a browser we cannot make fail on demand.
func fakeChrome(t *testing.T, msg string, code int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chrome")
	script := "#!/bin/sh\ncat >&2 <<'ITANEOF'\n" + msg + "\nITANEOF\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// The two ways Chromium refuses to sandbox both abort in ZygoteHostImpl, and
// both have to drop the sandbox rather than fail the render. The first
// message is the one that broke CI on ubuntu-latest; the second is what a
// container prints. Anything else — a missing library, a truncated download —
// must leave the sandbox alone so the real error stays visible instead of
// being silently traded for a weaker browser.
func TestProbeReadsTheBrowsersOwnVerdict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs are POSIX")
	}
	cases := []struct {
		name    string
		stderr  string
		code    int
		sandbox bool
	}{
		{"userns denied on Ubuntu 23.10+, the CI failure",
			"[9883:9883:FATAL:content/browser/zygote_host/zygote_host_impl_linux.cc:129] No usable sandbox! If you are running on Ubuntu 23.10+ or another Linux distro that has disabled unprivileged user namespaces with AppArmor...", 1, false},
		{"running as root",
			"[1682:1682:ERROR:content/browser/zygote_host/zygote_host_impl_linux.cc:101] Running as root without --no-sandbox is not supported.", 1, false},
		{"healthy browser", "", 0, true},
		{"unrelated failure keeps the sandbox on",
			"error while loading shared libraries: libnss3.so: cannot open shared object file", 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeSandbox(fakeChrome(t, tc.stderr, tc.code)); got != tc.sandbox {
				t.Fatalf("probeSandbox = %v, want %v", got, tc.sandbox)
			}
		})
	}
}

// The probe launches a browser, so it must run once per binary and not once
// per render — a 200-frame composition would otherwise pay for it 200 times.
func TestSandboxVerdictIsCached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs are POSIX")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "runs")
	chrome := filepath.Join(dir, "chrome")
	if err := os.WriteFile(chrome, []byte("#!/bin/sh\necho x >> "+counter+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if !probeCached(chrome) {
			t.Fatal("a clean exit should leave the sandbox on")
		}
	}
	runs, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(runs), "x"); n != 1 {
		t.Fatalf("probe launched the browser %d times, want 1", n)
	}
}

// ITAN_SANDBOX is the escape hatch for a machine the probe reads wrongly, so
// it has to win without launching anything — note the browser path below does
// not exist. Empty means unset, not "off".
func TestSandboxEnvOverride(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("ITAN_SANDBOX", "0")
	if Sandboxed(missing) {
		t.Fatal("ITAN_SANDBOX=0 should force the sandbox off")
	}
	t.Setenv("ITAN_SANDBOX", "1")
	if !Sandboxed(missing) {
		t.Fatal("ITAN_SANDBOX=1 should force the sandbox on")
	}
}

// Appending an option to a slice proves nothing; what matters is whether
// --no-sandbox reaches Chrome's argv. So launch a stub browser through the
// allocator exactly as a render would and read back the command line it was
// given. chromedp never gets a DevTools endpoint from the stub and errors —
// by then the argv has been recorded, which is the whole question.
func TestAllocatorOptionsReachTheCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs are POSIX")
	}
	argvOf := func(sandbox string) string {
		t.Setenv("ITAN_SANDBOX", sandbox)
		dir := t.TempDir()
		argv := filepath.Join(dir, "argv")
		chrome := filepath.Join(dir, "chrome")
		if err := os.WriteFile(chrome, []byte("#!/bin/sh\necho \"$@\" > "+argv+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		actx, cancelA := chromedp.NewExecAllocator(ctx, AllocatorOptions(chrome, 640, 360)...)
		defer cancelA()
		cctx, cancelC := chromedp.NewContext(actx)
		defer cancelC()
		_ = chromedp.Run(cctx) // the stub is not a browser; it will not connect
		out, err := os.ReadFile(argv)
		if err != nil {
			t.Fatalf("the allocator never launched the browser: %v", err)
		}
		return string(out)
	}
	if got := argvOf("0"); !strings.Contains(got, "--no-sandbox") {
		t.Fatalf("sandbox off: --no-sandbox missing from argv:\n%s", got)
	}
	// chromedp adds --no-sandbox by itself when the test runs as root, so
	// only a non-root run can show that we left the flag off.
	if os.Geteuid() != 0 {
		if got := argvOf("1"); strings.Contains(got, "--no-sandbox") {
			t.Fatalf("sandbox on: --no-sandbox should not be set:\n%s", got)
		}
	}
}
