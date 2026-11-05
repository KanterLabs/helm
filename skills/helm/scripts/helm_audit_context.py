"""Task and project lookup plus audit context assembly."""

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



def _task(client: Client, reference: str) -> dict[str, Any]:
    payload, _ = client.call("GET", "/tasks/" + parse.quote(reference, safe=""))
    if not isinstance(payload, dict) or not isinstance(payload.get("version"), int):
        raise HelmError("Helm returned an unexpected task")
    return payload


def _project(client: Client, reference: str) -> dict[str, Any]:
    projects, _ = _paged_collection_with_cursor(
        client,
        "/projects",
        [("limit", "200")],
        context="projects",
        collect_all=True,
    )
    folded = reference.casefold()
    for project in projects:
        candidates = (project.get("id"), project.get("key"), project.get("slug"), project.get("name"))
        if any(isinstance(item, str) and item.casefold() == folded for item in candidates):
            return project
    raise HelmError(f"project not found: {reference}")


def _audit_columns(client: Client, project_id: str, page_size: int) -> dict[str, dict[str, Any]]:
    path = f"/projects/{parse.quote(project_id, safe='')}/columns"
    columns = _paged_collection(client, path, [("limit", str(page_size))], context="columns")
    return {
        column_id: column
        for column in columns
        if (column_id := _audit_safe_identifier(column.get("id")))
        and column.get("project_id", project_id) == project_id
    }


def _audit_tasks(client: Client, project_id: str, states: tuple[str, ...], page_size: int) -> list[dict[str, Any]]:
    path = f"/projects/{parse.quote(project_id, safe='')}/tasks"
    tasks: list[dict[str, Any]] = []
    seen_ids: set[str] = set()
    for state in states:
        for task in _paged_collection(
            client,
            path,
            [("state", state), ("limit", str(page_size))],
            context=f"{state} task",
        ):
            task_id = _audit_safe_identifier(task.get("id"))
            # A task should belong to exactly one semantic state. Deduplicate
            # defensively when a deployment changes a board while pages are
            # being read or when a compatible server ignores a state filter.
            if task_id and task_id in seen_ids:
                continue
            if task_id:
                seen_ids.add(task_id)
            tasks.append(task)
    return tasks


def _audit_comments(client: Client, task_id: str, limit: int) -> tuple[list[dict[str, Any]], str | None]:
    if limit <= 0:
        return [], None
    path = f"/tasks/{parse.quote(task_id, safe='')}/comments"
    # Keep only the newest comments in memory while still traversing every
    # page. Comments are optional evidence: a caller with task-read access may
    # have a compatible deployment that does not expose this route, in which
    # case the audit remains useful and reports a bounded warning.
    comments: list[dict[str, Any]] = []
    cursor = ""
    seen: set[str] = set()
    # Request only the bounded evidence window per page. Traversing the
    # opaque cursor still lets us retain the newest comments without loading
    # older narrative into memory.
    params = [("limit", str(min(limit, 200)))]
    try:
        while True:
            page_params = list(params)
            if cursor:
                page_params.append(("cursor", cursor))
            payload, _ = client.call("GET", _query_path(path, page_params))
            comments.extend(_data(payload))
            if len(comments) > limit:
                comments = comments[-limit:]
            next_cursor = _collection_cursor(payload)
            if not next_cursor:
                return comments, None
            if next_cursor in seen or next_cursor == cursor:
                return comments, "recent evidence pagination returned a repeated cursor"
            seen.add(next_cursor)
            cursor = next_cursor
    except HelmError as exc:
        code = exc.error_code or (str(exc.status_code) if exc.status_code else "unavailable")
        # Do not copy a server error message into audit context; it may contain
        # task text or deployment details outside the bounded evidence schema.
        return comments, f"recent evidence unavailable ({code})"


def _audit_task_context(
    task: dict[str, Any],
    columns: dict[str, dict[str, Any]],
    evidence: list[dict[str, Any]],
    evidence_warning: str | None,
    now: datetime,
) -> dict[str, Any]:
    task_id = _audit_safe_identifier(task.get("id"))
    task_key = _audit_safe_identifier(task.get("key"))
    version = task.get("version")
    if not isinstance(version, int) or isinstance(version, bool):
        version = None
    description = task.get("description") if isinstance(task.get("description"), str) else ""
    current_column = _audit_current_column(task, columns)
    claim = _audit_claim(task, now)
    raw_work = task.get("agent_work")
    work = _audit_safe_agent_work(raw_work)
    liveness, derived_stale = _audit_liveness(raw_work if isinstance(raw_work, dict) else None, now)
    if work is not None and "stale" not in work and liveness != "unknown":
        work["stale"] = derived_stale
    verdict, destination, confidence, reason, warnings = _audit_classification(
        current_column.get("semantic_state"),
        claim,
        work,
        liveness,
        task.get("completed_at"),
    )
    if evidence_warning:
        warnings.append(evidence_warning)

    evidence_refs: list[str] = []
    if isinstance(work, dict):
        operation_id = _audit_safe_identifier(work.get("operation_id"))
        if operation_id:
            evidence_refs.append("agent-work:" + operation_id)
        refs = work.get("checkpoint_refs")
        if isinstance(refs, list):
            for ref in refs:
                safe_ref = _audit_safe_identifier(ref)
                if safe_ref:
                    evidence_refs.append("checkpoint:" + safe_ref)
    for entry in evidence:
        comment_id = _audit_safe_identifier(entry.get("id"))
        if comment_id:
            evidence_refs.append("comment:" + comment_id)
    evidence_refs = evidence_refs[:AUDIT_MAX_EVIDENCE_REFS]

    result: dict[str, Any] = {
        "id": task_id or None,
        "key": task_key or None,
        "version": version,
        "current_column": current_column,
        "title": _audit_redact_text(task.get("title"), AUDIT_MAX_TEXT),
        "description": _audit_redact_text(description, AUDIT_MAX_TEXT),
        "goal": _audit_goal(description),
        "acceptance_criteria": _audit_acceptance_criteria(description),
        "priority": task.get("priority") if isinstance(task.get("priority"), str) else None,
        "claim": claim,
        "agent_work": work,
        "liveness": {
            "state": liveness,
            "updated_at": work.get("updated_at") if isinstance(work, dict) else None,
            "stale": derived_stale,
        },
        "evidence": evidence,
        "evidence_refs": evidence_refs,
        "verdict": verdict,
        "suggested_semantic_state": destination,
        "confidence": round(confidence, 2),
        "confidence_label": _audit_confidence_label(confidence),
        "reason": _audit_redact_text(reason, 500),
        "warnings": warnings,
    }
    return result


