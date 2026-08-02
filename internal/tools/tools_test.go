package tools

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// resolveInput must accept every way a model plausibly names a file: the
// literal CURRENT (our own docs mention it), asset ids, bare output filenames
// as the ledger prints them, and real paths. Anything else gets an actionable
// error instead of a raw ffprobe failure.
func TestResolveInputAliases(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &media.Project{Dir: dir, Assets: []media.Asset{{ID: "a1", Path: src}}, Current: src}
	if err := os.MkdirAll(proj.OutDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(proj.OutDir(), "010-trim.mp4")
	if err := os.WriteFile(out, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Ctx{Context: context.Background(), Project: proj}

	cases := map[string]string{
		"":             src,
		"CURRENT":      src,
		"current":      src,
		"a1":           src,
		"clip.mp4":     src,
		"010-trim.mp4": out,
		out:            out,
	}
	for in, want := range cases {
		got, err := resolveInput(c, Args{"input": in})
		if err != nil {
			t.Fatalf("resolveInput(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("resolveInput(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := resolveInput(c, Args{"input": "no-such-thing.mp4"}); err == nil || !strings.Contains(err.Error(), "project-state") {
		t.Fatalf("unknown input must return an actionable error, got %v", err)
	}
}

func TestParseAspect(t *testing.T) {
	cases := map[string]float64{
		"16:9":       16.0 / 9,
		"9x16":       9.0 / 16,
		"1:1":        1,
		"2.39":       2.39,
		"vertical":   9.0 / 16,
		"tiktok":     9.0 / 16,
		"WIDESCREEN": 16.0 / 9,
	}
	for in, want := range cases {
		got, err := ParseAspect(in)
		if err != nil {
			t.Fatalf("ParseAspect(%q): %v", in, err)
		}
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("ParseAspect(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "banana", "0:0"} {
		if _, err := ParseAspect(bad); err == nil {
			t.Errorf("ParseAspect(%q): expected error", bad)
		}
	}
}

func TestArgsAccessorsForgiving(t *testing.T) {
	a := Args{"start": "1.5", "end": 3.0, "n": "7", "s": 42}
	if a.Float("start", 0) != 1.5 {
		t.Error("string float not coerced")
	}
	if a.Float("end", 0) != 3.0 {
		t.Error("float lost")
	}
	if a.Int("n", 0) != 7 {
		t.Error("string int not coerced")
	}
	if a.Str("s") != "42" {
		t.Error("number-as-string failed")
	}
	if a.Float("missing", 9) != 9 {
		t.Error("default ignored")
	}
}

func TestResultCompactCaps(t *testing.T) {
	r := Result{Summary: "ok", Data: map[string]any{"transcript": strings.Repeat("x", 5000)}}
	out := r.Compact(200)
	if len(out) > 220 {
		t.Fatalf("compact result too long: %d", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatal("expected truncation marker")
	}
}

func TestRegistryHasExpectedTools(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{
		"probe", "trim", "concat", "set_speed", "crop", "expand_frame",
		"change_background", "overlay_text", "render", "export",
		"blur_region", "pixelate_region", "zoom_region", "compose", "overlay_video", "fetch_page", "capture_page",
		"transcribe", "tts", "extract_audio", "replace_audio", "mix_audio",
		"read_text", "view_frames",
	} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	for _, tool := range r.All() {
		if tool.Description == "" || tool.Schema == nil {
			t.Errorf("tool %s lacks description/schema", tool.Name)
		}
	}
}

func TestEscapeDrawtext(t *testing.T) {
	out := escapeDrawtext(`it's 100%: done`)
	for _, frag := range []string{`\'`, `\%`, `\:`} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing escape %q in %q", frag, out)
		}
	}
}
