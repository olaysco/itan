package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tool that produces material — compose, tts, find_media — registers an
// asset and commits no op. A process that reopened the project and numbered
// from len(Ops) therefore started again from the beginning and wrote over
// finished renders. This is that bug: a re-rendered scene 1 landed on top of
// scene 2's video and four minutes of work vanished.
func TestNextOutputNeverOverwritesExistingRenders(t *testing.T) {
	dir := t.TempDir()
	first := &Project{Dir: dir}

	// Four composes: outputs numbered, no ops committed.
	var made []string
	for i := 0; i < 4; i++ {
		path := first.NextOutput("compose", ".mp4")
		if err := os.WriteFile(path, []byte("render "+filepath.Base(path)), 0o644); err != nil {
			t.Fatal(err)
		}
		made = append(made, path)
	}
	// One real edit does commit an op — and writes its file, like the tool does.
	asm := first.NextOutput("assemble", ".mp4")
	if err := os.WriteFile(asm, []byte("render "+filepath.Base(asm)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(EditOp{Tool: "assemble", Output: asm}); err != nil {
		t.Fatal(err)
	}
	made = append(made, asm)

	// A second process opens the same project and renders again.
	second := &Project{Dir: dir, Ops: first.Ops}
	next := second.NextOutput("compose", ".mp4")
	for _, existing := range made {
		if next == existing {
			t.Fatalf("new render would overwrite %s", filepath.Base(existing))
		}
	}
	if _, err := os.Stat(next); err == nil {
		t.Fatalf("%s already exists on disk", filepath.Base(next))
	}
	if !strings.HasPrefix(filepath.Base(next), "006-") {
		t.Errorf("numbering did not resume past what is on disk: %s", filepath.Base(next))
	}

	// The originals must still hold their own content.
	for _, path := range made {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "render "+filepath.Base(path) {
			t.Errorf("%s was overwritten", filepath.Base(path))
		}
	}
}

// Even with no ops at all, a reopened project must not reuse names.
func TestNextOutputResumesWithoutOps(t *testing.T) {
	dir := t.TempDir()
	p := &Project{Dir: dir}
	for i := 0; i < 3; i++ {
		os.WriteFile(p.NextOutput("compose", ".mp4"), []byte("x"), 0o644)
	}
	fresh := &Project{Dir: dir} // no ops, no memory
	if got := filepath.Base(fresh.NextOutput("compose", ".mp4")); !strings.HasPrefix(got, "004-") {
		t.Fatalf("reopened project restarted numbering at %s", got)
	}
}
