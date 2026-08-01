// Package cli implements the interactive REPL and shared terminal rendering.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olaysco/heydit/internal/agent"
	"github.com/olaysco/heydit/internal/config"
	"github.com/olaysco/heydit/internal/media"
	"github.com/olaysco/heydit/internal/provider"
	"github.com/olaysco/heydit/internal/skills"
)

const (
	dim    = "\033[2m"
	bold   = "\033[1m"
	cyan   = "\033[36m"
	green  = "\033[32m"
	yellow = "\033[33m"
	red    = "\033[31m"
	reset  = "\033[0m"
)

// Session wires one project + config + agent together for the REPL, one-shot
// mode, and the UI server alike.
type Session struct {
	Cfg     *config.Config
	Project *media.Project
	Agent   *agent.Agent
}

func NewSession(projectDir string) (*Session, error) {
	cfg, err := config.Load(projectDir)
	if err != nil {
		return nil, err
	}
	proj, err := media.LoadProject(projectDir)
	if err != nil {
		return nil, err
	}
	s := &Session{Cfg: cfg, Project: proj}
	if err := s.rebuildAgent(); err != nil {
		// A missing API key shouldn't block the REPL from starting; commands
		// like /model and /config are exactly how the user fixes it.
		fmt.Fprintf(os.Stderr, "%s! %v%s\n", yellow, err, reset)
	}
	return s, nil
}

// rebuildAgent recreates the agent after a model/config switch, preserving
// conversation history so switching models mid-session keeps context.
func (s *Session) rebuildAgent() error {
	p, err := provider.FromConfig(s.Cfg)
	if err != nil {
		return err
	}
	sk := skills.Load(s.Cfg, s.Project.Dir)
	fresh := agent.New(s.Cfg, p, s.Project, sk)
	if s.Agent != nil {
		fresh.History = s.Agent.History
		fresh.InputTokens = s.Agent.InputTokens
		fresh.OutputTokens = s.Agent.OutputTokens
	}
	s.Agent = fresh
	return nil
}

// Ask runs one request through the agent with terminal progress rendering.
func (s *Session) Ask(ctx context.Context, msg string) error {
	if s.Agent == nil {
		if err := s.rebuildAgent(); err != nil {
			return fmt.Errorf("no usable model: %w (use /model or set the API key)", err)
		}
	}
	reply, err := s.Agent.Run(ctx, msg, RenderEvent)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s%s%s\n", bold, reply, reset)
	return nil
}

// RenderEvent prints tool progress lines like:
//
//	⚙ trim {"start":0,"end":5}
//	✓ trim — trimmed to 0.0s–5.0s (450ms)
func RenderEvent(e agent.Event) {
	switch e.Kind {
	case "tool_start":
		args := e.Args
		if args == "{}" || args == "" {
			args = ""
		}
		fmt.Printf("%s⚙ %s%s %s%s\n", cyan, e.Tool, reset, dim+args, reset)
	case "tool_end":
		if e.Err != "" {
			fmt.Printf("%s✗ %s — %s%s\n", red, e.Tool, e.Err, reset)
			return
		}
		out := ""
		if e.Output != "" {
			out = " → " + filepath.Base(e.Output)
		}
		fmt.Printf("%s✓ %s%s — %s%s (%s)%s\n", green, e.Tool, reset, e.Summary, out, e.Duration, reset)
	}
}

// Repl runs the interactive loop.
func (s *Session) Repl(ctx context.Context) error {
	s.banner()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Printf("\n%s❯%s ", cyan, reset)
		if !scanner.Scan() {
			fmt.Println()
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if quit := s.slash(ctx, line); quit {
				return nil
			}
			continue
		}
		if err := s.Ask(ctx, line); err != nil {
			fmt.Printf("%s✗ %v%s\n", red, err, reset)
		}
	}
}

func (s *Session) banner() {
	fmt.Printf("%s%sHeydit%s — agentic video editor\n", bold, cyan, reset)
	fmt.Printf("%smodel: %s/%s · project: %s%s\n", dim, s.Cfg.Model.Provider, s.Cfg.Model.ID, s.Project.Dir, reset)
	if !media.Available() {
		fmt.Printf("%s! ffmpeg not found on PATH — install it before editing%s\n", yellow, reset)
	}
	if s.Project.Current != "" {
		fmt.Printf("%scurrent: %s%s\n", dim, filepath.Base(s.Project.Current), reset)
	} else {
		fmt.Printf("%sno video yet — `add <path>` or /help%s\n", dim, reset)
	}
	fmt.Printf("%stype a request, or /help for commands%s\n", dim, reset)
}

