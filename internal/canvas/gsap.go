package canvas

import (
	"embed"
	"strings"
)

// Compositions render offline (file://, no network), so the motion library
// has to travel with the binary the same way the fonts do. GSAP is the
// industry-standard motion toolkit — timelines, staggers, per-character
// text — and it is a better fit for this engine than CSS keyframes alone:
// frame stepping works by seeking time, and GSAP's globalTimeline.time(t)
// is exactly that operation, deterministically.
//
// GSAP is free for commercial use (Webflow's standard license, v3.13+),
// bundled minified and unmodified.

//go:embed vendor_js/gsap.min.js vendor_js/SplitText.min.js
var gsapFS embed.FS

// injectGSAP adds the bundled GSAP (and SplitText) to a composition that
// references gsap. Compositions that don't mention it pay nothing.
func injectGSAP(html string) string {
	if !strings.Contains(html, "gsap") {
		return html
	}
	core, err := gsapFS.ReadFile("vendor_js/gsap.min.js")
	if err != nil {
		return html
	}
	split, _ := gsapFS.ReadFile("vendor_js/SplitText.min.js")
	inject := "<script data-itan-gsap>" + string(core) + "\n" + string(split) +
		"\nif(window.gsap&&window.SplitText){gsap.registerPlugin(SplitText)}</script>"

	lower := strings.ToLower(html)
	if i := strings.Index(lower, "<head>"); i >= 0 {
		at := i + len("<head>")
		return html[:at] + inject + html[at:]
	}
	return inject + html
}
