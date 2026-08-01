# 🎬 Heydit — Agentic AI Video Editor

Post a video, then just **ask** for the edit in plain language. Heydit is an
agentic editor: a Claude tool‑use loop interprets your request and calls the
right video tools, chaining them when you ask for several changes at once.

It ships with three cloud‑model‑backed capabilities:

| Ask for… | Tool | What it does |
|---|---|---|
| *“Expand the frame to 16:9”, “uncrop this”, “make it vertical”* | **`expand_frame`** | Grows the canvas and generates new imagery to fill it (AI **outpainting**). |
| *“Fix the grammar in what they said”* | **`correct_audio_grammar`** | **Transcribes** the speech → **corrects grammar** → **re‑voices** it → muxes it back. |
| *“Put me on a sunset beach”, “make the background blue”* | **`change_background`** | **Mattes** the subject and composites a new AI‑generated or solid background. |

Everything runs **on your own machine** and calls hosted **cloud models** for
the heavy generative steps. No cloud keys? Every tool degrades to a working
local **stub** so the whole app still runs end‑to‑end.

---

## Quick start

```bash
# 1. System dependency (required for real video processing)
#    macOS:  brew install ffmpeg
#    Ubuntu: sudo apt-get install -y ffmpeg

# 2. Python deps
pip install -r requirements.txt

# 3. (optional) add cloud keys for full-quality AI
cp .env.example .env      # then edit .env

# 4. Run
python run.py             # -> http://localhost:8000
```

Open **http://localhost:8000**, drag in a video (or click *“try a demo clip”*),
and start typing edits. The badges in the top‑right show which capabilities are
running in **cloud** vs **stub** mode.

> No video handy? Click **“…or try a demo clip”** — Heydit synthesizes a short
> clip with ffmpeg so you can try every tool immediately.

---

## How the agent works

```
        your words                 tool-use loop                real edits
  ┌────────────────────┐      ┌──────────────────────┐    ┌────────────────────┐
  │ "expand to 16:9    │      │  Claude picks tools   │    │ ffmpeg + cloud     │
  │  and put me on a   │ ───▶ │  and arguments, then  │──▶ │ models mutate the  │
  │  beach"            │      │  reads the results    │    │ working video      │
  └────────────────────┘      └──────────────────────┘    └────────────────────┘
                                        │  each edit feeds the next
                                        ▼
                              chained result video
```

- **With `ANTHROPIC_API_KEY`** → a real Claude tool‑use loop decides which
  tool(s) to call, infers arguments (aspect ratios, background prompts…), and
  can chain several edits in one request.
- **Without it** → a deterministic keyword router keeps the app agentic and
  fully usable offline.

Each tool applies its edit to the *current* video, so
`current = tool(current)` — chaining “expand, then change background, then fix
grammar” just works.

---

## Cloud models (all optional & swappable via `.env`)

| Capability | Default provider / model | Env var |
|---|---|---|
| Agent brain + grammar | Anthropic `claude-opus-4-8` | `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL` |
| Speech‑to‑text | OpenAI `whisper-1` | `OPENAI_API_KEY`, `OPENAI_STT_MODEL` |
| Text‑to‑speech | OpenAI `gpt-4o-mini-tts` | `OPENAI_TTS_MODEL`, `OPENAI_TTS_VOICE` |
| Frame outpainting | Replicate `flux-fill-dev` | `REPLICATE_API_TOKEN`, `REPLICATE_OUTPAINT_MODEL` |
| Video matting | Replicate `robust_video_matting` | `REPLICATE_MATTING_MODEL` |

Set `FORCE_STUB=1` to run entirely offline regardless of keys present.

### Graceful degradation

| Tool | Cloud path | Stub / fallback path |
|---|---|---|
| `expand_frame` | Outpaints a keyframe → plays subject inside the AI‑generated scene | Content‑aware **blurred canvas expansion** (no black bars) |
| `correct_audio_grammar` | Whisper → Claude → TTS → remux | Rule‑based grammar fixer + reports the correction it would make |
| `change_background` | Matting → composite over new background | Stylized **color tint + vignette** wash |

No path ever crashes on a missing key — it downgrades and tells you so in the
tool trace shown under each reply.

---

## Project layout

```
Heydit/
├── run.py                     # entry point: python run.py
├── requirements.txt
├── .env.example
├── backend/
│   ├── main.py                # FastAPI app: /api/upload, /api/chat, /media
│   ├── config.py              # env-driven settings + capability flags
│   ├── projects.py            # in-memory project + edit-history store
│   ├── agent/
│   │   ├── tools.py           # tool JSON schemas + dispatch + offline router
│   │   └── orchestrator.py    # the agentic tool-use loop
│   └── services/
│       ├── llm.py             # Anthropic client + grammar fixer
│       ├── providers.py       # OpenAI (STT/TTS) + Replicate clients
│       ├── video_utils.py     # ffmpeg/ffprobe helpers
│       ├── frame_expand.py    # expand_frame tool
│       ├── audio_grammar.py   # correct_audio_grammar tool
│       └── background.py      # change_background tool
├── frontend/                  # single-page UI (no build step)
│   ├── index.html · styles.css · app.js
└── tests/
    └── test_smoke.py          # offline tests (no keys / no network)
```

---

## API

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Status + capability report (cloud vs stub) |
| `POST` | `/api/upload` | Multipart video upload → returns a project |
| `POST` | `/api/demo` | Generate a synthetic demo clip project |
| `POST` | `/api/chat` | `{project_id, message}` → runs the agent, returns reply + tool trace + updated video URLs |
| `GET` | `/media/{path}` | Serve uploaded / rendered media |

---

## Testing

```bash
pip install pytest
pytest -q          # deterministic, offline (FORCE_STUB=1), no ffmpeg required
```

The suite covers aspect/colour parsing, intent routing, the rule‑based grammar
fixer, tool‑schema well‑formedness, and the FastAPI surface.

---

## Notes & limits

- **ffmpeg is required** for actual rendering; without it the tools report that
  clearly instead of failing.
- State is **in‑memory** (single node). Media persists on disk under
  `storage/`.
- Cloud outpainting expands the scene using a representative keyframe as a
  generated backdrop rather than re‑generating every frame — a pragmatic,
  cost‑aware choice. Per‑frame outpainting can be added in `frame_expand.py`.
- This is a reference implementation intended to be extended: add tools by
  appending a schema in `agent/tools.py` and a service in `backend/services/`.
