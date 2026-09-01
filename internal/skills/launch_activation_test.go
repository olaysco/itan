package skills

import (
	"testing"

	"github.com/olaysco/itan/internal/config"
)

// Pasting a URL is how anyone actually asks for a video about a product, and
// none of these name a trigger word. Before tool activation the launch
// playbook — the pipeline, and the honesty rules that stop the model
// inventing features the page never claimed — reached none of them.
func TestLaunchPlaybookReachesAPastedURL(t *testing.T) {
	set := Load(&config.Config{}, t.TempDir())
	prompts := []string{
		"make a video from https://acme.com",
		"https://linear.app",
		"turn https://acme.com/pricing into a 30 second video",
		"build something from https://acme.com for our release",
	}
	for _, p := range prompts {
		if named(set.Match(p), "product-launch") {
			continue
		}
		// The words missed it. Reading the page must not.
		for _, tool := range []string{"fetch_page", "capture_page"} {
			if !named(set.MatchTool(tool), "product-launch") {
				t.Fatalf("%q: no launch playbook from words or from %s", p, tool)
			}
		}
	}
}

// The honesty rules are the part that must not be optional: without them the
// model is free to invent features, numbers and testimonials for a real
// company's product.
func TestLaunchPlaybookCarriesTheHonestyRules(t *testing.T) {
	set := Load(&config.Config{}, t.TempDir())
	sk, ok := set.Get("product-launch")
	if !ok {
		t.Fatal("product-launch skill missing")
	}
	for _, want := range []string{"NEVER invent", "never mock up fake UI"} {
		if !contains(sk.Body, want) {
			t.Errorf("playbook no longer states %q", want)
		}
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}()
}
