// Command launch renders Itan's own product launch video with Itan's own
// tool registry — the same compose → concat → export calls the agent makes,
// scripted here without a model in the loop. Dogfood and demo in one:
//
//	go run ./examples/launch
//
// needs ffmpeg and a Chromium-family browser (set ITAN_BROWSER to override
// discovery). Output: ./launch-build/itan-launch.mp4
//
// The scenes follow the built-in motion-design skill: Bricolage Grotesque
// display + IBM Plex Mono labels (embedded in the render engine), one
// easing pair, staggered mask reveals, left-anchored grids, a rest beat
// before every cut.
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

const style = `
  :root{--bg:#0A0A0C;--panel:#131316;--panel2:#1A1A1E;--line:#2C2C32;--t1:#F5F5F7;--t2:#B9B9C0;--t3:#8E8E93;
        --indigo:#0A84FF;--orange:#FF9F0A;--ok:#32D74B;
        --out:cubic-bezier(0.2,0.8,0.2,1);--spring:cubic-bezier(0.34,1.56,0.64,1)}
  *{box-sizing:border-box;margin:0}
  body{height:100vh;background:
       radial-gradient(760px 400px at 70% -10%,rgba(10,132,255,.10),transparent 62%),var(--bg);
       color:var(--t1);font-family:'Bricolage Grotesque',sans-serif;overflow:hidden;
       display:flex;flex-direction:column;justify-content:center;padding:0 96px}
  .mono{font-family:'IBM Plex Mono',monospace}
  .label{font-family:'IBM Plex Mono',monospace;font-size:13px;letter-spacing:.14em;color:var(--t3);
         text-transform:uppercase}
  .mask{overflow:hidden;display:block}
  .mask>span{display:block;transform:translateY(110%);animation:rise .7s var(--out) forwards}
  @keyframes rise{to{transform:none}}
  @keyframes up{from{opacity:0;transform:translateY(22px)}to{opacity:1;transform:none}}
  @keyframes in{from{opacity:0}to{opacity:1}}
  .bars i{display:block;background:var(--orange);border-radius:3px;height:6px;margin-bottom:6px;
          transform:scaleX(0);transform-origin:left;animation:bar .6s var(--out) forwards}
  .bars i:nth-child(1){width:56px}.bars i:nth-child(2){width:36px;opacity:.75;animation-delay:.1s}
  .bars i:nth-child(3){width:18px;opacity:.5;animation-delay:.2s}
  @keyframes bar{to{transform:none}}
`

