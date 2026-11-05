"""Task creation, backlog planning, and resume claims."""

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



def cmd_start(client: Client, args: argparse.Namespace) -> Any:
    args.operation_id = _command_operation_id(getattr(args, "operation_id", None))
    project = _project(client, args.project)
    project_id = str(project["id"])
    columns, _ = client.call("GET", f"/projects/{parse.quote(project_id, safe='')}/columns?limit=200")
    active = next((item for item in _data(columns) if item.get("semantic_state") == "active"), None)
    if not active:
        raise HelmError(f"project has no active column: {args.project}")
    description = "Goal: " + args.goal.strip()
    if args.step:
        description += "\n\nCheckpoints\n" + "\n".join(f"- [ ] {step.strip()}" for step in args.step)
    create_path = f"/projects/{parse.quote(project_id, safe='')}/tasks"
    create_body = {
        "title": args.title.strip(),
        "description": description,
        "column_id": active["id"],
        "priority": args.priority,
    }
    created, _ = client.call(
        "POST",
        create_path,
        body=create_body,
        idempotency_key=_command_mutation_id(args.operation_id, "POST", create_path, create_body),
    )
    if not isinstance(created, dict) or not isinstance(created.get("version"), int):
        raise HelmError("Helm returned an unexpected created task")
    claim_path = "/tasks/" + parse.quote(str(created["id"]), safe="") + "/claim"
    claim_body = {"lease_seconds": args.lease_seconds}
    claimed, _ = client.call(
        "POST",
        claim_path,
        body=claim_body,
        if_match=created["version"],
        idempotency_key=_command_mutation_id(args.operation_id, "POST", claim_path, claim_body),
    )
    if not isinstance(claimed, dict) or not isinstance(claimed.get("version"), int):
        raise HelmError("Helm returned an unexpected claimed task")
    _record_session_start(claimed, project_id, args.operation_id)
    return {"task": claimed, "agent_notes": _agent_notes(client, str(claimed["id"])), "operation_id": args.operation_id}


def cmd_backlog(client: Client, args: argparse.Namespace) -> Any:
    args.operation_id = _command_operation_id(getattr(args, "operation_id", None))
    project = _project(client, args.project)
    project_id = str(project["id"])
    columns, _ = client.call("GET", f"/projects/{parse.quote(project_id, safe='')}/columns?limit=200")
    backlog = next((item for item in _data(columns) if item.get("semantic_state") == "backlog"), None)
    if not backlog:
        raise HelmError(f"project has no backlog column: {args.project}")
    description = "Goal: " + args.goal.strip()
    if args.step:
        description += "\n\nAcceptance criteria\n" + "\n".join(f"- [ ] {step.strip()}" for step in args.step)
    create_path = f"/projects/{parse.quote(project_id, safe='')}/tasks"
    create_body = {"title": args.title.strip(), "description": description, "column_id": backlog["id"], "priority": args.priority}
    created, _ = client.call(
        "POST",
        create_path,
        body=create_body,
        idempotency_key=_command_mutation_id(args.operation_id, "POST", create_path, create_body),
    )
    if not isinstance(created, dict) or not isinstance(created.get("version"), int):
        raise HelmError("Helm returned an unexpected created task")
    return {"task": created, "operation_id": args.operation_id}


def cmd_resume(client: Client, args: argparse.Namespace) -> Any:
    args.operation_id = _command_operation_id(getattr(args, "operation_id", None))
    current = _task(client, args.task)
    # The claim endpoint is intentionally used for both an unclaimed task and
    # a same-owner reclaim. The server treats a same-owner POST /claim as a
    # lease renewal, which makes a retry safe after a lost response and avoids
    # making a race-prone claim/renew decision from a stale GET.
    task_id = str(current["id"])
    claim_path = "/tasks/" + parse.quote(task_id, safe="") + "/claim"
    claim_body = {"lease_seconds": args.lease_seconds}
    claimed, _ = client.call(
        "POST",
        claim_path,
        body=claim_body,
        if_match=current["version"],
        idempotency_key=_command_mutation_id(args.operation_id, "POST", claim_path, claim_body),
    )
    if not isinstance(claimed, dict) or not isinstance(claimed.get("version"), int):
        raise HelmError("Helm returned an unexpected claimed task")

    # Claiming does not alter board placement. Resolve the project's semantic
    # active column and move the task with an optimistic PATCH only when the
    # current column is not already active. A claim conflict from another actor
    # raises before this block, so a foreign active claim is never moved.
    project_id = str(current.get("project_id") or claimed.get("project_id") or "")
    if not project_id:
        raise HelmError("Helm returned an unexpected task project")
    current_column = claimed.get("column_id", current.get("column_id"))
    current_semantic = claimed.get("semantic_state", current.get("semantic_state"))
    if current_semantic is None:
        for candidate in (claimed, current):
            nested_column = candidate.get("column") if isinstance(candidate, dict) else None
            if isinstance(nested_column, dict) and nested_column.get("semantic_state") is not None:
                current_semantic = nested_column.get("semantic_state")
                break
    if current_semantic == "active":
        moved = claimed
    else:
        columns_path = f"/projects/{parse.quote(project_id, safe='')}/columns?limit=200"
        columns, _ = client.call("GET", columns_path)
        active = next((item for item in _data(columns) if item.get("semantic_state") == "active"), None)
        if not active or not isinstance(active.get("id"), str) or not active["id"].strip():
            raise HelmError("project has no active column")

        active_id = active["id"]
        if current_column == active_id:
            moved = claimed
        else:
            patch_path = "/tasks/" + parse.quote(task_id, safe="")
            patch_body = {"column_id": active_id}
            moved, _ = client.call(
                "PATCH",
                patch_path,
                body=patch_body,
                if_match=claimed["version"],
                idempotency_key=_command_mutation_id(args.operation_id, "PATCH", patch_path, patch_body),
            )
            if not isinstance(moved, dict) or not isinstance(moved.get("version"), int):
                raise HelmError("Helm returned an unexpected moved task")
    snapshot_ready = False
    for candidate in (moved, claimed):
        if not isinstance(candidate, dict) or not isinstance(candidate.get("agent_work"), dict):
            continue
        work = candidate["agent_work"]
        if work.get("operation_id") != args.operation_id:
            continue
        claimed_by = candidate.get("claimed_by")
        work_actor = work.get("actor_id")
        if isinstance(claimed_by, str) and isinstance(work_actor, str) and claimed_by.strip() != work_actor.strip():
            continue
        snapshot_ready = True
        break
    _record_session_start(moved, project_id, args.operation_id, snapshot_ready=snapshot_ready)
    return {"task": moved, "agent_notes": _agent_notes(client, task_id), "operation_id": args.operation_id}


