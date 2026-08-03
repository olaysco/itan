package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	readTextMaxLines = 200
	readTextMaxBytes = 8 * 1024
)

func miscTools() []Tool {
	return []Tool{
		{
			Name: "list_files",
			Description: "List media files in the project folder — video, audio, image — marking which are already " +
				"registered assets and which are just sitting there. Use this to FIND material the user dropped in " +
				"(a music bed for add_music, a logo, b-roll) instead of guessing paths or asking.",
			Schema: schema(nil, map[string]map[string]any{
				"kind": prop("string", "Filter: 'video', 'audio', 'image', or 'all' (default)."),
			}),
			Run:             runListFiles,
			ConcurrencySafe: true,
		},
		{
			Name: "read_text",
			Description: "Read a text file inside the project (transcripts, spilled tool outputs, subtitle files). " +
				"Returns up to 200 lines / 8KB per call; use `offset` to continue reading longer files.",
			Schema: schema([]string{"path"}, map[string]map[string]any{
				"path":   prop("string", "File path (must be inside the project directory)."),
				"offset": prop("integer", "1-based line to start from (default 1)."),
			}),
			Run:             runReadText,
			ConcurrencySafe: true,
		},
	}
}

var mediaKinds = map[string]string{
	".mp4": "video", ".mov": "video", ".mkv": "video", ".webm": "video", ".avi": "video", ".m4v": "video",
	".wav": "audio", ".mp3": "audio", ".m4a": "audio", ".aac": "audio", ".flac": "audio", ".ogg": "audio",
	".png": "image", ".jpg": "image", ".jpeg": "image", ".webp": "image", ".svg": "image",
}

// runListFiles walks the project folder so the model can discover material
// the user simply dropped in. Registered assets are marked, so the model
// knows what it may reference by id versus by path.
func runListFiles(c *Ctx, args Args) Result {
	want := strings.ToLower(args.Str("kind"))
	if want == "" {
		want = "all"
	}
	root, err := filepath.Abs(c.Project.Dir)
	if err != nil {
		return Result{Err: err}
	}
	registered := map[string]string{} // abs path → asset id
	for _, a := range c.Project.Assets {
		registered[a.Path] = a.ID
	}

	type entry struct{ line, kind string }
	var found []entry
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Intermediate renders are already in the ledger; skip the noise.
			if d.Name() == "out" || strings.HasPrefix(d.Name(), ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		kind, ok := mediaKinds[strings.ToLower(filepath.Ext(path))]
		if !ok || (want != "all" && kind != want) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		mark := "unregistered"
		if id, hit := registered[path]; hit {
			mark = "asset " + id
		}
		size := ""
		if info, ierr := d.Info(); ierr == nil {
			size = fmt.Sprintf("%.1f MB", float64(info.Size())/(1<<20))
		}
		found = append(found, entry{fmt.Sprintf("  %s (%s, %s, %s)", rel, kind, size, mark), kind})
		return nil
	})
	if len(found) == 0 {
		return Result{Summary: "no " + want + " files in the project folder"}
	}
	if len(found) > 60 {
		found = found[:60]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d file(s) in %s:\n", len(found), root)
	for _, f := range found {
		b.WriteString(f.line + "\n")
	}
	return Result{
		Summary: fmt.Sprintf("%d media file(s) found — paths are usable directly as tool inputs", len(found)),
		Data:    map[string]any{"files": b.String()},
	}
}

func runReadText(c *Ctx, args Args) Result {
	path := args.Str("path")
	if path == "" {
		return fail("read_text needs `path`")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.Project.Dir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Result{Err: err}
	}
	root, err := filepath.Abs(c.Project.Dir)
	if err != nil {
		return Result{Err: err}
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return fail("read_text is restricted to files inside the project directory")
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{Err: err}
	}
	lines := strings.Split(string(data), "\n")
	offset := args.Int("offset", 1)
	if offset < 1 {
		offset = 1
	}
	if offset > len(lines) {
		return fail("offset %d beyond end of file (%d lines)", offset, len(lines))
	}

	var b strings.Builder
	count := 0
	for i := offset - 1; i < len(lines) && count < readTextMaxLines && b.Len() < readTextMaxBytes; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
		count++
	}
	next := offset + count
	summary := fmt.Sprintf("%s lines %d-%d of %d", filepath.Base(abs), offset, next-1, len(lines))
	data2 := map[string]any{"content": strings.TrimRight(b.String(), "\n")}
	if next <= len(lines) {
		data2["continue"] = fmt.Sprintf("offset=%d to keep reading", next)
	}
	return Result{Summary: summary, Data: data2}
}
