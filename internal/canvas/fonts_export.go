package canvas

import "io/fs"

// BuiltinFontFS exposes the embedded faces so the desktop UI can serve the
// same typography the renderer uses. The interface used to pull them from
// Google Fonts, which meant the product looked wrong offline, behind a
// firewall, or on a plane — while carrying the identical files in its own
// binary the whole time.
func BuiltinFontFS() fs.FS {
	sub, err := fs.Sub(fontFS, "fonts")
	if err != nil {
		return fontFS
	}
	return sub
}
