"""Tool: correct a grammar error in the spoken audio of a video.

Pipeline:
  1. Extract the audio track (ffmpeg).
  2. Transcribe it to text (cloud STT / Whisper).
  3. Fix grammar in the transcript (cloud LLM).
  4. Re-synthesize the corrected speech (cloud TTS).
  5. Mux the new audio back onto the original video (ffmpeg).

Every cloud step degrades to a stub: without STT/TTS keys the tool still
reports the correction it *would* apply, and (when possible) still remuxes so
you get a rendered file back.
"""
from __future__ import annotations

from pathlib import Path

from ..config import settings
from . import llm, providers, video_utils


def run(video: Path, workspace: Path, instruction: str = "") -> dict:
    workspace.mkdir(parents=True, exist_ok=True)

    if not settings.has_ffmpeg:
        return {
            "mode": "stub",
            "output_video": None,
            "summary": "ffmpeg is not installed, so I couldn't process the audio track.",
        }

    info = video_utils.probe(video)
    if not info.has_audio:
        return {
            "mode": "stub",
            "output_video": None,
            "summary": "This video has no audio track, so there's no speech grammar to correct.",
        }

    audio = video_utils.extract_audio(video, workspace / "original.wav")

    # 1. Transcribe -------------------------------------------------------
    if settings.audio_live and audio:
        try:
            transcript = providers.transcribe_audio(audio)
            stt_mode = "cloud"
        except Exception as exc:
            transcript = f"[transcription unavailable: {exc}]"
            stt_mode = "stub"
    else:
        transcript = "[transcription requires OPENAI_API_KEY — showing grammar tooling on placeholder text]"
        stt_mode = "stub"

    # 2. Fix grammar (works even in stub mode via rule-based cleanup) ------
    corrected = llm.fix_grammar(transcript) if not transcript.startswith("[") else transcript
    grammar_mode = "cloud" if settings.agent_live else "stub"
    changed = corrected != transcript and not transcript.startswith("[")

    # 3 + 4. Re-synthesize and remux -------------------------------------
    out_video = workspace / "grammar_fixed.mp4"
    tts_mode = "skipped"
    if changed and settings.audio_live:
        try:
            new_audio = providers.synthesize_speech(corrected, workspace / "corrected.wav")
            video_utils.replace_audio(video, new_audio, out_video)
            tts_mode = "cloud"
        except Exception as exc:
            out_video = None
            tts_mode = f"failed ({exc})"
    else:
        # No new audio to synthesize (either no change, or no TTS key).
        out_video = None

    summary_lines = []
    if changed:
        summary_lines.append("Corrected the spoken grammar.")
    elif not transcript.startswith("["):
        summary_lines.append("The transcript was already grammatically clean — no changes needed.")
    else:
        summary_lines.append("Ran the grammar pipeline in stub mode (add OPENAI_API_KEY to transcribe real audio).")
    if tts_mode == "cloud":
        summary_lines.append("Re-voiced the audio and muxed it back onto the video.")
    elif changed:
        summary_lines.append("Add OPENAI_API_KEY to re-synthesize and remux the corrected speech.")

    return {
        "mode": stt_mode,
        "output_video": str(out_video) if out_video else None,
        "summary": " ".join(summary_lines),
        "details": {
            "transcript": transcript,
            "corrected": corrected,
            "changed": changed,
            "stt": stt_mode,
            "grammar": grammar_mode,
            "tts": tts_mode,
        },
    }
