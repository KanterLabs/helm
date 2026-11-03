"""Best-effort local lifecycle state persistence."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import stat
import sys
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib import error, parse, request

import helm_session



def _session_store() -> helm_session.StateStore | None:
    """Return the current session store, or ``None`` when no ID is exposed."""

    try:
        return helm_session.StateStore.from_env()
    except helm_session.SessionStateError as exc:
        _state_warning(exc)
        return None


def _state_warning(exc: BaseException) -> None:
    # Storage is advisory for the network CLI. Never turn an otherwise
    # successful Helm mutation into a failure, and never print a path or
    # caller-provided metadata in the warning.
    print(helm_session.warning(exc), file=sys.stderr)


def _task_identity(task: Any, project_id: str = "") -> tuple[str, str, str] | None:
    if not isinstance(task, dict):
        return None
    task_id = task.get("id")
    if not isinstance(task_id, str) or not task_id.strip():
        return None
    task_key = task.get("key", "")
    if not isinstance(task_key, str):
        task_key = ""
    resolved_project = task.get("project_id")
    if not isinstance(resolved_project, str) or not resolved_project.strip():
        resolved_project = project_id
    if not isinstance(resolved_project, str) or not resolved_project.strip():
        return None
    return task_id.strip(), task_key.strip(), resolved_project.strip()


def _record_session_start(task: Any, project_id: str, operation_id: str, *, snapshot_ready: bool = False) -> None:
    store = _session_store()
    if store is None:
        return
    identity = _task_identity(task, project_id)
    if identity is None:
        _state_warning(helm_session.SessionStateError("task identity is unavailable"))
        return
    task_id, task_key, resolved_project = identity
    try:
        store.save(
            helm_session.SessionState(
                task_id=task_id,
                task_key=task_key,
                project_id=resolved_project,
                operation_id=operation_id,
                agent_state="working",
                last_progress_at=helm_session.timestamp(),
                snapshot_ready=snapshot_ready,
            )
        )
    except helm_session.SessionStateError as exc:
        _state_warning(exc)


def _record_session_progress(task: Any, previous: Any, payload: dict[str, Any]) -> None:
    store = _session_store()
    if store is None:
        return
    project_id = ""
    if isinstance(task, dict) and isinstance(task.get("project_id"), str):
        project_id = task["project_id"]
    if not project_id and isinstance(previous, dict) and isinstance(previous.get("project_id"), str):
        project_id = previous["project_id"]
    identity = _task_identity(task, project_id) or _task_identity(previous, project_id)
    if identity is None:
        return
    task_id, task_key, resolved_project = identity
    try:
        store.save(
            helm_session.SessionState(
                task_id=task_id,
                task_key=task_key,
                project_id=resolved_project,
                operation_id=str(payload.get("operation_id", "")),
                agent_state=str(payload.get("state", "working")),
                checkpoint_completed=payload.get("checkpoint_completed"),
                checkpoint_total=payload.get("checkpoint_total"),
                last_progress_at=helm_session.timestamp(),
                snapshot_ready=True,
            )
        )
    except helm_session.SessionStateError as exc:
        _state_warning(exc)


def _record_session_heartbeat(task: Any, args: argparse.Namespace) -> None:
    store = _session_store()
    if store is None:
        return
    try:
        current = store.load()
        if current is None or current.operation_id != args.operation_id.strip():
            return
        response_task = task
        if isinstance(task, dict) and isinstance(task.get("task"), dict):
            response_task = task["task"]
        response_id = response_task.get("id") if isinstance(response_task, dict) else None
        reference = str(args.task).strip()
        if isinstance(response_id, str) and response_id.strip():
            if current.task_id != response_id.strip():
                return
        elif reference not in {current.task_id, current.task_key}:
            return
        store.update(last_heartbeat_at=helm_session.timestamp())
    except helm_session.SessionStateError as exc:
        _state_warning(exc)


def _clear_matching_session(task: Any, operation_id: str | None = None) -> None:
    store = _session_store()
    if store is None:
        return
    if not isinstance(task, dict) or not isinstance(task.get("id"), str):
        return
    try:
        store.clear_matching(task_id=task["id"], operation_id=operation_id)
    except helm_session.SessionStateError as exc:
        _state_warning(exc)


