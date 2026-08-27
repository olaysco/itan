// Command itan is the agentic video editor CLI.
//
// Usage:
//
//	itan                       interactive session in the current directory
//	itan -p "make it a reel"   one-shot request
//	itan add clip.mp4          add a source video to the project
//	itan ui [--addr host:port] desktop editing screen in the browser
//	itan app [--addr host:port] editing screen in a native app window
//	itan model use kimi/kimi-k3
//	itan models | config | skills | doctor | version
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/olaysco/itan/internal/browser"
	"github.com/olaysco/itan/internal/cli"
	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/provider"
	"github.com/olaysco/itan/internal/server"
	"github.com/olaysco/itan/internal/skills"
)

// version is stamped at build time by the release pipeline:
//
//	-ldflags "-X main.version=$(git describe --tags)"
//
// A hardcoded constant would ship every release claiming to be the one it was
// last edited on, which makes a bug report impossible to place.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "itan:", err)
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

	// `itan -c` / `--continue` resumes the saved conversation; it may be
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
			return fmt.Errorf("usage: itan -p \"request\"")
		}
		session, err := cli.NewSession(dir, resume)
		if err != nil {
			return err
		}
		return session.Ask(ctx, strings.Join(args[1:], " "))

	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: itan add <video...>")
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
		session, err := cli.NewSession(uiProjectDir(dir), resume)
		if err != nil {
			return err
		}
		go openBrowser("http://" + addr)
		return server.New(session).Listen(ctx, addr)

	case "app":
		addr := "127.0.0.1:4141"
		for i := 1; i < len(args)-1; i++ {
			if args[i] == "--addr" {
				addr = args[i+1]
			}
		}
		session, err := cli.NewSession(uiProjectDir(dir), resume)
		if err != nil {
			return err
		}
		go openAppWindow("http://" + addr)
		return server.New(session).Listen(ctx, addr)

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
		fmt.Println("itan", version)
		return nil

	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil

	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

const usage = `itan — agentic AI video editor

  itan                        interactive session (current dir = project)
  itan -c | --continue        resume the previous conversation
  itan -p "request"           one-shot edit, then exit
  itan add <video...>         register source videos with the project
  itan ui [--addr host:port]  open the desktop editing screen
  itan app [--addr host:port] open the editing screen in a native app window
  itan model use <spec>       switch model (anthropic | kimi/kimi-k3 | ollama/... )
                              route frames to a second model: itan config set model.vision kimi
  itan model                  show the active model
  itan models                 list provider presets
  itan config [get|set|list]  inspect or change configuration
                              stock footage: itan config set media.pixabay_key <key>
  itan skills                 list available skills
  itan doctor                 check ffmpeg, model, and voice endpoints
  itan version                print version

  ITAN_UI=<path/to/index.html>   serve the UI from disk: edit, refresh, no rebuild
`

// uiProjectDir picks the project the UI opens: the working directory when it
// is an initialized project (explicit intent), otherwise the most recently
// used project — so relaunching the app resumes where the user left off
// instead of silently starting a throwaway project in some random cwd.
func uiProjectDir(cwd string) string {
	if _, err := os.Stat(filepath.Join(cwd, ".itan")); err == nil {
		return cwd
	}
	if recents := config.RecentProjects(); len(recents) > 0 {
		fmt.Printf("resuming last project: %s  (cd into a project folder to override)\n", recents[0])
		return recents[0]
	}
	return cwd
}

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
		return fmt.Errorf("usage: itan model [show|use <provider[/model]>]")
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
			return fmt.Errorf("usage: itan config get <key>")
		}
		v, err := cfg.Get(args[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: itan config set <key> <value>")
		}
		if err := cfg.Set(args[1], strings.Join(args[2:], " ")); err != nil {
			return err
		}
		if err := config.SaveGlobal(cfg); err != nil {
			return err
		}
		fmt.Printf("✓ %s = %s\n", args[1], strings.Join(args[2:], " "))
	default:
		return fmt.Errorf("usage: itan config [list|get <key>|set <key> <value>]")
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
	if cfg.Model.Vision == "" {
		if config.CanSee(cfg.Model.Provider, cfg.Model.ID) {
			check("vision", true, "the model can see frames — the agent can check its own work")
		} else {
			check("vision", false, cfg.Model.ID+" cannot accept images, so the agent renders without ever looking. "+
				"Open the model picker and turn on Vision, or `itan config set model.vision <provider/model>`")
		}
	}
	if cfg.Model.Vision != "" {
		if _, vm, verr := provider.VisionFromConfig(cfg); verr != nil {
			check("vision model", false, verr.Error())
		} else {
			check("vision model", true, fmt.Sprintf("%s → %s (turns with frames route here)", cfg.Model.Vision, vm))
		}
	}
	if chrome, berr := browser.Find(); berr == nil {
		note := chrome + " — HTML motion graphics enabled"
		if !browser.Sandboxed(chrome) {
			note += "; sandbox off (this machine denies Chromium one — root, or a distro that blocks unprivileged user namespaces). Set ITAN_SANDBOX=1 to insist."
		}
		check("compose (browser)", true, note)
	} else {
		check("compose (browser)", false, "no Chromium-family browser; compose is disabled until one is installed")
	}
	if media.Available() {
		if media.HasFilter(context.Background(), "drawtext") {
			check("text overlays", true, "ffmpeg drawtext")
		} else {
			check("text overlays", true, "ffmpeg lacks drawtext — captions render via the browser engine")
		}
	}
	if key := cfg.PixabayKey(); key != "" {
		check("stock media", true, "Pixabay key set — find_media can source footage and stills")
	} else {
		check("stock media", false, "no Pixabay key: free at https://pixabay.com/api/docs, then `itan config set media.pixabay_key <key>` (or set PIXABAY_API_KEY). Without it a project can only show what you already have.")
	}
	check("tts", true, fmt.Sprintf("%s @ %s (voice %s)", cfg.Audio.TTS.Provider, cfg.Audio.TTS.BaseURL, cfg.Audio.TTS.Voice))
	check("stt", true, fmt.Sprintf("%s @ %s", cfg.Audio.STT.Provider, cfg.Audio.STT.BaseURL))
	fmt.Println("\nvoice endpoints are only contacted when a request needs them;")
	fmt.Println("run your kokoro/whisper servers locally or switch providers via `itan config set`.")
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
