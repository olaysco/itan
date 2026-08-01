package provider

import (
	"fmt"

	"github.com/olaysco/heydit/internal/config"
)

// FromConfig builds the active Provider from the resolved model config.
func FromConfig(cfg *config.Config) (Provider, error) {
	kind, baseURL, apiKey, err := cfg.ResolveModel()
	if err != nil {
		return nil, err
	}
	switch kind {
	case "anthropic":
		if apiKey == "" {
			return nil, fmt.Errorf("no API key: set %s", keyEnvFor(cfg))
		}
		return NewAnthropic(baseURL, apiKey), nil
	case "openai":
		// Local hosts (ollama, llama.cpp) legitimately run keyless.
		return NewOpenAI(baseURL, apiKey, cfg.Model.Provider), nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q", kind)
	}
}

func keyEnvFor(cfg *config.Config) string {
	if cfg.Model.KeyEnv != "" {
		return cfg.Model.KeyEnv
	}
	if p, ok := config.Presets[cfg.Model.Provider]; ok && p.KeyEnv != "" {
		return p.KeyEnv
	}
	return "the provider's API key env var"
}
