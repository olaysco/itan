// Command heydit is the agentic video editor CLI.
//
// Usage:
//
//	heydit                       interactive session in the current directory
//	heydit -p "make it a reel"   one-shot request
//	heydit add clip.mp4          add a source video to the project
//	heydit ui [--addr host:port] desktop editing screen in the browser
//	heydit model use kimi/kimi-k3
//	heydit models | config | skills | doctor | version
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/olaysco/heydit/internal/cli"
	"github.com/olaysco/heydit/internal/config"
	"github.com/olaysco/heydit/internal/media"
	"github.com/olaysco/heydit/internal/server"
	"github.com/olaysco/heydit/internal/skills"
)

const version = "0.2.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "heydit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// `heydit -c` / `--continue` resumes the saved conversation; it may be
	// combined with the REPL (default) or -p.
	resume := false
	filtered := args[:0:0]
	for _, a := range args {
		if a == "-c" || a == "--continue" {
			resume = true
			continue
		}
		filtered = append(filtered, a)
	}
	args = filtered

	if len(args) == 0 {
		session, err := cli.NewSession(dir, resume)
		if err != nil {
			return err
		}
		return session.Repl(ctx)
	}

	switch args[0] {
	case "-p", "--print":
		if len(args) < 2 {
			return fmt.Errorf("usage: heydit -p \"request\"")
		}
		session, err := cli.NewSession(dir, resume)
		if err != nil {
			return err
		}
		return session.Ask(ctx, strings.Join(args[1:], " "))

	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: heydit add <video...>")
		}
		proj, err := media.LoadProject(dir)
		if err != nil {
			return err
		}
		for _, path := range args[1:] {
			a, err := proj.AddAsset(ctx, path)
			if err != nil {
				return err
			}
			fmt.Printf("✓ %s added as %s (%s)\n", path, a.ID, a.Info.Compact())
		}
		return nil

	case "ui":
		addr := "127.0.0.1:4141"
		for i := 1; i < len(args)-1; i++ {
			if args[i] == "--addr" {
				addr = args[i+1]
			}
		}
		session, err := cli.NewSession(dir, resume)
		if err != nil {
			return err
		}
		go openBrowser("http://" + addr)
		return server.New(session).Listen(addr)

	case "model":
		return cmdModel(dir, args[1:])

	case "models":
		for _, name := range config.PresetNames() {
			p := config.Presets[name]
			fmt.Printf("%-11s %s (default: %s, key env: %s)\n", name, p.Note, p.DefaultModel, orNone(p.KeyEnv))
		}
		return nil

	case "config":
		return cmdConfig(dir, args[1:])

	case "skills":
		cfg, err := config.Load(dir)
		if err != nil {
			return err
		}
		for _, sk := range skills.Load(cfg, dir).All() {
			fmt.Printf("%-15s %s (%s)\n", sk.Name, sk.Description, sk.Source)
		}
		return nil

	case "doctor":
		return cmdDoctor(dir)

	case "version", "--version", "-v":
		fmt.Println("heydit", version)
		return nil

	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil

	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

const usage = `heydit — agentic AI video editor

  heydit                        interactive session (current dir = project)
  heydit -c | --continue        resume the previous conversation
  heydit -p "request"           one-shot edit, then exit
  heydit add <video...>         register source videos with the project
  heydit ui [--addr host:port]  open the desktop editing screen
  heydit model use <spec>       switch model (anthropic | kimi/kimi-k3 | ollama/... )
  heydit model                  show the active model
  heydit models                 list provider presets
  heydit config [get|set|list]  inspect or change configuration
  heydit skills                 list available skills
  heydit doctor                 check ffmpeg, model, and voice endpoints
  heydit version                print version
`

func cmdModel(dir string, args []string) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "show" {
		fmt.Printf("%s/%s\n", cfg.Model.Provider, cfg.Model.ID)
		return nil
	}
	if args[0] != "use" || len(args) < 2 {
		return fmt.Errorf("usage: heydit model [show|use <provider[/model]>]")
	}
	if err := cfg.UseModel(args[1]); err != nil {
		return err
	}
	if err := config.SaveGlobal(cfg); err != nil {
		return err
	}
	fmt.Printf("✓ now using %s/%s\n", cfg.Model.Provider, cfg.Model.ID)
	return nil
}

func cmdConfig(dir string, args []string) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		fmt.Print(cfg.Dump())
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: heydit config get <key>")
		}
		v, err := cfg.Get(args[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: heydit config set <key> <value>")
		}
		if err := cfg.Set(args[1], strings.Join(args[2:], " ")); err != nil {
			return err
		}
		if err := config.SaveGlobal(cfg); err != nil {
			return err
		}
		fmt.Printf("✓ %s = %s\n", args[1], strings.Join(args[2:], " "))
	default:
		return fmt.Errorf("usage: heydit config [list|get <key>|set <key> <value>]")
	}
	return nil
}

func cmdDoctor(dir string) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	check := func(label string, ok bool, detail string) {
		mark := "✓"
		if !ok {
			mark = "✗"
		}
		fmt.Printf("%s %-22s %s\n", mark, label, detail)
	}
	check("ffmpeg/ffprobe", media.Available(), "required for all rendering")

	kind, baseURL, apiKey, merr := cfg.ResolveModel()
	if merr != nil {
		check("model", false, merr.Error())
	} else {
		detail := fmt.Sprintf("%s/%s via %s (%s)", cfg.Model.Provider, cfg.Model.ID, baseURL, kind)
		ok := apiKey != "" || kind == "openai" && strings.Contains(baseURL, "localhost")
		if apiKey == "" && ok {
			detail += " — keyless local host"
		} else if apiKey == "" {
			detail += " — API KEY MISSING"
		}
		check("model", ok, detail)
	}
	check("tts", true, fmt.Sprintf("%s @ %s (voice %s)", cfg.Audio.TTS.Provider, cfg.Audio.TTS.BaseURL, cfg.Audio.TTS.Voice))
	check("stt", true, fmt.Sprintf("%s @ %s", cfg.Audio.STT.Provider, cfg.Audio.STT.BaseURL))
	fmt.Println("\nvoice endpoints are only contacted when a request needs them;")
	fmt.Println("run your kokoro/whisper servers locally or switch providers via `heydit config set`.")
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
