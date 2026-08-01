# 🎬 Heydit

**An agentic AI video editor with a strong, token-efficient harness — Claude Code, but for video.**

You talk; Heydit picks the tools, runs real ffmpeg renders, and every edit lands in an
undoable ledger. One Go binary gives you the CLI, the agent harness, and a desktop
editing screen. Every model — the brain, the voice, the ears — is configurable.

```
❯ make this a tiktok and fix the grammar in what I said
⚙ probe
✓ probe — clip.mp4: 1920x1080 30fps 41.2s audio:yes (120ms)
⚙ trim {"start":2.4,"end":31}
✓ trim — trimmed to 2.4s–31.0s → 002-trim.mp4 (1.9s)
⚙ crop {"aspect":"9:16"}
✓ crop — cropped to 608x1080 @ (656,0) → 003-crop.mp4 (2.2s)
⚙ transcribe
✓ transcribe — transcribed speech (3.1s)
⚙ tts {"text":"We were there when it launched — and it doesn't get better than this."}
✓ tts — synthesized 71 chars of speech (1.4s)
⚙ replace_audio {"audio":".heydit/out/005-tts.wav"}
✓ replace_audio — audio track replaced with 005-tts.wav → 006-newaudio.mp4 (0.8s)

Reframed to 9:16 with the hook first, corrected the spoken line, and re-voiced it.
```

---

## Install & run

```bash
# needs Go ≥ 1.22 and ffmpeg on PATH
go build -o heydit ./cmd/heydit

export ANTHROPIC_API_KEY=sk-ant-…   # or any provider below
cd ~/videos/my-project
./heydit add clip.mp4
./heydit                            # interactive session
./heydit -p "trim to the first 10s" # one-shot
./heydit ui                         # desktop editing screen
```

## The harness (the point of this project)

Heydit's harness is built for long editing sessions on small token budgets:

| Mechanism | What it does |
|---|---|
| **State ledger, not transcripts** | The full project state — sources, every edit applied, the current working video and its metadata — is serialized into a compact block re-injected each turn. The model never depends on old conversation turns to know where the video stands. |
| **Aggressive history compaction** | Because the ledger carries state, old `tool_use`/`tool_result` exchanges are redundant; past a budget threshold they collapse to one-line notes, oldest first, deterministically (no summarizer call). The first user message (session intent) always survives. |
| **Compact tool results** | Tools return one summary line plus a few structured facts, hard-capped by `context.tool_result_max_chars`. Raw ffmpeg logs never reach the model. |
| **Progressive skill disclosure** | Only a one-line index of every skill is always visible; a skill's full playbook enters context only when the request triggers it. |
| **Everything undoable** | Each mutating tool renders to a numbered file and commits an op to the ledger. `/undo` pops it; clicking any step in the UI previews that intermediate. |
| **Bounded by construction** | Every ffmpeg run has a hard timeout; every render is normalized (even dims, yuv420p); tool arguments are coerced forgivingly (string numbers etc.) so model quirks don't crash the loop. |

The agent's contract is small: work on CURRENT, chain mutating tools (each output
becomes the new CURRENT), trust the ledger over memory, use `render` (raw
filtergraph escape hatch) only when no dedicated tool fits.

### Tools

`probe · trim · concat · set_speed · crop · expand_frame · change_background ·
overlay_text · render · export · transcribe · tts · extract_audio ·
replace_audio · mix_audio`

## Models are configuration, not code

The brain is any Anthropic-native or OpenAI-compatible endpoint — which covers
Claude, Kimi (Moonshot), OpenRouter, Groq, Ollama, vLLM, llama.cpp:

```bash
heydit model use anthropic            # Claude (default)
heydit model use kimi/kimi-k3         # Moonshot Kimi K3
heydit model use ollama/qwen2.5:14b   # fully local, keyless
heydit model use openrouter/meta-llama/llama-4-maverick
heydit models                         # list presets + key env vars
```

Mid-session, `/model kimi/kimi-k3` switches while **keeping conversation
history** — the ledger means the new model picks up exactly where the old one
left off.

### Voice: open-source first

| Role | Default | Switch to |
|---|---|---|
| TTS | **Kokoro-82M** (best open-source TTS, Apache-2.0) via [kokoro-fastapi](https://github.com/remsky/Kokoro-FastAPI)'s OpenAI-compatible endpoint | `heydit config set audio.tts.provider elevenlabs` (or `openai`, or any custom endpoint) |
| STT | **Whisper** via any OpenAI-compatible server (faster-whisper-server, whisper.cpp) | `heydit config set audio.stt.provider openai` |

```bash
# run the default local voice stack
docker run -p 8880:8880 ghcr.io/remsky/kokoro-fastapi-cpu   # TTS
docker run -p 8000:8000 fedirz/faster-whisper-server         # STT
heydit doctor                                                # verify everything
```

Config is layered (defaults → `~/.heydit/config.yaml` → `<project>/.heydit/config.yaml`),
addressable by dotted paths, and API keys only ever come from environment variables.

## Skills

Skills are markdown playbooks the agent follows — `instagram-reel` and `tiktok`
ship built in (9:16 conversion strategy, hook-first trimming, muted-viewing
captions, pacing rules). Add your own:

```
~/.heydit/skills/my-brand/SKILL.md          # global
<project>/.heydit/skills/my-brand/SKILL.md  # per-project (overrides by name)
```

```markdown
---
name: my-brand
description: House style for product clips.
triggers: product, launch clip
---
Always overlay_text the product name in the first 2 seconds…
```

`heydit skills` lists them; a project skill with the same name overrides a built-in.

## Desktop

`heydit ui` serves an embedded editing screen (chat + preview + clickable edit
timeline + upload/undo/download) and opens your browser — same agent, same
project state as the CLI, so you can move between them freely. The server only
ever serves files registered in the project (assets, op outputs), never
arbitrary paths. Packaging this into a native shell (Wails/Tauri) reuses the
server unchanged.

## CLI reference

```
heydit                        interactive session (current dir = project)
heydit -p "request"           one-shot edit
heydit add <video...>         register source videos
heydit ui [--addr host:port]  desktop editing screen
heydit model [show|use spec]  switch models
heydit models                 provider presets
heydit config [list|get|set]  configuration
heydit skills                 list skills
heydit doctor                 environment checkup
```

REPL: `/model /models /config /ops /undo /skills /skill <name> /cost /export /help /quit`

## Layout

```
cmd/heydit/            CLI entry + subcommands
internal/agent/        the harness: loop, system prompt, compaction
internal/provider/     Anthropic + OpenAI-compatible adapters
internal/tools/        tool registry, video + audio tools
internal/media/        ffmpeg wrappers, project ledger, undo
internal/voice/        TTS/STT clients (Kokoro, ElevenLabs, Whisper, …)
internal/skills/       skill loading + built-in playbooks (embedded)
internal/config/       layered config, presets, dotted-path access
internal/server/       desktop UI server + embedded frontend
internal/cli/          REPL + terminal rendering
```

## Testing

```bash
go test ./...
```

Covers: the full agent loop against a scripted model with **real ffmpeg
renders** (trim commits an op, CURRENT advances, results stay compact, errors
survive as `is_error`), both provider wire formats via local fake servers,
history compaction invariants, skill loading/triggering/overriding, config
layering and model switching, and tool argument/aspect parsing.

## Roadmap

- Streaming responses and cancellable renders
- Cloud generative tools (outpainting for `expand_frame`, matting for
  `change_background`) behind the same tool contracts
- Native desktop packaging (Wails) around `internal/server`
- Skill marketplace + `heydit skills install`
