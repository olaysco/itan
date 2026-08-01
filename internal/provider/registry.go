package provider

import (
	"fmt"

	"github.com/olaysco/itan/internal/config"
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

// VisionFromConfig builds the secondary provider named by model.vision for
// image-carrying turns. Returns (nil, "", nil) when no vision route is set.
func VisionFromConfig(cfg *config.Config) (Provider, string, error) {
	spec := cfg.Model.Vision
	if spec == "" {
		return nil, "", nil
	}
	kind, baseURL, apiKey, modelID, err := cfg.ResolveSpec(spec)
	if err != nil {
		return nil, "", fmt.Errorf("model.vision %q: %w", spec, err)
	}
	switch kind {
	case "anthropic":
		if apiKey == "" {
			return nil, "", fmt.Errorf("model.vision %q: no API key set", spec)
		}
		return NewAnthropic(baseURL, apiKey), modelID, nil
	case "openai":
		return NewOpenAI(baseURL, apiKey, "vision"), modelID, nil
	default:
		return nil, "", fmt.Errorf("model.vision %q: unknown provider kind %q", spec, kind)
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
