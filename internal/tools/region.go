package tools

import (
	"fmt"

	"github.com/olaysco/itan/internal/media"
)

// Region tools operate on a fixed rectangle of the frame — blur it, pixelate
// it, or punch in on it. The rectangle is STATIC in frame coordinates; nothing
// here tracks motion, and every tool description says so, steering
// moving-subject requests toward shorter time-ranged applications instead.

const staticRegionNote = "The region is FIXED in frame coordinates — it does not track moving subjects. " +
	"For a moving subject, apply this over a shorter time range (or several ranges) so the region stays accurate."

func regionTools() []Tool {
	return []Tool{
		{
			Name: "blur_region",
			Description: "Blur a rectangular region of the frame (hide a logo, face, or plate). " +
				"Rects partially outside the frame are clamped. " + staticRegionNote,
			Schema: schema([]string{"x", "y", "w", "h"}, map[string]map[string]any{
				"x":        prop("integer", "Region left edge in source pixels."),
				"y":        prop("integer", "Region top edge in source pixels."),
				"w":        prop("integer", "Region width in px."),
				"h":        prop("integer", "Region height in px."),
				"strength": prop("integer", "Blur strength (boxblur radius, default 20)."),
				"start":    prop("number", "Apply from this second (optional)."),
				"end":      prop("number", "Stop after this second (optional; omit both for the whole video)."),
				"input":    prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runBlurRegion, Mutating: true,
		},
		{
			Name: "pixelate_region",
			Description: "Pixelate (mosaic) a rectangular region of the frame. " +
				"Rects partially outside the frame are clamped. " + staticRegionNote,
			Schema: schema([]string{"x", "y", "w", "h"}, map[string]map[string]any{
				"x":      prop("integer", "Region left edge in source pixels."),
				"y":      prop("integer", "Region top edge in source pixels."),
				"w":      prop("integer", "Region width in px."),
				"h":      prop("integer", "Region height in px."),
				"factor": prop("integer", "Pixelation factor — size of the mosaic blocks in px (default 12)."),
				"start":  prop("number", "Apply from this second (optional)."),
				"end":    prop("number", "Stop after this second (optional; omit both for the whole video)."),
				"input":  prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runPixelateRegion, Mutating: true,
		},
		{
			Name: "zoom_region",
			Description: "Punch in on a rectangular region: crop it and scale it back up to the source resolution, " +
				"for the WHOLE clip (no start/end — trim or split first to zoom a segment). The rect is minimally " +
				"grown to match the source aspect ratio (and even dimensions) so nothing distorts. " +
				"The region is fixed in frame coordinates — it does not follow a moving subject.",
			Schema: schema([]string{"x", "y", "w", "h"}, map[string]map[string]any{
				"x":     prop("integer", "Region left edge in source pixels."),
				"y":     prop("integer", "Region top edge in source pixels."),
				"w":     prop("integer", "Region width in px."),
				"h":     prop("integer", "Region height in px."),
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run: runZoomRegion, Mutating: true,
		},
	}
}

// --- implementations -------------------------------------------------------

func runBlurRegion(c *Ctx, args Args) Result {
	in, info, r, res := regionSetup(c, args)
	if res != nil {
		return *res
	}
	// boxblur caps the radius at min(w,h)/2; clamp so the render can't fail.
	strength := args.Int("strength", 20)
	if maxR := min(r.W, r.H) / 2; strength > maxR {
		strength = maxR
	}
	if strength < 1 {
		strength = 1
	}
	start, end := args.Float("start", 0), args.Float("end", -1)
	if end > info.Duration {
		end = info.Duration
	}
	fc := blurRegionGraph(r, strength, start, end)
	out := c.Project.NextOutput("blur", ".mp4")
	if err := media.Run(c.Context, "-i", in, "-filter_complex", fc, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "copy", out); err != nil {
		return Result{Err: err}
	}
	return Result{
		Summary: fmt.Sprintf("blurred %dx%d region at (%d,%d)%s", r.W, r.H, r.X, r.Y, rangeDesc(start, end)),
		Output:  out,
	}
}

func runPixelateRegion(c *Ctx, args Args) Result {
	in, info, r, res := regionSetup(c, args)
	if res != nil {
		return *res
	}
	factor := args.Int("factor", 12)
	if factor < 2 {
		factor = 2
	}
	start, end := args.Float("start", 0), args.Float("end", -1)
	if end > info.Duration {
		end = info.Duration
	}
	fc := pixelateRegionGraph(r, factor, start, end)
	out := c.Project.NextOutput("pixelate", ".mp4")
	if err := media.Run(c.Context, "-i", in, "-filter_complex", fc, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "copy", out); err != nil {
		return Result{Err: err}
	}
	return Result{
		Summary: fmt.Sprintf("pixelated %dx%d region at (%d,%d)%s", r.W, r.H, r.X, r.Y, rangeDesc(start, end)),
		Output:  out,
	}
}

func runZoomRegion(c *Ctx, args Args) Result {
	in, info, r, res := regionSetup(c, args)
	if res != nil {
		return *res
	}
	r = fitRectAspect(r, info.Width, info.Height)
	outW, outH := media.EvenDims(info.Width, info.Height)
	vf := fmt.Sprintf("crop=%d:%d:%d:%d,scale=%d:%d", r.W, r.H, r.X, r.Y, outW, outH)
	out := c.Project.NextOutput("zoom", ".mp4")
	if err := media.Run(c.Context, "-i", in, "-vf", vf, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "copy", out); err != nil {
		return Result{Err: err}
	}
	return Result{
		Summary: fmt.Sprintf("zoomed into %dx%d @ (%d,%d) → %dx%d", r.W, r.H, r.X, r.Y, outW, outH),
		Output:  out,
	}
}

// regionSetup resolves the input, probes it, and validates/clamps the
// requested rect — the shared preamble of every region tool. A non-nil Result
// is an early error return.
func regionSetup(c *Ctx, args Args) (string, media.Info, rect, *Result) {
	in, err := resolveInput(c, args)
	if err != nil {
		return "", media.Info{}, rect{}, &Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return "", media.Info{}, rect{}, &Result{Err: err}
	}
	r, err := clampRect(rect{args.Int("x", 0), args.Int("y", 0), args.Int("w", 0), args.Int("h", 0)}, info.Width, info.Height)
	if err != nil {
		return "", media.Info{}, rect{}, &Result{Err: err}
	}
	return in, info, r, nil
}

// --- pure helpers (unit-tested without ffmpeg) -----------------------------

type rect struct{ X, Y, W, H int }

// clampRect validates a requested region against the source frame: it errors
// if the rect is degenerate or lies fully outside, clips it when partially
// outside, and rounds width/height down to even values for yuv420p.
func clampRect(r rect, srcW, srcH int) (rect, error) {
	if r.W <= 0 || r.H <= 0 {
		return rect{}, fmt.Errorf("region needs positive `w` and `h`, got %dx%d", r.W, r.H)
	}
	if r.X >= srcW || r.Y >= srcH || r.X+r.W <= 0 || r.Y+r.H <= 0 {
		return rect{}, fmt.Errorf("region %dx%d at (%d,%d) is entirely outside the %dx%d frame", r.W, r.H, r.X, r.Y, srcW, srcH)
	}
	x1, y1 := max(r.X, 0), max(r.Y, 0)
	x2, y2 := min(r.X+r.W, srcW), min(r.Y+r.H, srcH)
	w, h := x2-x1, y2-y1
	w, h = w-w%2, h-h%2
	if w < 2 || h < 2 {
		return rect{}, fmt.Errorf("region %dx%d at (%d,%d) is too small after clamping to the %dx%d frame", r.W, r.H, r.X, r.Y, srcW, srcH)
	}
	return rect{x1, y1, w, h}, nil
}

// fitRectAspect minimally grows one side of the rect so it matches the source
// aspect ratio, re-centers it, and keeps it inside the frame (even dims).
func fitRectAspect(r rect, srcW, srcH int) rect {
	srcA := float64(srcW) / float64(srcH)
	nw, nh := r.W, r.H
	if a := float64(r.W) / float64(r.H); a > srcA {
		nh = int(float64(r.W)/srcA + 0.5)
	} else if a < srcA {
		nw = int(float64(r.H)*srcA + 0.5)
	}
	nw, nh = min(nw, srcW), min(nh, srcH)
	nx := min(max(r.X+(r.W-nw)/2, 0), srcW-nw)
	ny := min(max(r.Y+(r.H-nh)/2, 0), srcH-nh)
	return rect{nx, ny, nw - nw%2, nh - nh%2}
}

// regionEnable renders the enable= clause for a time-ranged region edit, or
// "" when the edit covers the whole video.
func regionEnable(start, end float64) string {
	if end > start {
		return fmt.Sprintf(":enable='between(t\\,%.3f\\,%.3f)'", start, end)
	}
	if start > 0 {
		return fmt.Sprintf(":enable='gte(t\\,%.3f)'", start)
	}
	return ""
}

// blurRegionGraph builds the filter_complex that crops the region, blurs it,
// and overlays it back at the same position.
func blurRegionGraph(r rect, strength int, start, end float64) string {
	return fmt.Sprintf("[0:v]split[base][roi];[roi]crop=%d:%d:%d:%d,boxblur=%d:2[fx];[base][fx]overlay=%d:%d%s",
		r.W, r.H, r.X, r.Y, strength, r.X, r.Y, regionEnable(start, end))
}

// pixelateRegionGraph crops the region, scales it down by factor, scales it
// back up with nearest-neighbor (hard mosaic blocks), and overlays it back.
func pixelateRegionGraph(r rect, factor int, start, end float64) string {
	dw, dh := max(r.W/factor, 2), max(r.H/factor, 2)
	return fmt.Sprintf("[0:v]split[base][roi];[roi]crop=%d:%d:%d:%d,scale=%d:%d,scale=%d:%d:flags=neighbor[fx];[base][fx]overlay=%d:%d%s",
		r.W, r.H, r.X, r.Y, dw, dh, r.W, r.H, r.X, r.Y, regionEnable(start, end))
}

// rangeDesc renders the applied time range for a summary line.
func rangeDesc(start, end float64) string {
	if end > start {
		return fmt.Sprintf(" for %.1fs–%.1fs", start, end)
	}
	if start > 0 {
		return fmt.Sprintf(" from %.1fs", start)
	}
	return ""
}
