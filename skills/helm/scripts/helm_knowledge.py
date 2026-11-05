"""Issue intake, triage, and reusable task notes."""

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



def _issue_query_params(args: argparse.Namespace) -> list[tuple[str, str]]:
    params: list[tuple[str, str]] = []
    fields = (
        ("project", "project"),
        ("state", "state"),
        ("column", "column"),
        ("priority", "priority"),
        ("severity", "severity"),
        ("label", "label"),
        ("assignee", "assignee"),
        ("reporter", "reporter"),
        ("resolution", "resolution"),
        ("agent_state", "agent_state"),
        ("query", "q"),
        ("updated_after", "updated_after"),
    )
    for source, target in fields:
        value = getattr(args, source, None)
        if value is not None and str(value).strip():
            params.append((target, str(value).strip()))
    action_needed = getattr(args, "action_needed", None)
    if action_needed is not None:
        if isinstance(action_needed, bool):
            params.append(("action_needed", "true" if action_needed else "false"))
        else:
            value = str(action_needed).strip().lower()
            if value not in {"true", "false"}:
                raise HelmError("action_needed must be true or false")
            params.append(("action_needed", value))
    return params


def cmd_issues(client: Client, args: argparse.Namespace) -> Any:
    params = _issue_query_params(args)
    params.append(("limit", str(_validate_limit(getattr(args, "limit", 50)))))
    items, next_cursor = _paged_collection_with_cursor(
        client,
        "/issues",
        params,
        context="issues",
        initial_cursor=str(getattr(args, "cursor", "") or "").strip(),
        collect_all=bool(getattr(args, "all", False)),
    )
    return {"data": items, "next_cursor": next_cursor}


def _optional_text(args: argparse.Namespace, name: str) -> str | None:
    value = getattr(args, name, None)
    if value is None:
        return None
    normalized = str(value).strip()
    return normalized or None


def cmd_bug_report(client: Client, args: argparse.Namespace) -> Any:
    title = _optional_text(args, "title")
    actual_behavior = _optional_text(args, "actual_behavior")
    if not title or not actual_behavior:
        raise HelmError("title and actual_behavior are required for a bug report")
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    project = _project(client, str(getattr(args, "project", "")))
    project_id = str(project["id"])
    bug: dict[str, Any] = {"actual_behavior": actual_behavior}
    for source in ("expected_behavior", "reproduction_steps", "environment", "affected_version", "severity"):
        value = _optional_text(args, source)
        if value:
            bug[source] = value
    body: dict[str, Any] = {"title": title, "kind": "bug", "bug": bug}
    for source, target in (("description", "description"), ("priority", "priority"), ("column", "column_id"), ("assignee", "assignee_id")):
        value = _optional_text(args, source)
        if value:
            body[target] = value
    path = "/projects/" + parse.quote(project_id, safe="") + "/tasks"
    task, _ = client.call(
        "POST",
        path,
        body=body,
        idempotency_key=_new_mutation_idempotency(operation_id),
    )
    return {"task": task, "operation_id": operation_id}


def cmd_bug_triage(client: Client, args: argparse.Namespace) -> Any:
    severity = _optional_text(args, "severity")
    if not severity:
        raise HelmError("severity is required for bug triage")
    body: dict[str, Any] = {"severity": severity}
    for source, target in (("priority", "priority"), ("assignee", "assignee_id"), ("column", "column_id")):
        value = _optional_text(args, source)
        if value:
            body[target] = value
    return _task_action_with_uuid(client, args, "triage", body)


def cmd_bug_resolve(client: Client, args: argparse.Namespace) -> Any:
    resolution = _optional_text(args, "resolution")
    if not resolution:
        raise HelmError("resolution is required for bug resolve")
    body: dict[str, Any] = {"resolution": resolution}
    for source, target in (("duplicate_of", "duplicate_of"), ("note", "note")):
        value = _optional_text(args, source)
        if value:
            body[target] = value
    return _task_action_with_uuid(client, args, "resolve", body)


