// Package permission gates tool execution.
//
// Design (borrowed from the strongest parts of Claude Code and opencode):
//   - Rules are evaluated last-match-wins over (config rules ++ session
//     rules), with '*' wildcards on the tool name. Default depends on mode.
//   - Modes: "auto" (mutating edits allowed — they are cheap and undoable),
//     "ask" (every mutating tool prompts), "plan" (mutating tools denied;
//     the agent must present a plan instead).
//   - A safety tier is bypass-immune: requests flagged with a safety reason
//     (overwriting an existing file outside the managed output dir, deleting
//     data) prompt even when rules or mode would allow them.
//   - A denial can carry feedback that flows back to the model as a tool
//     error — "deny with a correction", so the agent can adjust course
//     instead of just failing.
package permission

import (
	"fmt"
	"strings"
	"sync"
)

type Action string

const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

type Mode string

const (
	ModeAuto Mode = "auto"
	ModeAsk  Mode = "ask"
	ModePlan Mode = "plan"
)

// Rule matches a tool name (supports '*' suffix wildcard and bare '*').
type Rule struct {
	Tool   string `yaml:"tool" json:"tool"`
	Action Action `yaml:"action" json:"action"`
}

func (r Rule) matches(tool string) bool {
	if r.Tool == "*" || r.Tool == tool {
		return true
	}
	if prefix, ok := strings.CutSuffix(r.Tool, "*"); ok {
		return strings.HasPrefix(tool, prefix)
	}
	return false
}

// Request describes one attempted tool call.
type Request struct {
	Tool     string
	Args     map[string]any
	Mutating bool
	// Safety, when non-empty, explains why this call is in the bypass-immune
	// tier (e.g. "overwrites existing file x.mp4"). Safety requests always
	// prompt, regardless of rules or mode.
	Safety string
}

// Decision is the outcome. AlwaysAllow additionally records a session rule so
// the same tool is not asked about again.
type Decision struct {
	Action      Action
	Feedback    string // shown to the model on deny
	AlwaysAllow bool
}

// Asker is the blocking UI callback used to resolve an "ask".
type Asker func(Request) Decision

// Gate is the evaluator. Safe for concurrent use.
type Gate struct {
	mu    sync.Mutex
	mode  Mode
	rules []Rule // config rules; session rules are appended (last wins)
	ask   Asker
}

func NewGate(mode Mode, rules []Rule, ask Asker) *Gate {
	if mode == "" {
		mode = ModeAuto
	}
	return &Gate{mode: mode, rules: append([]Rule(nil), rules...), ask: ask}
}

func (g *Gate) Mode() Mode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mode
}

func (g *Gate) SetMode(m Mode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mode = m
}

// SetAsker attaches (or replaces) the interactive approver.
func (g *Gate) SetAsker(ask Asker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ask = ask
}

// Check resolves a request to allow or deny (asks are resolved inline via the
// Asker; with no Asker configured an ask degrades to a deny with guidance).
func (g *Gate) Check(req Request) Decision {
	g.mu.Lock()
	mode := g.mode
	rules := append([]Rule(nil), g.rules...)
	ask := g.ask
	g.mu.Unlock()

	// Read-only tools never need approval — but plan mode still blocks
	// mutations below regardless of rules.
	action := defaultAction(mode, req)

	// Last-match-wins over the ruleset.
	for _, r := range rules {
		if r.matches(req.Tool) {
			action = r.Action
		}
	}

	// Plan mode is a hard ceiling on mutations: no rule can lift it.
	if mode == ModePlan && req.Mutating {
		return Decision{Action: Deny, Feedback: "plan mode is active: do not edit. Present a concise numbered plan of the edits you would make and wait for approval."}
	}

	// Safety tier is bypass-immune: even an allow outcome must prompt.
	if req.Safety != "" && action == Allow {
		action = Ask
	}

	switch action {
	case Allow:
		return Decision{Action: Allow}
	case Deny:
		return Decision{Action: Deny, Feedback: fmt.Sprintf("the %s tool is denied by the user's permission rules", req.Tool)}
	default: // Ask
		if ask == nil {
			return Decision{Action: Deny, Feedback: fmt.Sprintf("%s requires interactive approval and no approver is attached; explain what you wanted to do and let the user run it from the CLI", req.Tool)}
		}
		dec := ask(req)
		if dec.Action == Allow && dec.AlwaysAllow && req.Safety == "" {
			g.mu.Lock()
			g.rules = append(g.rules, Rule{Tool: req.Tool, Action: Allow})
			g.mu.Unlock()
		}
		if dec.Action == Deny && dec.Feedback == "" {
			dec.Feedback = fmt.Sprintf("the user declined the %s call%s", req.Tool, feedbackHint(req))
		}
		return dec
	}
}

func defaultAction(mode Mode, req Request) Action {
	if !req.Mutating {
		return Allow
	}
	switch mode {
	case ModeAsk:
		return Ask
	default:
		return Allow
	}
}

func feedbackHint(req Request) string {
	if req.Safety != "" {
		return " (" + req.Safety + ")"
	}
	return ""
}
