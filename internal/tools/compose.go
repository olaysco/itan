package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olaysco/itan/internal/canvas"
	"github.com/olaysco/itan/internal/media"
)

func composeTools() []Tool {
	return []Tool{
		{
			Name: "compose",
			Description: "Create a motion-graphics clip (intro/title card, animated caption, explainer scene, " +
				"end screen) by writing a complete self-contained HTML document — it renders to video in a real " +
				"browser. Animate with the built-in frame API (exact at every frame, nothing to import): " +
				"`itan.frame(({frame, fps}) => { el.style.opacity = interpolate(frame, [0,15], [0,1]); })`, plus " +
				"`spring({frame, fps, config})` and `Seq(from, durationInFrames)`. CSS animations/transitions, the " +
				"Web Animations API, and GSAP 3 (+SplitText, pre-bundled — reference `gsap` and it is injected) all " +
				"work too; build one gsap.timeline() per scene. Renders are deterministic. Give elements " +
				"data-start=\"2\" data-duration=\"3\" (seconds) to control when they appear. Inline all CSS/JS — " +
				"renders are offline, so no network URLs, but LOCAL FILES ARE FINE and encouraged: embed the " +
				"project's logo, screenshots, or capture_page output with " +
				"<img src=\"file:///absolute/path.png\">. Compose at the delivery size (default 1920x1080) and " +
				"size type for that canvas — concat joins on the largest clip and letterboxes smaller ones. The " +
				"engine is a full browser, so depth is available and expected: CSS 3D (perspective/rotateY/" +
				"preserve-3d), layered box-shadows, backdrop-filter glass, blurred radial glows, and Canvas 2D or " +
				"WebGL when you draw from itan.frame rather than requestAnimationFrame. The " +
				"fonts 'Bricolage Grotesque' (display, weights 200-800) and 'IBM Plex Mono' are pre-installed — " +
				"use them, never system-ui. The result becomes a new project ASSET (it does not replace CURRENT) — " +
				"chain with concat for intros/outros or overlay_video to put it on footage.",
			Schema: schema([]string{"html", "duration"}, map[string]map[string]any{
				"html":     prop("string", "Complete HTML document, self-contained."),
				"duration": prop("number", "Clip length in seconds (max 120)."),
				"width":    prop("integer", "Canvas width px (default 1920)."),
				"height":   prop("integer", "Canvas height px (default 1080)."),
				"fps":      prop("integer", "Frame rate (default 30)."),
			}),
			Run: runCompose,
		},
		{
			Name: "overlay_video",
			Description: "Composite another clip (e.g. a compose result) over the working video for a time range. " +
				"Position defaults to centered; pass key to chroma-key a solid background out of the overlay.",
			Schema: schema([]string{"overlay"}, map[string]map[string]any{
				"overlay": prop("string", "Asset id (a2) or path of the clip to overlay."),
				"x":       prop("integer", "Left position in px (default: centered)."),
				"y":       prop("integer", "Top position in px (default: centered)."),
				"scale":   prop("integer", "Scale overlay to this width in px (optional)."),
				"start":   prop("number", "Show from this second (default 0)."),
				"end":     prop("number", "Hide after this second (default: overlay's end)."),
				"key":     prop("string", "Chroma key color to make transparent, e.g. '0x00FF00' (optional)."),
				"input":   prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runOverlayVideo, Mutating: true,
		},
	}
}

func runCompose(c *Ctx, args Args) Result {
	html := args.Str("html")
	if strings.TrimSpace(html) == "" {
		return fail("compose needs `html`")
	}
	dur := args.Float("duration", 0)
	if dur <= 0 || dur > 120 {
		return fail("duration must be in (0, 120] seconds, got %v", dur)
	}
	// A composition that reaches for the network renders wrong, silently:
	// the page loads, the library never arrives, and nothing moves. Drop the
	// dead references and say so instead of shipping a still video.
	html, external := canvas.StripExternal(html)

	out := c.Project.NextOutput("compose", ".mp4")
	// Keep the source next to the render so the composition is inspectable
	// and re-editable later (read_text can fetch it back).
	htmlPath := strings.TrimSuffix(out, ".mp4") + ".html"

	err := canvas.Render(c.Context, canvas.Opts{
		HTML:     html,
		Width:    args.Int("width", 1920),
		Height:   args.Int("height", 1080),
		FPS:      args.Int("fps", 30),
		Duration: dur,
		OutPath:  out,
	})
	if err != nil {
		return Result{Err: err}
	}
	_ = os.WriteFile(htmlPath, []byte(html), 0o644)

	// A composition is new material, not an edit of CURRENT: register it as
	// an asset so concat/overlay_video can reference it by id.
	asset, err := c.Project.AddAsset(c.Context, out)
	if err != nil {
		return Result{Err: fmt.Errorf("rendered but could not register asset: %w", err)}
	}
	summary := fmt.Sprintf("composed %.1fs graphic as asset %s", dur, asset.ID)
	data := map[string]any{
		"asset": asset.ID, "file": out, "html": htmlPath,
		"now": asset.Info.Compact(),
	}
	if note := canvas.ExternalNote(external); note != "" {
		summary += " — " + note
		data["external_dropped"] = external
	}
	return Result{Summary: summary, Data: data}
}

func runOverlayVideo(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	ov, err := resolveInput(c, Args{"input": args.Str("overlay")})
	if err != nil || args.Str("overlay") == "" {
		return fail("overlay_video needs `overlay` (asset id or path)")
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}

	var fc strings.Builder
	label := "[1:v]"
	if scale := args.Int("scale", 0); scale > 0 {
		fmt.Fprintf(&fc, "%sscale=%d:-2[sc];", label, scale-scale%2)
		label = "[sc]"
	}
	if key := args.Str("key"); key != "" {
		fmt.Fprintf(&fc, "%scolorkey=%s:0.3:0.1[ky];", label, key)
		label = "[ky]"
	}
	x, y := "(W-w)/2", "(H-h)/2"
	if _, ok := args["x"]; ok {
		x = fmt.Sprintf("%d", args.Int("x", 0))
	}
	if _, ok := args["y"]; ok {
		y = fmt.Sprintf("%d", args.Int("y", 0))
	}
	start := args.Float("start", 0)
	end := args.Float("end", -1)
	enable := fmt.Sprintf("gte(t,%.3f)", start)
	if end > start {
		enable = fmt.Sprintf("between(t,%.3f,%.3f)", start, end)
	}
	fmt.Fprintf(&fc, "[0:v]%soverlay=x=%s:y=%s:eof_action=pass:enable='%s'[v]", label, x, y, enable)

	out := c.Project.NextOutput("overlay", ".mp4")
	ff := []string{"-i", in}
	if start > 0 {
		// Delay the overlay stream so its first frame lands at `start`.
		ff = append(ff, "-itsoffset", fmt.Sprintf("%.3f", start))
	}
	ff = append(ff, "-i", ov,
		"-filter_complex", fc.String(),
		"-map", "[v]", "-map", "0:a?",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "copy",
		out)
	if err := media.Run(c.Context, ff...); err != nil {
		return Result{Err: err}
	}
	desc := fmt.Sprintf("overlaid %s on %dx%d from %.1fs", filepath.Base(ov), info.Width, info.Height, start)
	if end > start {
		desc += fmt.Sprintf(" to %.1fs", end)
	}
	return Result{Summary: desc, Output: out}
}
