package tools

import (
	"math"
	"strings"
	"testing"
)

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
		"transcribe", "tts", "extract_audio", "replace_audio", "mix_audio",
		"read_text",
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
