"""Select, admit and verify agent work on servers with atomic admission."""

from __future__ import annotations

import argparse
import os
import re
from typing import Any
from urllib.parse import quote, urlencode

import helm_session


_ADMISSION_REQUIRED_ENV = "HELM_REQUIRE_AGENT_ADMISSION"
_AGENT_SESSION_ENV = "HELM_AGENT_SESSION_ID"


def _agent_session_id(*, fallback: str = "", required: bool = False) -> str:
    explicit = os.environ.get(_AGENT_SESSION_ENV, "").strip()
    if explicit:
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}", explicit):
            raise HelmError("HELM_AGENT_SESSION_ID must contain 1 to 128 safe identifier characters")
        return explicit
    if fallback:
        return fallback
    runtime = helm_session.session_identifier()
    if runtime:
        return helm_session.session_key(runtime)
    if required:
        raise HelmError("Set HELM_AGENT_SESSION_ID to a unique, stable ID for this work session", error_code="session_required")
    return ""


def _uses_agent_admission(client: Client, *, required: bool = False) -> bool:
    # In-process integrations predating discovery may still supply a call-only
    # client. The CLI's HTTP client always negotiates explicitly.
    discover = getattr(client, "agent_work_admission", None)
    if discover is not None and discover():
        return True
    if required or os.environ.get(_ADMISSION_REQUIRED_ENV) == "1":
        raise HelmError("This Helm server does not advertise atomic agent admission", error_code="admission_unavailable")
    return False


def _admission_project_path(project_id: str) -> str:
    return f"/projects/{quote(project_id, safe='')}/agent-work"


def _admission_data(payload: Any) -> dict[str, Any]:
    data = payload.get("data") if isinstance(payload, dict) else None
    if not isinstance(data, dict):
        raise HelmError("Helm returned an unexpected agent admission response")
    return data


def _admission_intent(
    args: argparse.Namespace, *, next_action: str = "", finish_condition: str = "",
) -> dict[str, str]:
    intent = {
        "next_action": getattr(args, "next_action", None),
        "finish_condition": getattr(args, "finish_condition", None),
    }
    defaults = {"next_action": next_action, "finish_condition": finish_condition}
    missing = []
    for field, value in intent.items():
        intent[field] = (defaults[field] if value is None else value).strip()
        if not intent[field]:
            missing.append("--" + field.replace("_", "-"))
    if missing:
        raise HelmError(
            "Supply " + " and ".join(missing) + " with a concrete action and a verifiable finish condition",
            error_code="admission_details_required",
        )
    return intent


def _existing_admission_intent(task: dict[str, Any]) -> tuple[str, str]:
    next_action = _admission_text(task.get("next_action"))
    finish_condition = _admission_text(task.get("finish_condition"))
    checklist = task.get("checklist") or []
    open_item = next((
        text for item in checklist if isinstance(item, dict) and item.get("completed") is False
        if (text := _admission_text(item.get("text")))
    ), "")
    description = _admission_text(task.get("description"))
    # Older CLI-created cards have a Goal and an optional flat list of
    # checkpoints in the description. Use only that recognizable structure.
    heading, _, checkpoints = description.partition("\n\n")
    goal = heading.removeprefix("Goal: ").strip() if heading.startswith("Goal: ") else ""
    checkpoint_lines = checkpoints.splitlines()
    checkpoint = ""
    if checkpoint_lines and checkpoint_lines[0] in {"Checkpoints", "Acceptance criteria"}:
        checkpoint = next((line[6:].strip() for line in checkpoint_lines[1:] if line.startswith("- [ ] ") and line[6:].strip()), "")
    return next_action or open_item or checkpoint, finish_condition or goal


def _admission_text(value: Any) -> str:
    return value.strip() if isinstance(value, str) else ""


