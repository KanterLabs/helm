"""Private local copy of the original fence for a CLI admission."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import tempfile

import helm_session


def _admission_state_path(config: Config, session_id: str) -> Path:
    scope = "\0".join((config.base_url, config.token, session_id))
    key = hashlib.sha256(scope.encode()).hexdigest()
    return helm_session.default_state_dir() / "admission" / f"{key}.json"


def _read_agent_fence(config: Config, session_id: str, reference: str) -> str:
    path = _admission_state_path(config, session_id)
    try:
        helm_session._check_directory(path.parent, create=False)
        helm_session._check_file(path)
        try:
            with path.open("rb") as stream:
                raw = stream.read(4097)
        except FileNotFoundError:
            return ""
        if len(raw) > 4096:
            raise helm_session.SessionStateError("admission record exceeds the size limit")
        saved = json.loads(raw)
        if not isinstance(saved, dict):
            raise helm_session.SessionStateError("admission record is invalid")
        task_id = helm_session._safe_identifier(saved.get("task_id"), "task_id", required=True)
        task_key = helm_session._safe_identifier(saved.get("task_key", ""), "task_key")
        fence = helm_session._safe_identifier(saved.get("fence"), "fence", required=True, max_length=128)
        return fence if reference.casefold() in {task_id.casefold(), task_key.casefold()} else ""
    except (OSError, ValueError, helm_session.SessionStateError) as exc:
        raise HelmError(
            "Cannot read the private Helm admission record. Resume this task explicitly to recover admission.",
            error_code="admission_state_unavailable",
        ) from exc


def _save_agent_fence(config: Config, session_id: str, task: dict, fence: str) -> None:
    path = _admission_state_path(config, session_id)
    temp_name = None
    try:
        record = {
            "task_id": helm_session._safe_identifier(task.get("id"), "task_id", required=True),
            "task_key": helm_session._safe_identifier(task.get("key", ""), "task_key"),
            "fence": helm_session._safe_identifier(fence, "fence", required=True, max_length=128),
        }
        helm_session._check_directory(path.parent, create=True)
        with helm_session._locked(path.with_suffix(".lock")):
            helm_session._check_file(path)
            with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, delete=False) as stream:
                temp_name = stream.name
                os.fchmod(stream.fileno(), 0o600)
                json.dump(record, stream, separators=(",", ":"))
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temp_name, path)
            temp_name = None
    except (OSError, helm_session.SessionStateError) as exc:
        raise HelmError(
            "Helm admitted the task but could not save its local fence. Repair state storage and retry with the same operation ID.",
            error_code="admission_state_unavailable",
        ) from exc
    finally:
        if temp_name:
            try:
                Path(temp_name).unlink(missing_ok=True)
            except OSError:
                pass
