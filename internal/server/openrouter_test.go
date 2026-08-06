package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

// The catalogue endpoint is a convenience layer over a third-party response
// shape, so the parse is what needs testing: a field that moves should show
// up here rather than as an empty picker.
func TestOpenRouterCatalogueParse(t *testing.T) {
	const fixture = `{"data":[
	  {"id":"moonshotai/kimi-k2.5","name":"Kimi K2.5","context_length":262144,
	   "architecture":{"input_modalities":["text","image"]},
	   "pricing":{"prompt":"0.0000006","completion":"0.0000025"}},
	  {"id":"meta-llama/llama-3.3-70b:free","name":"Llama 3.3 70B (free)","context_length":131072,
	   "architecture":{"input_modalities":["text"]},
	   "pricing":{"prompt":"0","completion":"0"}}]}`

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
		Models []orModel `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("got %d models", len(got.Models))
	}
	byID := map[string]orModel{}
	for _, m := range got.Models {
		byID[m.ID] = m
	}
	kimi := byID["moonshotai/kimi-k2.5"]
	if !kimi.Vision {
		t.Error("image input modality was not surfaced as vision")
	}
	if kimi.Free {
		t.Error("a paid model was marked free")
	}
	if kimi.Price != "$0.60/$2.50 per M" {
		t.Errorf("price = %q", kimi.Price)
	}
	if kimi.Context != 262144 {
		t.Errorf("context = %d", kimi.Context)
	}
	llama := byID["meta-llama/llama-3.3-70b:free"]
	if !llama.Free || llama.Price != "free" {
		t.Errorf("free model not identified: %+v", llama)
	}
	if llama.Vision {
		t.Error("a text-only model was marked as vision")
	}
}

// An unreachable catalogue must produce a message that points at the way
// forward, not an empty list the user cannot interpret.
func TestOpenRouterUnreachableIsExplained(t *testing.T) {
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
	if rec.Code == 200 {
		t.Fatal("a failed upstream must not report success")
	}
	if body := rec.Body.String(); !contains(body, "type a model id") {
		t.Errorf("error does not offer the fallback: %s", body)
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

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
