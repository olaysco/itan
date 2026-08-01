package media

import (
	"context"
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

	// With edits on record, the newest op output wins the fallback.
	p2 := &Project{
		Dir:     dir,
		Assets:  []Asset{{ID: "a1", Path: "/x/one.mp4"}},
		Ops:     []EditOp{{Output: "/x/out1.mp4"}, {Output: "/x/out2.mp4"}},
		Current: "/x/one.mp4",
	}
	if _, err := p2.RemoveAsset("a1"); err != nil {
		t.Fatal(err)
	}
	if p2.Current != "/x/out2.mp4" {
		t.Fatalf("current must fall back to newest output, got %q", p2.Current)
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
