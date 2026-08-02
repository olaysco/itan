package tools

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/olaysco/itan/internal/canvas"
)

// The web tools give the agent eyes on the outside world for one purpose:
// turning a real product page into video material. fetch_page reads the copy
// (so scenes quote the actual product, not hallucinated claims) and
// capture_page photographs the UI (so compose can show the real thing).
// Both are read-only; nothing on the page is executed against the project.

func webTools() []Tool {
	return []Tool{
		{
			Name: "fetch_page",
			Description: "Fetch a web page and return its title, description, and readable text — use before " +
				"making a product/launch/explainer video about a site so every claim in the scenes comes from " +
				"the page itself, never invented.",
			Schema: schema([]string{"url"}, map[string]map[string]any{
				"url": prop("string", "Absolute http(s) URL."),
			}),
			Run:             runFetchPage,
			ConcurrencySafe: true,
		},
		{
			Name: "capture_page",
			Description: "Screenshot a live URL at 2x resolution and save it as a project image. Embed the result " +
				"in compose scenes via <img src=\"file://PATH\"> (animate it with CSS transforms for a Ken Burns " +
				"pan). Use full_page for the whole scroll, otherwise the hero viewport.",
			Schema: schema([]string{"url"}, map[string]map[string]any{
				"url":       prop("string", "Absolute http(s) URL."),
				"width":     prop("integer", "Viewport width in px (default 1440)."),
				"full_page": prop("boolean", "Capture the full scroll height (default false: hero viewport)."),
			}),
			Run: runCapturePage,
		},
	}
}

var fetchClient = &http.Client{Timeout: 30 * time.Second}

func runFetchPage(c *Ctx, args Args) Result {
	url := args.Str("url")
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fail("fetch_page needs an absolute http(s) url")
	}
	req, err := http.NewRequestWithContext(c.Context, "GET", url, nil)
	if err != nil {
		return Result{Err: err}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; itan/1.0)")
	resp, err := fetchClient.Do(req)
	if err != nil {
		return Result{Err: fmt.Errorf("fetch %s: %w", url, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fail("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Result{Err: err}
	}
	page := string(raw)

	title := firstMatch(page, `(?is)<title[^>]*>(.*?)</title>`)
	desc := firstMatch(page, `(?is)<meta[^>]+name=["']description["'][^>]*content=["']([^"']*)`)
	if desc == "" {
		desc = firstMatch(page, `(?is)<meta[^>]+property=["']og:description["'][^>]*content=["']([^"']*)`)
	}
	theme := firstMatch(page, `(?is)<meta[^>]+name=["']theme-color["'][^>]*content=["']([^"']*)`)

	return Result{
		Summary: "fetched " + url,
		Data: map[string]any{
			"title":       clean(title),
			"description": clean(desc),
			"theme_color": theme,
			"text":        readableText(page, 6000),
		},
	}
}

func runCapturePage(c *Ctx, args Args) Result {
	url := args.Str("url")
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fail("capture_page needs an absolute http(s) url")
	}
	out := c.Project.NextOutput("capture", ".png")
	full := false
	if v, ok := args["full_page"].(bool); ok {
		full = v
	}
	w, h, err := canvas.CapturePage(c.Context, url, args.Int("width", 1440), full, out)
	if err != nil {
		return Result{Err: err}
	}
	return Result{
		Summary: fmt.Sprintf("captured %s at %dx%d", url, w, h),
		Data:    map[string]any{"image": out, "embed_as": `<img src="file://` + out + `">`},
	}
}

func firstMatch(s, pattern string) string {
	if m := regexp.MustCompile(pattern).FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

var (
	dropBlocksRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head)[^>]*>.*?</\s*(script|style|noscript|svg|head)\s*>`)
	tagRe        = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe      = regexp.MustCompile(`[ \t\r\f]+`)
	blankRe      = regexp.MustCompile(`\n{3,}`)
)

// readableText is a deliberately crude reader: strip non-content blocks and
// tags, unescape entities, collapse whitespace, cap the size. Enough for a
// model to quote the page accurately.
func readableText(page string, maxChars int) string {
	t := dropBlocksRe.ReplaceAllString(page, " ")
	t = tagRe.ReplaceAllString(t, "\n")
	t = html.UnescapeString(t)
	t = spaceRe.ReplaceAllString(t, " ")
	lines := strings.Split(t, "\n")
	keep := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			keep = append(keep, l)
		}
	}
	t = blankRe.ReplaceAllString(strings.Join(keep, "\n"), "\n\n")
	if len(t) > maxChars {
		t = t[:maxChars] + "…"
	}
	return t
}

func clean(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(s), " "))
}
