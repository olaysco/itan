package canvas

import (
	"fmt"
	"regexp"
	"strings"
)

// Renders happen offline against a file:// document, so a subresource on
// http(s) can only fail. That failure is silent: the page still renders, just
// without whatever the tag was for. A composition that pulled a motion
// library from a CDN produces a video where nothing moves, and the render
// reports success.
//
// GSAP happens to survive because injectGSAP bundles it whenever the word
// appears — every other library does not. So external subresources are
// removed before the render and reported back, turning a silent wrong result
// into an explicit one the model can act on.

var (
	externalScript = regexp.MustCompile(`(?is)<script[^>]+src\s*=\s*["']https?://[^"']*["'][^>]*>\s*</script\s*>`)
	externalLink   = regexp.MustCompile(`(?is)<link[^>]+href\s*=\s*["']https?://[^"']*["'][^>]*/?>`)
	externalURL    = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["'](https?://[^"']+)["']`)
)

// StripExternal removes subresource tags that point at the network and
// returns the cleaned HTML plus every external URL the document referenced —
// including ones left in place (an <img>, say), because those are equally
// dead and the author still needs to know.
func StripExternal(html string) (string, []string) {
	var found []string
	seen := map[string]bool{}
	for _, m := range externalURL.FindAllStringSubmatch(html, -1) {
		u := m[1]
		if seen[u] {
			continue
		}
		seen[u] = true
		found = append(found, u)
	}
	if len(found) == 0 {
		return html, nil
	}
	out := externalScript.ReplaceAllString(html, "")
	out = externalLink.ReplaceAllString(out, "")
	return out, found
}

// ExternalNote is the sentence the compose tool reports when a composition
// reached for the network. It names the URLs so the model can inline them
// rather than guessing what went wrong.
func ExternalNote(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	noun := "external URL"
	if len(urls) != 1 {
		noun += "s"
	}
	return fmt.Sprintf("dropped %d %s (renders are offline, so these could only fail): ", len(urls), noun) +
		strings.Join(urls, ", ") +
		" — inline the code, or reference a local file with file:///absolute/path. GSAP is already bundled: just use `gsap`."
}
