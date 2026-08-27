package server

import (
	"strings"
	"testing"
)

// itan has no authentication and lets the UI open any directory on the
// machine. On loopback that is a local tool; on any other address it is a
// filesystem and a set of API keys handed to whoever finds the port.
func TestExposureWarning(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:4141", "localhost:4141", "[::1]:4141", ":4141"} {
		if w := exposureWarning(addr); w != "" {
			t.Errorf("%s is loopback but warned: %q", addr, w)
		}
	}
	for _, addr := range []string{"0.0.0.0:4141", "192.168.1.20:4141", "example.com:80", "[2001:db8::1]:4141"} {
		w := exposureWarning(addr)
		if w == "" {
			t.Errorf("%s is reachable from outside and did not warn", addr)
			continue
		}
		for _, want := range []string{"no", "authentication", "any folder", "API keys"} {
			if !strings.Contains(w, want) {
				t.Errorf("%s: warning does not mention %q", addr, want)
			}
		}
	}
}
