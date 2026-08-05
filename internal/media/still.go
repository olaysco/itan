package media

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Some perfectly usable assets are invisible to ffprobe — SVG above all,
// which the compose engine renders natively through the browser. Rejecting
// them at import would mean a user cannot bring in their own logo, so vector
// stills are registered with dimensions read from the markup itself.

var (
	svgWidthRe   = regexp.MustCompile(`(?i)\bwidth\s*=\s*"([0-9.]+)`)
	svgHeightRe  = regexp.MustCompile(`(?i)\bheight\s*=\s*"([0-9.]+)`)
	svgViewBoxRe = regexp.MustCompile(`(?i)\bviewBox\s*=\s*"\s*[-0-9.]+\s+[-0-9.]+\s+([0-9.]+)\s+([0-9.]+)`)
)

// StillInfo describes a file ffprobe cannot read but the renderer can use.
// The second return is false when the file is not such a case.
func StillInfo(path string) (Info, bool) {
	if strings.ToLower(filepath.Ext(path)) != ".svg" {
		return Info{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}
	head := string(data)
	if len(head) > 4096 {
		head = head[:4096]
	}
	if !strings.Contains(strings.ToLower(head), "<svg") {
		return Info{}, false
	}
	w, h := 0, 0
	if m := svgWidthRe.FindStringSubmatch(head); m != nil {
		w = atoiFloat(m[1])
	}
	if m := svgHeightRe.FindStringSubmatch(head); m != nil {
		h = atoiFloat(m[1])
	}
	if w == 0 || h == 0 {
		if m := svgViewBoxRe.FindStringSubmatch(head); m != nil {
			w, h = atoiFloat(m[1]), atoiFloat(m[2])
		}
	}
	// Duration stays zero: a still must never become the working video.
	return Info{Width: w, Height: h, Codec: "svg"}, true
}

func atoiFloat(s string) int {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f)
}
