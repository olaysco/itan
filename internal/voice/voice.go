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
		return fmt.Errorf("%s unreachable (server not running? check itan doctor): %w", t.Label, err)
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
	status, payload, err := s.transcribeOnce(ctx, audioPath)
	if err != nil {
		return "", err
	}
	// Speaches-style servers 404 until the model is downloaded — install it
	// ourselves and retry, instead of failing the user's edit.
	if status == 404 && bytes.Contains(payload, []byte("not installed")) {
		if ierr := s.installModel(ctx); ierr != nil {
			return "", fmt.Errorf("%s: model %q is not installed on the STT server and auto-install failed: %v — install manually: curl -X POST %s/models/%s",
				s.Label, s.Model, ierr, s.BaseURL, s.Model)
		}
		status, payload, err = s.transcribeOnce(ctx, audioPath)
		if err != nil {
			return "", err
		}
	}
	if status >= 400 {
		return "", fmt.Errorf("%s: %d: %s", s.Label, status, payload[:min(len(payload), 300)])
	}
	return strings.TrimSpace(string(payload)), nil
}

func (s *OpenAISTT) transcribeOnce(ctx context.Context, audioPath string) (int, []byte, error) {
	file, err := os.Open(audioPath)
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return 0, nil, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return 0, nil, err
	}
	_ = mw.WriteField("model", s.Model)
	_ = mw.WriteField("response_format", "text")
	if err := mw.Close(); err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s unreachable (server not running? check itan doctor): %w", s.Label, err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, payload, nil
}

// installModel asks the STT server to download the configured model. First
// downloads take minutes — the generous timeout is deliberate.
func (s *OpenAISTT) installModel(ctx context.Context) error {
	ictx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ictx, "POST", s.BaseURL+"/models/"+s.Model, nil)
	if err != nil {
		return err
	}
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := (&http.Client{}).Do(req) // no client timeout: ctx bounds it
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("install returned %s: %s", resp.Status, payload)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
