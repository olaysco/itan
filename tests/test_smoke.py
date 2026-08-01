"""Smoke tests that run without any cloud keys or ffmpeg.

They exercise the deterministic, offline paths: aspect parsing, colour parsing,
intent routing, the rule-based grammar fixer, and the FastAPI surface.
"""
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

os.environ["FORCE_STUB"] = "1"  # force everything offline for deterministic tests

from backend.agent import tools  # noqa: E402
from backend.services import frame_expand  # noqa: E402
from backend.services.background import _parse_color  # noqa: E402
from backend.services.llm import _stub_grammar  # noqa: E402


def test_parse_target_aspect():
    assert abs(frame_expand.parse_target_aspect("make it 16:9", 1.0) - 16 / 9) < 1e-6
    assert abs(frame_expand.parse_target_aspect("square please", 1.77) - 1.0) < 1e-6
    assert abs(frame_expand.parse_target_aspect("vertical for stories", 1.77) - 9 / 16) < 1e-6
    assert abs(frame_expand.parse_target_aspect("cinematic 2.39", 1.0) - 2.39) < 1e-6
    # unknown -> widen from current
    assert frame_expand.parse_target_aspect("do something", 1.0) >= 16 / 9


def test_parse_color():
    assert _parse_color("make the background blue") == "#1e3a8a"
    assert _parse_color("use #a1b2c3 please") == "#a1b2c3"
    assert _parse_color("put me on a beach") == "#38bdf8"
    assert _parse_color("no colour mentioned here") is None


def test_intent_routing():
    assert tools.infer_tool("expand the frame to 16:9")[0] == "expand_frame"
    assert tools.infer_tool("fix the grammar in the audio")[0] == "correct_audio_grammar"
    assert tools.infer_tool("change the background to a beach")[0] == "change_background"
    assert tools.infer_tool("what's the weather") is None


def test_stub_grammar_fixes_agreement():
    assert "doesn't" in _stub_grammar("he don't care")
    assert _stub_grammar("we was there").startswith("We were")
    assert "I" in _stub_grammar("i went home")


def test_tool_schemas_wellformed():
    for schema in tools.TOOL_SCHEMAS:
        assert set(schema) >= {"name", "description", "input_schema"}
        assert schema["input_schema"]["type"] == "object"
    assert tools.TOOL_NAMES == ["expand_frame", "correct_audio_grammar", "change_background"]


def test_health_endpoint():
    from fastapi.testclient import TestClient

    from backend.main import app

    client = TestClient(app)
    res = client.get("/api/health")
    assert res.status_code == 200
    body = res.json()
    assert body["status"] == "ok"
    assert "capabilities" in body


def test_chat_requires_project():
    from fastapi.testclient import TestClient

    from backend.main import app

    client = TestClient(app)
    res = client.post("/api/chat", json={"project_id": "nope", "message": "hi"})
    assert res.status_code == 404
