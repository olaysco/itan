// Package skills implements Itan's skill system: reusable editing playbooks
// injected into the agent with progressive disclosure.
//
// Token efficiency: only the one-line index of every skill is always visible
// to the model. A skill's full body is loaded into context only when the
// user's message matches its triggers (or it is invoked as /skill <name>).
//
// Skills are plain markdown files with YAML-ish frontmatter:
//
//	---
//	name: tiktok
//	description: one line shown in the index
//	triggers: tiktok, fyp
//	---
//	...playbook body...
//
// Search order (later wins on name clash): built-ins → ~/.itan/skills/*/SKILL.md
// → <project>/.itan/skills/*/SKILL.md → cfg.SkillDirs.
package skills

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olaysco/itan/internal/config"
)

//go:embed defaults/*/SKILL.md
var defaultsFS embed.FS

type Skill struct {
	Name        string
	Description string
	Triggers    []string
	Body        string
	Source      string // "builtin" or the file path
}

type Set struct {
	byName map[string]Skill
	order  []string
}

// Load gathers all skills visible to a project.
func Load(cfg *config.Config, projectDir string) *Set {
	s := &Set{byName: map[string]Skill{}}

	_ = fs.WalkDir(defaultsFS, "defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "SKILL.md") {
			return nil
		}
		raw, rerr := defaultsFS.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if sk, ok := parse(string(raw), "builtin"); ok {
			s.add(sk)
		}
		return nil
	})

	dirs := []string{
		filepath.Join(config.GlobalDir(), "skills"),
		filepath.Join(projectDir, ".itan", "skills"),
	}
	dirs = append(dirs, cfg.SkillDirs...)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name(), "SKILL.md")
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				continue
			}
			if sk, ok := parse(string(raw), p); ok {
				s.add(sk)
			}
		}
	}
	return s
}

func (s *Set) add(sk Skill) {
	if _, exists := s.byName[sk.Name]; !exists {
		s.order = append(s.order, sk.Name)
	}
	s.byName[sk.Name] = sk
}

func (s *Set) Get(name string) (Skill, bool) {
	sk, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	return sk, ok
}

func (s *Set) All() []Skill {
	names := append([]string(nil), s.order...)
	sort.Strings(names)
	out := make([]Skill, 0, len(names))
	for _, n := range names {
		out = append(out, s.byName[n])
	}
	return out
}

// Index is the always-visible one-line-per-skill catalogue.
func (s *Set) Index() string {
	if len(s.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Skills (playbooks)\n")
	for _, sk := range s.All() {
		b.WriteString("- " + sk.Name + ": " + sk.Description + "\n")
	}
	b.WriteString("When a skill's playbook arrives in a <skill-playbook> block, it MUST guide your edits.\n")
	return b.String()
}

// Match returns skills whose triggers appear in the message as whole words.
// Word boundaries matter: the trigger "ig" must not fire on "designed", nor
// "insta" on "instantly" — a wrongly injected playbook actively misleads.
func (s *Set) Match(message string) []Skill {
	m := strings.ToLower(message)
	var hits []Skill
	for _, sk := range s.All() {
		for _, trig := range sk.Triggers {
			if trig != "" && containsWord(m, trig) {
				hits = append(hits, sk)
				break
			}
		}
	}
	return hits
}

// containsWord reports whether trig occurs in m with non-alphanumeric (or
// string edge) on both sides. Multi-word triggers match phrase-wise.
func containsWord(m, trig string) bool {
	for start := 0; ; {
		i := strings.Index(m[start:], trig)
		if i < 0 {
			return false
		}
		i += start
		before := i == 0 || !isWordChar(m[i-1])
		after := i+len(trig) == len(m) || !isWordChar(m[i+len(trig)])
		if before && after {
			return true
		}
		start = i + 1
	}
}

func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func parse(raw, source string) (Skill, bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(raw, "\ufeff\n\r "), "---")
	if !ok {
		return Skill{}, false
	}
	front, body, ok := strings.Cut(rest, "---")
	if !ok {
		return Skill{}, false
	}
	sk := Skill{Body: strings.TrimSpace(body), Source: source}
	for _, line := range strings.Split(front, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "name":
			sk.Name = strings.ToLower(val)
		case "description":
			sk.Description = val
		case "triggers":
			for _, t := range strings.Split(val, ",") {
				if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
					sk.Triggers = append(sk.Triggers, t)
				}
			}
		}
	}
	return sk, sk.Name != ""
}
