// Command itan-drive executes a list of tool calls against the real registry
// without a model in the loop — the harness exactly as the agent runs it,
// with the calls chosen by hand. Used to dogfood the toolset.
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
	dir, script := os.Args[1], os.Args[2]
	raw, err := os.ReadFile(script)
	if err != nil {
		panic(err)
	}
	var calls []call
	if err := json.Unmarshal(raw, &calls); err != nil {
		panic(err)
	}

	proj, err := media.LoadProject(dir)
	if err != nil {
		panic(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		panic(err)
	}
	c := &tools.Ctx{Context: context.Background(), Project: proj, Config: cfg,
		TTS: voice.TTSFromConfig(cfg), STT: voice.STTFromConfig(cfg)}
	r := tools.NewRegistry()

	for i, cl := range calls {
		t0 := time.Now()
		fmt.Printf("→ %s\n", cl.Tool)
		res := r.Execute(c, cl.Tool, cl.Args)
		el := time.Since(t0).Round(100 * time.Millisecond)
		if res.Err != nil {
			fmt.Printf("  ✕ %v   (%s)\n", res.Err, el)
			os.Exit(1)
		}
		fmt.Printf("  ✓ %s   (%s)\n", res.Summary, el)
		if len(res.Frames) > 0 {
			fmt.Printf("    [%d frames returned for the model to look at]\n", len(res.Frames))
		}
		_ = i
	}
}
