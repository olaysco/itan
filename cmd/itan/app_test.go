package main

import (
	"reflect"
	"testing"
)

func TestAppBrowserCandidates(t *testing.T) {
	base := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"}
	if got := appBrowserCandidates(""); !reflect.DeepEqual(got, base) {
		t.Errorf("no override: got %v", got)
	}
	got := appBrowserCandidates("my-chromium")
	if got[0] != "my-chromium" {
		t.Errorf("$ITAN_BROWSER must be tried first, got %v", got)
	}
	if !reflect.DeepEqual(got[1:], base) {
		t.Errorf("override must not displace the defaults, got %v", got)
	}
}

func TestAppModeArgv(t *testing.T) {
	cases := []struct {
		name          string
		browser, goos string
		want          []string
	}{
		{
			"linux binary", "/usr/bin/chromium", "linux",
			[]string{"/usr/bin/chromium", "--app=http://127.0.0.1:4141", "--window-size=1680,1000", "--user-data-dir=/home/u/.itan/app-profile"},
		},
		{
			"darwin open launcher", "open", "darwin",
			[]string{"open", "-na", "Google Chrome", "--args", "--app=http://127.0.0.1:4141", "--window-size=1680,1000", "--user-data-dir=/home/u/.itan/app-profile"},
		},
		{
			"darwin resolved binary", "/opt/homebrew/bin/chromium", "darwin",
			[]string{"/opt/homebrew/bin/chromium", "--app=http://127.0.0.1:4141", "--window-size=1680,1000", "--user-data-dir=/home/u/.itan/app-profile"},
		},
	}
	for _, tc := range cases {
		got := appModeArgv(tc.browser, tc.goos, "http://127.0.0.1:4141", "/home/u/.itan/app-profile")
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
