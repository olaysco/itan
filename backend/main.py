"""FastAPI application wiring the agent, tools, and web UI together."""
from __future__ import annotations

from pathlib import Path

from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from .agent import orchestrator
from .config import settings
from .projects import store

app = FastAPI(title="Heydit — Agentic AI Video Editor", version="0.1.0")
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

FRONTEND_DIR = Path(__file__).resolve().parent.parent / "frontend"

MAX_UPLOAD_BYTES = 512 * 1024 * 1024  # 512 MB
ALLOWED_SUFFIXES = {".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v"}


class ChatRequest(BaseModel):
    project_id: str
    message: str


@app.get("/api/health")
def health() -> dict:
    return {
        "status": "ok",
        "capabilities": settings.capability_report(),
        "model": settings.anthropic_model,
    }


@app.post("/api/upload")
async def upload(file: UploadFile = File(...)) -> dict:
    suffix = Path(file.filename or "").suffix.lower()
    if suffix and suffix not in ALLOWED_SUFFIXES:
        raise HTTPException(400, f"Unsupported file type '{suffix}'. Allowed: {sorted(ALLOWED_SUFFIXES)}")
    data = await file.read()
    if not data:
        raise HTTPException(400, "Empty upload.")
    if len(data) > MAX_UPLOAD_BYTES:
        raise HTTPException(413, "File too large (max 512 MB).")
    project = store.create(file.filename or "input.mp4", data)
    return project.to_public()


@app.post("/api/demo")
def demo() -> dict:
    """Create a project from a synthetic clip (no upload needed)."""
    if not settings.has_ffmpeg:
        raise HTTPException(400, "ffmpeg is required to generate a demo clip.")
    from .services import video_utils

    path = settings.uploads_dir / "demo.mp4"
    video_utils.make_placeholder_video(path, seconds=4.0, text="Heydit")
    project = store.register_path(path)
    return project.to_public()


@app.post("/api/chat")
def chat(req: ChatRequest) -> dict:
    project = store.get(req.project_id)
    if project is None:
        raise HTTPException(404, "Unknown project. Upload a video first.")
    if not req.message.strip():
        raise HTTPException(400, "Empty message.")

    result = orchestrator.run_agent(req.message, project.current, project.workspace)

    if result.get("current_video"):
        project.current = Path(result["current_video"])
    entry = {"message": req.message, "reply": result["reply"], "steps": result["steps"]}
    project.history.append(entry)

    return {
        "reply": result["reply"],
        "steps": result["steps"],
        "project": project.to_public(),
    }


@app.get("/media/{path:path}")
def media(path: str):
    """Serve any file under the storage dir (uploads + outputs)."""
    target = (settings.storage_dir / path).resolve()
    if not str(target).startswith(str(settings.storage_dir.resolve())):
        raise HTTPException(403, "Forbidden")
    if not target.exists() or not target.is_file():
        raise HTTPException(404, "Not found")
    return FileResponse(target)


@app.get("/", response_class=HTMLResponse)
def index() -> HTMLResponse:
    index_file = FRONTEND_DIR / "index.html"
    if index_file.exists():
        return HTMLResponse(index_file.read_text())
    return HTMLResponse("<h1>Heydit</h1><p>Frontend not found.</p>")


if FRONTEND_DIR.exists():
    app.mount("/static", StaticFiles(directory=FRONTEND_DIR), name="static")


def main() -> None:
    import uvicorn

    uvicorn.run("backend.main:app", host=settings.host, port=settings.port, reload=False)


if __name__ == "__main__":
    main()
