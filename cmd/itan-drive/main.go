// Command itan-drive runs a list of tool calls against the real registry with
// no model in the loop. It is how the toolset gets dogfooded: the calls are
// chosen by hand, but everything under them — permission, ledger, renderer,
// ffmpeg — is exactly what the agent uses.
//
//	itan-drive <project-dir> <calls.json>
//
// where calls.json is [{"tool":"compose","args":{...}}, ...].
//
// Every bug worth finding in this project was found by running the real
// pipeline end to end; a test that renders 320x240 for half a second never
// meets a five-minute timeout or a two-hour render.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/tools"
	"github.com/olaysco/itan/internal/voice"
)

type call struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: itan-drive <project-dir> <calls.json>")
		os.Exit(2)
	}
	dir, script := os.Args[1], os.Args[2]

	raw, err := os.ReadFile(script)
	if err != nil {
		fatal(err)
	}
	var calls []call
	if err := json.Unmarshal(raw, &calls); err != nil {
		fatal(err)
	}

	proj, err := media.LoadProject(dir)
	if err != nil {
		fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		fatal(err)
	}
	c := &tools.Ctx{Context: context.Background(), Project: proj, Config: cfg,
		TTS: voice.TTSFromConfig(cfg), STT: voice.STTFromConfig(cfg)}
	r := tools.NewRegistry()

	failed := 0
	for _, cl := range calls {
		t0 := time.Now()
		fmt.Printf("→ %s\n", cl.Tool)
		res := r.Execute(c, cl.Tool, cl.Args)
		el := time.Since(t0).Round(100 * time.Millisecond)
		if res.Err != nil {
			fmt.Printf("  ✕ %v   (%s)\n", res.Err, el)
			failed++
			continue // keep going: one broken step should not hide the rest
		}
		fmt.Printf("  ✓ %s   (%s)\n", res.Summary, el)
		if len(res.Frames) > 0 {
			fmt.Printf("    [%d frames returned for the model to look at]\n", len(res.Frames))
		}
	}
	if failed > 0 {
		fmt.Printf("\n%d of %d calls failed\n", failed, len(calls))
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "itan-drive:", err)
	os.Exit(1)
}
