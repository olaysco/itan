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
