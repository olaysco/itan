package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	mu  sync.Mutex
	seq int // in-memory output counter; survives parallel tools without collisions
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
	a := Asset{ID: fmt.Sprintf("a%d", len(p.Assets)+1), Path: abs, Info: info}
	p.Assets = append(p.Assets, a)
	if p.Current == "" {
		p.Current = abs
	}
	return &a, p.Save()
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
	if len(p.Assets) == 0 {
		b.WriteString("No source video loaded yet. Ask the user for one, or use tools once a file is added.\n")
		return b.String()
	}
	b.WriteString("Sources:\n")
	for _, a := range p.Assets {
		fmt.Fprintf(&b, "  %s: %s (%s)\n", a.ID, filepath.Base(a.Path), a.Info.Compact())
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
