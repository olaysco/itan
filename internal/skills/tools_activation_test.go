package skills

import (
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/config"
)

// Word triggers only fire on vocabulary the user happens to use. These are
// ordinary ways to ask for a video, and not one of them names a craft term —
// so before tool activation existed, the motion-design playbook loaded on none
// of them and every one of these projects was designed with no art direction.
func TestCraftPlaybookReachesOrdinaryRequests(t *testing.T) {
	set := Load(&config.Config{}, t.TempDir())
	prompts := []string{
		"make me a 45-second video about load balancing for TikTok",
		"create a video explaining our new pricing",
		"turn this blog post into a video",
		"make a short video about our company",
		"produce a 60 second product video",
		"build me a video from these notes",
		"summarise this document as a video",
		"make a promo for the new release",
		"a 30s video introducing our team",
		"video about why sleep matters",
	}
	for _, p := range prompts {
		if named(set.Match(p), "motion-design") {
			continue // fine, but not what this test is about
		}
		// The words missed it; the work must not.
		if !named(set.MatchTool("storyboard"), "motion-design") {
			t.Fatalf("%q: no craft playbook from words or from storyboard", p)
		}
		if !named(set.MatchTool("compose"), "motion-design") {
			t.Fatalf("%q: no craft playbook from words or from compose", p)
		}
	}
}

// A tool that has nothing to do with design must not drag a playbook in.
func TestUnrelatedToolsActivateNothing(t *testing.T) {
	set := Load(&config.Config{}, t.TempDir())
	for _, tool := range []string{"trim", "probe", "export", "add_music", ""} {
		if got := set.MatchTool(tool); len(got) != 0 {
			t.Fatalf("tool %q activated %d skill(s), want none", tool, len(got))
		}
	}
}

func TestToolsFrontmatterParses(t *testing.T) {
	sk, ok := parse("---\nname: x\ndescription: d\ntools: Compose , storyboard\n---\nbody", "test")
	if !ok {
		t.Fatal("parse failed")
	}
	if got := strings.Join(sk.Tools, ","); got != "compose,storyboard" {
		t.Fatalf("Tools = %q, want lowercased and trimmed", got)
	}
}

func named(hits []Skill, name string) bool {
	for _, sk := range hits {
		if sk.Name == name {
			return true
		}
	}
	return false
}
