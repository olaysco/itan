package browser

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Chromium's sandbox needs two things a build machine often withholds. It
// refuses outright to sandbox as root (crbug.com/638180), and it needs an
// unprivileged user namespace — which Ubuntu 23.10+ denies to any binary
// without a matching AppArmor profile, i.e. every Chrome a CI job unpacks
// into a temp directory. Either way the browser aborts in ZygoteHostImpl
// before it ever opens a page.
//
// Rather than pattern-match /proc and hope, ask the browser: launch it once
// against about:blank and see whether it lives. The answer is cached per
// binary, so a render pays for the probe at most once, and the sandbox is
// only dropped where it provably cannot work.

var (
	sandboxMu    sync.Mutex
	sandboxCache = map[string]bool{}
)

// Sandboxed reports whether chrome can start with its sandbox enabled here.
// $ITAN_SANDBOX overrides the probe for a machine it reads wrongly: 0 forces
// the sandbox off, any other non-empty value forces it on.
func Sandboxed(chrome string) bool {
	switch os.Getenv("ITAN_SANDBOX") {
	case "":
		// Unset: decide for ourselves, below.
	case "0", "false":
		return false
	default:
		return true
	}
	// Only Linux has the namespace/setuid machinery that fails this way;
	// macOS and Windows sandbox without help.
	if runtime.GOOS != "linux" {
		return true
	}
	// Root is a certainty rather than a guess — Chromium declines before it
	// tries — so skip the launch. (chromedp reaches the same conclusion on
	// its own; saying it here keeps one place that decides.)
	if os.Geteuid() == 0 {
		return false
	}
	return probeCached(chrome)
}

// probeCached runs probeSandbox at most once per binary. The probe launches a
// browser, and a 200-frame composition must not pay for that 200 times.
func probeCached(chrome string) bool {
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	if ok, seen := sandboxCache[chrome]; seen {
		return ok
	}
	ok := probeSandbox(chrome)
	sandboxCache[chrome] = ok
	return ok
}

// probeSandbox launches chrome for one about:blank dump. A clean exit means
// the sandbox holds. A failure only counts as a sandbox failure if the
// browser said so — anything else (a missing shared library, a corrupt
// download) leaves the sandbox on, so the real error surfaces at render
// time instead of being silently traded away for a weaker browser.
func probeSandbox(chrome string) bool {
	dir, err := os.MkdirTemp("", "itan-sandbox-probe-*")
	if err != nil {
		return true
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome,
		"--headless", "--disable-gpu", "--no-first-run",
		"--user-data-dir="+dir, "--dump-dom", "about:blank")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return true
	}
	return !strings.Contains(stderr.String(), "zygote_host")
}

// AllocatorOptions builds the exec-allocator options shared by every browser
// itan drives — the render engine, the page capture, the still snapshot, and
// the UI smoke tests. Centralising it keeps one launch policy: they all agree
// on the binary, on the flags, and on when the sandbox has to come off.
func AllocatorOptions(chrome string, width, height int, extra ...chromedp.ExecAllocatorOption) []chromedp.ExecAllocatorOption {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.WindowSize(width, height),
	)
	if !Sandboxed(chrome) {
		opts = append(opts, chromedp.NoSandbox)
	}
	return append(opts, extra...)
}