def _admit_agent_work(
    client: Client,
    args: argparse.Namespace,
    project_id: str,
    target: dict[str, Any],
    intent: dict[str, str],
) -> dict[str, Any]:
    session_id = _agent_session_id(required=True)
    path = _admission_project_path(project_id) + "/admit"
    body = {**target, **intent, "session_id": session_id, "lease_seconds": args.lease_seconds}
    # An operation identifies the request even if the caller supplies
    # different intent on a retry. Let the server reject that mismatch.
    key = _derived_uuid4("\0".join(("agent-work/admit", session_id, args.operation_id)))
    try:
        receipt, _ = client.call("POST", path, body=body, idempotency_key=key)
    except HelmError as exc:
        if exc.status_code is not None and exc.status_code < 500:
            raise
        raise _admission_retry_error(exc, args.operation_id) from exc
    try:
        return _finish_agent_admission(
            client, _admission_data(receipt), args.operation_id, project_id, path,
            session_id, action="started" if "title" in target else "resumed",
        )
    except HelmError as exc:
        raise _admission_retry_error(exc, args.operation_id) from exc


def _admission_retry_error(exc: HelmError, operation_id: str) -> HelmError:
    return HelmError(
        f"Operation ID {operation_id}. {exc}", status_code=exc.status_code,
        error_code=exc.error_code, retry_after=exc.retry_after,
    )


def _finish_agent_admission(
    client: Client, admitted: dict[str, Any], operation_id: str, project_id: str,
    path: str, session_id: str, *, action: str,
) -> dict[str, Any]:
    task = admitted.get("task")
    identity = _task_identity(task, project_id)
    fence = _admission_text(admitted.get("fence"))
    if (
        identity is None
        or admitted.get("action") != action
        or admitted.get("session_id") != session_id
        or not re.fullmatch(r"[0-9a-f]{32}", fence)
    ):
        raise HelmError("Helm returned an incomplete agent admission receipt")

    # An idempotent POST may replay a receipt after the lease has ended, or
    # after the same session has been admitted again. Verify the live fence
    # before persisting it and before returning authorization to work.
    current_path = path + "?" + urlencode({"session_id": session_id})
    live_task = _current_agent_task(client, current_path, identity, session_id, fence)
    _save_agent_fence(client.config, session_id, live_task, fence)

    warnings = []
    try:
        current_task = _task(client, live_task["id"])
    except HelmError:
        current_task = {}
        warnings.append("Could not refresh task details. Read the card before continuing.")
    try:
        notes = _agent_notes(client, live_task["id"])
    except HelmError:
        notes = []
        warnings.append("Could not read active agent notes. Run notes list before continuing.")
    live_task = _current_agent_task(client, current_path, identity, session_id, fence)
    current_task.update(live_task)
    work = current_task.get("agent_work")
    _record_session_start(
        current_task, project_id, operation_id,
        snapshot_ready=isinstance(work, dict) and work.get("operation_id") == operation_id
        and bool(work.get("actor_id")) and work.get("actor_id") == live_task.get("claimed_by"),
    )
    result = {
        "task": current_task, "action": action, "session_id": session_id,
        "next_action": admitted.get("next_action"), "finish_condition": admitted.get("finish_condition"),
        "agent_notes": notes, "operation_id": operation_id,
    }
    if warnings:
        result["warnings"] = warnings
    return result


def _current_agent_task(
    client: Client, path: str, identity: tuple[str, str, str], session_id: str, fence: str,
) -> dict[str, Any]:
    current, _ = client.call("GET", path)
    live = _admission_data(current)
    live_task = live.get("task")
    if (
        _task_identity(live_task, identity[2]) != identity
        or live.get("session_id") != session_id
        or live.get("fence") != fence
        or not isinstance(live_task.get("version"), int)
    ):
        raise HelmError("The recorded agent admission is no longer current", error_code="agent_admission_lost")
    return live_task


def cmd_select_next(client: Client, args: argparse.Namespace) -> dict[str, Any]:
    _uses_agent_admission(client, required=True)
    project = _project(client, args.project)
    path = _admission_project_path(str(project["id"])) + "/eligibility"
    payload, _ = client.call("GET", path)
    data = _admission_data(payload)
    tasks = data.get("tasks")
    if not isinstance(tasks, list) or any(not isinstance(task, dict) or not _admission_text(task.get("key")) for task in tasks):
        raise HelmError("Helm returned an unexpected eligible task list")
    result = {"task": tasks[0] if tasks else None}
    if reason := _admission_text(data.get("reason")):
        result["reason"] = reason
    elif not tasks:
        result["reason"] = "no_eligible_task"
    return result