var scenes = []struct {
	dur  float64
	html string
}{
	// 1 · hook — label, bars, per-letter wordmark reveal, rest beat
	{3.2, `<!DOCTYPE html><html><head><style>` + style + `
  .label{animation:in .5s .15s both}
  .bars{margin:26px 0 18px}
  h1{font-size:170px;font-weight:800;letter-spacing:-.03em;line-height:1;display:flex}
  h1 .mask>span{animation-duration:.6s}
  h1 .mask:nth-child(1)>span{animation-delay:.5s}
  h1 .mask:nth-child(2)>span{animation-delay:.58s}
  h1 .mask:nth-child(3)>span{animation-delay:.66s}
  h1 .mask:nth-child(4)>span{animation-delay:.74s}
  p{font-size:24px;color:var(--t3);margin-top:22px;animation:up .7s var(--out) 1.25s both}
  </style></head><body>
  <span class="label">a new kind of editor</span>
  <div class="bars"><i></i><i></i><i></i></div>
  <h1><span class="mask"><span>ì</span></span><span class="mask"><span>t</span></span><span class="mask"><span>à</span></span><span class="mask"><span>n</span></span></h1>
  <p>every video is a story.</p>
  </body></html>`},

	// 2 · the thesis — kinetic headline left, live conversation right
	{6.0, `<!DOCTYPE html><html><head><style>` + style + `
  body{flex-direction:row;align-items:center;gap:70px}
  .left{width:420px;flex:none}
  .left .label{animation:in .5s .1s both;display:block;margin-bottom:20px}
  h2{font-size:64px;font-weight:750;letter-spacing:-.02em;line-height:1.08}
  h2 .mask>span{animation-delay:calc(.35s + var(--i)*.09s)}
  .right{flex:1;display:flex;flex-direction:column;gap:12px;max-width:560px}
  .u{align-self:flex-end;background:var(--panel2);border:1px solid var(--line);border-radius:14px 14px 4px 14px;
     padding:12px 18px;font-size:20px;opacity:0;animation:up .55s var(--out) 1.5s forwards}
  .trace{background:var(--panel);border:1px solid var(--line);border-radius:12px;font-size:15.5px;
     opacity:0;animation:up .55s var(--out) 2.2s forwards}
  .trace .hd{padding:10px 16px;font-size:11px;letter-spacing:.14em;color:var(--t3)}
  .trace .r{padding:10px 16px;border-top:1px solid var(--line);opacity:0;transform:translateX(-14px)}
  .trace .r{animation:row .45s var(--out) forwards}
  .trace .r:nth-child(2){animation-delay:2.7s}.trace .r:nth-child(3){animation-delay:3.3s}.trace .r:nth-child(4){animation-delay:3.9s}
  @keyframes row{to{opacity:1;transform:none}}
  .ok{color:var(--ok)}.nm{color:var(--indigo);font-weight:700}.mut{color:var(--t3)}
  .a{align-self:flex-start;color:var(--t2);font-size:18px;max-width:440px;line-height:1.5;
     opacity:0;animation:up .55s var(--out) 4.6s forwards}
  </style></head><body>
  <div class="left"><span class="label">no timeline. no panels.</span>
    <h2>
      <span class="mask" style="--i:0"><span>Editing is</span></span>
      <span class="mask" style="--i:1"><span>a conversation</span></span>
      <span class="mask" style="--i:2"><span>now.</span></span>
    </h2></div>
  <div class="right mono">
    <div class="u">make this a tiktok</div>
    <div class="trace"><div class="hd">TOOL TRACE</div>
      <div class="r"><span class="ok">✓</span> <span class="nm">trim</span> <span class="mut">2.4s–31.0s → 002-trim.mp4</span></div>
      <div class="r"><span class="ok">✓</span> <span class="nm">crop</span> <span class="mut">9:16 · face-tracked → 608×1080</span></div>
      <div class="r"><span class="ok">✓</span> <span class="nm">captions</span> <span class="mut">42 lines burned in</span></div>
    </div>
    <div class="a">Done — 608×1080 · 28.6s. Every step is a numbered render you can rewind.</div>
  </div>
  </body></html>`},

	// 3 · the pillars — numbered list rows, not centered cards
	{5.5, `<!DOCTYPE html><html><head><style>` + style + `
  h2{font-size:58px;font-weight:750;letter-spacing:-.02em;margin-bottom:52px}
  h2 .mask>span{animation-delay:calc(.25s + var(--i)*.09s)}
  .row{display:flex;align-items:baseline;gap:34px;padding:24px 0;border-top:1px solid var(--line);
       opacity:0;transform:translateX(-26px);animation:slide .6s var(--out) forwards}
  .row:nth-of-type(1){animation-delay:1.1s}.row:nth-of-type(2){animation-delay:1.5s}.row:nth-of-type(3){animation-delay:1.9s}
  @keyframes slide{to{opacity:1;transform:none}}
  .num{font-family:'IBM Plex Mono',monospace;font-size:15px;color:var(--orange);flex:none;width:36px}
  .row b{font-size:29px;font-weight:650;width:360px;flex:none;letter-spacing:-.01em}
  .row span{font-size:18px;color:var(--t3);line-height:1.45}
  </style></head><body>
  <h2><span class="mask" style="--i:0"><span>One binary. Your models.</span></span>
      <span class="mask" style="--i:1"><span>Your machine.</span></span></h2>
  <div class="row"><span class="num">01</span><b>Swap brains mid-edit</b><span>Claude ↔ Kimi ↔ local Ollama — context survives the switch.</span></div>
  <div class="row"><span class="num">02</span><b>Speak the edit</b><span>Whisper + Kokoro run locally. Nothing uploads, ever.</span></div>
  <div class="row"><span class="num">03</span><b>Rewind anything</b><span>Every edit is a chapter of the session's story.</span></div>
  </body></html>`},

	// 4 · the meta beat — typewriter, then the reveal
	{4.6, `<!DOCTYPE html><html><head><style>` + style + `
  .label{animation:in .5s .2s both;display:block;margin-bottom:30px}
  .type{font-family:'IBM Plex Mono',monospace;font-size:44px;font-weight:700;white-space:nowrap;
        overflow:hidden;width:0;border-right:3px solid var(--indigo);
        animation:type 1.7s steps(34) .7s forwards,caret .9s step-end infinite}
  @keyframes type{to{width:34ch}}
  @keyframes caret{0%,55%{border-color:var(--indigo)}56%,100%{border-color:transparent}}
  .type i{font-style:normal;color:var(--indigo)}.type b{color:var(--orange)}
  p{font-size:21px;color:var(--t3);margin-top:34px;animation:up .6s var(--out) 2.8s both}
  </style></head><body>
  <span class="label">this video rendered itself</span>
  <div class="type">$ itan <i>compose</i> → <i>concat</i> → <b>export</b></div>
  <p>HTML in. Deterministic frames out. No cloud, no Node, no render fees.</p>
  </body></html>`},

	// 5 · close — quiet, then the ask
	{3.6, `<!DOCTYPE html><html><head><style>` + style + `
  .bars{margin-bottom:20px}
  h1{font-size:110px;font-weight:800;letter-spacing:-.03em;line-height:1}
  h1 .mask>span{animation-delay:.35s}
  p{font-size:34px;font-weight:650;color:var(--orange);margin-top:16px}
  p .mask>span{animation-delay:.75s}
  small{font-family:'IBM Plex Mono',monospace;font-size:16px;color:var(--t3);margin-top:40px;
        animation:in .8s 1.5s both;display:block}
  </style></head><body>
  <div class="bars"><i></i><i></i><i></i></div>
  <h1><span class="mask"><span>ìtàn</span></span></h1>
  <p><span class="mask"><span>tell it the story.</span></span></p>
  <small>github.com/olaysco/itan</small>
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
	exec("concat", map[string]any{"inputs": ids, "transition": "fade", "transition_duration": 0.5})
	exec("export", map[string]any{"path": "itan-launch.mp4"})
	fmt.Printf("\nrendered %d scenes in %s → %s/itan-launch.mp4\n", len(scenes), time.Since(start).Round(time.Second), dir)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "launch:", err)
	os.Exit(1)
}
