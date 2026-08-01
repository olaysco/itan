// Command launch renders Itan's own product launch video with Itan's own
// tool registry — the same compose → concat → export calls the agent makes,
// scripted here without a model in the loop. Dogfood and demo in one:
//
//	go run ./examples/launch
//
// needs ffmpeg and a Chromium-family browser (set ITAN_BROWSER to override
// discovery). Output: ./launch-build/itan-launch.mp4
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
)

// Shared look: the UI's dark design tokens, system fonts (renders are
// offline, so no webfonts).
const style = `
  :root{--bg:#000;--fill:#1A1A1E;--line:#2C2C32;--t1:#F5F5F7;--t2:#B9B9C0;--t3:#8E8E93;
        --indigo:#0A84FF;--orange:#FF9F0A;--ok:#32D74B}
  *{box-sizing:border-box}
  body{margin:0;height:100vh;background:
       radial-gradient(700px 340px at 66% -12%,rgba(10,132,255,.14),transparent 60%),
       radial-gradient(520px 300px at 8% 110%,rgba(255,159,10,.12),transparent 60%),var(--bg);
       color:var(--t1);font-family:system-ui,sans-serif;display:flex;flex-direction:column;
       align-items:center;justify-content:center;overflow:hidden}
  .mono{font-family:ui-monospace,monospace}
  .bars i{display:block;background:var(--orange);border-radius:4px;height:8px;margin-bottom:7px;
          animation:bar .7s cubic-bezier(.2,.8,.2,1) both}
  .bars i:nth-child(1){width:64px}.bars i:nth-child(2){width:42px;opacity:.75;animation-delay:.15s}
  .bars i:nth-child(3){width:22px;opacity:.5;animation-delay:.3s}
  @keyframes bar{from{transform:translateX(-40px);opacity:0}to{opacity:inherit}}
  @keyframes up{from{opacity:0;transform:translateY(26px)}to{opacity:1;transform:none}}
  @keyframes in{from{opacity:0}to{opacity:1}}
  .up{animation:up .8s cubic-bezier(.2,.8,.2,1) both}
`

