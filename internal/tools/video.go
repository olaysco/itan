package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/olaysco/itan/internal/media"
)

func videoTools() []Tool {
	return []Tool{
		{
			Name:        "probe",
			Description: "Inspect a media file: dimensions, fps, duration, audio presence. Use when metadata is unknown.",
			Schema: schema(nil, map[string]map[string]any{
				"input": prop("string", "Asset id (a1) or path. Defaults to CURRENT."),
			}),
			Run:             runProbe,
			ConcurrencySafe: true,
		},
		{
			Name:        "trim",
			Description: "Cut the video to [start, end] seconds. Omit end to keep through the end.",
			Schema: schema([]string{"start"}, map[string]map[string]any{
				"start": prop("number", "Start time in seconds."),
				"end":   prop("number", "End time in seconds (optional)."),
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runTrim, Mutating: true,
		},
		{
			Name:        "concat",
			Description: "Join multiple clips into one video, normalized to the first clip's dimensions/fps.",
			Schema: schema([]string{"inputs"}, map[string]map[string]any{
				"inputs": {"type": "array", "items": map[string]any{"type": "string"}, "description": "Ordered asset ids or paths (2+)."},
			}),
			Run: runConcat, Mutating: true,
		},
		{
			Name:        "set_speed",
			Description: "Speed the video up or down. factor=2 is 2x faster, 0.5 is half speed. Audio pitch is preserved.",
			Schema: schema([]string{"factor"}, map[string]map[string]any{
				"factor": prop("number", "Speed multiplier in (0.25, 4]."),
				"input":  prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runSpeed, Mutating: true,
		},
		{
			Name:        "crop",
			Description: "Crop the frame. Give aspect (e.g. '9:16' center-crop) OR explicit w/h (+ optional x/y offsets).",
			Schema: schema(nil, map[string]map[string]any{
				"aspect": prop("string", "Target aspect like '9:16', '1:1', '16:9' — centered crop."),
				"w":      prop("integer", "Crop width in px."),
				"h":      prop("integer", "Crop height in px."),
				"x":      prop("integer", "Left offset (default centered)."),
				"y":      prop("integer", "Top offset (default centered)."),
				"input":  prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runCrop, Mutating: true,
		},
		{
			Name: "expand_frame",
			Description: "Expand/uncrop the canvas to a wider or taller aspect ratio, filling new area with a " +
				"content-aware blurred fill (mode=blur, default) or a solid color (mode=color).",
			Schema: schema([]string{"aspect"}, map[string]map[string]any{
				"aspect": prop("string", "Target aspect like '16:9', '9:16', '1:1', '2.39'."),
				"mode":   prop("string", "'blur' (default) or 'color'."),
				"color":  prop("string", "Fill color for mode=color (e.g. 'black', '#112233')."),
				"input":  prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runExpandFrame, Mutating: true,
		},
		{
			Name: "change_background",
			Description: "Restyle or replace the background. With `image` + a green/blue-screen source it chroma-keys " +
				"the subject over the image; otherwise applies a strong color grade toward `color` behind a vignette.",
			Schema: schema(nil, map[string]map[string]any{
				"color": prop("string", "Target background color/mood (e.g. 'blue', '#1e3a8a')."),
				"image": prop("string", "Path to a background image for chroma-key compositing."),
				"key":   prop("string", "Chroma key color, default '0x00FF00' (green screen)."),
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runChangeBackground, Mutating: true,
		},
		{
			Name:        "overlay_text",
			Description: "Burn a text caption onto the video for a time range.",
			Schema: schema([]string{"text"}, map[string]map[string]any{
				"text":     prop("string", "The caption text."),
				"position": prop("string", "'top' | 'center' | 'bottom' (default 'bottom')."),
				"start":    prop("number", "Show from this second (default 0)."),
				"end":      prop("number", "Hide after this second (default: whole video)."),
				"size":     prop("integer", "Font size (default 42)."),
				"color":    prop("string", "Font color (default 'white')."),
				"input":    prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runOverlayText, Mutating: true,
		},
		{
			Name: "render",
			Description: "Escape hatch: run a custom ffmpeg filtergraph on the working video when no dedicated tool fits. " +
				"Provide `vf` (video filter) and/or `af` (audio filter). Output handling is managed by the harness.",
			Schema: schema(nil, map[string]map[string]any{
				"vf":    prop("string", "ffmpeg -vf video filter chain."),
				"af":    prop("string", "ffmpeg -af audio filter chain."),
				"note":  prop("string", "One short line describing the intent (for the edit ledger)."),
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runRender, Mutating: true,
		},
		{
			Name:        "export",
			Description: "Export the CURRENT working video to a final path (e.g. 'final.mp4' in the project dir).",
			Schema: schema(nil, map[string]map[string]any{
				"path": prop("string", "Destination path; defaults to '<project>/itan-export.mp4'."),
			}),
			Run: runExport,
		},
	}
}

// --- implementations -------------------------------------------------------

func runProbe(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	return Result{
		Summary: fmt.Sprintf("%s: %s", filepath.Base(in), info.Compact()),
		Data:    map[string]any{"aspect": fmt.Sprintf("%.3f", info.Aspect())},
	}
}

func runTrim(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	start := args.Float("start", 0)
	end := args.Float("end", -1)
	out := c.Project.NextOutput("trim", ".mp4")

	ff := []string{"-ss", fmt.Sprintf("%.3f", start), "-i", in}
	if end > start {
		ff = append(ff, "-t", fmt.Sprintf("%.3f", end-start))
	}
	ff = append(ff, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", out)
	if err := media.Run(c.Context, ff...); err != nil {
		return Result{Err: err}
	}
	desc := fmt.Sprintf("trimmed to %.1fs–", start)
	if end > start {
		desc += fmt.Sprintf("%.1fs", end)
	} else {
		desc += "end"
	}
	return Result{Summary: desc, Output: out}
}

func runConcat(c *Ctx, args Args) Result {
	rawList, ok := args["inputs"].([]any)
	if !ok || len(rawList) < 2 {
		return fail("concat needs an `inputs` array of 2+ clips")
	}
	var paths []string
	for _, item := range rawList {
		p, err := resolveInput(c, Args{"input": item})
		if err != nil {
			return Result{Err: err}
		}
		paths = append(paths, p)
	}
	first, err := media.Probe(c.Context, paths[0])
	if err != nil {
		return Result{Err: err}
	}
	w, h := media.EvenDims(first.Width, first.Height)

	ff := []string{}
	var fc strings.Builder
	for i, p := range paths {
		ff = append(ff, "-i", p)
		// Normalize every clip (and give silent clips a real audio track).
		fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30[v%d];", i, w, h, w, h, i)
		info, perr := media.Probe(c.Context, p)
		if perr == nil && info.HasAudio {
			fmt.Fprintf(&fc, "[%d:a]aresample=44100[a%d];", i, i)
		} else {
			fmt.Fprintf(&fc, "anullsrc=r=44100:cl=stereo:d=%.3f[a%d];", maxf(info.Duration, 0.1), i)
		}
	}
	for i := range paths {
		fmt.Fprintf(&fc, "[v%d][a%d]", i, i)
	}
	fmt.Fprintf(&fc, "concat=n=%d:v=1:a=1[v][a]", len(paths))

	out := c.Project.NextOutput("concat", ".mp4")
	ff = append(ff, "-filter_complex", fc.String(), "-map", "[v]", "-map", "[a]",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", out)
	if err := media.Run(c.Context, ff...); err != nil {
		return Result{Err: err}
	}
	return Result{Summary: fmt.Sprintf("joined %d clips at %dx%d", len(paths), w, h), Output: out}
}

func runSpeed(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	factor := args.Float("factor", 0)
	if factor <= 0.25 || factor > 4 {
		return fail("factor must be in (0.25, 4], got %v", factor)
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	out := c.Project.NextOutput("speed", ".mp4")
	vf := fmt.Sprintf("setpts=PTS/%.4f", factor)
	ff := []string{"-i", in, "-vf", vf}
	if info.HasAudio {
		ff = append(ff, "-af", fmt.Sprintf("atempo=%.4f", factor))
	}
	ff = append(ff, "-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if err := media.Run(c.Context, ff...); err != nil {
		return Result{Err: err}
	}
	return Result{Summary: fmt.Sprintf("speed ×%.2f (now ~%.1fs)", factor, info.Duration/factor), Output: out}
}

func runCrop(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}

	w, h := args.Int("w", 0), args.Int("h", 0)
	if aspect := args.Str("aspect"); aspect != "" && (w == 0 || h == 0) {
		ratio, perr := ParseAspect(aspect)
		if perr != nil {
			return Result{Err: perr}
		}
		if ratio < info.Aspect() { // narrower: keep height
			h = info.Height
			w = int(float64(h) * ratio)
		} else { // wider: keep width
			w = info.Width
			h = int(float64(w) / ratio)
		}
	}
	if w <= 0 || h <= 0 {
		return fail("crop needs `aspect` or positive `w` and `h`")
	}
	if w > info.Width || h > info.Height {
		return fail("crop %dx%d exceeds source %dx%d — use expand_frame to grow the canvas", w, h, info.Width, info.Height)
	}
	w, h = w-w%2, h-h%2
	x := args.Int("x", (info.Width-w)/2)
	y := args.Int("y", (info.Height-h)/2)

	out := c.Project.NextOutput("crop", ".mp4")
	vf := fmt.Sprintf("crop=%d:%d:%d:%d", w, h, x, y)
	if err := media.Run(c.Context, "-i", in, "-vf", vf, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "copy", out); err != nil {
		return Result{Err: err}
	}
	return Result{Summary: fmt.Sprintf("cropped to %dx%d @ (%d,%d)", w, h, x, y), Output: out}
}

func runExpandFrame(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	ratio, err := ParseAspect(args.Str("aspect"))
	if err != nil {
		return Result{Err: err}
	}

	var newW, newH int
	if ratio >= info.Aspect() { // widen
		newW, newH = media.EvenDims(int(float64(info.Height)*ratio), info.Height)
	} else { // heighten
		newW, newH = media.EvenDims(info.Width, int(float64(info.Width)/ratio))
	}

	out := c.Project.NextOutput("expand", ".mp4")
	mode := args.Str("mode")
	if mode == "color" {
		color := args.Str("color")
		if color == "" {
			color = "black"
		}
		vf := fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:%s", newW, newH, color)
		if err := media.Run(c.Context, "-i", in, "-vf", vf, "-c:a", "copy", out); err != nil {
			return Result{Err: err}
		}
	} else {
		fc := fmt.Sprintf(
			"[0:v]split[bg][fg];[bg]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,boxblur=24:2[bgb];[bgb][fg]overlay=(W-w)/2:(H-h)/2",
			newW, newH, newW, newH)
		if err := media.Run(c.Context, "-i", in, "-filter_complex", fc, "-c:a", "copy", out); err != nil {
			return Result{Err: err}
		}
		mode = "blur"
	}
	return Result{
		Summary: fmt.Sprintf("expanded %dx%d → %dx%d (%s fill)", info.Width, info.Height, newW, newH, mode),
		Output:  out,
		Data:    map[string]any{"aspect": fmt.Sprintf("%.3f", float64(newW)/float64(newH))},
	}
}

func runChangeBackground(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	out := c.Project.NextOutput("background", ".mp4")

	if img := args.Str("image"); img != "" {
		key := args.Str("key")
		if key == "" {
			key = "0x00FF00"
		}
		fc := fmt.Sprintf(
			"[1:v]scale=%d:%d[bg];[0:v]chromakey=%s:0.3:0.1[fg];[bg][fg]overlay=shortest=1",
			info.Width, info.Height, key)
		err := media.Run(c.Context, "-i", in, "-loop", "1", "-i", img,
			"-filter_complex", fc, "-c:a", "copy", "-shortest", out)
		if err != nil {
			return Result{Err: err}
		}
		return Result{Summary: fmt.Sprintf("chroma-keyed subject over %s", filepath.Base(img)), Output: out}
	}

	color := args.Str("color")
	if color == "" {
		color = "#0f172a"
	}
	dur := maxf(info.Duration, 0.5)
	fps := info.FPS
	if fps == 0 {
		fps = 25
	}
	// Finite tint source composited over the video — bounded by construction.
	fc := fmt.Sprintf(
		"color=c=%s@0.35:s=%dx%d:d=%.3f:r=%.4g,format=yuva420p[tint];[0:v][tint]overlay=shortest=1,vignette=PI/4",
		color, info.Width, info.Height, dur, fps)
	if err := media.Run(c.Context, "-i", in, "-filter_complex", fc, "-c:a", "copy", out); err != nil {
		return Result{Err: err}
	}
	return Result{Summary: fmt.Sprintf("graded background toward %s with vignette", color), Output: out}
}

func runOverlayText(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	text := args.Str("text")
	if text == "" {
		return fail("overlay_text needs `text`")
	}
	size := args.Int("size", 42)
	color := args.Str("color")
	if color == "" {
		color = "white"
	}
	var yExpr string
	switch args.Str("position") {
	case "top":
		yExpr = "h*0.08"
	case "center":
		yExpr = "(h-th)/2"
	default:
		yExpr = "h*0.85-th"
	}
	start := args.Float("start", 0)
	end := args.Float("end", -1)
	enable := fmt.Sprintf("gte(t\\,%.3f)", start)
	if end > start {
		enable = fmt.Sprintf("between(t\\,%.3f\\,%.3f)", start, end)
	}
	vf := fmt.Sprintf(
		"drawtext=text='%s':fontcolor=%s:fontsize=%d:x=(w-tw)/2:y=%s:borderw=2:bordercolor=black@0.6:enable='%s'",
		escapeDrawtext(text), color, size, yExpr, enable)

	out := c.Project.NextOutput("text", ".mp4")
	if err := media.Run(c.Context, "-i", in, "-vf", vf, "-c:a", "copy", out); err != nil {
		return Result{Err: err}
	}
	return Result{Summary: fmt.Sprintf("caption %q burned in", clip(text, 40)), Output: out}
}

func runRender(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	vf, af := args.Str("vf"), args.Str("af")
	if vf == "" && af == "" {
		return fail("render needs `vf` and/or `af`")
	}
	out := c.Project.NextOutput("render", ".mp4")
	ff := []string{"-i", in}
	if vf != "" {
		ff = append(ff, "-vf", vf)
	}
	if af != "" {
		ff = append(ff, "-af", af)
	} else {
		ff = append(ff, "-c:a", "copy")
	}
	ff = append(ff, "-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if err := media.Run(c.Context, ff...); err != nil {
		return Result{Err: err}
	}
	note := args.Str("note")
	if note == "" {
		note = "custom filtergraph"
	}
	return Result{Summary: note, Output: out}
}

func runExport(c *Ctx, args Args) Result {
	if c.Project.Current == "" {
		return fail("nothing to export yet")
	}
	dest := args.Str("path")
	if dest == "" {
		dest = filepath.Join(c.Project.Dir, "itan-export.mp4")
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(c.Project.Dir, dest)
	}
	// Overwrites are gated by the safety tier upstream; once approved, the
	// old version is still preserved so nothing is ever destroyed.
	backup, err := backupIfExists(dest, c.Project)
	if err != nil {
		return Result{Err: err}
	}
	if err := media.Run(c.Context, "-i", c.Project.Current, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", dest); err != nil {
		return Result{Err: err}
	}
	data := map[string]any{"path": dest}
	if backup != "" {
		data["previous_version"] = backup
	}
	return Result{Summary: "exported to " + dest, Data: data}
}

// backupIfExists copies an about-to-be-overwritten file into .itan/backup.
func backupIfExists(dest string, p *media.Project) (string, error) {
	if _, err := os.Stat(dest); err != nil {
		return "", nil
	}
	dir := filepath.Join(p.Dir, ".itan", "backup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	backup := filepath.Join(dir, time.Now().UTC().Format("20060102-150405")+"-"+filepath.Base(dest))
	src, err := os.Open(dest)
	if err != nil {
		return "", err
	}
	defer src.Close()
	out, err := os.Create(backup)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return "", err
	}
	return backup, nil
}

// --- helpers ---------------------------------------------------------------

var aspectWords = map[string]float64{
	"widescreen": 16.0 / 9, "landscape": 16.0 / 9, "wide": 16.0 / 9,
	"cinematic": 2.39, "square": 1, "vertical": 9.0 / 16, "portrait": 9.0 / 16,
	"story": 9.0 / 16, "reel": 9.0 / 16, "tiktok": 9.0 / 16, "short": 9.0 / 16,
}

var ratioRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[:x/]\s*(\d+(?:\.\d+)?)`)

// ParseAspect turns "16:9", "1.85", "vertical", "9x16" into a float ratio.
func ParseAspect(s string) (float64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return 0, fmt.Errorf("missing aspect (e.g. '16:9', '9:16', '1:1')")
	}
	if m := ratioRe.FindStringSubmatch(t); m != nil {
		var w, h float64
		fmt.Sscanf(m[1], "%g", &w)
		fmt.Sscanf(m[2], "%g", &h)
		if h > 0 && w > 0 {
			return w / h, nil
		}
	}
	if r, ok := aspectWords[t]; ok {
		return r, nil
	}
	var f float64
	if _, err := fmt.Sscanf(t, "%g", &f); err == nil && f > 0.1 && f < 10 {
		return f, nil
	}
	return 0, fmt.Errorf("cannot parse aspect %q", s)
}

func escapeDrawtext(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `:`, `\:`, `%`, `\%`)
	return r.Replace(s)
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
