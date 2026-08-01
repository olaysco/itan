"""Low-level cloud-model API clients.

Each function makes a single, well-scoped call to a hosted "cloud model" and
returns bytes or text. They are intentionally free of business logic so the
tool services (frame_expand, audio_grammar, background) can compose them.

Networking uses httpx directly (rather than each vendor SDK) so the surface is
small and predictable, and so a missing/blank key produces a clear error that
the caller can turn into a stub fallback.
"""
from __future__ import annotations

import base64
import time
from pathlib import Path
from typing import Optional

import httpx

from ..config import settings


class ProviderError(RuntimeError):
    pass


# --------------------------------------------------------------------------
# OpenAI — Whisper (speech-to-text) and TTS (text-to-speech)
# --------------------------------------------------------------------------
def transcribe_audio(audio_path: Path) -> str:
    """Transcribe speech to text via OpenAI Whisper."""
    if not settings.openai_api_key:
        raise ProviderError("OPENAI_API_KEY not set")
    with open(audio_path, "rb") as fh:
        files = {"file": (audio_path.name, fh, "audio/wav")}
        data = {"model": settings.openai_stt_model, "response_format": "text"}
        resp = httpx.post(
            "https://api.openai.com/v1/audio/transcriptions",
            headers={"Authorization": f"Bearer {settings.openai_api_key}"},
            files=files,
            data=data,
            timeout=120,
        )
    if resp.status_code >= 400:
        raise ProviderError(f"transcription failed: {resp.status_code} {resp.text[:300]}")
    return resp.text.strip()


def synthesize_speech(text: str, out_path: Path) -> Path:
    """Convert text to spoken audio (wav) via OpenAI TTS."""
    if not settings.openai_api_key:
        raise ProviderError("OPENAI_API_KEY not set")
    resp = httpx.post(
        "https://api.openai.com/v1/audio/speech",
        headers={
            "Authorization": f"Bearer {settings.openai_api_key}",
            "Content-Type": "application/json",
        },
        json={
            "model": settings.openai_tts_model,
            "voice": settings.openai_tts_voice,
            "input": text,
            "response_format": "wav",
        },
        timeout=120,
    )
    if resp.status_code >= 400:
        raise ProviderError(f"tts failed: {resp.status_code} {resp.text[:300]}")
    out_path.write_bytes(resp.content)
    return out_path


# --------------------------------------------------------------------------
# Replicate — hosted diffusion / matting models (async prediction API)
# --------------------------------------------------------------------------
def _replicate_run(model: str, payload: dict, poll_seconds: float = 2.0, timeout: float = 600) -> dict:
    """Run a Replicate model and block until the prediction completes."""
    if not settings.replicate_api_token:
        raise ProviderError("REPLICATE_API_TOKEN not set")
    headers = {
        "Authorization": f"Token {settings.replicate_api_token}",
        "Content-Type": "application/json",
    }
    # Model refs may be "owner/name" (latest) or "owner/name:version".
    if ":" in model:
        _, version = model.split(":", 1)
        create = httpx.post(
            "https://api.replicate.com/v1/predictions",
            headers=headers,
            json={"version": version, "input": payload},
            timeout=60,
        )
    else:
        create = httpx.post(
            f"https://api.replicate.com/v1/models/{model}/predictions",
            headers=headers,
            json={"input": payload},
            timeout=60,
        )
    if create.status_code >= 400:
        raise ProviderError(f"replicate create failed: {create.status_code} {create.text[:300]}")

    pred = create.json()
    get_url = pred["urls"]["get"]
    deadline = time.monotonic() + timeout
    while pred["status"] not in {"succeeded", "failed", "canceled"}:
        if time.monotonic() > deadline:
            raise ProviderError("replicate prediction timed out")
        time.sleep(poll_seconds)
        pred = httpx.get(get_url, headers=headers, timeout=60).json()
    if pred["status"] != "succeeded":
        raise ProviderError(f"replicate prediction {pred['status']}: {pred.get('error')}")
    return pred


def _download(url: str, out_path: Path) -> Path:
    resp = httpx.get(url, timeout=300)
    resp.raise_for_status()
    out_path.write_bytes(resp.content)
    return out_path


def _file_data_uri(path: Path) -> str:
    mime = "image/png" if path.suffix.lower() == ".png" else "application/octet-stream"
    if path.suffix.lower() in {".mp4", ".mov", ".webm"}:
        mime = "video/mp4"
    return f"data:{mime};base64," + base64.b64encode(path.read_bytes()).decode()


def outpaint_image(image: Path, mask: Path, prompt: str, out_path: Path) -> Path:
    """Fill masked (transparent) regions of an image via a hosted Flux/SD model."""
    pred = _replicate_run(
        settings.replicate_outpaint_model,
        {
            "image": _file_data_uri(image),
            "mask": _file_data_uri(mask),
            "prompt": prompt or "extend the scene naturally, photorealistic, seamless",
        },
    )
    output = pred["output"]
    url = output[0] if isinstance(output, list) else output
    return _download(url, out_path)


def matte_video(video: Path, out_path: Path) -> Path:
    """Run robust video matting to isolate the foreground with alpha."""
    pred = _replicate_run(
        settings.replicate_matting_model,
        {"input_video": _file_data_uri(video), "output_type": "green-screen"},
    )
    output = pred["output"]
    url = output[0] if isinstance(output, list) else output
    return _download(url, out_path)
