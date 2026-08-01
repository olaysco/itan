"""Tool: expand / uncrop a video's frame (aspect-ratio expansion, outpainting).

Strategy:
  * Parse the requested target aspect ratio from natural-language params
    (e.g. "16:9", "widescreen", "make it square", "1.85").
  * When a cloud outpainting model + Pillow are available, generate an
    AI-extended still from a representative frame and play the moving subject
    centered inside that generated scene.
  * Otherwise, fall back to a content-aware blurred expansion (no black bars)
    via ffmpeg. Both paths produce a real, larger-canvas video.
"""
from __future__ import annotations

import re
from pathlib import Path
from typing import Optional

from ..config import settings
from . import providers, video_utils

ASPECT_WORDS = {
    "widescreen": 16 / 9,
    "wide": 16 / 9,
    "cinematic": 2.39,
    "cinema": 2.39,
    "square": 1.0,
    "portrait": 9 / 16,
    "vertical": 9 / 16,
    "story": 9 / 16,
    "stories": 9 / 16,
    "landscape": 16 / 9,
    "standard": 4 / 3,
}


def parse_target_aspect(text: str, current: float) -> float:
    if not text:
        return max(current * 1.4, 16 / 9)  # default: widen noticeably
    t = text.lower()
    m = re.search(r"(\d+(?:\.\d+)?)\s*[:x/]\s*(\d+(?:\.\d+)?)", t)
    if m:
        w, h = float(m.group(1)), float(m.group(2))
        if h:
            return w / h
    m = re.search(r"\b(\d\.\d{1,2})\b", t)  # bare ratio like 1.85
    if m:
        return float(m.group(1))
    for word, ratio in ASPECT_WORDS.items():
        if word in t:
            return ratio
    return max(current * 1.4, 16 / 9)


def run(video: Path, workspace: Path, target: str = "", prompt: str = "", timestamp: float = 0.0) -> dict:
    workspace.mkdir(parents=True, exist_ok=True)
    out_video = workspace / "expanded.mp4"

    if not settings.has_ffmpeg:
        return {
            "mode": "stub",
            "output_video": None,
            "summary": (
                "ffmpeg is not installed, so I could not render the expansion. "
                "Install ffmpeg to enable frame expansion."
            ),
        }

    info = video_utils.probe(video)
    target_aspect = parse_target_aspect(target or prompt, info.aspect)
    canvas_w, canvas_h = video_utils.expanded_canvas_size(info, target_aspect)

    # ---- Cloud path: AI outpainting of a keyframe -> generated backdrop ----
    if settings.visual_live:
        try:
            frame = video_utils.extract_frame(video, workspace / "frame.png", timestamp)
            canvas_png = workspace / "canvas.png"
            mask_png = workspace / "mask.png"
            video_utils.build_outpaint_canvas(frame, canvas_png, mask_png, canvas_w, canvas_h)
            filled = providers.outpaint_image(
                canvas_png,
                mask_png,
                prompt or "seamlessly extend the surrounding scene, photorealistic",
                workspace / "outpainted.png",
            )
            video_utils.center_over_background_image(video, filled, out_video, canvas_w, canvas_h)
            return {
                "mode": "cloud",
                "output_video": str(out_video),
                "summary": (
                    f"Expanded the frame from {info.width}x{info.height} "
                    f"(~{info.aspect:.2f}) to {canvas_w}x{canvas_h} (~{target_aspect:.2f}) "
                    f"using AI outpainting to generate the new surroundings."
                ),
                "details": {"target_aspect": round(target_aspect, 3), "model": settings.replicate_outpaint_model},
            }
        except Exception as exc:  # fall through to deterministic path
            fallback_note = f" (cloud outpaint unavailable: {exc}; used blurred expansion)"
    else:
        fallback_note = ""

    # ---- Deterministic path: content-aware blurred expansion --------------
    video_utils.blurred_expand(video, out_video, target_aspect)
    return {
        "mode": "stub" if not settings.visual_live else "cloud-fallback",
        "output_video": str(out_video),
        "summary": (
            f"Expanded the frame from {info.width}x{info.height} (~{info.aspect:.2f}) "
            f"to {canvas_w}x{canvas_h} (~{target_aspect:.2f}) using a content-aware "
            f"blurred fill{fallback_note}."
        ),
        "details": {"target_aspect": round(target_aspect, 3)},
    }
