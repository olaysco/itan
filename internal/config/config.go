// Package config implements Itan's layered configuration.
//
// Precedence (lowest → highest):
//  1. built-in defaults
//  2. global   ~/.itan/config.yaml
//  3. project  <project>/.itan/config.yaml
//  4. process environment (API keys are only ever read from env)
//
// Any value is addressable by a dotted path (e.g. "model.id",
// "audio.tts.provider") for `itan config get|set` and the /config command.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/olaysco/itan/internal/permission"
)

// Model selects the LLM that drives the harness.
type Model struct {
	Provider string `yaml:"provider"` // anthropic | openai | kimi | deepseek | zai | openrouter | ollama | custom
	ID       string `yaml:"id"`
	BaseURL  string `yaml:"base_url,omitempty"` // override for custom / self-hosted
	KeyEnv   string `yaml:"key_env,omitempty"`  // env var holding the API key
	// Vision optionally names a second model ("preset" or "preset/model-id")
	// that handles turns carrying frames, so a cheap text-only model can be
	// the main brain while a multimodal one does the looking. When unset,
	// frames go to the main model.
	Vision string `yaml:"vision,omitempty"`
}

// TTS / STT select the voice models. Both default to the best open-source
// stacks (Kokoro for TTS, Whisper for STT) served behind OpenAI-compatible
// endpoints, and can be switched to hosted providers per config.
type TTS struct {
	Provider string `yaml:"provider"` // kokoro | elevenlabs | openai | custom
	BaseURL  string `yaml:"base_url,omitempty"`
	Model    string `yaml:"model,omitempty"`
	Voice    string `yaml:"voice,omitempty"`
	KeyEnv   string `yaml:"key_env,omitempty"`
}

type STT struct {
	Provider string `yaml:"provider"` // whisper | openai | custom
	BaseURL  string `yaml:"base_url,omitempty"`
	Model    string `yaml:"model,omitempty"`
	KeyEnv   string `yaml:"key_env,omitempty"`
}

type Audio struct {
	TTS TTS `yaml:"tts"`
	STT STT `yaml:"stt"`
}

// Context tunes the token-efficiency machinery of the harness.
type Context struct {
	MaxTokens          int     `yaml:"max_tokens"`            // working context budget
	CompactAt          float64 `yaml:"compact_at"`            // fraction of budget that triggers compaction
	ToolResultMaxChars int     `yaml:"tool_result_max_chars"` // hard cap per tool result fed back to the model
	MaxTurns           int     `yaml:"max_turns"`             // tool-use iterations per user request
	// ReplyMaxTokens caps one model response. Reasoning models (Kimi K2.5+,
	// DeepSeek) spend from this budget on thinking BEFORE visible output —
	// too small and they hit the cap with nothing to show.
	ReplyMaxTokens int `yaml:"reply_max_tokens"`
}

type Config struct {
	Model   Model   `yaml:"model"`
	Audio   Audio   `yaml:"audio"`
	Media   Media   `yaml:"media"`
	Context Context `yaml:"context"`
	// Mode is the permission mode: auto (default), ask, or plan.
	Mode string `yaml:"mode,omitempty"`
	// Permissions are tool rules evaluated last-match-wins, e.g.
	//   permissions: [{tool: "render", action: ask}, {tool: "*", action: allow}]
	Permissions []permission.Rule `yaml:"permissions,omitempty"`
	// SkillDirs are extra directories scanned for skills, beyond the
	// built-ins and ~/.itan/skills.
	SkillDirs []string `yaml:"skill_dirs,omitempty"`
}

// Media holds credentials for sourcing footage the project does not own.
type Media struct {
	// PixabayKey authorizes stock lookups. Free key from
	// https://pixabay.com/api/docs; $PIXABAY_API_KEY is honored as a
	// fallback so a key never has to be written to disk.
	PixabayKey string `yaml:"pixabay_key,omitempty"`
}

// PixabayKey resolves the key from config, then the environment.
func (c *Config) PixabayKey() string {
	if c.Media.PixabayKey != "" {
		return c.Media.PixabayKey
	}
	return os.Getenv("PIXABAY_API_KEY")
}

// ProviderPreset describes a known model host so users can switch with
// `itan model use kimi/kimi-k3` without hand-writing base URLs.
type ProviderPreset struct {
	Kind         string // wire protocol: "anthropic" or "openai"
	BaseURL      string
	KeyEnv       string
	DefaultModel string
	Note         string
	// Models are the ones worth offering in a picker. Any id can still be
	// typed; this is the shortlist, not a limit.
	Models []PresetModel
}

// PresetModel is one offerable model. Vision is the field that matters most:
// a model that cannot see turns the harness into a system that writes
// motion graphics without ever looking at them, and until now nothing in
// the product recorded which models those are.
type PresetModel struct {
	ID     string
	Name   string
	Vision bool
	Ctx    string // human-readable context window, e.g. "200k"
}

