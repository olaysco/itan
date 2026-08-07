package canvas

import "strings"

// A style brief that lives only in the model's head lasts exactly as long as
// its context. Every compose call is an independent HTML document, so four
// scenes of one video were four unrelated designs — cartoon figures in the
// first, glass cards in the last — and nothing in the system noticed.
//
// The kit is CSS the project owns, injected into every composition after the
// fonts and before the scene's own styles: the scene inherits the ground,
// type scale, panels and captions by default, and can still override any of
// it with ordinary cascade rules.

// injectStyleKit puts the project's shared CSS in front of each scene. It
// goes in immediately after <head> like the fonts, but the fonts are injected
// first, so a kit that names a family it depends on still resolves.
func injectStyleKit(html, css string) string {
	if strings.TrimSpace(css) == "" {
		return html
	}
	inject := "<style data-itan-kit>\n" + css + "\n</style>"
	lower := strings.ToLower(html)
	// After the font block when one is present, so the kit can use the
	// families; otherwise at the top of head.
	if i := strings.Index(lower, "</style>"); i >= 0 && strings.Contains(lower[:i], "data-itan-fonts") {
		at := i + len("</style>")
		return html[:at] + inject + html[at:]
	}
	if i := strings.Index(lower, "<head>"); i >= 0 {
		at := i + len("<head>")
		return html[:at] + inject + html[at:]
	}
	return inject + html
}
