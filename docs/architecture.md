# ìtàn — architecture and the road to "wow"

State of the system as of 2026-08, the honest comparison against production
systems, and the agreed build order. This is the working memory for
architecture decisions — update it when direction changes.

## Overall architecture (`*` = planned, not yet built)

```
                                   USERS
     CLI (itan · -p · -c)    │    web UI / native app    │    voice (hands-free)
             └───────────────┴──────────┬────────────────┴───────────────┘
                                        ▼
┌────────────────────────  CONTROL PLANE · the harness  ────────────────────────┐
│  projects: folder = session (ledger · conversation · checkpoints · revert)    │
│  static cached prompt + <project-state> ledger deltas · compaction            │
│  permission gate (auto/ask/plan · bypass-immune tier · deny-with-feedback)    │
│  defenses: doom loop · repeat-failure block · stall nudge · budget escalation │
│  skills: triggered markdown playbooks     *6 asset/template packs             │
│  providers: anthropic ⇄ openai-compatible (claude ⇄ kimi ⇄ local)             │
│  vision router (model.vision) · reasoning-model handling (thinking, caps)     │
│                                                                               │
│  *3 SCENE PIPELINE:  storyboard spec ─→ per-scene workers ─→ critic/QA pass   │
│                      (plan)             (compose→look→revise)  (assemble)     │
└──────────────────────────────────┬────────────────────────────────────────────┘
                                   ▼  tool calls — every mutation ledger-committed
┌─────────────────────────────  TOOL PLANES  ───────────────────────────────────┐
│                                                                               │
│  MEDIA · hands              GRAPHICS · pen               PERCEPTION · eyes    │
│  trim · concat+xfade        compose: HTML/CSS/GSAP       probe                │
│  crop · expand · speed      → seek-stepped Chromium      view_frames (detail) │
│  regions (blur/pixelate/    → frames → mp4               *7 view_strip:       │
│  zoom) · overlay_video      embedded fonts · charset     scene-detected       │
│  overlay_text (+browser     snapshot → transparent PNG   contact sheet        │
│  fallback) · render         *2 GEN ASSETS: image-gen     (structure/pacing)   │
│  export (stream-copy)       tool for backdrops/heroes                         │
│                             (cloud · permission-gated)   WEB                  │
│  AUDIO · voice                                           fetch_page           │
│  tts (kokoro/elevenlabs)    *1 SOUND DESIGN              capture_page         │
│  stt (whisper, auto-        music bed · auto-duck                             │
│  install) · mix/replace     under voice · SFX vocabulary                      │
└──────────────────────────────────┬────────────────────────────────────────────┘
                                   ▼
┌───────────────────────  STATE · per project folder  ──────────────────────────┐
│  .itan/  project.json (ledger)  ·  session.json (conversation+checkpoints)    │
│          out/ (immutable numbered renders + scene HTML)  ·  uploads/          │
└───────────────────────────────────────────────────────────────────────────────┘

  Build order: *1 sound → *3 scene pipeline → *2 gen assets → *6 packs · *7 fits
  inside *3 as the critic's storyboard view but ships standalone first.
```

## The four planes

1. **Control plane (harness)** — `internal/agent`, `provider`, `permission`,
   `skills`. Single-agent tool loop: cache-stable static system prompt,
   project state as a compact ledger injected as deltas, skills as
   trigger-injected playbooks, deterministic compaction, permission gate
   with plan mode, checkpoints/revert, doom-loop + stall + reasoning-model
   defenses, multimodal tool results (view_frames), Anthropic + OpenAI
   dialects, optional vision router (`model.vision`).
2. **Media plane (hands)** — `internal/tools`, `media`. ~24 deterministic
   ffmpeg operations over an immutable numbered-render ledger; every edit is
   a real file, undo/revert are pointer moves.
3. **Graphics plane (pen)** — `internal/canvas`. LLM-authored HTML →
   seek-stepped Chromium frames → ffmpeg. Embedded fonts + GSAP 3/SplitText,
   2× supersampling, charset guarantee. Determinism locked by golden tests.
4. **Voice plane** — Kokoro TTS / Whisper STT behind OpenAI-compatible
   interfaces; STT models auto-install on first use.

## Comparison against production systems

- **Agent harnesses (Claude Code, opencode):** competitive — it is their
  architecture on purpose. Missing piece: subagent orchestration.
- **Generative video (Runway/Pika/Sora/Veo):** their wow is generative
  pixels; we generate none — we transform footage and render graphics.
- **Media understanding (Descript/Opus Clip):** their wow is
  transcript-anchored editing; our transcribe is a tool, not a structure.
- **Template systems (HeyGen/InVideo/CapCut):** their wow is asset richness
  (music, templates, brand kits); we are offline and asset-poor by design.
- **Programmatic video (Remotion + LLM shops):** the closest cousin. Their
  lesson, learned independently: raw one-shot LLM markup has high variance —
  production quality comes from component libraries and multi-pass
  refine loops.

**Verdict:** the harness mechanics are near state of the art for a
single-agent CLI harness. The output ceiling is the weak layer: one LLM
writing HTML in one pass, over silence.

## Gap ranking (wow per effort) and agreed order

Build order agreed: **1 → 3 → 2**, then 4/5/6.

1. **Sound design** — music bed + auto-ducking under voice + SFX
   vocabulary. Silent video reads as demo; sounded video reads as product.
2. **Generative visual assets** — pluggable image-gen tool (cloud, behind
   the permission gate) for backdrops/hero imagery inside compositions.
3. **Orchestrated scene pipeline** — storyboard spec → per-scene worker
   loop (compose → look → revise until pass) → assembly → final QA pass.
   Separates planner, workers, critic — the harness-architecture upgrade.
4. **Transcript-anchored editing** — the transcript as a first-class
   editable structure ("cut the um's").
5. **Generative b-roll** — optional cloud video-gen tools (Veo/Runway/
   Kling) behind the permission gate.
6. **Template/asset packs** — skills that carry assets (component CSS,
   layout systems) to constrain generation variance.

## Vision-loop improvements under consideration

- **Filmstrip / contact-sheet frames**: instead of N separate images,
  tile thumbnails into one labeled grid (optionally one frame per detected
  scene cut) so temporal order is spatial and reference is unambiguous;
  keep single full-res frames for detail inspection. Hybrid of coverage
  (strip) + detail (frames).
