package tools

import (
	"strings"
	"testing"
)

// Without a key the tool must say exactly how to get one — this is the
// first thing a new user hits, and "search failed" would strand them.
func TestFindMediaWithoutKeyExplainsSetup(t *testing.T) {
	c := composeCtx(t)
	c.Config.Media.PixabayKey = ""
	t.Setenv("PIXABAY_API_KEY", "")
	res := NewRegistry().Execute(c, "find_media", []byte(`{"query":"coastline sunrise"}`))
	if res.Err == nil {
		t.Fatal("expected an error without a key")
	}
	if !strings.Contains(res.Err.Error(), "pixabay.com/api/docs") {
		t.Errorf("error does not say where to get a key: %v", res.Err)
	}
}

func TestFindMediaValidatesArgs(t *testing.T) {
	c := composeCtx(t)
	c.Config.Media.PixabayKey = "k"
	r := NewRegistry()

	if res := r.Execute(c, "find_media", []byte(`{"query":"  "}`)); res.Err == nil {
		t.Error("an empty query must be rejected before any network call")
	}
	res := r.Execute(c, "find_media", []byte(`{"query":"x","kind":"gif"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "video") {
		t.Errorf("an unknown kind should name the valid ones: %v", res.Err)
	}
}

// A key set only in the environment must work — people should not have to
// write a credential into a config file.
func TestPixabayKeyFallsBackToEnv(t *testing.T) {
	c := composeCtx(t)
	c.Config.Media.PixabayKey = ""
	t.Setenv("PIXABAY_API_KEY", "from-env")
	if got := c.Config.PixabayKey(); got != "from-env" {
		t.Errorf("PixabayKey() = %q, want the environment value", got)
	}
	c.Config.Media.PixabayKey = "from-config"
	if got := c.Config.PixabayKey(); got != "from-config" {
		t.Errorf("config must win over the environment, got %q", got)
	}
}
