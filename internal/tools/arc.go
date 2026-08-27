package tools

import (
	"fmt"
	"strings"

	"github.com/olaysco/itan/internal/media"
)

// A narrated piece has an arc that cannot be improvised: the script fixes
// what is said, the read fixes how long each scene lasts, the scenes get
// composed against those durations, and assembly lays the voice back onto
// the cut at the offsets the cut actually produced. The two ends of that arc
// live here. The middle stays with the model, because composing a scene is
// authorship — and keeping each stage a separate step is what lets the
// ledger revert one of them without unwinding the rest.

func arcTools() []Tool {
	return []Tool{
		{
			Name: "voice_scenes",
			Description: "Synthesize every storyboard scene's `say` line and retime the storyboard to the real " +
				"read. Do this AFTER storyboard and BEFORE composing: a line you guessed at 4s may read in 6.2s, " +
				"and composing to the guess is what makes narration run past its picture. Each scene's duration " +
				"becomes speech + a tail pause. Voice tracks are kept and reused; edit a scene's `say` and only " +
				"that scene re-synthesizes.",
			Schema: schema(nil, map[string]map[string]any{
				"pad":   prop("number", "Seconds of rest after each line (default 0.6)."),
				"force": prop("boolean", "Re-synthesize every scene, even unchanged ones."),
				"scene": prop("integer", "Only voice this scene number."),
			}),
			Run: runVoiceScenes,
		},
		{
			Name: "assemble",
			Description: "Join the rendered scenes in storyboard order and lay each scene's narration back on " +
				"at the offset the cut actually produced, optionally over a ducked music bed. This becomes " +
				"CURRENT. Every scene must be rendered first (storyboard shows which are not). Use this instead " +
				"of concat for a narrated piece — concat would join the pictures and lose the voice alignment.",
			Schema: schema(nil, map[string]map[string]any{
				"music":       prop("string", "Path or asset id of a music bed (optional)."),
				"music_level": prop("number", "Bed volume 0-1 (default 0.18)."),
				"transition":  prop("string", "Cross-fade style between scenes: 'cut' (default), 'fade', 'fadeblack'."),
			}),
			Run: runAssemble, Mutating: true,
		},
	}
}

func runVoiceScenes(c *Ctx, args Args) Result {
	scenes := c.Project.Scenes
	if len(scenes) == 0 {
		return fail("no storyboard yet — call storyboard first, with a `say` line per narrated scene")
	}
	pad := args.Float("pad", 0.6)
	if pad < 0 {
		pad = 0
	}
	force, _ := args["force"].(bool)
	only := args.Int("scene", 0)

	var voiced, reused, silent int
	var lines []string
	for i := range c.Project.Scenes {
		s := &c.Project.Scenes[i]
		if only > 0 && s.N != only {
			continue
		}
		if strings.TrimSpace(s.Say) == "" {
			silent++
			continue
		}
		if s.Voice != "" && !force && fileExists(s.Voice) {
			reused++
			continue
		}
		out := c.Project.NextOutput(fmt.Sprintf("vo-scene%d", s.N), ".wav")
		if err := c.TTS.Speak(c.Context, s.Say, out); err != nil {
			return Result{Err: fmt.Errorf("scene %d narration (%s): %w", s.N, c.TTS.Describe(), err)}
		}
		info, err := media.Probe(c.Context, out)
		if err != nil {
			return Result{Err: fmt.Errorf("scene %d: synthesized but unreadable: %w", s.N, err)}
		}
		s.Voice = out
		was := s.Duration
		s.Duration = info.Duration + pad
		voiced++
		lines = append(lines, fmt.Sprintf("scene %d: %.1fs → %.1fs", s.N, was, s.Duration))
	}
	if err := c.Project.Save(); err != nil {
		return Result{Err: err}
	}

	var total float64
	for _, s := range c.Project.Scenes {
		total += s.Duration
	}
	summary := fmt.Sprintf("voiced %d scenes (%d reused, %d silent) — piece is now %.1fs", voiced, reused, silent, total)
	if len(lines) > 0 {
		summary += ": " + strings.Join(lines, ", ")
	}
	if voiced > 0 {
		summary += ". Compose each scene to its NEW duration."
	}
	return Result{Summary: summary, Data: map[string]any{"total_duration": total}}
}

