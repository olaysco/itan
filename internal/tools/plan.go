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
			Description: "Declare or update the script for a multi-scene piece BEFORE composing. This IS the " +
				"script: each scene carries n, intent (why it exists), say (the narration line, verbatim — leave " +
				"empty for a silent scene), visual (what is on screen), and duration. Write `say` as speech, not " +
				"prose. The plan lives in <project-state> so every turn sees what is still unrendered, and any " +
				"tool can address a finished scene as `scene 3`. Attach a render with mark_rendered. " +
				"Workflow: storyboard → voice_scenes (retimes to the real narration) → per scene " +
				"[find_media / compose → view_frames → revise] → assemble → view_strip to judge it → export.",
			Schema: schema(nil, map[string]map[string]any{
				"scenes": {"type": "array", "description": "Full script (replaces existing): [{n, intent, say, visual, duration}].",
					"items": map[string]any{"type": "object", "properties": map[string]any{
						"n":        map[string]any{"type": "integer"},
						"intent":   map[string]any{"type": "string"},
						"say":      map[string]any{"type": "string"},
						"visual":   map[string]any{"type": "string"},
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
			s.Say, _ = m["say"].(string)
			s.Visual, _ = m["visual"].(string)
			if d, ok := m["duration"].(float64); ok {
				s.Duration = d
			}
			// Rewriting the script drops stale renders and voice tracks for
			// scenes whose words changed: keeping them would silently pair
			// new narration with old pictures.
			for _, prev := range c.Project.Scenes {
				if prev.N == s.N && prev.Say == s.Say && prev.Visual == s.Visual {
					s.Output, s.Voice = prev.Output, prev.Voice
				}
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
		spoken := 0
		for _, s := range scenes {
			total += s.Duration
			if strings.TrimSpace(s.Say) != "" {
				spoken++
			}
		}
		msg := fmt.Sprintf("script set: %d scenes, %.1fs planned", len(scenes), total)
		if spoken > 0 {
			msg += fmt.Sprintf(", %d narrated — run voice_scenes to retime to the real read", spoken)
		}
		return Result{Summary: msg}
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