def cmd_bug_duplicate(client: Client, args: argparse.Namespace) -> Any:
    duplicate_of = _optional_text(args, "duplicate_of")
    if not duplicate_of:
        raise HelmError("duplicate_of is required when resolving a duplicate bug")
    body = {"resolution": "duplicate", "duplicate_of": duplicate_of}
    note = _optional_text(args, "note")
    if note:
        body["note"] = note
    return _task_action_with_uuid(client, args, "resolve", body)


def cmd_bug_reopen(client: Client, args: argparse.Namespace) -> Any:
    reason = _optional_text(args, "reason")
    if not reason:
        raise HelmError("reason is required for bug reopen")
    return _task_action_with_uuid(client, args, "reopen", {"reason": reason})


def _agent_notes(client: Client, task_id: str) -> list[dict[str, Any]]:
    payload, _ = client.call("GET", "/tasks/" + parse.quote(task_id, safe="") + "/agent-notes")
    return [dict(note) for note in _data(payload) if isinstance(note, dict)]


def cmd_note_list(client: Client, args: argparse.Namespace) -> Any:
    task = _task(client, args.task)
    suffix = "?include_resolved=true" if getattr(args, "include_resolved", False) else ""
    payload, _ = client.call("GET", "/tasks/" + parse.quote(str(task["id"]), safe="") + "/agent-notes" + suffix)
    return {"task": task.get("key", task["id"]), "data": _data(payload)}


def cmd_note_add(client: Client, args: argparse.Namespace) -> Any:
    operation_id = _command_operation_id(getattr(args, "operation_id", None))
    task = _task(client, args.task)
    path = "/tasks/" + parse.quote(str(task["id"]), safe="") + "/agent-notes"
    body = {"category": args.category, "body": args.body.strip(), "evidence": args.evidence}
    note, _ = client.call("POST", path, body=body, idempotency_key=_command_mutation_id(operation_id, "POST", path, body))
    return {"task": task.get("key", task["id"]), "agent_note": note, "operation_id": operation_id}


def _note(client: Client, task_id: str, note_id: str) -> dict[str, Any]:
    path = "/tasks/" + parse.quote(task_id, safe="") + "/agent-notes/" + parse.quote(note_id, safe="")
    payload, _ = client.call("GET", path)
    if not isinstance(payload, dict) or not isinstance(payload.get("version"), int):
        raise HelmError("Helm returned an unexpected agent note")
    return payload


def cmd_note_update(client: Client, args: argparse.Namespace) -> Any:
    operation_id = _command_operation_id(getattr(args, "operation_id", None))
    task = _task(client, args.task); task_id = str(task["id"]); current = _note(client, task_id, args.note)
    path = "/tasks/" + parse.quote(task_id, safe="") + "/agent-notes/" + parse.quote(args.note, safe="")
    body = {"category": args.category or current.get("category"), "body": args.body.strip() if args.body else current.get("body"), "evidence": args.evidence if args.evidence is not None else current.get("evidence", [])}
    note, _ = client.call("PATCH", path, body=body, if_match=current["version"], idempotency_key=_command_mutation_id(operation_id, "PATCH", path, body))
    return {"task": task.get("key", task_id), "agent_note": note, "operation_id": operation_id}


def cmd_note_resolve(client: Client, args: argparse.Namespace) -> Any:
    operation_id = _command_operation_id(getattr(args, "operation_id", None))
    task = _task(client, args.task); task_id = str(task["id"]); current = _note(client, task_id, args.note)
    path = "/tasks/" + parse.quote(task_id, safe="") + "/agent-notes/" + parse.quote(args.note, safe="")
    client.call("DELETE", path, if_match=current["version"], idempotency_key=_command_mutation_id(operation_id, "DELETE", path))
    return {"task": task.get("key", task_id), "resolved_agent_note": args.note, "operation_id": operation_id}


