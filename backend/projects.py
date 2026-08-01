"""In-memory project registry.

A "project" is one uploaded video plus the chain of edits applied to it. State
is kept in memory (fine for a single-node dev server); the actual media lives
on disk under STORAGE_DIR so it survives restarts and can be re-registered.
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from .config import settings


@dataclass
class Project:
    id: str
    original: Path
    current: Path
    history: list[dict] = field(default_factory=list)

    @property
    def workspace(self) -> Path:
        d = settings.outputs_dir / self.id
        d.mkdir(parents=True, exist_ok=True)
        return d

    def to_public(self) -> dict:
        return {
            "id": self.id,
            "original_url": f"/media/{self.id}/{self.original.name}",
            "current_url": self._url(self.current),
            "history": self.history,
        }

    def _url(self, path: Path) -> str:
        try:
            rel = path.relative_to(settings.storage_dir)
            return f"/media/{rel.as_posix()}"
        except ValueError:
            return f"/media/{self.id}/{path.name}"


class ProjectStore:
    def __init__(self) -> None:
        self._projects: dict[str, Project] = {}

    def create(self, upload_name: str, data: bytes) -> Project:
        pid = uuid.uuid4().hex[:12]
        proj_dir = settings.uploads_dir / pid
        proj_dir.mkdir(parents=True, exist_ok=True)
        safe_name = Path(upload_name).name or "input.mp4"
        dest = proj_dir / safe_name
        dest.write_bytes(data)
        project = Project(id=pid, original=dest, current=dest)
        self._projects[pid] = project
        return project

    def register_path(self, path: Path) -> Project:
        """Register an existing file on disk as a new project (used for demos)."""
        pid = uuid.uuid4().hex[:12]
        project = Project(id=pid, original=path, current=path)
        self._projects[pid] = project
        return project

    def get(self, pid: str) -> Optional[Project]:
        return self._projects.get(pid)


store = ProjectStore()
