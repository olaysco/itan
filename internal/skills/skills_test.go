package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/config"
)

func TestBuiltinsLoad(t *testing.T) {
	s := Load(config.Default(), t.TempDir())
	for _, name := range []string{"tiktok", "instagram-reel", "motion-design", "product-launch"} {
		sk, ok := s.Get(name)
		if !ok {
			t.Fatalf("builtin %s missing", name)
		}
		if sk.Description == "" || sk.Body == "" || len(sk.Triggers) == 0 {
			t.Errorf("%s incomplete: %+v", name, sk)
		}
	}
	if !strings.Contains(s.Index(), "tiktok:") {
		t.Error("index missing tiktok line")
	}
}

func TestTriggerMatching(t *testing.T) {
	s := Load(config.Default(), t.TempDir())
	hits := s.Match("please make this a TikTok for me")
	if len(hits) != 1 || hits[0].Name != "tiktok" {
		t.Fatalf("match = %+v", hits)
	}
	if len(s.Match("just trim the video")) != 0 {
		t.Fatal("false positive trigger")
	}
}

// Triggers match whole words only: "ig" inside "designed" or "insta" inside
// "instantly" must not inject a playbook.
func TestTriggerWordBoundaries(t *testing.T) {
	s := Load(config.Default(), t.TempDir())
	for _, msg := range []string{
		"make this well designed and sharp",
		"do it instantly please",
		"configure the settings",
	} {
		if hits := s.Match(msg); len(hits) != 0 {
			t.Fatalf("Match(%q) = %v, want none", msg, hits)
		}
	}
	if hits := s.Match("post this on IG tonight"); len(hits) != 1 || hits[0].Name != "instagram-reel" {
		t.Fatalf("whole-word ig should still match: %v", hits)
	}
	if hits := s.Match("make me a product launch video"); len(hits) == 0 {
		t.Fatal("multi-word trigger lost")
	}
}

// A skill directory with assets/ is a pack: {ASSETS} resolves to the real
// path and the asset inventory is appended to the body.
func TestSkillAssetPack(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".itan", "skills", "brandkit")
	if err := os.MkdirAll(filepath.Join(skillDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "assets", "logo.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: brandkit\ndescription: brand pack\ntriggers: brandkit\n---\nUse <img src=\"file://{ASSETS}/logo.svg\">."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Load(config.Default(), dir)
	sk, ok := s.Get("brandkit")
	if !ok {
		t.Fatal("pack skill missing")
	}
	assets := filepath.Join(skillDir, "assets")
	if !strings.Contains(sk.Body, "file://"+assets+"/logo.svg") {
		t.Fatalf("{ASSETS} not resolved: %s", sk.Body)
	}
	if !strings.Contains(sk.Body, "Pack assets") || !strings.Contains(sk.Body, "logo.svg") {
		t.Fatal("asset inventory not appended")
	}
}

func TestProjectSkillOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".itan", "skills", "tiktok")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "---\nname: tiktok\ndescription: my house style\ntriggers: tiktok\n---\nAlways add my watermark."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Load(config.Default(), dir)
	sk, _ := s.Get("tiktok")
	if sk.Description != "my house style" || !strings.Contains(sk.Body, "watermark") {
		t.Fatalf("project skill did not override builtin: %+v", sk)
	}
}

func TestParseRejectsNoFrontmatter(t *testing.T) {
	if _, ok := parse("just a plain file", "x"); ok {
		t.Fatal("should reject files without frontmatter")
	}
}