// CanSee reports whether a provider/model pair accepts image input. An
// unknown model on a known provider inherits the provider's default answer,
// which is the safest guess available before the first request is made.
func CanSee(provider, id string) bool {
	preset, ok := Presets[provider]
	if !ok {
		return false
	}
	for _, m := range preset.Models {
		if m.ID == id {
			return m.Vision
		}
	}
	// Unlisted id: assume the provider's flagship behaviour.
	for _, m := range preset.Models {
		if m.ID == preset.DefaultModel {
			return m.Vision
		}
	}
	return false
}

var Presets = map[string]ProviderPreset{
	"anthropic": {Kind: "anthropic", BaseURL: "https://api.anthropic.com", KeyEnv: "ANTHROPIC_API_KEY", DefaultModel: "claude-opus-4-8", Note: "Anthropic Claude",
		Models: []PresetModel{
			{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Vision: true, Ctx: "200k"},
			{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Vision: true, Ctx: "200k"},
			{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", Vision: true, Ctx: "200k"},
		}},
	"openai": {Kind: "openai", BaseURL: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY", DefaultModel: "gpt-4.1", Note: "OpenAI",
		Models: []PresetModel{
			{ID: "gpt-4.1", Name: "GPT-4.1", Vision: true, Ctx: "1M"},
			{ID: "gpt-4.1-mini", Name: "GPT-4.1 mini", Vision: true, Ctx: "1M"},
			{ID: "o4-mini", Name: "o4-mini", Vision: true, Ctx: "200k"},
		}},
	"kimi": {Kind: "openai", BaseURL: "https://api.moonshot.ai/v1", KeyEnv: "MOONSHOT_API_KEY", DefaultModel: "kimi-k2.5", Note: "Moonshot Kimi (open weights)",
		Models: []PresetModel{
			{ID: "kimi-k2.5", Name: "Kimi K2.5", Vision: true, Ctx: "256k"},
			{ID: "kimi-k3", Name: "Kimi K3", Vision: true, Ctx: "256k"},
		}},
	"deepseek": {Kind: "openai", BaseURL: "https://api.deepseek.com/v1", KeyEnv: "DEEPSEEK_API_KEY", DefaultModel: "deepseek-v4-pro", Note: "DeepSeek V4 — text only",
		Models: []PresetModel{
			{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Ctx: "128k"},
			{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Ctx: "128k"},
		}},
	"zai": {Kind: "openai", BaseURL: "https://api.z.ai/api/paas/v4", KeyEnv: "ZAI_API_KEY", DefaultModel: "glm-5.2", Note: "Z.ai GLM (open weights, MIT)",
		Models: []PresetModel{
			{ID: "glm-5.2", Name: "GLM-5.2", Ctx: "128k"},
			{ID: "glm-5v", Name: "GLM-5V", Vision: true, Ctx: "64k"},
		}},
	"openrouter": {Kind: "openai", BaseURL: "https://openrouter.ai/api/v1", KeyEnv: "OPENROUTER_API_KEY", DefaultModel: "anthropic/claude-sonnet-4.5", Note: "OpenRouter (any listed model)"},
	"ollama": {Kind: "openai", BaseURL: "http://localhost:11434/v1", KeyEnv: "", DefaultModel: "qwen3-vl:8b", Note: "local Ollama (on this machine)",
		Models: []PresetModel{
			{ID: "qwen3-vl:8b", Name: "Qwen3-VL 8B", Vision: true, Ctx: "32k"},
			{ID: "qwen2.5-vl:7b", Name: "Qwen2.5-VL 7B", Vision: true, Ctx: "32k"},
			{ID: "llama3.3:70b", Name: "Llama 3.3 70B", Ctx: "128k"},
		}},
}

var TTSPresets = map[string]TTS{
	// Kokoro-82M behind kokoro-fastapi: the best-quality open-source TTS,
	// Apache-licensed, OpenAI-compatible endpoint. This is the default.
	"kokoro":     {Provider: "kokoro", BaseURL: "http://localhost:8880/v1", Model: "kokoro", Voice: "af_heart"},
	"elevenlabs": {Provider: "elevenlabs", BaseURL: "https://api.elevenlabs.io", Model: "eleven_multilingual_v2", Voice: "Rachel", KeyEnv: "ELEVENLABS_API_KEY"},
	"openai":     {Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini-tts", Voice: "alloy", KeyEnv: "OPENAI_API_KEY"},
}

var STTPresets = map[string]STT{
	// Any OpenAI-compatible Whisper server. Open source by default; the
	// reference server is Speaches (formerly faster-whisper-server):
	//   docker run -p 8000:8000 ghcr.io/speaches-ai/speaches:latest-cpu
	// whisper.cpp `server` and compatible hosts work too.
	"whisper": {Provider: "whisper", BaseURL: "http://localhost:8000/v1", Model: "Systran/faster-whisper-large-v3"},
	"openai":  {Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "whisper-1", KeyEnv: "OPENAI_API_KEY"},
}

func Default() *Config {
	return &Config{
		Model: Model{Provider: "anthropic", ID: "claude-opus-4-8"},
		Audio: Audio{TTS: TTSPresets["kokoro"], STT: STTPresets["whisper"]},
		Context: Context{
			MaxTokens:          160_000,
			CompactAt:          0.7,
			ToolResultMaxChars: 1600,
			MaxTurns:           16,
			ReplyMaxTokens:     8192,
		},
	}
}

// GlobalDir returns ~/.itan (created on demand by Save).
func GlobalDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".itan"
	}
	return filepath.Join(home, ".itan")
}

