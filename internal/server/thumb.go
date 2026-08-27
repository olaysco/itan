package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/olaysco/itan/internal/media"
)

// A wall of ▶ glyphs is not a library — you cannot pick a clip you cannot
// see. Thumbnails are extracted on demand and cached under the project's
// state dir, keyed by path and modification time so a re-render invalidates
// its own poster without anyone having to remember to.

const thumbWidth = 320

func (s *Server) thumbDir() string {
	return filepath.Join(s.Session.Project.Dir, ".itan", "thumbs")
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !s.allowed(path) {
		httpErr(w, 403, "not a project file")
		return
	}
	at, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	out, err := s.thumbFor(r.Context(), path, at)
	if err != nil {
		// A poster is a nicety; failing it must not look like a broken file.
		httpErr(w, 404, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, out)
}

func (s *Server) thumbFor(ctx context.Context, path string, at float64) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("no such file")
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%.2f", path, st.ModTime().UnixNano(), st.Size(), at)))
	dir := s.thumbDir()
	out := filepath.Join(dir, hex.EncodeToString(sum[:])+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	kind := assetKind(path, "video")
	switch kind {
	case "audio":
		return "", fmt.Errorf("audio has no poster")
	case "image":
		// Stills are their own poster; just resize so the drawer is not
		// loading full-resolution art into 150px tiles.
		if err := media.Run(ctx, "-i", path,
			"-vf", fmt.Sprintf("scale=%d:-2:flags=lanczos", thumbWidth),
			"-frames:v", "1", out); err != nil {
			return "", fmt.Errorf("could not read the image")
		}
		return out, nil
	}

	// Video: a frame from a little way in, because frame zero is often black
	// or a fade-up and tells you nothing about the clip.
	seek := at
	if seek <= 0 {
		seek = 0.5
		if info, err := media.Probe(ctx, path); err == nil && info.Duration > 0 {
			if q := info.Duration * 0.25; q > seek {
				seek = q
			}
		}
	}
	args := []string{"-ss", fmt.Sprintf("%.3f", seek), "-i", path,
		"-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2:flags=lanczos", thumbWidth), out}
	if err := media.Run(ctx, args...); err != nil {
		// Seeking past the end of a very short clip: fall back to the first frame.
		if err2 := media.Run(ctx, "-i", path, "-frames:v", "1",
			"-vf", fmt.Sprintf("scale=%d:-2:flags=lanczos", thumbWidth), out); err2 != nil {
			return "", fmt.Errorf("could not read a frame")
		}
	}
	return out, nil
}

// thumbURL is the poster link for a media path, or "" for files that cannot
// have one. It carries the file's mtime so a re-render busts the browser
// cache along with the disk cache.
func thumbURL(path string) string {
	if path == "" || assetKind(path, "video") == "audio" {
		return ""
	}
	v := ""
	if st, err := os.Stat(path); err == nil {
		v = "&v=" + strconv.FormatInt(st.ModTime().Unix(), 10)
	}
	return "/thumb?path=" + urlQueryEscape(path) + v
}

func urlQueryEscape(s string) string {
	// mediaURL passes paths through unescaped and has always worked; keep
	// the two consistent rather than having one encode and one not.
	return strings.ReplaceAll(s, " ", "%20")
}

// PruneThumbs drops cached posters that no longer match a live file, so the
// cache cannot grow without bound across a long session.
func (s *Server) PruneThumbs(maxAge time.Duration) {
	entries, err := os.ReadDir(s.thumbDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(s.thumbDir(), e.Name()))
	}
}