func (s *Session) slash(ctx context.Context, line string) (quit bool) {
	cmd, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "help":
		fmt.Print(`  add <path>          add a source video (also works without the slash)
  /model [spec]       show or switch model: /model kimi/kimi-k3, /model anthropic
  /models             list provider presets
  /config [k [v]]     show config, get a key, or set key value (saved globally)
  /ops                show the edit stack
  /undo               undo the last edit
  /skills             list skills; /skill <name> shows one
  /cost               session token usage
  /export [path]      export CURRENT
  /quit               exit
`)
	case "add":
		s.cmdAdd(ctx, arg)
	case "model":
		if arg == "" {
			fmt.Printf("  %s/%s\n", s.Cfg.Model.Provider, s.Cfg.Model.ID)
			break
		}
		if err := s.Cfg.UseModel(arg); err != nil {
			fmt.Printf("%s✗ %v%s\n", red, err, reset)
			break
		}
		if err := config.SaveGlobal(s.Cfg); err != nil {
			fmt.Printf("%s! config not saved: %v%s\n", yellow, err, reset)
		}
		if err := s.rebuildAgent(); err != nil {
			fmt.Printf("%s! switched, but: %v%s\n", yellow, err, reset)
		} else {
			fmt.Printf("%s✓ now using %s/%s%s\n", green, s.Cfg.Model.Provider, s.Cfg.Model.ID, reset)
		}
	case "models":
		for _, name := range config.PresetNames() {
			p := config.Presets[name]
			fmt.Printf("  %-11s %s (default: %s, key: %s)\n", name, p.Note, p.DefaultModel, orNone(p.KeyEnv))
		}
	case "config":
		s.cmdConfig(arg)
	case "ops":
		if len(s.Project.Ops) == 0 {
			fmt.Println("  no edits yet")
		}
		for _, op := range s.Project.Ops {
			fmt.Printf("  %2d. %-16s %s\n", op.Seq, op.Tool, op.Summary)
		}
	case "undo":
		op, err := s.Project.Undo()
		if err != nil {
			fmt.Printf("%s✗ %v%s\n", red, err, reset)
			break
		}
		fmt.Printf("%s✓ undid %s — current is now %s%s\n", green, op.Tool, filepath.Base(s.Project.Current), reset)
	case "skills":
		for _, sk := range skills.Load(s.Cfg, s.Project.Dir).All() {
			fmt.Printf("  %-15s %s %s(%s)%s\n", sk.Name, sk.Description, dim, sk.Source, reset)
		}
	case "skill":
		if sk, ok := skills.Load(s.Cfg, s.Project.Dir).Get(arg); ok {
			fmt.Println(sk.Body)
		} else {
			fmt.Printf("%s✗ no skill %q%s\n", red, arg, reset)
		}
	case "cost":
		if s.Agent != nil {
			fmt.Println("  " + s.Agent.CostLine())
		}
	case "export":
		msg := "export the current video"
		if arg != "" {
			msg += " to " + arg
		}
		if err := s.Ask(ctx, msg); err != nil {
			fmt.Printf("%s✗ %v%s\n", red, err, reset)
		}
	case "quit", "exit", "q":
		return true
	default:
		fmt.Printf("%s✗ unknown command /%s (try /help)%s\n", red, cmd, reset)
	}
	return false
}

func (s *Session) cmdAdd(ctx context.Context, path string) {
	if path == "" {
		fmt.Printf("%s✗ usage: add <path-to-video>%s\n", red, reset)
		return
	}
	a, err := s.Project.AddAsset(ctx, path)
	if err != nil {
		fmt.Printf("%s✗ %v%s\n", red, err, reset)
		return
	}
	fmt.Printf("%s✓ %s added as %s (%s)%s\n", green, filepath.Base(a.Path), a.ID, a.Info.Compact(), reset)
}

func (s *Session) cmdConfig(arg string) {
	key, val, hasVal := strings.Cut(arg, " ")
	switch {
	case arg == "":
		fmt.Print(indent(s.Cfg.Dump()))
	case !hasVal:
		v, err := s.Cfg.Get(key)
		if err != nil {
			fmt.Printf("%s✗ %v%s\n", red, err, reset)
			return
		}
		fmt.Printf("  %s = %s\n", key, v)
	default:
		if err := s.Cfg.Set(key, strings.TrimSpace(val)); err != nil {
			fmt.Printf("%s✗ %v%s\n", red, err, reset)
			return
		}
		if err := config.SaveGlobal(s.Cfg); err != nil {
			fmt.Printf("%s! not saved: %v%s\n", yellow, err, reset)
		}
		if err := s.rebuildAgent(); err != nil {
			fmt.Printf("%s! applied, but: %v%s\n", yellow, err, reset)
		} else {
			fmt.Printf("%s✓ %s = %s%s\n", green, key, val, reset)
		}
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return "  " + strings.Join(lines, "\n  ") + "\n"
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
