"""Central configuration, loaded from environment / .env file.

Every provider key is optional. When a key is missing (or FORCE_STUB=1) the
matching capability degrades to a deterministic local stub so the application
still runs end to end without any cloud credentials.
"""
from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from pathlib import Path

try:
    from dotenv import load_dotenv

    load_dotenv()
except Exception:  # pragma: no cover - dotenv is optional at runtime
    pass


def _flag(name: str, default: str = "0") -> bool:
    return os.getenv(name, default).strip().lower() in {"1", "true", "yes", "on"}


@dataclass
class Settings:
    # Agentic brain
    anthropic_api_key: str = field(default_factory=lambda: os.getenv("ANTHROPIC_API_KEY", "").strip())
    anthropic_model: str = field(default_factory=lambda: os.getenv("ANTHROPIC_MODEL", "claude-opus-4-8").strip())

    # Audio (speech <-> text)
    openai_api_key: str = field(default_factory=lambda: os.getenv("OPENAI_API_KEY", "").strip())
    openai_stt_model: str = field(default_factory=lambda: os.getenv("OPENAI_STT_MODEL", "whisper-1").strip())
    openai_tts_model: str = field(default_factory=lambda: os.getenv("OPENAI_TTS_MODEL", "gpt-4o-mini-tts").strip())
    openai_tts_voice: str = field(default_factory=lambda: os.getenv("OPENAI_TTS_VOICE", "alloy").strip())

    # Visual generative models
    replicate_api_token: str = field(default_factory=lambda: os.getenv("REPLICATE_API_TOKEN", "").strip())
    replicate_outpaint_model: str = field(
        default_factory=lambda: os.getenv("REPLICATE_OUTPAINT_MODEL", "black-forest-labs/flux-fill-dev").strip()
    )
    replicate_matting_model: str = field(
        default_factory=lambda: os.getenv("REPLICATE_MATTING_MODEL", "arielreplicate/robust_video_matting").strip()
    )

    # Server / storage
    host: str = field(default_factory=lambda: os.getenv("HOST", "0.0.0.0").strip())
    port: int = field(default_factory=lambda: int(os.getenv("PORT", "8000")))
    storage_dir: Path = field(default_factory=lambda: Path(os.getenv("STORAGE_DIR", "storage")))
    force_stub: bool = field(default_factory=lambda: _flag("FORCE_STUB"))

    def __post_init__(self) -> None:
        self.uploads_dir = self.storage_dir / "uploads"
        self.outputs_dir = self.storage_dir / "outputs"
        self.uploads_dir.mkdir(parents=True, exist_ok=True)
        self.outputs_dir.mkdir(parents=True, exist_ok=True)

    # --- capability flags -------------------------------------------------
    @property
    def has_ffmpeg(self) -> bool:
        return shutil.which("ffmpeg") is not None and shutil.which("ffprobe") is not None

    def use_real(self, key: str) -> bool:
        """Whether a given provider should make real cloud calls."""
        if self.force_stub:
            return False
        return bool(key)

    @property
    def agent_live(self) -> bool:
        return self.use_real(self.anthropic_api_key)

    @property
    def audio_live(self) -> bool:
        return self.use_real(self.openai_api_key)

    @property
    def visual_live(self) -> bool:
        return self.use_real(self.replicate_api_token)

    def capability_report(self) -> dict:
        return {
            "agent": "cloud" if self.agent_live else "stub",
            "audio": "cloud" if self.audio_live else "stub",
            "visual": "cloud" if self.visual_live else "stub",
            "ffmpeg": self.has_ffmpeg,
            "force_stub": self.force_stub,
        }


settings = Settings()
