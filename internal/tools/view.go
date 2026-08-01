package tools

import (
	"fmt"
	"strings"

	"github.com/olaysco/itan/internal/media"
)

// view_frames closes the see→judge→revise loop: every other tool reports
// numbers, this one shows the model the actual pixels so visual edits can be
// checked (and re-done) instead of assumed correct.

const maxViewFrames = 4

func viewTools() []Tool {
	return []Tool{
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
