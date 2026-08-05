package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Project is the durable state of an editing session: the source assets, the
// stack of edits applied, and the current working output.
//
// The ledger it renders is the heart of Itan's token efficiency: instead of
// replaying long tool transcripts to the model every turn, the full editing
// state is carried in one compact block, so old conversation turns can be
// aggressively compacted without the agent losing track of the video.
type Project struct {
	Dir     string   `json:"-"`
	Assets  []Asset  `json:"assets"`
	Ops     []EditOp `json:"ops"`
	Current string   `json:"current"` // path of the working video
	// Scenes is the storyboard: the declared plan for a multi-scene piece.
	// It renders into the ledger, so the plan survives compaction and every
	// turn sees which scenes are still unrendered.
	Scenes []Scene `json:"scenes,omitempty"`

	mu  sync.Mutex
	seq int // in-memory output counter; survives parallel tools without collisions
}

// Scene is one storyboard entry. Status is derived: planned until an Output
// is attached.
type Scene struct {
	N        int     `json:"n"`
	Intent   string  `json:"intent"` // what this scene must communicate
	Duration float64 `json:"duration"`
	Output   string  `json:"output,omitempty"` // render path once composed
}

type Asset struct {
	ID   string `json:"id"` // a1, a2, ...
	Path string `json:"path"`
	Info Info   `json:"info"`
}

type EditOp struct {
	Seq     int            `json:"seq"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args,omitempty"`
	Input   string         `json:"input"`
	Output  string         `json:"output"`
	Summary string         `json:"summary"`
	At      time.Time      `json:"at"`
}

func stateDir(dir string) string { return filepath.Join(dir, ".itan") }
func statePath(dir string) string {
	return filepath.Join(stateDir(dir), "project.json")
}

// OutDir is where rendered intermediates and results live.
func (p *Project) OutDir() string { return filepath.Join(stateDir(p.Dir), "out") }

// LoadProject reads (or initializes) the project rooted at dir.
func LoadProject(dir string) (*Project, error) {
	p := &Project{Dir: dir}
	data, err := os.ReadFile(statePath(dir))
	if err == nil {
		if err := json.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("corrupt project state %s: %w", statePath(dir), err)
		}
		p.Dir = dir
	}
	return p, nil
}

func (p *Project) Save() error {
	if err := os.MkdirAll(stateDir(p.Dir), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(p.Dir), data, 0o644)
}

// AddAsset probes and registers a source file; it becomes Current if the
// project has no working video yet.
func (p *Project) AddAsset(ctx context.Context, path string) (*Asset, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("no such file: %s", path)
	}
	info, err := Probe(ctx, abs)
	if err != nil {
		return nil, err
	}
	// IDs are max+1, not len+1: after a removal, len+1 would mint a duplicate
	// of an id the ledger and conversation still reference.
	next := 0
	for _, a := range p.Assets {
		if n, err := strconv.Atoi(strings.TrimPrefix(a.ID, "a")); err == nil && n > next {
			next = n
		}
	}
	a := Asset{ID: fmt.Sprintf("a%d", next+1), Path: abs, Info: info}
	p.Assets = append(p.Assets, a)
	// Only actual footage can become the working video — importing a logo or
	// a music bed must not hijack CURRENT. Stills report a width but no
	// duration, which is what separates them from video.
	if p.Current == "" && info.Width > 0 && info.Duration > 0 {
		p.Current = abs
	}
	return &a, p.Save()
}

// ReplaceAsset swaps the file behind an asset id — the user dropping in a
// better screenshot or corrected clip. The id is stable (the ledger and
// conversation may reference it); existing rendered ops are untouched, and
// Current follows only if it pointed at the old file.
func (p *Project) ReplaceAsset(ctx context.Context, id, newPath string) (*Asset, error) {
	abs, err := filepath.Abs(newPath)
	if err != nil {
		return nil, err
	}
	info, err := Probe(ctx, abs)
	if err != nil {
		return nil, err
	}
	for i := range p.Assets {
		if p.Assets[i].ID != id {
			continue
		}
		old := p.Assets[i].Path
		p.Assets[i].Path = abs
		p.Assets[i].Info = info
		if p.Current == old {
			p.Current = abs
		}
		return &p.Assets[i], p.Save()
	}
	return nil, fmt.Errorf("no asset %q", id)
}

