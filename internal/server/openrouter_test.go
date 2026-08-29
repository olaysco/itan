package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/config"
)

func TestPerMillionAndPriceLabel(t *testing.T) {
	// OpenRouter quotes per-token prices as strings; the picker shows dollars
	// per million, which is the unit people actually compare in.
	cases := []struct {
		in, out string
		want    string
	}{
		{"0.000003", "0.000015", "$3.00/$15.00 per M"},
		{"0", "0", "free"},
		{"0.0000002", "0.0000006", "$0.20/$0.60 per M"},
		{"not-a-number", "0", "free"},
	}
	for _, c := range cases {
		got := priceLabel(perMillion(c.in), perMillion(c.out))
		if got != c.want {
			t.Errorf("priceLabel(%s,%s) = %q, want %q", c.in, c.out, got, c.want)
		}
	}
}

// The picker must show the supported set and nothing else. The fixture below
// carries a supported model, a model nobody vouched for, and a supported id
// that has gone text-only — the last two must not reach the user, because a
// model that cannot see renders blind and one we never tested fails halfway
// through a project.
func TestOpenRouterListsOnlyTheSupportedSet(t *testing.T) {
	const fixture = `{"data":[
	  {"id":"moonshotai/kimi-k2.5","name":"Kimi K2.5 (vendor label)","context_length":262144,
	   "architecture":{"input_modalities":["text","image"]},
	   "pricing":{"prompt":"0.0000006","completion":"0.0000025"}},
	  {"id":"meta-llama/llama-3.3-70b:free","name":"Llama 3.3 70B (free)","context_length":131072,
	   "architecture":{"input_modalities":["text"]},
	   "pricing":{"prompt":"0","completion":"0"}},
	  {"id":"anthropic/claude-haiku-4-5","name":"Claude Haiku 4.5","context_length":200000,
	   "architecture":{"input_modalities":["text"]},
	   "pricing":{"prompt":"0.000001","completion":"0.000005"}}]}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixture))
	}))
	defer upstream.Close()

	old := openRouterCatalogue
	openRouterCatalogue = upstream.URL
	defer func() { openRouterCatalogue = old }()
	resetORCache()

	rec := httptest.NewRecorder()
	(&Server{}).handleOpenRouterModels(rec, httptest.NewRequest("GET", "/api/models/openrouter", nil))
	if rec.Code != 200 {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Models      []orModel `json:"models"`
		Unavailable []string  `json:"unavailable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	byID := map[string]orModel{}
	for _, m := range got.Models {
		byID[m.ID] = m
	}
	if _, ok := byID["meta-llama/llama-3.3-70b:free"]; ok {
		t.Error("a model outside the supported set reached the picker")
	}
	if _, ok := byID["anthropic/claude-haiku-4-5"]; ok {
		t.Error("a supported model that lost image input was still offered")
	}
	kimi, ok := byID["moonshotai/kimi-k2.5"]
	if !ok {
		t.Fatalf("supported model missing from the list: %+v", got.Models)
	}
	// Live catalogue supplies the facts; we supply the label.
	if kimi.Name != "Kimi K2.5" {
		t.Errorf("name = %q, want our label not the vendor string", kimi.Name)
	}
	if !kimi.Vision || kimi.Free || kimi.Context != 262144 || kimi.Price != "$0.60/$2.50 per M" {
		t.Errorf("catalogue facts not carried through: %+v", kimi)
	}
	// Anything we vouch for but cannot offer has to be said out loud.
	joined := strings.Join(got.Unavailable, " ")
	if !strings.Contains(joined, "anthropic/claude-haiku-4-5") {
		t.Errorf("a supported model that went text-only was dropped silently: %v", got.Unavailable)
	}
	if !strings.Contains(joined, "anthropic/claude-sonnet-4.5") {
		t.Errorf("a supported model absent upstream was dropped silently: %v", got.Unavailable)
	}
}

// Every supported entry must be vision-capable: view_frames is how the agent
// judges its own work, and a blind model cannot do the job at all.
func TestSupportedSetIsAllVision(t *testing.T) {
	if len(config.OpenRouterSupported) == 0 {
		t.Fatal("the supported set is empty")
	}
	for _, m := range config.OpenRouterSupported {
		if !m.Vision {
			t.Errorf("%s is in the supported set but is not vision-capable", m.ID)
		}
		if m.Name == "" || m.Ctx == "" {
			t.Errorf("%s is missing a display name or context label", m.ID)
		}
	}
}

// An unreachable catalogue must not empty the picker. The supported set is
// known locally, so it is still offered — just without live pricing — and the
// reason is stated rather than surfaced as a bare failure.
func TestOpenRouterUnreachableFallsBackToTheSupportedSet(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()

	old := openRouterCatalogue
	openRouterCatalogue = dead.URL
	defer func() { openRouterCatalogue = old }()
	resetORCache()

	rec := httptest.NewRecorder()
	(&Server{}).handleOpenRouterModels(rec, httptest.NewRequest("GET", "/api/models/openrouter", nil))
	if rec.Code != 200 {
		t.Fatalf("an unreachable catalogue emptied the picker: HTTP %d", rec.Code)
	}
	var got struct {
		Models []orModel `json:"models"`
		Pinned bool      `json:"pinned"`
		Note   string    `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Pinned || len(got.Models) != len(config.OpenRouterSupported) {
		t.Fatalf("fallback did not offer the supported set: %+v", got)
	}
	if !strings.Contains(got.Note, "503") {
		t.Errorf("the reason was not stated: %q", got.Note)
	}
}

// The catalogue is cached, so opening the picker repeatedly does not hammer
// a third party.
func TestOpenRouterCaches(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"data":[{"id":"a/b","name":"A B","context_length":1000,"pricing":{"prompt":"0","completion":"0"}}]}`))
	}))
	defer upstream.Close()

	old := openRouterCatalogue
	openRouterCatalogue = upstream.URL
	defer func() { openRouterCatalogue = old }()
	resetORCache()

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		(&Server{}).handleOpenRouterModels(rec, httptest.NewRequest("GET", "/api/models/openrouter", nil))
		if rec.Code != 200 {
			t.Fatalf("call %d: HTTP %d", i, rec.Code)
		}
	}
	if calls != 1 {
		t.Errorf("hit upstream %d times, want 1", calls)
	}
}
