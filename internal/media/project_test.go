package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRemoveAsset(t *testing.T) {
	dir := t.TempDir()
	p := &Project{
		Dir:     dir,
		Assets:  []Asset{{ID: "a1", Path: "/x/one.mp4"}, {ID: "a2", Path: "/x/two.mp4"}},
		Current: "/x/one.mp4",
	}
	removed, err := p.RemoveAsset("a1")
	if err != nil || removed.ID != "a1" {
		t.Fatalf("remove: %v %+v", err, removed)
	}
	if len(p.Assets) != 1 || p.Assets[0].ID != "a2" {
		t.Fatalf("assets after remove: %+v", p.Assets)
	}
	if p.Current != "/x/two.mp4" {
		t.Fatalf("current must fall back to remaining asset, got %q", p.Current)
	}
	if _, err := p.RemoveAsset("zz"); err == nil {
		t.Fatal("expected error for unknown asset")
	}

	// NO cascade: op outputs are immutable rendered files, so edits built
	// from a removed source stay valid and CURRENT (an op output) stays put.
	p2 := &Project{
		Dir: dir,
		Assets: []Asset{
			{ID: "a1", Path: "/x/one.mp4"},
			{ID: "a2", Path: "/x/two.mp4"},
		},
		Ops: []EditOp{
			{Tool: "trim", Input: "/x/one.mp4", Output: "/x/001-trim.mp4"},
			{Tool: "crop", Input: "/x/001-trim.mp4", Output: "/x/002-crop.mp4"},
		},
		Current: "/x/002-crop.mp4",
	}
	if _, err := p2.RemoveAsset("a1"); err != nil {
		t.Fatal(err)
	}
	if len(p2.Ops) != 2 {
		t.Fatalf("removing a source must keep its edits, got %+v", p2.Ops)
	}
	if p2.Current != "/x/002-crop.mp4" {
		t.Fatalf("current must stay on the op output, got %q", p2.Current)
	}

	// When the removed source WAS current, fall back to the newest output.
	p3 := &Project{
		Dir:     dir,
		Assets:  []Asset{{ID: "a1", Path: "/x/one.mp4"}},
		Ops:     []EditOp{{Output: "/x/out1.mp4"}, {Output: "/x/out2.mp4"}},
		Current: "/x/one.mp4",
	}
	if _, err := p3.RemoveAsset("a1"); err != nil {
		t.Fatal(err)
	}
	if p3.Current != "/x/out2.mp4" {
		t.Fatalf("current must fall back to newest output, got %q", p3.Current)
	}

	// Removing the newest edit is Undo: current moves back one step.
	p4 := &Project{
		Dir:     dir,
		Assets:  []Asset{{ID: "a1", Path: "/x/one.mp4"}},
		Ops:     []EditOp{{Output: "/x/out1.mp4"}, {Output: "/x/out2.mp4"}},
		Current: "/x/out2.mp4",
	}
	if _, err := p4.Undo(); err != nil {
		t.Fatal(err)
	}
	if p4.Current != "/x/out1.mp4" {
		t.Fatalf("undo must move current back, got %q", p4.Current)
	}
	if _, err := p4.Undo(); err != nil {
		t.Fatal(err)
	}
	if p4.Current != "/x/one.mp4" {
		t.Fatalf("undoing the last edit must return to the source, got %q", p4.Current)
	}
}

// Removing an asset must never let a later Add mint a duplicate id — the
// ledger and conversation still reference the old ones.
func TestAssetIDsStayUniqueAfterRemove(t *testing.T) {
	if !Available() {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", clip)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test clip: %v\n%s", err, raw)
	}

	p := &Project{Dir: dir}
	ctx := context.Background()
	a1, err := p.AddAsset(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := p.AddAsset(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RemoveAsset(a1.ID); err != nil {
		t.Fatal(err)
	}
	a3, err := p.AddAsset(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	if a3.ID == a2.ID {
		t.Fatalf("duplicate asset id %s after remove", a3.ID)
	}
}

// SVG is invisible to ffprobe but renders natively in compose — importing a
// logo must work, carry its dimensions, and never become the working video.
func TestAddSVGAsset(t *testing.T) {
	dir := t.TempDir()
	svg := filepath.Join(dir, "logo.svg")
	body := `<svg width="1024" height="768" viewBox="0 0 1024 768" xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`
	if err := os.WriteFile(svg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Project{Dir: dir}
	a, err := p.AddAsset(context.Background(), svg)
	if err != nil {
		t.Fatalf("SVG import rejected: %v", err)
	}
	if a.Info.Width != 1024 || a.Info.Height != 768 {
		t.Fatalf("svg dims = %dx%d, want 1024x768", a.Info.Width, a.Info.Height)
	}
	if p.Current != "" {
		t.Fatalf("a still must not become the working video: %q", p.Current)
	}
	if got := a.Info.Compact(); got != "1024x768 still image" {
		t.Fatalf("still metadata reads as broken video: %q", got)
	}
	// viewBox-only SVGs import too (exact dimensions depend on whether the
	// local ffprobe can decode SVG at all — the contract is that it lands).
	svg2 := filepath.Join(dir, "vb.svg")
	if err := os.WriteFile(svg2, []byte(`<svg viewBox="0 0 200 100" xmlns="http://www.w3.org/2000/svg"/>`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := p.AddAsset(context.Background(), svg2)
	if err != nil || b.Info.Width == 0 {
		t.Fatalf("viewBox svg import: %v %+v", err, b)
	}
	// A genuinely unreadable file is still rejected.
	junk := filepath.Join(dir, "notes.svg")
	if err := os.WriteFile(junk, []byte("this is not markup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddAsset(context.Background(), junk); err == nil {
		t.Fatal("a non-SVG file with an .svg name must still be rejected")
	}
}

// StillInfo parses SVG dimensions without ffmpeg — the deterministic half of
// the import path, independent of whether the local ffprobe decodes SVG.
func TestStillInfoSVG(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name, body string
		w, h       int
	}{
		{"attrs.svg", `<svg width="640" height="480" xmlns="http://www.w3.org/2000/svg"/>`, 640, 480},
		{"viewbox.svg", `<svg viewBox="0 0 200 100" xmlns="http://www.w3.org/2000/svg"/>`, 200, 100},
		{"float.svg", `<svg width="99.5" height="50.2" xmlns="http://www.w3.org/2000/svg"/>`, 99, 50},
	}
	for _, c := range cases {
		info, ok := StillInfo(write(c.name, c.body))
		if !ok || info.Width != c.w || info.Height != c.h {
			t.Errorf("%s = %dx%d (ok=%v), want %dx%d", c.name, info.Width, info.Height, ok, c.w, c.h)
		}
		if info.Duration != 0 {
			t.Errorf("%s: a still must have no duration", c.name)
		}
	}
	if _, ok := StillInfo(write("notes.svg", "plain text")); ok {
		t.Error("non-markup .svg must not be treated as a still")
	}
	if _, ok := StillInfo(write("clip.mp4", "whatever")); ok {
		t.Error("only SVG takes the still path")
	}
}
