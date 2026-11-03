"""Structured progress, legacy comments, and terminal actions."""

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



def cmd_progress(client: Client, args: argparse.Namespace) -> Any:
    args.operation_id = _command_operation_id(getattr(args, "operation_id", None))
    current = _task(client, args.task)
    if (getattr(args, "images", None) or getattr(args, "attachment_ids", None)) and not _structured_progress_supplied(args):
        raise HelmError("images require structured progress; supply --state or use comment")
    if _structured_progress_supplied(args):
        if not _active_claim(current):
            raise HelmError("structured progress requires an actively claimed task")
        payload = _structured_progress_payload(args)
        images = _update_images(client, args, current["id"])
        if images:
            payload["attachment_ids"] = [item["id"] for item in images]
        path = "/tasks/" + parse.quote(str(current["id"]), safe="") + "/progress"
        try:
            updated, _ = client.call(
                "POST", path, body=payload, if_match=current["version"],
                idempotency_key=_command_mutation_id(args.operation_id, "POST", path, payload),
            )
        except HelmError as exc:
            if images:
                raise AttachmentError(str(exc), payload["attachment_ids"], args.operation_id) from None
            raise
        if not isinstance(updated, dict) or not isinstance(updated.get("version"), int):
            raise HelmError("Helm returned an unexpected updated task")
        _record_session_progress(updated, current, payload)
        return {"task": updated, "operation_id": args.operation_id}
    message = _progress_message(args)
    path = "/tasks/" + parse.quote(str(current["id"]), safe="") + "/comments"
    comment_body = {"body": message}
    comment, _ = client.call(
        "POST",
        path,
        body=comment_body,
        idempotency_key=_command_mutation_id(args.operation_id, "POST", path, comment_body),
    )
    return {"task": current.get("key", current["id"]), "comment": comment}


def cmd_heartbeat(client: Client, args: argparse.Namespace) -> Any:
    """Refresh agent-work liveness without changing the task version.

    Heartbeats deliberately omit both If-Match and Idempotency-Key. The server
    operation is an inherently idempotent timestamp touch, and persisting an
    idempotency row for every hook pulse would create unnecessary write load.
    """

    args.operation_id = _validate_operation_id(args.operation_id)
    task_id = parse.quote(str(args.task), safe="")
    path = "/tasks/" + task_id + "/heartbeat"
    body = {"operation_id": args.operation_id}
    payload, _ = client.call("POST", path, body=body)
    current = payload if isinstance(payload, dict) else {"id": args.task}
    _record_session_heartbeat(current, args)
    return {"task": payload, "operation_id": args.operation_id}


def _structured_progress_supplied(args: argparse.Namespace) -> bool:
    """Return whether the caller selected the structured progress API."""

    for field in ("state", "phase", "completed", "total", "next_step"):
        if getattr(args, field, None) is not None:
            return True
    refs = getattr(args, "checkpoint_refs", ())
    return isinstance(refs, (list, tuple)) and bool(refs)


def _active_claim(task: dict[str, Any]) -> bool:
    """Check the claim data available in a task response before posting."""

    claimed_by = task.get("claimed_by")
    if not isinstance(claimed_by, str) or not claimed_by.strip():
        return False
    # Older task representations may expose the claimant without the lease
    # timestamp. Let the progress endpoint remain authoritative in that case,
    # while rejecting an explicitly absent, malformed, or expired lease.
    if "claim_expires_at" not in task:
        return True
    expires_at = task.get("claim_expires_at")
    if not isinstance(expires_at, str) or not expires_at.strip():
        return False
    try:
        expires = datetime.fromisoformat(expires_at.replace("Z", "+00:00"))
    except ValueError:
        return False
    if expires.tzinfo is None:
        expires = expires.replace(tzinfo=timezone.utc)
    return expires > datetime.now(timezone.utc)


