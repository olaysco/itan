package voice

import (
	"os"

	"github.com/olaysco/heydit/internal/config"
)

// TTSFromConfig builds the active TTS client from config.
func TTSFromConfig(cfg *config.Config) TTS {
	c := cfg.Audio.TTS
	key := ""
	if c.KeyEnv != "" {
		key = os.Getenv(c.KeyEnv)
	}
	if c.Provider == "elevenlabs" {
		return &ElevenLabsTTS{BaseURL: c.BaseURL, APIKey: key, Model: c.Model, Voice: c.Voice}
	}
	return &OpenAITTS{BaseURL: c.BaseURL, APIKey: key, Model: c.Model, Voice: c.Voice, Label: c.Provider}
}

// STTFromConfig builds the active STT client from config.
func STTFromConfig(cfg *config.Config) STT {
	c := cfg.Audio.STT
	key := ""
	if c.KeyEnv != "" {
		key = os.Getenv(c.KeyEnv)
	}
	return &OpenAISTT{BaseURL: c.BaseURL, APIKey: key, Model: c.Model, Label: c.Provider}
}