// RemoveAsset unregisters a source file from the project; files on disk are
// kept and edits are NOT cascaded — every op output is an immutable rendered
// file, so edits stay valid without their source registered. Remaining ids
// are unchanged — the ledger may still reference them. When the removed
// source was the working video, Current falls back to the newest op output,
// then the first remaining source.
func (p *Project) RemoveAsset(id string) (*Asset, error) {
	idx := -1
	for i, a := range p.Assets {
		if a.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("no asset %q", id)
	}
	removed := p.Assets[idx]
	p.Assets = append(p.Assets[:idx], p.Assets[idx+1:]...)
	if p.Current == removed.Path {
		p.Current = ""
		for i := len(p.Ops) - 1; i >= 0; i-- {
			if p.Ops[i].Output != "" {
				p.Current = p.Ops[i].Output
				break
			}
		}
		if p.Current == "" && len(p.Assets) > 0 {
			p.Current = p.Assets[0].Path
		}
	}
	return &removed, p.Save()
}

// NextOutput reserves a numbered output path for a tool run. The counter is
// monotonic and mutex-guarded so concurrency-safe tools running in parallel
// never collide on a filename.
func (p *Project) NextOutput(tool, ext string) string {
	_ = os.MkdirAll(p.OutDir(), 0o755)
	p.mu.Lock()
	if p.seq < len(p.Ops) {
		p.seq = len(p.Ops)
	}
	p.seq++
	n := p.seq
	p.mu.Unlock()
	return filepath.Join(p.OutDir(), fmt.Sprintf("%03d-%s%s", n, tool, ext))
}

// Commit records a completed edit and advances the working video when the
// tool produced one.
func (p *Project) Commit(op EditOp) error {
	op.Seq = len(p.Ops) + 1
	op.At = time.Now().UTC()
	p.Ops = append(p.Ops, op)
	if op.Output != "" {
		p.Current = op.Output
	}
	return p.Save()
}

// Undo pops the newest edit and restores the previous working video.
func (p *Project) Undo() (*EditOp, error) {
	if len(p.Ops) == 0 {
		return nil, fmt.Errorf("nothing to undo")
	}
	last := p.Ops[len(p.Ops)-1]
	p.Ops = p.Ops[:len(p.Ops)-1]
	p.Current = ""
	for i := len(p.Ops) - 1; i >= 0; i-- {
		if p.Ops[i].Output != "" {
			p.Current = p.Ops[i].Output
			break
		}
	}
	if p.Current == "" && len(p.Assets) > 0 {
		p.Current = p.Assets[0].Path
	}
	return &last, p.Save()
}

// Ledger renders the whole project state in a few hundred tokens. It is
// injected into the system prompt every turn and is the single source of
// truth the model needs — which is what lets the harness prune history hard.
func (p *Project) Ledger(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("## Project state\n")
	if len(p.Assets) == 0 && len(p.Scenes) == 0 && len(p.Ops) == 0 {
		b.WriteString("No source video loaded yet. Compose scenes from scratch, or ask the user for footage.\n")
		return b.String()
	}
	if len(p.Assets) > 0 {
		b.WriteString("Sources:\n")
		for _, a := range p.Assets {
			fmt.Fprintf(&b, "  %s: %s (%s)\n", a.ID, filepath.Base(a.Path), a.Info.Compact())
		}
	}
	if len(p.Scenes) > 0 {
		b.WriteString("Storyboard:\n")
		for _, s := range p.Scenes {
			status := "PLANNED"
			if s.Output != "" {
				status = "rendered → " + filepath.Base(s.Output)
			}
			fmt.Fprintf(&b, "  scene %d (%.1fs, %s): %s\n", s.N, s.Duration, status, s.Intent)
		}
	}
	if len(p.Ops) > 0 {
		b.WriteString("Edits applied (oldest first):\n")
		for _, op := range p.Ops {
			fmt.Fprintf(&b, "  %d. %s — %s\n", op.Seq, op.Tool, op.Summary)
		}
	}
	if p.Current != "" {
		line := fmt.Sprintf("CURRENT working video: %s", filepath.Base(p.Current))
		if info, err := Probe(ctx, p.Current); err == nil {
			line += " (" + info.Compact() + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("All edit tools operate on CURRENT unless an explicit `input` is given.\n")
	return b.String()
}
