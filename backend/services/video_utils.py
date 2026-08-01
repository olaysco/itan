"""Thin, dependency-light wrappers around ffmpeg / ffprobe.

These helpers do the "plumbing" parts of video editing (probing metadata,
pulling a frame, splitting/merging audio, letterboxing) so the higher level
AI tools can focus on the generative step. Everything degrades gracefully:
if ffmpeg is not installed the callers detect it via `settings.has_ffmpeg`
and switch to stub behaviour instead of crashing.
"""
from __future__ import annotations

import json
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Optional


class FFmpegError(RuntimeError):
    pass


def _run(cmd: list[str]) -> str:
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        raise FFmpegError(
            f"command failed ({' '.join(cmd[:2])} ...): {proc.stderr.strip()[:500]}"
        )
    return proc.stdout


@dataclass
class VideoInfo:
    width: int
    height: int
    duration: float
    fps: float
    has_audio: bool

    @property
    def aspect(self) -> float:
        return self.width / self.height if self.height else 0.0


def probe(path: Path) -> VideoInfo:
    """Return basic metadata for a video file using ffprobe."""
    out = _run(
        [
            "ffprobe",
            "-v",
            "quiet",
            "-print_format",
            "json",
            "-show_streams",
            "-show_format",
            str(path),
        ]
    )
    data = json.loads(out)
    video_stream = next((s for s in data["streams"] if s["codec_type"] == "video"), None)
    audio_stream = next((s for s in data["streams"] if s["codec_type"] == "audio"), None)
    if video_stream is None:
        raise FFmpegError("no video stream found")

    # fps comes as a rational string like "30000/1001"
    num, _, den = video_stream.get("r_frame_rate", "30/1").partition("/")
    fps = float(num) / float(den) if den and float(den) else float(num or 30)

    duration = float(data.get("format", {}).get("duration", 0) or 0)
    return VideoInfo(
        width=int(video_stream["width"]),
        height=int(video_stream["height"]),
        duration=duration,
        fps=fps,
        has_audio=audio_stream is not None,
    )


def extract_frame(video: Path, out_image: Path, timestamp: float = 0.0) -> Path:
    """Grab a single frame as a PNG at the given timestamp (seconds)."""
    _run(
        [
            "ffmpeg",
            "-y",
            "-ss",
            str(timestamp),
            "-i",
            str(video),
            "-frames:v",
            "1",
            str(out_image),
        ]
    )
    return out_image


def extract_audio(video: Path, out_audio: Path) -> Optional[Path]:
    """Extract the audio track as a wav. Returns None if there's no audio."""
    if not probe(video).has_audio:
        return None
    _run(
        [
            "ffmpeg",
            "-y",
            "-i",
            str(video),
            "-vn",
            "-acodec",
            "pcm_s16le",
            "-ar",
            "16000",
            "-ac",
            "1",
            str(out_audio),
        ]
    )
    return out_audio


def replace_audio(video: Path, audio: Path, out_video: Path) -> Path:
    """Mux a new audio track onto an existing video (video stream copied)."""
    _run(
        [
            "ffmpeg",
            "-y",
            "-i",
            str(video),
            "-i",
            str(audio),
            "-map",
            "0:v:0",
            "-map",
            "1:a:0",
            "-c:v",
            "copy",
            "-c:a",
            "aac",
            "-shortest",
            str(out_video),
        ]
    )
    return out_video


def pad_to_aspect(video: Path, out_video: Path, target_aspect: float, color: str = "black") -> Path:
    """Letterbox/pillarbox a video onto a canvas of `target_aspect`.

    This is the deterministic fallback for frame expansion when no generative
    outpainting model is available — it grows the canvas instead of inventing
    new pixels.
    """
    info = probe(video)
    if target_aspect >= info.aspect:
        # widen: keep height, grow width
        new_w = int(round(info.height * target_aspect))
        new_w += new_w % 2
        pad = f"{new_w}:{info.height}:({new_w}-{info.width})/2:0:{color}"
    else:
        # heighten: keep width, grow height
        new_h = int(round(info.width / target_aspect))
        new_h += new_h % 2
        pad = f"{info.width}:{new_h}:0:({new_h}-{info.height})/2:{color}"
    _run(
        [
            "ffmpeg",
            "-y",
            "-i",
            str(video),
            "-vf",
            f"pad={pad}",
            "-c:a",
            "copy",
            str(out_video),
        ]
    )
    return out_video


