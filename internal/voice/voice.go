// Package voice provides speech synthesis and transcription behind small
// interfaces so audio models are swappable in config.
//
// Defaults are open-source-first:
//   - TTS: Kokoro-82M via kokoro-fastapi (OpenAI-compatible /audio/speech)
//   - STT: Whisper via any OpenAI-compatible /audio/transcriptions server
//     (faster-whisper-server, whisper.cpp server, speaches, ...)
//
// Hosted alternatives (ElevenLabs, OpenAI) are one config switch away.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TTS interface {
	// Speak synthesizes text into an audio file at outPath.
	Speak(ctx context.Context, text, outPath string) error
	Describe() string
}

type STT interface {
	// Transcribe returns the speech content of an audio file as text.
	Transcribe(ctx context.Context, audioPath string) (string, error)
	Describe() string
}

var httpClient = &http.Client{Timeout: 4 * time.Minute}

// --- OpenAI-compatible TTS (Kokoro, OpenAI, any /v1/audio/speech server) ---

type OpenAITTS struct {
	BaseURL, APIKey, Model, Voice, Label string
}

func (t *OpenAITTS) Describe() string {
	return fmt.Sprintf("%s (model=%s voice=%s @ %s)", t.Label, t.Model, t.Voice, t.BaseURL)
}

func (t *OpenAITTS) Speak(ctx context.Context, text, outPath string) error {
	body, _ := json.Marshal(map[string]any{
		"model":           t.Model,
		"voice":           t.Voice,
		"input":           text,
		"response_format": "wav",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", t.BaseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable: %w", t.Label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s: %s", t.Label, resp.Status, payload)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// --- ElevenLabs TTS --------------------------------------------------------

type ElevenLabsTTS struct {
	BaseURL, APIKey, Model, Voice string
}

func (t *ElevenLabsTTS) Describe() string {
	return fmt.Sprintf("elevenlabs (model=%s voice=%s)", t.Model, t.Voice)
}

func (t *ElevenLabsTTS) Speak(ctx context.Context, text, outPath string) error {
	if t.APIKey == "" {
		return fmt.Errorf("elevenlabs: set ELEVENLABS_API_KEY")
	}
	body, _ := json.Marshal(map[string]any{
		"text":     text,
		"model_id": t.Model,
	})
	url := fmt.Sprintf("%s/v1/text-to-speech/%s", t.BaseURL, t.Voice)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", t.APIKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("elevenlabs: %s: %s", resp.Status, payload)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// --- OpenAI-compatible STT (Whisper servers, OpenAI) -----------------------

type OpenAISTT struct {
	BaseURL, APIKey, Model, Label string
}

func (s *OpenAISTT) Describe() string {
	return fmt.Sprintf("%s (model=%s @ %s)", s.Label, s.Model, s.BaseURL)
}

func (s *OpenAISTT) Transcribe(ctx context.Context, audioPath string) (string, error) {
	file, err := os.Open(audioPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	_ = mw.WriteField("model", s.Model)
	_ = mw.WriteField("response_format", "text")
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s unreachable: %w", s.Label, err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s: %s: %s", s.Label, resp.Status, payload[:min(len(payload), 300)])
	}
	return strings.TrimSpace(string(payload)), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
