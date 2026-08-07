package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/cli"
	"github.com/olaysco/itan/internal/config"
)

// CanSee is the fact the whole picker rests on. Getting it wrong in either
// direction is bad: a false negative nags about a model that works, a false
// positive lets someone ship a run that never looks at its own output.
func TestCanSee(t *testing.T) {
	cases := []struct {
		provider, id string
		want         bool
	}{
		{"anthropic", "claude-opus-4-8", true},
		{"anthropic", "claude-haiku-4-5", true},
		{"deepseek", "deepseek-v4-pro", false},
		{"deepseek", "deepseek-v4-flash", false},
		{"ollama", "qwen3-vl:8b", true},
		{"ollama", "llama3.3:70b", false},
		{"zai", "glm-5v", true},
		{"zai", "glm-5.2", false},
		// An unlisted id inherits the provider's flagship answer, which is
		// the best guess available before any request is made.
		{"anthropic", "claude-something-new", true},
		{"deepseek", "deepseek-something-new", false},
		// An unknown provider is not assumed to see.
		{"nosuchprovider", "x", false},
	}
	for _, c := range cases {
		if got := config.CanSee(c.provider, c.id); got != c.want {
			t.Errorf("CanSee(%q, %q) = %v, want %v", c.provider, c.id, got, c.want)
		}
	}
}

// Every preset that ships a shortlist must name at least one model that can
// see, or choosing that provider is a dead end the picker cannot resolve.
func TestEveryProviderOffersSomethingThatSees(t *testing.T) {
	textOnly := map[string]bool{"deepseek": true} // deliberate: DeepSeek is text-only
	for name, preset := range config.Presets {
		if len(preset.Models) == 0 || textOnly[name] {
			continue
		}
		var sighted bool
		for _, m := range preset.Models {
			if m.Vision {
				sighted = true
			}
		}
		if !sighted {
			t.Errorf("provider %q offers no model that can see", name)
		}
	}
}

// stateFor builds a server whose config names the given models and returns
// the state the UI would receive.
func stateFor(t *testing.T, model, vision string) stateView {
	t.Helper()
	s := testServer(t)
	if err := s.Session.Cfg.UseModel(model); err != nil {
		t.Fatal(err)
	}
	s.Session.Cfg.Model.Vision = vision
	return s.state()
}

func testServer(t *testing.T) *Server {
	t.Helper()
	session, err := cli.NewSession(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	// Config lives globally; keep each test's edits out of the real file.
	t.Setenv("ITAN_HOME", t.TempDir())
	return New(session)
}

// The one question the UI asks: can this setup see?
func TestVisionStateAnswersCanSee(t *testing.T) {
	if v := stateFor(t, "anthropic/claude-opus-4-8", "").Vision; !v.CanSee || v.Model != "" {
		t.Errorf("a sighted model with no route should just see: %+v", v)
	}
	if v := stateFor(t, "deepseek/deepseek-v4-pro", "").Vision; v.CanSee {
		t.Errorf("a text-only model with no route must report that it cannot see: %+v", v)
	}
	// A route makes a text-only reasoning model able to see.
	v := stateFor(t, "deepseek/deepseek-v4-pro", "ollama/qwen3-vl:8b").Vision
	if !v.CanSee {
		t.Errorf("a vision route was ignored: %+v", v)
	}
	if v.Model != "ollama/qwen3-vl:8b" || v.Via != "ollama" {
		t.Errorf("route not reported back: %+v", v)
	}
}

// The active model must always appear in the list, even when it is an id
// nobody shortlisted — otherwise the picker shows nothing selected.
func TestUnlistedActiveModelStillAppears(t *testing.T) {
	st := stateFor(t, "anthropic/claude-brand-new-9", "")
	var found bool
	for _, m := range st.Models {
		if m.Active && m.ID == "claude-brand-new-9" {
			found = true
		}
	}
	if !found {
		t.Fatal("the model actually in use is missing from the picker list")
	}
}

// Setting the vision role must be reachable over HTTP, and clearing it must
// put frames back on the reasoning model.
func TestModelEndpointSetsAndClearsVision(t *testing.T) {
	s := testServer(t)
	post := func(body string) stateView {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleModel(rec, httptest.NewRequest("POST", "/api/model", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
		var st stateView
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}

	st := post(`{"spec":"ollama/qwen3-vl:8b","role":"vision"}`)
	if st.Vision.Model != "ollama/qwen3-vl:8b" || !st.Vision.CanSee {
		t.Fatalf("vision not set: %+v", st.Vision)
	}
	st = post(`{"spec":"","role":"vision"}`)
	if st.Vision.Model != "" {
		t.Fatalf("vision not cleared: %+v", st.Vision)
	}
}
