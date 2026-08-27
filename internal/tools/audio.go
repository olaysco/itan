package tools

import (
	"fmt"
	"os"
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
		{
			Name: "add_music",
			Description: "Lay a music bed under the whole video: loops or trims the track to fit, fades in/out, " +
				"and when the video has speech, auto-DUCKS the music under it (sidechain). This is what makes a " +
				"video feel finished — prefer it over mix_audio for soundtracks.",
			Schema: schema([]string{"audio"}, map[string]map[string]any{
				"audio":  prop("string", "Path to the music file (wav/mp3/m4a)."),
				"volume": prop("number", "Music level 0–1 (default 0.20)."),
				"duck":   {"type": "boolean", "description": "Duck the music under existing speech (default true when the video has audio)."},
				"fade":   prop("number", "Fade in/out seconds (default 1.0)."),
				"input":  prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runAddMusic, Mutating: true,
		},
	}
}

// runAddMusic builds the full music-bed graph: loop → trim to duration →
// level → fades → optional sidechain duck against the existing track → mix.
func runAddMusic(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	music := args.Str("audio")
	if music == "" {
		return fail("add_music needs `audio` (a music file path)")
	}
	if _, err := os.Stat(music); err != nil {
		return fail("music file not found: %s", music)
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	vol := args.Float("volume", 0.20)
	if vol <= 0 || vol > 1 {
		vol = 0.20
	}
	fade := args.Float("fade", 1.0)
	duck := info.HasAudio
	if v, ok := args["duck"].(bool); ok {
		duck = v && info.HasAudio
	}
	dur := info.Duration
	fadeOutStart := dur - fade
	if fadeOutStart < 0 {
		fadeOutStart = 0
	}

	// Music chain: infinite loop → cut to video length → level → fades.
	bed := fmt.Sprintf(
		"[1:a]aloop=loop=-1:size=2e9,atrim=0:%.3f,asetpts=N/SR/TB,volume=%.3f,afade=t=in:d=%.2f,afade=t=out:st=%.3f:d=%.2f[bed]",
		dur, vol, fade, fadeOutStart, fade)

	out := c.Project.NextOutput("music", ".mp4")
	var fc string
	if duck {
		// Speech is the sidechain key: music dips when the voice speaks.
		fc = bed + ";[bed][0:a]sidechaincompress=threshold=0.03:ratio=8:attack=80:release=400[ducked];" +
			"[0:a][ducked]amix=inputs=2:duration=first:normalize=0[aout]"
	} else if info.HasAudio {
		fc = bed + ";[0:a][bed]amix=inputs=2:duration=first:normalize=0[aout]"
	} else {
		fc = bed + ";[bed]anull[aout]"
	}
	if err := media.Run(c.Context, "-i", in, "-i", music,
		"-filter_complex", fc, "-map", "0:v", "-map", "[aout]",
		"-c:v", "copy", "-c:a", "aac", "-shortest", out); err != nil {
		return Result{Err: err}
	}
	how := "mixed"
	if duck {
		how = "ducked under the speech"
	}
	return Result{
		Summary: fmt.Sprintf("music bed %s at %.0f%%, %.1fs fades (%s)", how, vol*100, fade, filepath.Base(music)),
		Output:  out,
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
