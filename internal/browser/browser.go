// Package browser locates a Chromium-family binary. Both the `itan app`
// window and the compose rendering engine need one; discovery lives here so
// they agree on the search order.
package browser

import (
	"fmt"
	"os"
	"os/exec"
)

// Candidates lists the Chromium-family binaries to try, in order. A non-empty
// override (normally $ITAN_BROWSER) goes first.
func Candidates(override string) []string {
	base := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"}
	if override == "" {
		return base
	}
	return append([]string{override}, base...)
}

// Find resolves the first available Chromium-family binary, honoring
// $ITAN_BROWSER. Returns a clear, actionable error when none exists.
func Find() (string, error) {
	for _, cand := range Candidates(os.Getenv("ITAN_BROWSER")) {
		if path, err := exec.LookPath(cand); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no Chromium-family browser found (tried $ITAN_BROWSER, google-chrome, chromium, chromium-browser, microsoft-edge, brave-browser) — install one or set ITAN_BROWSER to its path")
}
