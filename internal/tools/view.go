package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/olaysco/itan/internal/canvas"
	"github.com/olaysco/itan/internal/media"
)

// view_frames closes the see→judge→revise loop: every other tool reports
// numbers, this one shows the model the actual pixels so visual edits can be
// checked (and re-done) instead of assumed correct.

const maxViewFrames = 4

func viewTools() []Tool {
	return []Tool{
		{
			Name: "view_strip",
			Description: "LOOK at the whole video as one storyboard image: a tiled contact sheet with one " +
				"labeled cell per detected scene (or evenly sampled). Use this FIRST to judge structure, " +
				"pacing, and flow — then view_frames to inspect any single moment in full resolution.",
			Schema: schema(nil, map[string]map[string]any{
				"input":     prop("string", "Asset id or path; omit for the current working video."),
				"max_cells": prop("integer", "Maximum cells in the sheet (default 12, max 16)."),
			}),
			Run:             runViewStrip,
			ConcurrencySafe: true,
		},
		{
			Name: "view_frames",
			Description: "LOOK at the video: extract up to 4 frames as images you can actually see. Use after any " +
				"edit that changes the picture (compose, change_background, region tools, overlays) to verify the " +
				"result before telling the user it's done — and to diagnose when something looks wrong. Defaults " +
				"to frames at 10%/50%/90% of the duration.",
			Schema: schema(nil, map[string]map[string]any{
				"times": {"type": "array", "items": map[string]any{"type": "number"}, "description": "Timestamps in seconds (max 4). Default: 10%, 50%, 90% of duration."},
				"input": prop("string", "Asset id or path; defaults to CURRENT."),
			}),
			Run:             runViewFrames,
			ConcurrencySafe: true,
		},
	}
}

// runViewStrip builds the storyboard sheet: pick times (scene cuts when
// detectable, else even sampling), extract thumbs, tile them into ONE image
// with timestamp labels so temporal order is spatial and reference is
// unambiguous. Full-res detail stays view_frames' job.
func runViewStrip(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}
	maxCells := args.Int("max_cells", 12)
	if maxCells < 4 {
		maxCells = 4
	}
	if maxCells > 16 {
		maxCells = 16
	}

	times := []float64{0.05}
	scenes := true
	for _, t := range media.SceneCuts(c.Context, in, 0.30) {
		if len(times) >= maxCells {
			break
		}
		times = append(times, t)
	}
	if len(times) < 3 { // one flat scene: sample evenly instead
		scenes = false
		times = times[:1]
		n := maxCells
		if info.Duration < float64(n) {
			n = int(info.Duration) + 2
		}
		for i := 1; i < n; i++ {
			times = append(times, info.Duration*float64(i)/float64(n))
		}
	}

	var thumbs []string
	var labels []string
	for i, t := range times {
		if info.Duration > 0 && t >= info.Duration {
			t = info.Duration - 0.05
		}
		out := c.Project.NextOutput("cell", ".jpg")
		if err := media.Run(c.Context,
			"-ss", fmt.Sprintf("%.3f", t), "-i", in,
			"-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "6", out); err != nil {
			return Result{Err: fmt.Errorf("cell at %.1fs: %w", t, err)}
		}
		thumbs = append(thumbs, out)
		end := info.Duration
		if i+1 < len(times) {
			end = times[i+1]
		}
		if scenes {
			labels = append(labels, fmt.Sprintf("scene %d · %.1f–%.1fs", i+1, t, end))
		} else {
			labels = append(labels, fmt.Sprintf("t=%.1fs", t))
		}
	}

	mode := "evenly sampled"
	if scenes {
		mode = "scene-detected"
	}
	sheet := c.Project.NextOutput("strip", ".png")
	if err := renderStripSheet(c, thumbs, labels, sheet); err != nil {
		// No browser for the labeled grid: degrade to loose frames, order
		// and labels carried by the summary.
		var frames []FrameRef
		for _, p := range thumbs {
			frames = append(frames, FrameRef{Path: p, MediaType: "image/jpeg"})
		}
		return Result{
			Summary: fmt.Sprintf("storyboard (%s, %d frames in order — no browser for the tiled sheet): %s",
				mode, len(thumbs), strings.Join(labels, " | ")),
			Frames: frames,
		}
	}
	return Result{
		Summary: fmt.Sprintf("storyboard (%s, %d cells, read left→right top→bottom): %s",
			mode, len(thumbs), strings.Join(labels, " | ")),
		Frames: []FrameRef{{Path: sheet, MediaType: "image/png"}},
	}
}

// renderStripSheet tiles labeled thumbnails via the browser engine (embedded
// fonts, crisp labels) into a single PNG.
func renderStripSheet(c *Ctx, thumbs, labels []string, outPath string) error {
	cols := 4
	if len(thumbs) < 4 {
		cols = len(thumbs)
	}
	rows := (len(thumbs) + cols - 1) / cols
	var cells strings.Builder
	for i, p := range thumbs {
		fmt.Fprintf(&cells,
			`<div class="cell"><img src="file://%s"><span>%s</span></div>`, p, labels[i])
	}
	w := cols*336 + 32
	h := rows*242 + 32
	page := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><style>
html,body{margin:0;background:#101014}
.grid{display:grid;grid-template-columns:repeat(%d,1fr);gap:12px;padding:16px}
.cell{border:2px solid #34343e;border-radius:6px;overflow:hidden;background:#000}
.cell img{width:100%%;display:block}
.cell span{display:block;font:600 13px 'IBM Plex Mono',monospace;color:#fff;background:#1a1a22;padding:4px 8px;border-top:1px solid #34343e}
</style></head><body><div class="grid">%s</div></body></html>`, cols, cells.String())
	png, err := canvas.Snapshot(c.Context, page, w, h)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, png, 0o644)
}

func runViewFrames(c *Ctx, args Args) Result {
	in, err := resolveInput(c, args)
	if err != nil {
		return Result{Err: err}
	}
	info, err := media.Probe(c.Context, in)
	if err != nil {
		return Result{Err: err}
	}

	var times []float64
	if raw, ok := args["times"].([]any); ok {
		for _, t := range raw {
			if f, ok := t.(float64); ok {
				times = append(times, f)
			}
		}
	}
	if len(times) == 0 {
		d := info.Duration
		times = []float64{d * 0.1, d * 0.5, d * 0.9}
	}
	if len(times) > maxViewFrames {
		times = times[:maxViewFrames]
	}

	var frames []FrameRef
	var stamps []string
	for _, t := range times {
		if t < 0 {
			t = 0
		}
		if info.Duration > 0 && t > info.Duration {
			t = info.Duration - 0.05
		}
		out := c.Project.NextOutput("frame", ".jpg")
		// Downscale for context economy: 640px wide is plenty to judge
		// composition, text legibility, and color.
		err := media.Run(c.Context,
			"-ss", fmt.Sprintf("%.3f", t), "-i", in,
			"-frames:v", "1", "-vf", "scale='min(640,iw)':-2", "-q:v", "5", out)
		if err != nil {
			return Result{Err: fmt.Errorf("frame at %.1fs: %w", t, err)}
		}
		frames = append(frames, FrameRef{Path: out, MediaType: "image/jpeg"})
		stamps = append(stamps, fmt.Sprintf("%.1fs", t))
	}
	return Result{
		Summary: fmt.Sprintf("showing %d frames at %s — judge them before declaring the edit done", len(frames), strings.Join(stamps, ", ")),
		Frames:  frames,
	}
}
