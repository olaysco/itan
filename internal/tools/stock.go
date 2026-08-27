package tools

import (
	"fmt"
	"strings"

	"github.com/olaysco/itan/internal/stock"
)

func stockTools() []Tool {
	return []Tool{
		{
			Name: "find_media",
			Description: "Search Pixabay for footage or stills and add the matches to the project as assets. " +
				"This is how a project gets pictures it does not already own — without it the only thing on " +
				"screen is text on a background. Write a VISUAL query (subject + setting + treatment, e.g. " +
				"'aerial coastline sunrise drone'), never a line of narration: the search is keyword-based. " +
				"Then LOOK at what came back with view_frames before building on it — tags lie, and a wrong " +
				"clip is worse than none. Results are Pixabay-licensed: commercial use, no attribution needed. " +
				"They arrive as assets (CURRENT is untouched); use them with concat, overlay_video, or embed a " +
				"still in a compose via <img src=\"file://…\">.",
			Schema: schema([]string{"query"}, map[string]map[string]any{
				"query":       prop("string", "Visual search terms — subject, setting, treatment."),
				"kind":        prop("string", "'video' (default) or 'photo'."),
				"count":       prop("integer", "How many to fetch (default 3, max 8)."),
				"orientation": prop("string", "'horizontal', 'vertical', or omit for any."),
				"min_width":   prop("integer", "Reject results narrower than this (e.g. 1920)."),
			}),
			Run: runFindMedia,
		},
	}
}

func runFindMedia(c *Ctx, args Args) Result {
	query := strings.TrimSpace(args.Str("query"))
	if query == "" {
		return fail("find_media needs a `query` — describe what should be on screen")
	}
	kind := strings.ToLower(args.Str("kind"))
	if kind == "" {
		kind = "video"
	}
	if kind != "video" && kind != "photo" {
		return fail("kind must be 'video' or 'photo', got %q", kind)
	}
	count := args.Int("count", 3)
	if count < 1 {
		count = 1
	}
	if count > 8 {
		count = 8
	}

	client := stock.New(c.Config.PixabayKey())
	hits, err := client.Search(c.Context, stock.Query{
		Text:        query,
		Kind:        kind,
		Count:       count,
		Orientation: args.Str("orientation"),
		MinWidth:    args.Int("min_width", 0),
	})
	if err != nil {
		return Result{Err: err}
	}

	var added []string
	var lines []string
	var failures []string
	for _, h := range hits {
		path, err := client.Download(c.Context, h, c.Project.Dir)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%d: %v", h.ID, err))
			continue
		}
		asset, err := c.Project.AddAsset(c.Context, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%d: %v", h.ID, err))
			continue
		}
		added = append(added, asset.ID)
		lines = append(lines, fmt.Sprintf("%s %s", asset.ID, h.Describe()))
	}
	if len(added) == 0 {
		return fail("found %d results but none could be added: %s", len(hits), strings.Join(failures, "; "))
	}

	summary := fmt.Sprintf("added %d %s from Pixabay for %q: %s",
		len(added), kind, query, strings.Join(added, ", "))
	if len(failures) > 0 {
		// Never let a partial fetch read as a clean one.
		summary += fmt.Sprintf(" (%d failed: %s)", len(failures), strings.Join(failures, "; "))
	}
	return Result{
		Summary: summary,
		Data: map[string]any{
			"assets":  added,
			"results": lines,
			"next":    "view_frames on these before building on them — tags are not a preview",
		},
	}
}
