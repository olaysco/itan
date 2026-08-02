// Package browser locates a Chromium-family binary. Both the `itan app`
// window and the compose rendering engine need one; discovery lives here so
// they agree on the search order.
package browser

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// Candidates lists the Chromium-family binaries to try, in order. A non-empty
// override (normally $ITAN_BROWSER) goes first. PATH names come before the
// OS-specific install locations, which is where macOS and Windows browsers
// actually live — app bundles are never on PATH.
func Candidates(override string) []string {
	base := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"}
	switch runtime.GOOS {
	case "darwin":
		base = append(base,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Arc.app/Contents/MacOS/Arc",
		)
	case "windows":
		base = append(base,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			os.ExpandEnv(`${LOCALAPPDATA}\Google\Chrome\Application\chrome.exe`),
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
		)
	}
	if override == "" {
		return base
	}
	return append([]string{override}, base...)
}

// Find resolves the first available Chromium-family binary, honoring
// $ITAN_BROWSER. exec.LookPath handles both bare names (searched on PATH)
// and absolute paths (checked directly). Returns a clear, actionable error
// when none exists.
func Find() (string, error) {
	for _, cand := range Candidates(os.Getenv("ITAN_BROWSER")) {
		if path, err := exec.LookPath(cand); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no Chromium-family browser found (tried $ITAN_BROWSER, PATH names, and the standard install locations for this OS) — install Chrome/Chromium/Edge/Brave or set ITAN_BROWSER to the browser binary's full path")
}
