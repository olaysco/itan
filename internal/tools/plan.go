package tools

import (
	"fmt"
	"strings"

	"github.com/olaysco/itan/internal/media"
)

// The storyboard is the scene pipeline's backbone: declare the plan before
// composing, mark scenes rendered as they land, and the ledger carries the
// live plan through every turn (and through compaction). The critic pass is
// view_strip over the assembly.

func planTools() []Tool {
	return []Tool{
		{
			Name: "storyboard",
			Description: "Declare or update the scene plan for a multi-scene piece BEFORE composing. Each scene: " +
				"n, intent (what it must communicate), duration. The plan lives in <project-state> so every turn " +
				"sees which scenes are still unrendered. Attach output once a scene is composed (mark_rendered). " +
				"Workflow: storyboard → per scene [compose → view_frames → revise] → concat → view_strip to judge " +
				"the assembly → export.",
			Schema: schema(nil, map[string]map[string]any{
				"scenes": {"type": "array", "description": "Full plan (replaces existing): [{n, intent, duration}].",
					"items": map[string]any{"type": "object", "properties": map[string]any{
						"n":        map[string]any{"type": "integer"},
						"intent":   map[string]any{"type": "string"},
						"duration": map[string]any{"type": "number"},
					}}},
				"mark_rendered": {"type": "object", "description": "Attach a render to a scene: {n, output}.",
					"properties": map[string]any{
						"n":      map[string]any{"type": "integer"},
						"output": map[string]any{"type": "string"},
					}},
			}),
			Run:             runStoryboard,
			ConcurrencySafe: true,
		},
	}
}

func runStoryboard(c *Ctx, args Args) Result {
	if raw, ok := args["scenes"].([]any); ok && len(raw) > 0 {
		var scenes []media.Scene
		for i, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			s := media.Scene{N: i + 1}
			if n, ok := m["n"].(float64); ok {
				s.N = int(n)
			}
			s.Intent, _ = m["intent"].(string)
			if d, ok := m["duration"].(float64); ok {
				s.Duration = d
			}
			scenes = append(scenes, s)
		}
		if len(scenes) == 0 {
			return fail("storyboard: scenes empty or malformed")
		}
		c.Project.Scenes = scenes
		if err := c.Project.Save(); err != nil {
			return Result{Err: err}
		}
		var total float64
		for _, s := range scenes {
			total += s.Duration
		}
		return Result{Summary: fmt.Sprintf("storyboard set: %d scenes, %.1fs planned", len(scenes), total)}
	}

	if m, ok := args["mark_rendered"].(map[string]any); ok {
		n, _ := m["n"].(float64)
		output, _ := m["output"].(string)
		if n <= 0 || output == "" {
			return fail("mark_rendered needs {n, output}")
		}
		// Accept asset ids and ledger filenames the same way inputs do.
		path, err := resolveInput(c, Args{"input": output})
		if err != nil {
			return Result{Err: err}
		}
		for i := range c.Project.Scenes {
			if c.Project.Scenes[i].N == int(n) {
				c.Project.Scenes[i].Output = path
				if err := c.Project.Save(); err != nil {
					return Result{Err: err}
				}
				rendered := 0
				for _, s := range c.Project.Scenes {
					if s.Output != "" {
						rendered++
					}
				}
				return Result{Summary: fmt.Sprintf("scene %d rendered (%d/%d done)", int(n), rendered, len(c.Project.Scenes))}
			}
		}
		return fail("no scene %d in the storyboard", int(n))
	}

	// No args: report the plan.
	if len(c.Project.Scenes) == 0 {
		return Result{Summary: "no storyboard set"}
	}
	var lines []string
	for _, s := range c.Project.Scenes {
		st := "planned"
		if s.Output != "" {
			st = "rendered"
		}
		lines = append(lines, fmt.Sprintf("%d(%s)", s.N, st))
	}
	return Result{Summary: "storyboard: " + strings.Join(lines, " ")}
}
