"""Tool schemas exposed to the agentic model, plus the dispatch table that maps
a tool name + arguments to the concrete video service.

The JSON schemas here are what the LLM "sees". The dispatch functions turn a
tool call into a real edit on the working video and return a result dict.
"""
from __future__ import annotations

from pathlib import Path
from typing import Any, Callable

from ..services import audio_grammar, background, frame_expand

# --- Schemas advertised to the model --------------------------------------
TOOL_SCHEMAS: list[dict] = [
    {
        "name": "expand_frame",
        "description": (
            "Expand, uncrop, or change the aspect ratio of the video's frame — "
            "generating new imagery to fill the enlarged canvas (outpainting). "
            "Use for requests like 'expand the frame', 'make it widescreen/16:9', "
            "'uncrop this', 'make it square/vertical', or 'add more scene on the sides'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "target": {
                    "type": "string",
                    "description": "Desired aspect, e.g. '16:9', '1:1', 'vertical', 'cinematic', or '1.85'.",
                },
                "prompt": {
                    "type": "string",
                    "description": "What should appear in the newly generated surrounding area.",
                },
                "timestamp": {
                    "type": "number",
                    "description": "Seconds into the video to sample as the reference frame (default 0).",
                },
            },
        },
    },
    {
        "name": "correct_audio_grammar",
        "description": (
            "Fix grammar mistakes in the spoken audio: transcribe the speech, "
            "correct grammar, re-voice it, and mux it back. Use for requests like "
            "'fix the grammar in the audio', 'correct what they said', or "
            "'clean up the speech'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "instruction": {
                    "type": "string",
                    "description": "Optional extra guidance, e.g. 'keep it casual' or 'British spelling'.",
                }
            },
        },
    },
    {
        "name": "change_background",
        "description": (
            "Replace or restyle the video background while keeping the subject. "
            "Use for requests like 'change the background to a beach', 'make the "
            "background blue', 'put me in an office', or 'remove the background'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "prompt": {
                    "type": "string",
                    "description": "Description of the new background (a scene like 'sunset beach' or a colour like 'blue').",
                }
            },
        },
    },
]

TOOL_NAMES = [t["name"] for t in TOOL_SCHEMAS]


def dispatch(name: str, args: dict[str, Any], video: Path, workspace: Path) -> dict:
    """Execute a tool call against the working video."""
    if name == "expand_frame":
        return frame_expand.run(
            video,
            workspace / "expand_frame",
            target=str(args.get("target", "")),
            prompt=str(args.get("prompt", "")),
            timestamp=float(args.get("timestamp", 0) or 0),
        )
    if name == "correct_audio_grammar":
        return audio_grammar.run(
            video,
            workspace / "correct_audio_grammar",
            instruction=str(args.get("instruction", "")),
        )
    if name == "change_background":
        return background.run(
            video,
            workspace / "change_background",
            prompt=str(args.get("prompt", "")),
        )
    return {"mode": "error", "output_video": None, "summary": f"Unknown tool: {name}"}


# --- Keyword intent parser (used when no LLM key is configured) -----------
def infer_tool(message: str) -> tuple[str, dict[str, Any]] | None:
    """Very small rule-based router so the app is still 'agentic' offline."""
    m = message.lower()
    if any(k in m for k in ["expand", "uncrop", "aspect", "16:9", "widescreen", "square", "vertical", "wider", "outpaint"]):
        return "expand_frame", {"target": message, "prompt": message}
    if any(k in m for k in ["grammar", "speech", "said", "audio", "sound", "spoken", "transcript", "mispronoun"]):
        return "correct_audio_grammar", {"instruction": message}
    if any(k in m for k in ["background", "backdrop", "green screen", "scene behind", "behind me", "remove background"]):
        return "change_background", {"prompt": message}
    return None
