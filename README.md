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
| **Prompt-cache hygiene** | The system prompt is byte-stable for the whole session (identity + memory files + skill index). Volatile state travels as tagged reminder blocks on user messages — and the project ledger is a **delta**: re-sent only when it actually changed. Providers cache the prefix across every turn. |
| **State ledger, not transcripts** | The full project state — sources, every edit applied, the current working video and its metadata — lives in one compact `<project-state>` block. The model never depends on old conversation turns to know where the video stands. |
| **Layered compaction** | Old `tool_use`/`tool_result` exchanges collapse to one-line notes past a budget threshold, deterministically (no summarizer call); the session-intent message always survives. `/compact` additionally does a model-written structured summary (creative direction with verbatim quotes, timeline state, assets, failures, pending work). |
| **Compact results + disk spill** | Tools return one summary line plus structured facts, hard-capped by `context.tool_result_max_chars`. Anything truncated is spilled in full to `.heydit/out/tool-results/` and readable back via the `read_text` tool — the cap can't destroy information. |
| **Probe-after-edit feedback** | Every mutating tool's result carries a fresh probe of its output (`now=1080x1920 30fps 12.1s audio:yes`), so the model immediately sees the concrete effect of each edit — the video analogue of a coding agent seeing compiler diagnostics after a write. |
| **Permission gate** | Rules (`{tool, action}`) evaluated last-match-wins, three modes (`auto`/`ask`/`plan`), interactive approve with `always`, and **deny-with-feedback**: type a correction instead of "no" and it reaches the model as guidance. A bypass-immune safety tier prompts for destructive writes (e.g. export over an existing file) even when rules allow. |
| **Plan mode** | `/plan` flips the agent to propose-only: mutating tools are hard-denied regardless of rules, and a reminder instructs the model to present a numbered plan. |
| **Doom-loop detection** | The third byte-identical tool call in a row is refused with corrective feedback instead of burning another render. |
| **Visible retries** | Provider failures are classified (429/5xx/network retry; 4xx never), honor `Retry-After`, back off exponentially with jitter — and each wait is emitted as an event, so the CLI shows `↻ attempt 2/5, retrying in 4s` instead of silently hanging. |
| **Parallel-safe tool batches** | Consecutive read-only calls (probe, read_text) run concurrently with results emitted in call order; mutating renders stay strictly serial. |
| **Sessions that survive** | History persists to `.heydit/session.json` after every turn; `heydit -c` resumes the conversation — after a crash, a reboot, or a model switch. |
| **Progressive skill disclosure** | Only a one-line index of every skill is always visible; a skill's full playbook is injected once, on trigger. |
| **Everything undoable** | Each mutating tool renders to a numbered file and commits a ledger op. `/undo` pops it; clicking any step in the UI previews that intermediate. |
| **Bounded by construction** | Hard timeout on every ffmpeg run; normalized renders (even dims, yuv420p); forgiving argument coercion so model quirks don't crash the loop. |

The agent's contract is small: work on CURRENT, chain mutating tools (each output
becomes the new CURRENT), trust the newest `<project-state>` over memory, use
`render` (raw filtergraph escape hatch) only when no dedicated tool fits.

### Memory files

Drop a `HEYDIT.md` in the project root (or `~/.heydit/HEYDIT.md` globally) with
standing instructions — house style, caption fonts, export conventions. It is
loaded into the static system prompt every session, project file last so it
wins.

### Permissions in config

```yaml
mode: ask            # auto (default) | ask | plan
permissions:         # last match wins; '*' and 'prefix*' wildcards
  - {tool: "*", action: allow}
  - {tool: "render", action: ask}
  - {tool: "change_*", action: deny}
```

### Tools

`probe · trim · concat · set_speed · crop · expand_frame · change_background ·
overlay_text · render · export · transcribe · tts · extract_audio ·
replace_audio · mix_audio · read_text`

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
heydit -c | --continue        resume the previous conversation
heydit -p "request"           one-shot edit
heydit add <video...>         register source videos
heydit ui [--addr host:port]  desktop editing screen
heydit model [show|use spec]  switch models
heydit models                 provider presets
heydit config [list|get|set]  configuration
heydit skills                 list skills
heydit doctor                 environment checkup
```

REPL: `/model /models /config /mode /plan /compact /ops /undo /skills /skill <name> /cost /export /help /quit`

## Layout

```
cmd/heydit/            CLI entry + subcommands
internal/agent/        the harness: loop, reminders, compaction, sessions
internal/permission/   rule engine, modes, safety tier
internal/provider/     Anthropic + OpenAI-compatible adapters + retry
internal/tools/        tool registry, video + audio + text tools
internal/media/        ffmpeg wrappers, project ledger, undo
internal/voice/        TTS/STT clients (Kokoro, ElevenLabs, Whisper, …)
internal/skills/       skill loading + built-in playbooks (embedded)
internal/config/       layered config, presets, dotted-path access
internal/server/       desktop UI server + embedded frontend
internal/cli/          REPL, permission prompts, terminal rendering
```

## Testing

```bash
go test ./...
```

Covers: the full agent loop against a scripted model with **real ffmpeg
renders** (trim commits an op, CURRENT advances, results stay compact, errors
survive as `is_error`), prompt-cache hygiene (static system prompt, ledger
deltas, no duplicate skill injection), permission rules/modes/safety tier,
doom-loop refusal, plan mode blocking real renders, session save/load,
max-turns honesty, retry classification with fake 429/400 servers, both
provider wire formats, compaction invariants, skill loading/overriding, and
config layering.

## Roadmap

- Streaming responses and cancellable renders
- Shadow-git snapshots of the project dir for multi-turn revert (opencode-style)
- Cloud generative tools (outpainting for `expand_frame`, matting for
  `change_background`) behind the same tool contracts
- Native desktop packaging (Wails) around `internal/server`
- Skill marketplace + `heydit skills install`