func globalPath() string { return filepath.Join(GlobalDir(), "config.yaml") }
func projectPath(dir string) string {
	return filepath.Join(dir, ".itan", "config.yaml")
}

// Load merges defaults ← global ← project config for the given project dir.
func Load(projectDir string) (*Config, error) {
	cfg := Default()
	for _, p := range []string{globalPath(), projectPath(projectDir)} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue // missing layers are fine
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
	}
	return cfg, nil
}

// SaveGlobal persists cfg as the user-level config.
func SaveGlobal(cfg *Config) error {
	if err := os.MkdirAll(GlobalDir(), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(globalPath(), data, 0o644)
}

// UseModel switches the active model, accepting "preset", "preset/model-id",
// or a bare model id (provider kept). Examples:
//
//	itan model use kimi/kimi-k3
//	itan model use anthropic
//	/model claude-sonnet-4.5
func (c *Config) UseModel(spec string) error {
	provider, id, hasSlash := strings.Cut(spec, "/")
	if !hasSlash {
		if p, ok := Presets[spec]; ok { // bare preset name
			c.Model = Model{Provider: spec, ID: p.DefaultModel}
			return nil
		}
		c.Model.ID = spec // bare model id on current provider
		return nil
	}
	p, ok := Presets[provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (known: %s)", provider, strings.Join(PresetNames(), ", "))
	}
	if id == "" {
		id = p.DefaultModel
	}
	c.Model = Model{Provider: provider, ID: id}
	return nil
}

// ResolveModel fills in wire kind, base URL and API key from presets + env.
func (c *Config) ResolveModel() (kind, baseURL, apiKey string, err error) {
	m := c.Model
	preset, known := Presets[m.Provider]
	switch {
	case known:
		kind, baseURL = preset.Kind, preset.BaseURL
	case m.BaseURL != "":
		kind, baseURL = "openai", m.BaseURL // custom hosts speak the OpenAI dialect
	default:
		return "", "", "", fmt.Errorf("unknown provider %q and no base_url set", m.Provider)
	}
	if m.BaseURL != "" {
		baseURL = m.BaseURL
	}
	keyEnv := m.KeyEnv
	if keyEnv == "" && known {
		keyEnv = preset.KeyEnv
	}
	if keyEnv != "" {
		apiKey = os.Getenv(keyEnv)
	}
	return kind, baseURL, apiKey, nil
}

// ResolveSpec resolves a "preset[/model-id]" spec independently of the
// active model — used for the model.vision route.
func (c *Config) ResolveSpec(spec string) (kind, baseURL, apiKey, modelID string, err error) {
	tmp := *c
	tmp.Model.BaseURL, tmp.Model.KeyEnv = "", ""
	if err = tmp.UseModel(spec); err != nil {
		return "", "", "", "", err
	}
	kind, baseURL, apiKey, err = tmp.ResolveModel()
	return kind, baseURL, apiKey, tmp.Model.ID, err
}

func PresetNames() []string {
	names := make([]string, 0, len(Presets))
	for n := range Presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// --- dotted-path access ----------------------------------------------------

// Get returns the value at a dotted path via the YAML round trip, so it stays
// in sync with the struct tags automatically.
func (c *Config) Get(path string) (string, error) {
	node := map[string]any{}
	raw, _ := yaml.Marshal(c)
	_ = yaml.Unmarshal(raw, &node)
	cur := any(node)
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("no such key: %s", path)
		}
		cur, ok = m[part]
		if !ok {
			return "", fmt.Errorf("no such key: %s", path)
		}
	}
	return fmt.Sprintf("%v", cur), nil
}

// Set assigns a value at a dotted path, then re-hydrates the struct.
func (c *Config) Set(path, value string) error {
	node := map[string]any{}
	raw, _ := yaml.Marshal(c)
	_ = yaml.Unmarshal(raw, &node)

	parts := strings.Split(path, ".")
	cur := node
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	leaf := parts[len(parts)-1]
	cur[leaf] = coerce(value)

	raw, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	fresh := Default()
	if err := yaml.Unmarshal(raw, fresh); err != nil {
		return fmt.Errorf("invalid value for %s: %w", path, err)
	}
	*c = *fresh
	return nil
}

func coerce(v string) any {
	if i, err := strconv.Atoi(v); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return v
}

// Dump renders the effective config as YAML for `itan config list`.
func (c *Config) Dump() string {
	raw, _ := yaml.Marshal(c)
	return string(raw)
}
