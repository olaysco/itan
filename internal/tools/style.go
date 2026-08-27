package tools

import (
	"fmt"
	"strings"
)

// The skill has always asked for a style brief held across every scene. It
// had no way to be held: each compose is an independent document, so the
// brief lived in the model's context and evaporated with it. A project that
// owns its style applies it to every scene without anyone having to remember.

func styleTools() []Tool {
	return []Tool{
		{
			Name: "style_kit",
			Description: "Set the project's shared visual identity ONCE, before composing, then write scenes " +
				"that use it. `brief` is the decision in a sentence or two — palette, type pairing, easing " +
				"personality, layout anchor, light direction. `css` is that decision made executable: classes for " +
				"the ground, panels, captions, type scale. The CSS is injected into every compose automatically, " +
				"after the fonts and before the scene's own styles, so scenes inherit it and can still override " +
				"any rule. This is what makes four scenes look like one video instead of four. Call with no " +
				"arguments to read the current kit back; pass clear:true to drop it.",
			Schema: schema(nil, map[string]map[string]any{
				"brief": prop("string", "The style decision in prose: palette, type, easing, layout, light."),
				"css":   prop("string", "Shared CSS — ground, panels, captions, type scale. No @import or network URLs."),
				"clear": prop("boolean", "Remove the kit."),
			}),
			Run:             runStyleKit,
			ConcurrencySafe: true,
		},
	}
}

func runStyleKit(c *Ctx, args Args) Result {
	if clear, _ := args["clear"].(bool); clear {
		c.Project.Style.Brief, c.Project.Style.CSS = "", ""
		if err := c.Project.Save(); err != nil {
			return Result{Err: err}
		}
		return Result{Summary: "style kit cleared — scenes now carry only their own styles"}
	}

	brief, hasBrief := args["brief"].(string)
	css, hasCSS := args["css"].(string)
	if !hasBrief && !hasCSS {
		// No arguments: report what the project is currently holding.
		st := c.Project.Style
		if st.Brief == "" && st.CSS == "" {
			return Result{Summary: "no style kit set — every scene is designing from scratch"}
		}
		return Result{
			Summary: fmt.Sprintf("style kit: %s (%d lines of CSS, injected into every compose)",
				firstLine(st.Brief), lineCount(st.CSS)),
			Data: map[string]any{"brief": st.Brief, "css": st.CSS},
		}
	}

	if hasCSS {
		// A kit that reaches for the network fails silently at render time,
		// and it would fail in every scene rather than just one.
		if strings.Contains(css, "@import") || strings.Contains(strings.ToLower(css), "url(http") {
			return fail("the style kit cannot use @import or network URLs — renders are offline. " +
				"Inline the rules, or reference a local file with url(file:///absolute/path)")
		}
		c.Project.Style.CSS = css
	}
	if hasBrief {
		c.Project.Style.Brief = brief
	}
	if err := c.Project.Save(); err != nil {
		return Result{Err: err}
	}

	n := lineCount(c.Project.Style.CSS)
	summary := fmt.Sprintf("style kit set: %d lines of CSS, now injected into every compose", n)
	if n == 0 {
		summary = "style brief recorded (no CSS yet — add `css` so scenes actually inherit it)"
	}
	return Result{Summary: summary}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:87] + "…"
	}
	if s == "" {
		return "(no brief)"
	}
	return s
}

func lineCount(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return strings.Count(strings.TrimSpace(s), "\n") + 1
}
