package tools

import (
	"fmt"
	"path/filepath"

	"github.com/olaysco/itan/internal/media"
)

func audioTools() []Tool {
	return []Tool{
		{
			Name:        "transcribe",
			Description: "Transcribe the speech in the video/audio to text (Whisper-class STT). Use before correcting speech.",
			Schema: schema(nil, map[string]map[string]any{
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runTranscribe,
		},
		{
			Name: "tts",
			Description: "Synthesize speech from text with the configured voice model; returns the path of the " +
				"generated wav. Chain with replace_audio to put it on the video.",
			Schema: schema([]string{"text"}, map[string]map[string]any{
				"text": prop("string", "Exact text to speak."),
			}),
			Run: runTTS,
		},
		{
			Name:        "extract_audio",
			Description: "Extract the audio track to a wav file; returns its path.",
			Schema: schema(nil, map[string]map[string]any{
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runExtractAudio,
		},
		{
			Name:        "replace_audio",
			Description: "Replace the video's audio track with the given audio file (video stream untouched).",
			Schema: schema([]string{"audio"}, map[string]map[string]any{
				"audio": prop("string", "Path to the new audio file (e.g. a tts output)."),
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runReplaceAudio, Mutating: true,
		},
		{
			Name:        "mix_audio",
			Description: "Mix an extra audio file (e.g. background music) under the existing track.",
			Schema: schema([]string{"audio"}, map[string]map[string]any{
				"audio":  prop("string", "Path to the music/effect audio file."),
				"volume": prop("number", "Level for the added track, 0–1 (default 0.25)."),
				"input":  prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runMixAudio, Mutating: true,
		},
	}
}

func runTranscribe(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	if !info.HasAudio {
		return fail("%s has no audio track", filepath.Base(in))
	}
	wav := c.Project.NextOutput("stt-src", ".wav")
	if err := media.Run(c.Context, "-i", in, "-vn", "-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1", wav); err != nil {
		return Result{Err: err}
	}
	text, err := c.STT.Transcribe(c.Context, wav)
	if err != nil {
		return Result{Err: fmt.Errorf("STT (%s): %w", c.STT.Describe(), err)}
	}
	return Result{
		Summary: "transcribed speech",
		Data:    map[string]any{"transcript": text},
	}
}

func runTTS(c *Ctx, args Args) Result {
	text := args.Str("text")
	if text == "" {
		return fail("tts needs `text`")
	}
	out := c.Project.NextOutput("tts", ".wav")
	if err := c.TTS.Speak(c.Context, text, out); err != nil {
		return Result{Err: fmt.Errorf("TTS (%s): %w", c.TTS.Describe(), err)}
	}
	return Result{
		Summary: fmt.Sprintf("synthesized %d chars of speech", len(text)),
		Data:    map[string]any{"audio": out},
	}
}

func runExtractAudio(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	if !info.HasAudio {
		return fail("%s has no audio track", filepath.Base(in))
	}
	out := c.Project.NextOutput("audio", ".wav")
	if err := media.Run(c.Context, "-i", in, "-vn", "-acodec", "pcm_s16le", out); err != nil {
		return Result{Err: err}
	}
	return Result{Summary: "audio extracted", Data: map[string]any{"audio": out}}
}

func runReplaceAudio(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	audio := args.Str("audio")
	if audio == "" {
		return fail("replace_audio needs `audio`")
	}
	out := c.Project.NextOutput("newaudio", ".mp4")
	err = media.Run(c.Context,
		"-i", in, "-i", audio,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "copy", "-c:a", "aac", "-shortest", out)
	if err != nil {
		return Result{Err: err}
	}
	return Result{Summary: "audio track replaced with " + filepath.Base(audio), Output: out}
}

func runMixAudio(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	audio := args.Str("audio")
	if audio == "" {
		return fail("mix_audio needs `audio`")
	}
	vol := args.Float("volume", 0.25)
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	out := c.Project.NextOutput("mix", ".mp4")
	var fc string
	if info.HasAudio {
		fc = fmt.Sprintf("[1:a]volume=%.3f[m];[0:a][m]amix=inputs=2:duration=first:dropout_transition=2[a]", vol)
	} else {
		fc = fmt.Sprintf("[1:a]volume=%.3f[a]", vol)
	}
	err = media.Run(c.Context,
		"-i", in, "-stream_loop", "-1", "-i", audio,
		"-filter_complex", fc,
		"-map", "0:v:0", "-map", "[a]",
		"-c:v", "copy", "-c:a", "aac", "-shortest", out)
	if err != nil {
		return Result{Err: err}
	}
	return Result{Summary: fmt.Sprintf("mixed in %s at %.0f%%", filepath.Base(audio), vol*100), Output: out}
}
