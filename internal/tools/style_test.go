package tools

import (
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

func TestStyleKitSetShowClear(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()

	if res := r.Execute(c, "style_kit", []byte(`{}`)); !strings.Contains(res.Summary, "no style kit") {
		t.Errorf("an empty project should say so: %s", res.Summary)
	}

	res := r.Execute(c, "style_kit", []byte(`{"brief":"Dark ground, one amber accent, energetic springs.",
	  "css":".cap{font-size:62px}\n.panel{border-radius:28px}\nbody{background:#0A0A14}"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !strings.Contains(res.Summary, "3 lines") {
		t.Errorf("summary should count the CSS: %s", res.Summary)
	}
	if c.Project.Style.CSS == "" || c.Project.Style.Brief == "" {
		t.Fatal("kit was not stored on the project")
	}

	// It must survive a reload — a style that lives only in memory is the
	// problem this tool exists to fix.
	reloaded, err := media.LoadProject(c.Project.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Style.CSS != c.Project.Style.CSS || reloaded.Style.Brief != c.Project.Style.Brief {
		t.Error("style kit did not persist across a reload")
	}

	res = r.Execute(c, "style_kit", []byte(`{}`))
	if !strings.Contains(res.Summary, "Dark ground") {
		t.Errorf("reading back lost the brief: %s", res.Summary)
	}

	// Setting only the brief must not wipe the CSS.
	r.Execute(c, "style_kit", []byte(`{"brief":"Revised."}`))
	if c.Project.Style.CSS == "" {
		t.Error("updating the brief cleared the CSS")
	}

	if res = r.Execute(c, "style_kit", []byte(`{"clear":true}`)); res.Err != nil {
		t.Fatal(res.Err)
	}
	if c.Project.Style.CSS != "" || c.Project.Style.Brief != "" {
		t.Error("clear left something behind")
	}
}

// A kit that reaches the network fails silently in every scene at once.
func TestStyleKitRejectsNetworkCSS(t *testing.T) {
	c := composeCtx(t)
	r := NewRegistry()
	for _, bad := range []string{
		`{"css":"@import url('https://fonts.googleapis.com/css2?family=Inter');"}`,
		`{"css":"body{background:url(https://example.com/bg.png)}"}`,
	} {
		res := r.Execute(c, "style_kit", []byte(bad))
		if res.Err == nil {
			t.Errorf("accepted a networked kit: %s", bad)
		} else if !strings.Contains(res.Err.Error(), "file:///") {
			t.Errorf("error does not offer the local alternative: %v", res.Err)
		}
	}
	// A local file reference is legitimate.
	if res := r.Execute(c, "style_kit", []byte(`{"css":"body{background:url(file:///tmp/bg.png)}"}`)); res.Err != nil {
		t.Errorf("rejected a local file reference: %v", res.Err)
	}
}

// The ledger has to carry the brief, so every turn designs to the same
// decision, without carrying the whole stylesheet every turn.
func TestStyleKitAppearsInLedger(t *testing.T) {
	c := composeCtx(t)
	NewRegistry().Execute(c, "style_kit", []byte(`{"brief":"Editorial, warm, calm.","css":"a{}\nb{}"}`))
	ledger := c.Project.Ledger(c.Context)
	if !strings.Contains(ledger, "Editorial, warm, calm") {
		t.Errorf("ledger is missing the brief:\n%s", ledger)
	}
	if !strings.Contains(ledger, "2 lines of shared CSS") {
		t.Errorf("ledger does not mention the injected CSS:\n%s", ledger)
	}
	if strings.Contains(ledger, "a{}") {
		t.Error("ledger is carrying the stylesheet itself — that cost is paid every turn")
	}
}
