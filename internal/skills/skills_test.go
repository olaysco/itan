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
	for _, name := range []string{"tiktok", "instagram-reel", "motion-design"} {
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
