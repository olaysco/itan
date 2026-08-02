package browser

import (
	"runtime"
	"strings"
	"testing"
)

func TestCandidatesOverrideFirst(t *testing.T) {
	if got := Candidates("/custom/chrome"); got[0] != "/custom/chrome" {
		t.Fatalf("$ITAN_BROWSER must be tried first, got %v", got)
	}
}

// Browsers on macOS and Windows live in app bundles / Program Files, never on
// PATH — the candidate list must include those install locations or doctor
// reports "no browser" on a machine with Chrome installed.
func TestCandidatesIncludeOSInstallPaths(t *testing.T) {
	all := strings.Join(Candidates(""), "\n")
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(all, "/Applications/Google Chrome.app/") {
			t.Fatal("missing macOS Chrome bundle path")
		}
	case "windows":
		if !strings.Contains(all, `Chrome\Application\chrome.exe`) {
			t.Fatal("missing Windows Chrome install path")
		}
	default:
		if !strings.Contains(all, "google-chrome") {
			t.Fatal("missing PATH candidates")
		}
	}
}