def _structured_progress_payload(args: argparse.Namespace) -> dict[str, Any]:
    completed = getattr(args, "completed", None)
    total = getattr(args, "total", None)
    if (completed is None) != (total is None):
        raise HelmError("completed and total must be provided together")
    if completed is not None and (total < 1 or total > 100 or completed < 0 or completed > total):
        raise HelmError("progress must satisfy 0 <= completed <= total <= 100")

    operation_id = _validate_operation_id(getattr(args, "operation_id", ""))
    state = getattr(args, "state", None)
    message = getattr(args, "message", "")
    payload: dict[str, Any] = {
        "operation_id": operation_id.strip(),
        "state": state or "working",
        "summary": message.strip(),
    }
    phase = getattr(args, "phase", None)
    if phase is not None:
        phase = phase.strip()
        if not phase:
            raise HelmError("phase must not be empty")
        payload["phase"] = phase
    next_step = getattr(args, "next_step", None)
    if next_step is not None:
        next_step = next_step.strip()
        if not next_step:
            raise HelmError("next action must not be empty")
        payload["next_action"] = next_step
    if completed is not None:
        payload["checkpoint_completed"] = completed
        payload["checkpoint_total"] = total

    refs = getattr(args, "checkpoint_refs", ())
    if not isinstance(refs, (list, tuple)):
        refs = ()
    if refs:
        normalized_refs = [ref.strip() if isinstance(ref, str) else "" for ref in refs]
        if any(not isinstance(ref, str) or not ref for ref in normalized_refs):
            raise HelmError("checkpoint references must be non-empty")
        if len(normalized_refs) > 100:
            raise HelmError("checkpoint references must contain at most 100 items")
        if len(set(normalized_refs)) != len(normalized_refs):
            raise HelmError("checkpoint references must not contain duplicates")
        if any(len(ref) > 128 for ref in normalized_refs):
            raise HelmError("checkpoint references must be at most 128 characters")
        if completed is None:
            raise HelmError("checkpoint counts are required when checkpoint refs are provided")
        if len(normalized_refs) != total:
            raise HelmError("checkpoint refs must match checkpoint total")
        payload["checkpoint_refs"] = normalized_refs
    return payload


def _canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def _progress_message(args: argparse.Namespace) -> str:
    completed = args.completed
    total = args.total
    if (completed is None) != (total is None):
        raise HelmError("completed and total must be provided together")
    if completed is not None and (total < 1 or completed < 0 or completed > total):
        raise HelmError("progress must satisfy 0 <= completed <= total and total >= 1")
    if not any((args.state, args.phase, args.next_step, completed is not None)):
        return args.message.strip()
    heading = ["Agent update"]
    if args.state:
        heading.append(args.state.capitalize())
    if args.phase:
        heading.append(args.phase.strip())
    if completed is not None:
        heading.append(f"{completed}/{total}")
    lines = [" · ".join(heading), "", args.message.strip()]
    if args.next_step:
        lines.extend(("", "Next: " + args.next_step.strip()))
    return "\n".join(lines)


def _action(client: Client, args: argparse.Namespace, action: str, field: str) -> Any:
    args.operation_id = _command_operation_id(getattr(args, "operation_id", None))
    current = _task(client, args.task)
    value = getattr(args, field)
    path = "/tasks/" + parse.quote(str(current["id"]), safe="") + "/" + action
    body = {field: value.strip()}
    payload, _ = client.call(
        "POST",
        path,
        body=body,
        if_match=current["version"],
        idempotency_key=_command_mutation_id(args.operation_id, "POST", path, body),
    )
    # Terminal actions use a fresh operation ID after progress may have
    # written a different one locally. Match the task only so successful
    # completion/block actions always clear that task's recovery record.
    if action in {"complete", "block"}:
        _clear_matching_session(current, operation_id=None)
    else:
        _clear_matching_session(current, args.operation_id)
    return {"task": payload, "operation_id": args.operation_id}


def cmd_config_check(client: Client, _args: argparse.Namespace) -> dict[str, Any]:
    """Describe validated local configuration without making a request."""

    config = client.config
    return {
        "origin": config.base_url,
        "url_source": config.url_source,
        "config_source": config.config_source,
        "application_token_present": bool(config.token),
        "cloudflare_pair_present": bool(config.cf_access_client_id and config.cf_access_client_secret),
    }


