// Package tools defines the editing tools the agent can call.
//
// Tools follow two harness rules that keep the loop token-efficient and safe:
//  1. Results are compact: one summary line plus a few structured facts —
//     never raw ffmpeg logs or file dumps.
//  2. Every render lands in a numbered file under .itan/out and is
//     committed to the project ledger, so state lives in the ledger rather
//     than in conversation history, and any step can be undone or previewed.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/voice"
)

// Ctx carries everything a tool may need.
type Ctx struct {
	Context context.Context
	Project *media.Project
	Config  *config.Config
	TTS     voice.TTS
	STT     voice.STT
}

// Result is what a tool reports back to the agent (and the ledger).
type Result struct {
	Summary string         // one terse line, required
	Output  string         // rendered file path, if the tool produced one
	Data    map[string]any // small structured facts (transcript, dims, ...)
	// Frames are images the model should SEE (view_frames output); the
	// agent attaches them to the tool result so a multimodal model can
	// judge its own work instead of trusting the numbers.
	Frames []FrameRef
	Err    error
}

// FrameRef points at an extracted frame on disk.
type FrameRef struct {
	Path      string
	MediaType string
}

func fail(format string, a ...any) Result { return Result{Err: fmt.Errorf(format, a...)} }

// Compact renders the result as the string fed back to the model, hard-capped
// by config so no tool can blow up the context.
func (r Result) Compact(maxChars int) string {
	if r.Err != nil {
		return "ERROR: " + clip(r.Err.Error(), maxChars)
	}
	out := r.Summary
	if len(r.Data) > 0 {
		keys := make([]string, 0, len(r.Data))
		for k := range r.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, r.Data[k]))
		}
		out += "\n" + strings.Join(parts, " | ")
	}
	return clip(out, maxChars)
}

// Full renders the complete, uncapped result text (used for disk spill when
// the compact form had to truncate).
func (r Result) Full() string { return r.Compact(0) }

func clip(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…[truncated]"
}

// Tool couples a JSON-schema declaration with its implementation.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Run         func(c *Ctx, args Args) Result
	// Mutating tools commit an EditOp and advance CURRENT.
	Mutating bool
	// ConcurrencySafe tools (pure reads) may run in parallel with each other.
	ConcurrencySafe bool
}

// Args wraps decoded tool arguments with forgiving accessors: models
// sometimes send numbers as strings and vice versa, and the harness should
// absorb that rather than error.
type Args map[string]any

func (a Args) Str(key string) string {
	switch v := a[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (a Args) Float(key string, def float64) float64 {
	switch v := a[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return def
}

func (a Args) Int(key string, def int) int { return int(a.Float(key, float64(def))) }

// Registry holds the tool set in a stable order.
type Registry struct {
	tools  []Tool
	byName map[string]*Tool
}

func NewRegistry() *Registry {
	r := &Registry{byName: map[string]*Tool{}}
	for _, t := range videoTools() {
		r.add(t)
	}
	for _, t := range regionTools() {
		r.add(t)
	}
	for _, t := range audioTools() {
		r.add(t)
	}
	for _, t := range composeTools() {
		r.add(t)
	}
	for _, t := range webTools() {
		r.add(t)
	}
	for _, t := range viewTools() {
		r.add(t)
	}
	for _, t := range miscTools() {
		r.add(t)
	}
	return r
}

func (r *Registry) add(t Tool) {
	r.tools = append(r.tools, t)
	r.byName[t.Name] = &r.tools[len(r.tools)-1]
}

func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) All() []Tool { return r.tools }

// Execute runs a tool by name with raw JSON input, commits mutations to the
// project, and returns the result.
func (r *Registry) Execute(c *Ctx, name string, rawInput json.RawMessage) Result {
	tool, ok := r.Get(name)
	if !ok {
		return fail("unknown tool %q", name)
	}
	args := Args{}
	if len(rawInput) > 0 {
		if err := json.Unmarshal(rawInput, &args); err != nil {
			return fail("bad arguments for %s: %v", name, err)
		}
	}
	res := tool.Run(c, args)
	if res.Err == nil && tool.Mutating && res.Output != "" {
		commitErr := c.Project.Commit(media.EditOp{
			Tool:    name,
			Args:    args,
			Input:   args.Str("input"),
			Output:  res.Output,
			Summary: res.Summary,
		})
		if commitErr != nil {
			res.Err = fmt.Errorf("edit rendered but could not be committed: %w", commitErr)
		}
		// Probe-after-edit feedback: the model immediately sees the concrete
		// effect of its edit (dimensions/duration/audio), the way a coding
		// agent sees compiler diagnostics after a write.
		if res.Err == nil {
			if info, perr := media.Probe(c.Context, res.Output); perr == nil {
				if res.Data == nil {
					res.Data = map[string]any{}
				}
				res.Data["now"] = info.Compact()
			}
		}
	}
	return res
}

// schema builds a JSON-schema object from property name → spec pairs.
func schema(required []string, props map[string]map[string]any) map[string]any {
	p := map[string]any{}
	for name, spec := range props {
		p[name] = spec
	}
	s := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// resolveInput maps an optional "input" argument (asset id like "a1", or a
// path) onto a concrete file, defaulting to the project's CURRENT video.
func resolveInput(c *Ctx, args Args) (string, error) {
	in := args.Str("input")
	if in == "" {
		if c.Project.Current == "" {
			return "", fmt.Errorf("no working video: add a source file first")
		}
		return c.Project.Current, nil
	}
	for _, a := range c.Project.Assets {
		if a.ID == in {
			return a.Path, nil
		}
	}
	return in, nil // treat as a path; the tool's probe/render will validate it
}
