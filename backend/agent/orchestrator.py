"""The agentic loop.

Given a user's natural-language request and a working video, the orchestrator
either:
  * drives a real Claude tool-use loop (when ANTHROPIC_API_KEY is set), letting
    the model decide which editing tool(s) to call and in what order; or
  * falls back to a deterministic keyword router so the app stays agentic and
    fully functional with zero cloud keys.

Each tool call mutates the "current" video (the output of one edit becomes the
input to the next), enabling chained requests like
"expand to 16:9 and change the background to a beach".
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Callable, Optional

from ..config import settings
from ..services import llm
from . import tools

SYSTEM_PROMPT = (
    "You are Heydit, an agentic AI video editor. You receive a user's request "
    "and metadata about their uploaded video, and you accomplish edits by "
    "calling the provided tools. Guidelines:\n"
    "- Pick the tool(s) that match the request. Chain multiple tools when the "
    "user asks for several edits; each edit is applied to the result of the "
    "previous one.\n"
    "- Infer sensible arguments from the request (aspect ratios, background "
    "descriptions, etc.).\n"
    "- After the tools run, briefly tell the user what you did in plain language. "
    "Do not invent capabilities beyond the tools.\n"
    "- If the request doesn't match any tool, explain what you *can* do."
)


def run_agent(
    message: str,
    video: Path,
    workspace: Path,
    on_step: Optional[Callable[[dict], None]] = None,
    max_turns: int = 6,
) -> dict:
    """Return {reply, steps, current_video} after processing `message`."""
    workspace.mkdir(parents=True, exist_ok=True)
    state = {"current_video": video, "steps": []}

    def emit(step: dict) -> None:
        state["steps"].append(step)
        if on_step:
            on_step(step)

    if settings.agent_live:
        reply = _run_llm_loop(message, state, workspace, emit, max_turns)
    else:
        reply = _run_offline(message, state, workspace, emit)

    return {
        "reply": reply,
        "steps": state["steps"],
        "current_video": str(state["current_video"]) if state["current_video"] else None,
    }


def _apply_tool(name: str, args: dict, state: dict, workspace: Path, emit) -> dict:
    video = state["current_video"]
    result = tools.dispatch(name, args, video, workspace)
    if result.get("output_video"):
        state["current_video"] = Path(result["output_video"])
    step = {"tool": name, "args": args, **result}
    emit(step)
    return result


def _run_llm_loop(message: str, state: dict, workspace: Path, emit, max_turns: int) -> str:
    from ..services import video_utils

    try:
        info = video_utils.probe(state["current_video"]) if settings.has_ffmpeg else None
        meta = (
            f"width={info.width}, height={info.height}, aspect={info.aspect:.2f}, "
            f"duration={info.duration:.1f}s, has_audio={info.has_audio}"
            if info
            else "metadata unavailable (ffmpeg not installed)"
        )
    except Exception:
        meta = "metadata unavailable"

    messages: list[dict[str, Any]] = [
        {"role": "user", "content": f"Video metadata: {meta}\n\nUser request: {message}"}
    ]

    final_text = ""
    for _ in range(max_turns):
        resp = llm.create_message(SYSTEM_PROMPT, messages, tools=tools.TOOL_SCHEMAS, max_tokens=1024)
        assistant_content: list[dict] = []
        tool_uses = []
        for block in resp.content:
            if block.type == "text":
                assistant_content.append({"type": "text", "text": block.text})
                final_text += block.text
            elif block.type == "tool_use":
                assistant_content.append(
                    {"type": "tool_use", "id": block.id, "name": block.name, "input": block.input}
                )
                tool_uses.append(block)
        messages.append({"role": "assistant", "content": assistant_content})

        if resp.stop_reason != "tool_use" or not tool_uses:
            break

        tool_results = []
        for tu in tool_uses:
            result = _apply_tool(tu.name, dict(tu.input or {}), state, workspace, emit)
            tool_results.append(
                {
                    "type": "tool_result",
                    "tool_use_id": tu.id,
                    "content": json.dumps(
                        {"mode": result.get("mode"), "summary": result.get("summary"),
                         "details": result.get("details", {})}
                    ),
                }
            )
        messages.append({"role": "user", "content": tool_results})

    return final_text.strip() or "Done."


def _run_offline(message: str, state: dict, workspace: Path, emit) -> str:
    intent = tools.infer_tool(message)
    if intent is None:
        return (
            "I couldn't match that to an edit. I can (1) expand/uncrop the frame or "
            "change its aspect ratio, (2) correct grammar in the spoken audio, or "
            "(3) change the background. Try e.g. 'make it 16:9' or 'change the "
            "background to a beach'."
        )
    name, args = intent
    result = _apply_tool(name, args, state, workspace, emit)
    tail = " (Running without cloud keys — add them in .env for full-quality AI results.)"
    return (result.get("summary") or "Done.") + tail
