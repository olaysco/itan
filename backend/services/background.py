"""Tool: change / replace the background of a video.

Pipeline:
  1. Matte the subject out of the video (cloud robust-video-matting model),
     producing a green-screen or alpha foreground.
  2. Generate or load a new background:
       * a solid colour (parsed from the request, e.g. "blue"),
       * an AI-generated scene (outpaint/txt2img model) from a prompt,
       * or a user-supplied image.
  3. Composite the foreground over the new background (ffmpeg).

Stub fallback: without a matting key we can't cleanly segment the subject, so
we apply a stylized background wash (tint + vignette) and clearly report that
true replacement needs the cloud model. Nothing crashes.
"""
from __future__ import annotations

import re
from pathlib import Path
from typing import Optional

from ..config import settings
from . import providers, video_utils

COLORS = {
    "black": "black", "white": "white", "blue": "#1e3a8a", "sky": "#7dd3fc",
    "green": "#166534", "red": "#991b1b", "purple": "#6b21a8", "pink": "#db2777",
    "orange": "#ea580c", "yellow": "#eab308", "gray": "#374151", "grey": "#374151",
    "teal": "#0f766e", "beach": "#38bdf8", "sunset": "#fb7185", "office": "#94a3b8",
}


def _parse_color(text: str) -> Optional[str]:
    if not text:
        return None
    t = text.lower()
    m = re.search(r"#([0-9a-f]{6})", t)
    if m:
        return "#" + m.group(1)
    for word, hexv in COLORS.items():
        if word in t:
            return hexv
    return None


def _solid_bg_image(color: str, w: int, h: int, out: Path) -> Path:
    from ..services.video_utils import _run

    _run(["ffmpeg", "-y", "-f", "lavfi", "-i", f"color=c={color}:s={w}x{h}:d=1", "-frames:v", "1", str(out)])
    return out


def run(
    video: Path,
    workspace: Path,
    prompt: str = "",
    background_image: Optional[str] = None,
) -> dict:
    workspace.mkdir(parents=True, exist_ok=True)

    if not settings.has_ffmpeg:
        return {"mode": "stub", "output_video": None, "summary": "ffmpeg is not installed; cannot render."}

    info = video_utils.probe(video)
    out_video = workspace / "new_background.mp4"

    # ---- Build the new background image --------------------------------
    bg_image: Optional[Path] = None
    bg_desc = ""
    color = _parse_color(prompt)
    if background_image and Path(background_image).exists():
        bg_image = Path(background_image)
        bg_desc = "your uploaded image"
    elif settings.visual_live and prompt and not color:
        # Generate a background scene from the prompt (outpaint model with a
        # fully-white mask acts as text-to-image over a blank canvas).
        try:
            blank = workspace / "blank.png"
            mask = workspace / "fullmask.png"
            _make_blank_and_mask(blank, mask, info.width, info.height)
            bg_image = providers.outpaint_image(blank, mask, prompt, workspace / "bg_scene.png")
            bg_desc = f"an AI-generated scene ({prompt!r})"
        except Exception:
            bg_image = None
    if bg_image is None:
        chosen = color or "#0f172a"
        bg_image = _solid_bg_image(chosen, info.width, info.height, workspace / "bg_solid.png")
        bg_desc = f"a solid {chosen} background"

    # ---- Matte the subject (cloud) then composite ----------------------
    if settings.visual_live:
        try:
            matted = providers.matte_video(video, workspace / "matted.mp4")
            # robust_video_matting green-screen output -> chroma key it out
            _composite_greenscreen(matted, bg_image, out_video, info.width, info.height)
            return {
                "mode": "cloud",
                "output_video": str(out_video),
                "summary": f"Segmented the subject and replaced the background with {bg_desc}.",
                "details": {"background": bg_desc, "model": settings.replicate_matting_model},
            }
        except Exception as exc:
            note = f" (matting unavailable: {exc}; applied a stylized wash instead)"
    else:
        note = ""

    # ---- Stub fallback: stylized background wash -----------------------
    _stylized_wash(video, color or "#38bdf8", out_video, info)
    return {
        "mode": "stub" if not settings.visual_live else "cloud-fallback",
        "output_video": str(out_video),
        "summary": (
            f"Applied a stylized background treatment toward {bg_desc}{note}. "
            "True subject/background separation activates with REPLICATE_API_TOKEN."
        ),
        "details": {"background": bg_desc},
    }


def _make_blank_and_mask(blank: Path, mask: Path, w: int, h: int) -> None:
    from PIL import Image

    Image.new("RGB", (w, h), (127, 127, 127)).save(blank)
    Image.new("L", (w, h), 255).save(mask)


def _composite_greenscreen(fg_video: Path, bg_image: Path, out: Path, w: int, h: int) -> Path:
    from ..services.video_utils import _run

    fc = (
        f"[0:v]scale={w}:{h}[bg];"
        f"[1:v]scale={w}:{h},colorkey=0x00FF00:0.3:0.2[fg];"
        f"[bg][fg]overlay=shortest=1"
    )
    _run(
        [
            "ffmpeg", "-y",
            "-loop", "1", "-i", str(bg_image),
            "-i", str(fg_video),
            "-filter_complex", fc,
            "-c:a", "copy",
            str(out),
        ]
    )
    return out


def _stylized_wash(video: Path, color: str, out: Path, info) -> Path:
    """Tint the video toward the target background colour and add a vignette.

    A visible, deterministic 'mood change' when true matting isn't available.
    Uses a *finite* lavfi colour source (with explicit duration) alpha-composited
    over the video, so it can never run unbounded like a looped-image blend.
    """
    from ..services.video_utils import _run

    dur = max(info.duration, 0.5)
    fps = info.fps or 25
    fc = (
        f"color=c={color}@0.35:s={info.width}x{info.height}:d={dur:.3f}:r={fps},"
        f"format=yuva420p[tint];"
        f"[0:v][tint]overlay=shortest=1,vignette=PI/4"
    )
    _run(
        [
            "ffmpeg", "-y",
            "-i", str(video),
            "-filter_complex", fc,
            "-c:a", "copy",
            str(out),
        ]
    )
    return out