var scenes = []struct {
	dur  float64
	html string
}{
	{4.0, `<!DOCTYPE html><html><head><style>` + style + `
  h1{font-size:130px;font-weight:800;letter-spacing:-.02em;margin:26px 0 10px;animation:up .8s .5s both}
  p{font-size:26px;color:var(--t3);margin:0;animation:up .8s 1.4s both}
  </style></head><body>
  <div class="bars"><i></i><i></i><i></i></div>
  <h1>ìtàn</h1>
  <p>every video is a story.</p>
  </body></html>`},

	{6.0, `<!DOCTYPE html><html><head><style>` + style + `
  body{justify-content:flex-start;padding-top:70px}
  h2{font-size:44px;font-weight:700;animation:up .8s both;margin:0 0 40px}
  .chat{width:640px;display:flex;flex-direction:column;gap:14px}
  .u{align-self:flex-end;background:#2E2E34;border-radius:14px 14px 4px 14px;padding:13px 18px;font-size:22px}
  .trace{background:var(--fill);border-radius:12px;padding:6px 0;font-size:17px}
  .trace div{padding:9px 18px;border-top:1px solid var(--line)}
  .trace div:first-child{border-top:none;color:var(--t3);font-size:12px;letter-spacing:.12em}
  .ok{color:var(--ok)}.nm{color:var(--indigo);font-weight:700}.mut{color:var(--t3)}
  .a{align-self:flex-start;color:var(--t2);font-size:21px}
  </style></head><body>
  <h2>Editing is a conversation now.</h2>
  <div class="chat">
    <div class="u up" data-start="0.8">make this a tiktok</div>
    <div class="trace mono up" data-start="1.7">
      <div>TOOL TRACE</div>
      <div class="up" data-start="1.9"><span class="ok">✓</span> <span class="nm">trim</span> <span class="mut">2.4s–31.0s → 002-trim.mp4</span></div>
      <div class="up" data-start="2.7"><span class="ok">✓</span> <span class="nm">crop</span> <span class="mut">9:16 → 608×1080</span></div>
      <div class="up" data-start="3.5"><span class="ok">✓</span> <span class="nm">captions</span> <span class="mut">42 lines burned in</span></div>
    </div>
    <div class="a up" data-start="4.5">Done — 608×1080 · 28.6s. Every step is a numbered render you can rewind.</div>
  </div>
  </body></html>`},

	{6.0, `<!DOCTYPE html><html><head><style>` + style + `
  h2{font-size:44px;font-weight:700;animation:up .8s both;margin:0 0 46px}
  .row{display:flex;gap:22px}
  .card{width:330px;background:var(--fill);border:1px solid var(--line);border-radius:16px;padding:26px}
  .card b{font-size:23px;display:block;margin-bottom:10px}
  .card span{font-size:17px;color:var(--t2);line-height:1.5}
  .tag{display:inline-block;font-size:13px;color:var(--orange);letter-spacing:.1em;margin-bottom:12px}
  </style></head><body>
  <h2>One binary. Your models. Your machine.</h2>
  <div class="row">
    <div class="card up mono" data-start="0.9"><span class="tag">BRAIN</span><b>Swap models mid-edit</b><span>Claude ↔ Kimi ↔ local Ollama — context survives the switch.</span></div>
    <div class="card up mono" data-start="1.7"><span class="tag">VOICE</span><b>Speak the edit</b><span>Whisper + Kokoro run on this machine. Nothing uploads.</span></div>
    <div class="card up mono" data-start="2.5"><span class="tag">STORY</span><b>Rewind anything</b><span>Every edit is a chapter — undo, revert, replay the session.</span></div>
  </div>
  </body></html>`},

	{5.0, `<!DOCTYPE html><html><head><style>` + style + `
  p{font-size:27px;color:var(--t2);margin:0 0 30px;animation:up .8s .3s both}
  code{font-size:52px;font-weight:700;animation:up .8s 1.2s both;display:block}
  code i{font-style:normal;color:var(--indigo)}
  code b{color:var(--orange)}
  small{font-size:20px;color:var(--t3);margin-top:30px;animation:up .8s 2.4s both;display:block}
  </style></head><body class="mono">
  <p>This launch video was rendered by ìtàn itself.</p>
  <code><i>compose</i> → <i>concat</i> → <b>export</b></code>
  <small>HTML in. Deterministic frames out. No cloud, no Node, no render fees.</small>
  </body></html>`},

	{4.0, `<!DOCTYPE html><html><head><style>` + style + `
  h1{font-size:96px;font-weight:800;letter-spacing:-.02em;margin:24px 0 12px;animation:up .8s .2s both}
  p{font-size:30px;color:var(--orange);margin:0;font-weight:600;animation:up .8s 1s both}
  small{font-size:19px;color:var(--t3);margin-top:26px;animation:in 1s 1.9s both}
  </style></head><body>
  <div class="bars"><i></i><i></i><i></i></div>
  <h1>ìtàn</h1>
  <p>tell it the story.</p>
  <small class="mono">github.com/olaysco/itan</small>
  </body></html>`},
}

func main() {
	start := time.Now()
	dir := "launch-build"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	proj, err := media.LoadProject(dir)
	if err != nil {
		fail(err)
	}
	reg := tools.NewRegistry()
	tctx := &tools.Ctx{Context: context.Background(), Project: proj, Config: config.Default()}

	exec := func(tool string, args map[string]any) tools.Result {
		raw, _ := json.Marshal(args)
		res := reg.Execute(tctx, tool, raw)
		if res.Err != nil {
			fail(fmt.Errorf("%s: %w", tool, res.Err))
		}
		fmt.Printf("✓ %s — %s\n", tool, res.Summary)
		return res
	}

	var ids []any
	for i, sc := range scenes {
		res := exec("compose", map[string]any{
			"html": sc.html, "duration": sc.dur,
			"width": 1280, "height": 720, "fps": 24,
		})
		ids = append(ids, res.Data["asset"])
		fmt.Printf("  scene %d/%d done\n", i+1, len(scenes))
	}
	exec("concat", map[string]any{"inputs": ids, "transition": "fade", "transition_duration": 0.6})
	exec("export", map[string]any{"path": "itan-launch.mp4"})
	fmt.Printf("\nrendered %d scenes in %s → %s/itan-launch.mp4\n", len(scenes), time.Since(start).Round(time.Second), dir)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "launch:", err)
	os.Exit(1)
}
