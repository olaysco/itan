package canvas

import (
	"embed"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/olaysco/itan/internal/config"
)

// Renders are offline, so without provisioning every composition falls back
// to whatever the OS ships (DejaVu on headless Linux) — the fastest way to
// make motion graphics look cheap. Instead the engine embeds Itan's brand
// faces (both OFL-licensed, see fonts/OFL.txt) and injects them into every
// composition as data-URI @font-face rules, so good typography is the floor,
// not an option. Users add their own faces by dropping .ttf/.otf/.woff2
// files into ~/.itan/fonts — the family name is the file's base name.

//go:embed fonts/*.ttf
var fontFS embed.FS

// builtinFonts maps embedded files to the @font-face declarations they need.
// Bricolage is a variable font, so one file covers weights 200–800.
var builtinFonts = []struct {
	file, family, weight string
}{
	{"fonts/BricolageGrotesque.ttf", "Bricolage Grotesque", "200 800"},
	{"fonts/IBMPlexMono-Regular.ttf", "IBM Plex Mono", "400"},
	{"fonts/IBMPlexMono-Bold.ttf", "IBM Plex Mono", "700"},
}

var (
	fontCSSOnce sync.Once
	fontCSS     string
)

// fontFaceCSS builds the injected <style> block once per process: embedded
// faces first, then anything the user dropped into ~/.itan/fonts.
func fontFaceCSS() string {
	fontCSSOnce.Do(func() {
		var b strings.Builder
		for _, f := range builtinFonts {
			data, err := fontFS.ReadFile(f.file)
			if err != nil {
				continue
			}
			writeFontFace(&b, f.family, f.weight, filepath.Ext(f.file), data)
		}
		userDir := filepath.Join(config.GlobalDir(), "fonts")
		entries, err := os.ReadDir(userDir)
		if err == nil {
			for _, e := range entries {
				ext := strings.ToLower(filepath.Ext(e.Name()))
				if ext != ".ttf" && ext != ".otf" && ext != ".woff2" {
					continue
				}
				data, rerr := os.ReadFile(filepath.Join(userDir, e.Name()))
				if rerr != nil {
					continue
				}
				family := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				writeFontFace(&b, family, "100 900", ext, data)
			}
		}
		fontCSS = b.String()
	})
	return fontCSS
}

func writeFontFace(b *strings.Builder, family, weight, ext string, data []byte) {
	format := map[string]string{".ttf": "truetype", ".otf": "opentype", ".woff2": "woff2"}[ext]
	fmt.Fprintf(b,
		"@font-face{font-family:'%s';font-weight:%s;font-style:normal;src:url(data:font/%s;base64,%s) format('%s');}\n",
		family, weight, strings.TrimPrefix(ext, "."), base64.StdEncoding.EncodeToString(data), format)
}

// injectFonts prepends the @font-face rules to a composition, right after
// <head> when one exists so author styles can still override.
func injectFonts(html string) string {
	style := "<style data-itan-fonts>\n" + fontFaceCSS() + "</style>"
	lower := strings.ToLower(html)
	if i := strings.Index(lower, "<head>"); i >= 0 {
		at := i + len("<head>")
		return html[:at] + style + html[at:]
	}
	return style + html
}

// FontFamilies lists the families available to compositions, for tool
// descriptions and doctor output.
func FontFamilies() []string {
	families := []string{"Bricolage Grotesque", "IBM Plex Mono"}
	userDir := filepath.Join(config.GlobalDir(), "fonts")
	if entries, err := os.ReadDir(userDir); err == nil {
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".ttf" || ext == ".otf" || ext == ".woff2" {
				families = append(families, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
			}
		}
	}
	return families
}