def overlay_on_background(fg_video: Path, bg_image: Path, out_video: Path) -> Path:
    """Composite a (chroma/alpha) foreground video over a static background.

    Used by the background-replacement tool once matting has produced a
    foreground with transparency (or the stub green screen).
    """
    info = probe(fg_video)
    _run(
        [
            "ffmpeg",
            "-y",
            "-loop",
            "1",
            "-i",
            str(bg_image),
            "-i",
            str(fg_video),
            "-filter_complex",
            f"[0:v]scale={info.width}:{info.height}[bg];[bg][1:v]overlay=shortest=1",
            "-c:a",
            "copy",
            str(out_video),
        ]
    )
    return out_video


def expanded_canvas_size(info: VideoInfo, target_aspect: float) -> tuple[int, int]:
    """Compute the new (even) canvas size to reach `target_aspect`."""
    if target_aspect >= info.aspect:
        new_w = int(round(info.height * target_aspect))
        new_w += new_w % 2
        return new_w, info.height
    new_h = int(round(info.width / target_aspect))
    new_h += new_h % 2
    return info.width, new_h


def blurred_expand(video: Path, out_video: Path, target_aspect: float, blur: int = 24) -> Path:
    """Expand a video to `target_aspect` by filling the new area with a scaled,
    blurred copy of the frame (no black bars). This is the deterministic,
    no-cloud "frame expansion" — visually pleasing and content-aware-ish.
    """
    info = probe(video)
    new_w, new_h = expanded_canvas_size(info, target_aspect)
    fc = (
        f"[0:v]split[bg][fg];"
        f"[bg]scale={new_w}:{new_h}:force_original_aspect_ratio=increase,"
        f"crop={new_w}:{new_h},boxblur={blur}:2[bgb];"
        f"[bgb][fg]overlay=(W-w)/2:(H-h)/2"
    )
    _run(["ffmpeg", "-y", "-i", str(video), "-filter_complex", fc, "-c:a", "copy", str(out_video)])
    return out_video


def center_over_background_image(
    video: Path, bg_image: Path, out_video: Path, canvas_w: int, canvas_h: int
) -> Path:
    """Overlay the (unscaled) video centered on a full-canvas background still.

    Used with an AI-outpainted still so the moving subject plays inside an
    AI-generated, expanded scene.
    """
    fc = (
        f"[0:v]scale={canvas_w}:{canvas_h}[bg];"
        f"[bg][1:v]overlay=(W-w)/2:(H-h)/2:shortest=1"
    )
    _run(
        [
            "ffmpeg",
            "-y",
            "-loop",
            "1",
            "-i",
            str(bg_image),
            "-i",
            str(video),
            "-filter_complex",
            fc,
            "-c:a",
            "copy",
            str(out_video),
        ]
    )
    return out_video


def build_outpaint_canvas(frame: Path, canvas_png: Path, mask_png: Path, canvas_w: int, canvas_h: int) -> None:
    """Place `frame` centered on a transparent canvas and write both the canvas
    and a mask (white = region to be generated) for an outpainting model.
    Requires Pillow; raises ImportError if unavailable so caller can fall back.
    """
    from PIL import Image  # local import; optional dependency

    src = Image.open(frame).convert("RGBA")
    canvas = Image.new("RGBA", (canvas_w, canvas_h), (0, 0, 0, 0))
    ox, oy = (canvas_w - src.width) // 2, (canvas_h - src.height) // 2
    canvas.paste(src, (ox, oy))
    canvas.convert("RGB").save(canvas_png)

    mask = Image.new("L", (canvas_w, canvas_h), 255)  # white everywhere = fill
    black = Image.new("L", (src.width, src.height), 0)  # keep original region
    mask.paste(black, (ox, oy))
    mask.save(mask_png)


def make_placeholder_video(out_video: Path, seconds: float = 4.0, text: str = "Heydit") -> Path:
    """Generate a small synthetic clip. Handy for tests / demos with no upload."""
    _run(
        [
            "ffmpeg",
            "-y",
            "-f",
            "lavfi",
            "-i",
            f"testsrc=duration={seconds}:size=640x360:rate=25",
            "-f",
            "lavfi",
            "-i",
            f"sine=frequency=440:duration={seconds}",
            "-vf",
            f"drawtext=text='{text}':fontcolor=white:fontsize=48:x=(w-tw)/2:y=(h-th)/2",
            "-c:v",
            "libx264",
            "-pix_fmt",
            "yuv420p",
            "-c:a",
            "aac",
            "-shortest",
            str(out_video),
        ]
    )
    return out_video