func runAssemble(c *Ctx, args Args) Result {
	scenes := c.Project.Scenes
	if len(scenes) == 0 {
		return fail("nothing to assemble — no storyboard")
	}
	var missing []string
	for _, s := range scenes {
		if s.Output == "" {
			missing = append(missing, fmt.Sprintf("%d", s.N))
		}
	}
	if len(missing) > 0 {
		return fail("scenes %s are not rendered yet — compose them and mark_rendered before assembling",
			strings.Join(missing, ", "))
	}

	// Offsets come from what the scene renders actually are, never from the
	// planned durations: a scene composed a beat short would otherwise drag
	// every later narration out of sync.
	offsets := make([]float64, len(scenes))
	var running float64
	w, h := 0, 0
	for i, s := range scenes {
		info, err := media.Probe(c.Context, s.Output)
		if err != nil {
			return Result{Err: fmt.Errorf("scene %d: %w", s.N, err)}
		}
		if info.Width*info.Height > w*h {
			w, h = info.Width, info.Height
		}
		offsets[i] = running
		running += info.Duration
	}
	if w <= 0 || h <= 0 {
		return fail("no scene has a video stream to assemble")
	}
	w, h = media.EvenDims(w, h)

	var ff []string
	var fc strings.Builder
	for i, s := range scenes {
		ff = append(ff, "-i", s.Output)
		fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30[v%d];",
			i, w, h, w, h, i)
		info, _ := media.Probe(c.Context, s.Output)
		if info.HasAudio {
			fmt.Fprintf(&fc, "[%d:a]aresample=44100[a%d];", i, i)
		} else {
			fmt.Fprintf(&fc, "anullsrc=r=44100:cl=stereo:d=%.3f[a%d];", maxf(info.Duration, 0.1), i)
		}
	}
	for i := range scenes {
		fmt.Fprintf(&fc, "[v%d][a%d]", i, i)
	}
	fmt.Fprintf(&fc, "concat=n=%d:v=1:a=1[vout][scenea];", len(scenes))

	// Each voice track is delayed to its scene's start on the assembled cut.
	next := len(scenes)
	mix := []string{"[scenea]"}
	for i, s := range scenes {
		if s.Voice == "" || !fileExists(s.Voice) {
			continue
		}
		ff = append(ff, "-i", s.Voice)
		ms := int(offsets[i]*1000 + 0.5)
		label := fmt.Sprintf("[vo%d]", i)
		fmt.Fprintf(&fc, "[%d:a]aresample=44100,adelay=%d|%d%s;", next, ms, ms, label)
		mix = append(mix, label)
		next++
	}

	music, _ := resolveInput(c, Args{"input": args.Str("music")})
	hasMusic := args.Str("music") != "" && music != ""
	if hasMusic {
		level := args.Float("music_level", 0.18)
		if level <= 0 || level > 1 {
			level = 0.18
		}
		fade := 1.0
		fadeOut := running - fade
		if fadeOut < 0 {
			fadeOut = 0
		}
		ff = append(ff, "-i", music)
		fmt.Fprintf(&fc, "[%d:a]aloop=loop=-1:size=2e9,atrim=0:%.3f,asetpts=N/SR/TB,volume=%.3f,afade=t=in:d=%.2f,afade=t=out:st=%.3f:d=%.2f[bed];",
			next, running, level, fade, fadeOut, fade)
	}

	// Narration and scene audio first; the bed is ducked against that mix so
	// music dips under speech rather than fighting it.
	voiceLabel := "[voices]"
	if len(mix) == 1 {
		fmt.Fprintf(&fc, "[scenea]anull%s;", voiceLabel)
	} else {
		fmt.Fprintf(&fc, "%samix=inputs=%d:duration=longest:normalize=0%s;", strings.Join(mix, ""), len(mix), voiceLabel)
	}
	if hasMusic {
		fmt.Fprintf(&fc, "[bed]%ssidechaincompress=threshold=0.03:ratio=8:attack=80:release=400[ducked];", voiceLabel)
		fmt.Fprintf(&fc, "%s[ducked]amix=inputs=2:duration=first:normalize=0[aout]", voiceLabel)
	} else {
		fmt.Fprintf(&fc, "%sanull[aout]", voiceLabel)
	}

	out := c.Project.NextOutput("assemble", ".mp4")
	ff = append(ff, "-filter_complex", fc.String(),
		"-map", "[vout]", "-map", "[aout]",
		"-c:v", "libx264", "-preset", "medium", "-crf", "18",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", out)
	if err := media.Run(c.Context, ff...); err != nil {
		return Result{Err: err}
	}

	narrated := len(mix) - 1
	summary := fmt.Sprintf("assembled %d scenes at %dx%d (%.1fs)", len(scenes), w, h, running)
	if narrated > 0 {
		summary += fmt.Sprintf(", %d narration tracks aligned to the cut", narrated)
	}
	if hasMusic {
		summary += ", music ducked under speech"
	}
	return Result{Summary: summary, Output: out}
}
