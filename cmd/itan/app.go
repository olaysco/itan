package main

// `itan app` opens the desktop editing screen in a native-feeling window: it
// runs the same local server as `itan ui`, then launches a Chromium-family
// browser in app mode (no tabs, no URL bar, dedicated profile). Real native
// packaging is documented in docs/desktop.md; this gets a standalone window
// today with zero extra dependencies.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/olaysco/itan/internal/config"
)

// appWindowSize is the initial geometry of the app-mode window.
const appWindowSize = "1680,1000"

// appBrowserCandidates lists the Chromium-family binaries to try, in order.
// A non-empty $ITAN_BROWSER override goes first.
func appBrowserCandidates(envBrowser string) []string {
	base := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"}
	if envBrowser == "" {
		return base
	}
	return append([]string{envBrowser}, base...)
}

// appModeArgv builds the command that opens url in a dedicated app-mode
// window. `browser` is a resolved binary path — except "open" on darwin,
// which routes through macOS's `open -na "Google Chrome" --args` launcher.
func appModeArgv(browser, goos, url, profileDir string) []string {
	flags := []string{"--app=" + url, "--window-size=" + appWindowSize, "--user-data-dir=" + profileDir}
	if goos == "darwin" && browser == "open" {
		return append([]string{"open", "-na", "Google Chrome", "--args"}, flags...)
	}
	return append([]string{browser}, flags...)
}

// launchAppWindow tries each candidate browser and reports whether one was
// launched. The profile under ~/.itan keeps the window its own instance
// instead of a tab of the daily browser.
func launchAppWindow(url string) bool {
	profile := filepath.Join(config.GlobalDir(), "app-profile")
	for _, cand := range appBrowserCandidates(os.Getenv("ITAN_BROWSER")) {
		path, err := exec.LookPath(cand)
		if err != nil {
			continue
		}
		argv := appModeArgv(path, runtime.GOOS, url, profile)
		if exec.Command(argv[0], argv[1:]...).Start() == nil {
			return true
		}
	}
	if runtime.GOOS == "darwin" {
		argv := appModeArgv("open", "darwin", url, profile)
		if exec.Command(argv[0], argv[1:]...).Run() == nil {
			return true
		}
	}
	return false
}

// openAppWindow launches the app-mode window once the server has had a
// moment to bind, falling back to the default browser with a note.
func openAppWindow(url string) {
	time.Sleep(300 * time.Millisecond)
	if launchAppWindow(url) {
		return
	}
	fmt.Println("itan: no Chromium-family browser found for app mode; opening the default browser instead")
	openBrowser(url)
}
