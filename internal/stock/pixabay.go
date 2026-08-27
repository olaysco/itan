// Package stock fetches licensed footage and stills so a project is not
// limited to material the user already owns. Without it the only thing an
// empty project can put on screen is text on a background, which is why
// generated videos looked like slide decks no matter how good the motion was.
//
// Pixabay is the source: its content license permits commercial use without
// attribution, and the API is free with a key. Search is keyword-based, not
// semantic, so the tool layer steers the model to write a visual query
// (subject, setting, treatment) rather than pasting a line of narration.
package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Endpoints are variables so tests can point the client at a local server
// instead of hitting the real API for every run.
var (
	photoEndpoint = "https://pixabay.com/api/"
	videoEndpoint = "https://pixabay.com/api/videos/"
)

// Hit is one search result, normalized across the photo and video endpoints.
type Hit struct {
	ID       int
	Tags     string
	Width    int
	Height   int
	Duration float64 // videos only
	Bytes    int64   // declared size of the rendition we would download
	Download string  // direct media URL
	Page     string  // human-viewable page, for credit or verification
	Kind     string  // "video" or "photo"
}

// Query describes a search. Kind is "video" or "photo".
type Query struct {
	Text        string
	Kind        string
	Count       int
	Orientation string // horizontal, vertical, or empty for any
	MinWidth    int
}

type Client struct {
	Key  string
	HTTP *http.Client
}

func New(key string) *Client {
	return &Client{Key: key, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

type photoResp struct {
	TotalHits int `json:"totalHits"`
	Hits      []struct {
		ID            int    `json:"id"`
		PageURL       string `json:"pageURL"`
		Tags          string `json:"tags"`
		LargeImageURL string `json:"largeImageURL"`
		ImageWidth    int    `json:"imageWidth"`
		ImageHeight   int    `json:"imageHeight"`
	} `json:"hits"`
}

type videoResp struct {
	TotalHits int `json:"totalHits"`
	Hits      []struct {
		ID       int     `json:"id"`
		PageURL  string  `json:"pageURL"`
		Tags     string  `json:"tags"`
		Duration float64 `json:"duration"`
		Videos   map[string]struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Size   int64  `json:"size"`
		} `json:"videos"`
	} `json:"hits"`
}

// Search returns the top matches. Errors carry the reason a caller can act
// on — a missing key and a rate limit need different responses.
func (c *Client) Search(ctx context.Context, q Query) ([]Hit, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("no Pixabay key: get a free one at https://pixabay.com/api/docs and run `itan config set media.pixabay_key <key>` (or set PIXABAY_API_KEY)")
	}
	if strings.TrimSpace(q.Text) == "" {
		return nil, fmt.Errorf("find_media needs a query")
	}
	if q.Count <= 0 {
		q.Count = 3
	}

	v := url.Values{}
	v.Set("key", c.Key)
	v.Set("q", q.Text)
	v.Set("safesearch", "true")
	// Pixabay rejects per_page below 3; ask for a few extra and trim, so a
	// count of 1 does not error.
	perPage := q.Count
	if perPage < 3 {
		perPage = 3
	}
	v.Set("per_page", fmt.Sprintf("%d", perPage))
	if q.Orientation != "" && q.Orientation != "all" {
		v.Set("orientation", q.Orientation)
	}
	if q.MinWidth > 0 {
		v.Set("min_width", fmt.Sprintf("%d", q.MinWidth))
	}

	endpoint := videoEndpoint
	if q.Kind == "photo" {
		endpoint = photoEndpoint
		v.Set("image_type", "photo")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pixabay: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("pixabay rejected the key (HTTP %d) — check media.pixabay_key", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("pixabay rate limit reached (100 requests/minute) — wait a moment and retry")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("pixabay: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var hits []Hit
	if q.Kind == "photo" {
		var pr photoResp
		if err := json.Unmarshal(body, &pr); err != nil {
			return nil, fmt.Errorf("pixabay: unreadable response: %w", err)
		}
		for _, h := range pr.Hits {
			hits = append(hits, Hit{
				ID: h.ID, Tags: h.Tags, Width: h.ImageWidth, Height: h.ImageHeight,
				Download: h.LargeImageURL, Page: h.PageURL, Kind: "photo",
			})
		}
	} else {
		var vr videoResp
		if err := json.Unmarshal(body, &vr); err != nil {
			return nil, fmt.Errorf("pixabay: unreadable response: %w", err)
		}
		for _, h := range vr.Hits {
			r, ok := bestRendition(h.Videos, q.MinWidth)
			if !ok {
				continue
			}
			hits = append(hits, Hit{
				ID: h.ID, Tags: h.Tags, Width: r.Width, Height: r.Height,
				Duration: h.Duration, Bytes: r.Size, Download: r.URL,
				Page: h.PageURL, Kind: "video",
			})
		}
	}
	if len(hits) > q.Count {
		hits = hits[:q.Count]
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("no Pixabay results for %q — try a plainer visual query (subject + setting), or kind=photo", q.Text)
	}
	return hits, nil
}

type rendition struct {
	URL    string
	Width  int
	Height int
	Size   int64
}

// bestRendition picks the largest rendition that is still sane to download:
// deliverables are 1080p, so anything wider is bytes spent on detail the
// encode throws away.
func bestRendition(vs map[string]struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}, minWidth int) (rendition, bool) {
	const ceiling = 1920
	var best rendition
	for _, v := range vs {
		if v.URL == "" || v.Width < minWidth {
			continue
		}
		better := v.Width > best.Width
		// Prefer the widest at or under 1080p; only exceed it if nothing fits.
		if best.Width > ceiling && v.Width <= ceiling {
			better = true
		} else if best.Width <= ceiling && v.Width > ceiling {
			better = false
		}
		if better {
			best = rendition{URL: v.URL, Width: v.Width, Height: v.Height, Size: v.Size}
		}
	}
	return best, best.URL != ""
}

// Download writes a hit into dir and returns the file path. The name carries
// the Pixabay id so the same clip is recognizable across projects and a
// second fetch of it is obviously a duplicate.
func (c *Client) Download(ctx context.Context, h Hit, dir string) (string, error) {
	ext := filepath.Ext(strings.SplitN(h.Download, "?", 2)[0])
	if ext == "" {
		if h.Kind == "video" {
			ext = ".mp4"
		} else {
			ext = ".jpg"
		}
	}
	out := filepath.Join(dir, fmt.Sprintf("pixabay-%d%s", h.ID, ext))
	if _, err := os.Stat(out); err == nil {
		return out, nil // already fetched; re-downloading would just burn bandwidth
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Download, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %d: %w", h.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %d: HTTP %d", h.ID, resp.StatusCode)
	}

	// Write to a temp name first so an interrupted fetch cannot leave a
	// truncated file that later looks cached.
	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("download %d: %w", h.ID, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

// Describe is the one-line summary the model sees for a candidate.
func (h Hit) Describe() string {
	if h.Kind == "video" {
		return fmt.Sprintf("%dx%d %.0fs — %s", h.Width, h.Height, h.Duration, h.Tags)
	}
	return fmt.Sprintf("%dx%d still — %s", h.Width, h.Height, h.Tags)
}
