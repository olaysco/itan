package config

import (
	"testing"
)

func TestUseModel(t *testing.T) {
	cases := []struct {
		spec         string
		provider, id string
		wantErr      bool
	}{
		{spec: "kimi/kimi-k3", provider: "kimi", id: "kimi-k3"},
		{spec: "anthropic", provider: "anthropic", id: "claude-opus-4-8"},
		{spec: "kimi", provider: "kimi", id: Presets["kimi"].DefaultModel},
		{spec: "claude-sonnet-4.5", provider: "anthropic", id: "claude-sonnet-4.5"}, // bare id keeps provider
		{spec: "nope/x", wantErr: true},
	}
	for _, tc := range cases {
		cfg := Default()
		err := cfg.UseModel(tc.spec)
		if tc.wantErr {
			if err == nil {
				t.Errorf("UseModel(%q): expected error", tc.spec)
			}
			continue
		}
		if err != nil {
			t.Fatalf("UseModel(%q): %v", tc.spec, err)
		}
		if cfg.Model.Provider != tc.provider || cfg.Model.ID != tc.id {
			t.Errorf("UseModel(%q) = %s/%s, want %s/%s", tc.spec, cfg.Model.Provider, cfg.Model.ID, tc.provider, tc.id)
		}
	}
}

func TestGetSetDottedPath(t *testing.T) {
	cfg := Default()
	if err := cfg.Set("audio.tts.provider", "elevenlabs"); err != nil {
		t.Fatal(err)
	}
	if cfg.Audio.TTS.Provider != "elevenlabs" {
		t.Fatalf("Set did not hydrate struct: %+v", cfg.Audio.TTS)
	}
	got, err := cfg.Get("audio.tts.provider")
	if err != nil || got != "elevenlabs" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := cfg.Set("context.max_tokens", "90000"); err != nil {
		t.Fatal(err)
	}
	if cfg.Context.MaxTokens != 90000 {
		t.Fatalf("int coercion failed: %d", cfg.Context.MaxTokens)
	}
	if _, err := cfg.Get("no.such.key"); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestResolveSpec(t *testing.T) {
	cfg := Default()
	t.Setenv("MOONSHOT_API_KEY", "mk")
	kind, base, key, id, err := cfg.ResolveSpec("kimi")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "openai" || base != "https://api.moonshot.ai/v1" || key != "mk" || id != Presets["kimi"].DefaultModel {
		t.Fatalf("kimi spec resolved to %s %s %s %s", kind, base, key, id)
	}
	if cfg.Model.Provider != "anthropic" {
		t.Fatal("ResolveSpec must not touch the active model")
	}
	if _, _, _, _, err := cfg.ResolveSpec("nope/x"); err == nil {
		t.Fatal("expected error for unknown provider spec")
	}
}

func TestResolveModel(t *testing.T) {
	cfg := Default()
	cfg.Model = Model{Provider: "kimi", ID: "kimi-k3"}
	kind, base, _, err := cfg.ResolveModel()
	if err != nil {
		t.Fatal(err)
	}
	if kind != "openai" || base != "https://api.moonshot.ai/v1" {
		t.Fatalf("kimi resolves to %s %s", kind, base)
	}

	cfg.Model = Model{Provider: "myhost", BaseURL: "http://x:1234/v1"}
	kind, base, _, err = cfg.ResolveModel()
	if err != nil || kind != "openai" || base != "http://x:1234/v1" {
		t.Fatalf("custom host: %s %s %v", kind, base, err)
	}

	cfg.Model = Model{Provider: "mystery"}
	if _, _, _, err := cfg.ResolveModel(); err == nil {
		t.Fatal("expected error for unknown provider without base_url")
	}
}
