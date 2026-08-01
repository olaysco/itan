"""Anthropic client helpers: the agentic tool-use loop and grammar fixing.

Kept separate from the orchestrator so the same client can be reused by the
audio-grammar tool (which just needs a one-shot text correction) and by the
agent (which needs the full tool-use loop).
"""
from __future__ import annotations

from typing import Any, Optional

from ..config import settings

try:
    import anthropic
except Exception:  # pragma: no cover
    anthropic = None


def _client():
    if anthropic is None:
        raise RuntimeError("anthropic package not installed")
    if not settings.anthropic_api_key:
        raise RuntimeError("ANTHROPIC_API_KEY not set")
    return anthropic.Anthropic(api_key=settings.anthropic_api_key)


def create_message(
    system: str,
    messages: list[dict[str, Any]],
    tools: Optional[list[dict]] = None,
    max_tokens: int = 1024,
) -> Any:
    """Single call to the Messages API. Returns the raw response object."""
    client = _client()
    kwargs: dict[str, Any] = {
        "model": settings.anthropic_model,
        "max_tokens": max_tokens,
        "system": system,
        "messages": messages,
    }
    if tools:
        kwargs["tools"] = tools
    return client.messages.create(**kwargs)


def fix_grammar(text: str) -> str:
    """Return a grammar-corrected version of `text`.

    Uses the cloud model when available; otherwise a small deterministic
    rule-based cleanup so the audio tool still produces a sensible result.
    """
    if not settings.agent_live:
        return _stub_grammar(text)
    resp = create_message(
        system=(
            "You are a meticulous copy editor for spoken-word transcripts. "
            "Fix grammar, subject-verb agreement, tense, and word-choice errors. "
            "Preserve the speaker's meaning, tone, and contractions. Do not add "
            "or remove sentences. Return ONLY the corrected text, nothing else."
        ),
        messages=[{"role": "user", "content": text}],
        max_tokens=1024,
    )
    parts = [b.text for b in resp.content if getattr(b, "type", None) == "text"]
    corrected = "".join(parts).strip()
    return corrected or text


def _stub_grammar(text: str) -> str:
    """Tiny rule-based grammar cleanup used when no cloud model is configured."""
    import re

    fixes = {
        r"\bi\b": "I",
        r"\bdont\b": "don't",
        r"\bcant\b": "can't",
        r"\bwont\b": "won't",
        r"\bim\b": "I'm",
        r"\bhe don't\b": "he doesn't",
        r"\bshe don't\b": "she doesn't",
        r"\bit don't\b": "it doesn't",
        r"\bwe was\b": "we were",
        r"\bthey was\b": "they were",
        r"\byou was\b": "you were",
        r"\bthere is (\w+ )?(\w+s)\b": r"there are \1\2",
        r"\ba apple\b": "an apple",
        r"\bmore better\b": "better",
    }
    out = text
    for pattern, repl in fixes.items():
        out = re.sub(pattern, repl, out, flags=re.IGNORECASE if pattern.startswith(r"\bi\b") is False else 0)
    # capitalise first letter of each sentence
    out = re.sub(r"(^|[.!?]\s+)([a-z])", lambda m: m.group(1) + m.group(2).upper(), out)
    return out.strip()
