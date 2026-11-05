"""Project, task, and product release reads."""

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



def cmd_projects(client: Client, args: argparse.Namespace) -> Any:
    limit = _validate_limit(getattr(args, "limit", 200), default=200)
    cursor = str(getattr(args, "cursor", "") or "").strip()
    projects, next_cursor = _paged_collection_with_cursor(
        client,
        "/projects",
        [("limit", str(limit))],
        context="projects",
        initial_cursor=cursor,
        collect_all=bool(getattr(args, "all", False)),
    )
    selected = [{key: item.get(key) for key in ("id", "key", "slug", "name")} for item in projects]
    return {"projects": selected, "next_cursor": next_cursor}


def cmd_tasks(client: Client, args: argparse.Namespace) -> Any:
    project = _project(client, args.project)
    limit = _validate_limit(getattr(args, "limit", 200), default=200)
    params: list[tuple[str, str]] = [("limit", str(limit))]
    if getattr(args, "query", ""):
        params.append(("q", str(args.query)))
    tasks, next_cursor = _paged_collection_with_cursor(
        client,
        f"/projects/{parse.quote(str(project['id']), safe='')}/tasks",
        params,
        context="tasks",
        initial_cursor=str(getattr(args, "cursor", "") or "").strip(),
        collect_all=bool(getattr(args, "all", False)),
    )
    return {"project": project.get("key"), "tasks": tasks, "next_cursor": next_cursor}


def cmd_releases_list(client: Client, args: argparse.Namespace) -> Any:
    """List releases in one project, preserving the API's cursor contract."""

    project = _release_project(client, str(getattr(args, "project", "")))
    limit = _validate_limit(getattr(args, "limit", 50))
    releases, next_cursor = _project_releases(
        client,
        project,
        status=str(getattr(args, "status", "") or "").strip() or None,
        target_from=str(getattr(args, "target_from", "") or "").strip() or None,
        target_to=str(getattr(args, "target_to", "") or "").strip() or None,
        cursor=str(getattr(args, "cursor", "") or "").strip(),
        limit=limit,
        collect_all=bool(getattr(args, "all", False)),
    )
    return {
        "project": project.get("key"),
        "releases": releases,
        "next_cursor": next_cursor,
    }


def cmd_releases_get(client: Client, args: argparse.Namespace) -> Any:
    """Resolve a project-local release name/ID, then read its canonical resource."""

    project, release = _release_reference(
        client,
        str(getattr(args, "project", "")),
        str(getattr(args, "release", "")),
    )
    release_id = str(release["id"])
    payload, _ = client.call("GET", "/releases/" + parse.quote(release_id, safe=""))
    if not isinstance(payload, dict) or not isinstance(payload.get("version"), int):
        raise HelmError("Helm returned an unexpected release")
    # The project argument is a scope guard rather than an output wrapper. A
    # malformed compatible response that resolves to another project must not
    # be presented as the requested project's release.
    payload_project = payload.get("project_id")
    if isinstance(payload_project, str) and payload_project.strip() != str(project["id"]).strip():
        raise HelmError("release does not belong to the requested project")
    return payload


def _release_queue_page(
    client: Client,
    release_id: str,
    *,
    cursor: str,
    limit: int,
) -> tuple[dict[str, Any], str]:
    """Read and validate one release work-queue page."""

    path = "/releases/" + parse.quote(release_id, safe="") + "/work-queue"
    params: list[tuple[str, str]] = [("limit", str(limit))]
    if cursor:
        params.append(("cursor", cursor))
    payload, _ = client.call("GET", _query_path(path, params))
    if not isinstance(payload, dict):
        raise HelmError("Helm returned an unexpected release work queue")
    rows = payload.get("data")
    if not isinstance(rows, list) or not all(isinstance(item, dict) for item in rows):
        raise HelmError("Helm returned an unexpected release work queue")
    try:
        next_cursor = _collection_cursor(payload)
    except HelmError as exc:
        raise HelmError("invalid release work queue response") from exc
    return dict(payload), next_cursor


def _release_workflow_projection(queue: dict[str, Any]) -> dict[str, Any]:
    """Derive deterministic agent guidance from one queue snapshot.

    This is deliberately a projection. It never claims, resumes, releases,
    completes, blocks, or otherwise mutates a task or release.
    """

    summary = queue.get("summary")
    if not isinstance(summary, dict):
        return {
            "state": "waiting",
            "guidance": ["Queue summary is unavailable; re-read the release queue before acting."],
            "next": "refresh_queue",
        }

    def count(name: str) -> int:
        value = summary.get(name, 0)
        return value if isinstance(value, int) and not isinstance(value, bool) and value >= 0 else 0

    direct = count("direct")
    required = count("required")
    completed = count("completed")
    owned = count("owned")
    claimable = count("claimable")
    blockers = (
        ("dependency_blocked", "dependency blockers"),
        ("manually_blocked", "manual blockers"),
        ("claimed_elsewhere", "foreign claims"),
        ("cross_release_conflicts", "cross-release conflicts"),
    )
    blocked = [(name, label, count(name)) for name, label in blockers if count(name) > 0]

    if direct > 0 and required > 0 and completed >= required:
        state = "ready"
        next_action = "release_review"
        guidance = [
            "All required release work is complete; review the release summary.",
            "Do not auto-complete the product release; release completion is an explicit separate action.",
        ]
    elif owned > 0:
        state = "handoff"
        next_action = "resume_owned_task"
        guidance = [
            "Resume your owned active task before claiming another task.",
            "Hold no more than one new claim at a time and refresh the queue after every task mutation.",
        ]
    elif claimable > 0:
        state = "handoff"
        next_action = "claim_one_task"
        guidance = [
            "Claim exactly one claimable task in queue order, then use its goal and acceptance criteria as the work contract.",
            "Refresh the queue from the first page after every completion, block, released claim, stale ETag, or scope-changing event.",
        ]
    elif required == 0 or direct == 0:
        state = "waiting"
        next_action = "confirm_release_scope"
        guidance = [
            "The release has no direct member work; confirm its scope before taking action.",
            "Do not mark an empty release released automatically.",
        ]
    else:
        state = "waiting"
        next_action = "resolve_blockers"
        guidance = [
            "No task is currently claimable; stop and report the blockers with their next actions.",
            "Do not bypass a manual blocker, override a foreign claim, or work a prerequisite assigned to another planned release.",
        ]

    if blocked:
        labels = ", ".join(f"{label} ({amount})" for _name, label, amount in blocked)
        guidance.append("Current queue blockers: " + labels + ".")
    warning_count = count("checklist_warning_count")
    if warning_count:
        guidance.append(f"Review {warning_count} completed task checklist warning(s) before release review.")
    return {"state": state, "next": next_action, "guidance": guidance}


def cmd_release_work(client: Client, args: argparse.Namespace) -> Any:
    """Read a dependency-aware release queue without mutating Helm."""

    _project, release = _release_reference(
        client,
        str(getattr(args, "project", "")),
        str(getattr(args, "release", "")),
    )
    release_id = str(release["id"])
    limit = _validate_limit(getattr(args, "limit", 50))
    requested_cursor = str(getattr(args, "cursor", "") or "").strip()
    collect_all = bool(getattr(args, "all", False))
    restart_count = 0

    while True:
        cursor = requested_cursor
        seen: set[str] = {cursor} if cursor else set()
        combined: list[dict[str, Any]] = []
        first_page: dict[str, Any] | None = None
        try:
            while True:
                page, next_cursor = _release_queue_page(
                    client,
                    release_id,
                    cursor=cursor,
                    limit=limit,
                )
                if first_page is None:
                    first_page = page
                combined.extend(page["data"])
                if not collect_all or not next_cursor:
                    result = page if not collect_all else dict(first_page)
                    if collect_all:
                        result["data"] = combined
                        result["next_cursor"] = ""
                    # Checklist warnings live on the release summary while
                    # queue pages carry the dependency/claim counters. Merge
                    # the one warning count into a private projection copy so
                    # the public queue shape remains exactly the API shape.
                    workflow_input = result
                    release_summary = release.get("summary")
                    if isinstance(release_summary, dict) and isinstance(result.get("summary"), dict):
                        checklist_warnings = release_summary.get("checklist_warning_count")
                        if isinstance(checklist_warnings, int) and checklist_warnings >= 0:
                            workflow_input = dict(result)
                            workflow_input["summary"] = dict(result["summary"])
                            workflow_input["summary"].setdefault("checklist_warning_count", checklist_warnings)
                    workflow = _release_workflow_projection(workflow_input)
                    # Keep the API's queue fields at the top level while
                    # exposing an explicit, deterministic agent projection.
                    result["state"] = workflow["state"]
                    result["guidance"] = workflow["guidance"]
                    result["workflow"] = workflow
                    if restart_count:
                        result["queue_restarts"] = restart_count
                    return result
                if next_cursor in seen or next_cursor == cursor:
                    raise HelmError("release work queue returned a repeated cursor")
                seen.add(next_cursor)
                cursor = next_cursor
        except HelmError as exc:
            if exc.error_code != "release_queue_changed":
                raise
            if restart_count >= MAX_RELEASE_QUEUE_RESTARTS:
                raise HelmError(
                    "release work queue changed repeatedly; stop and restart from the first page",
                    status_code=exc.status_code,
                    error_code=exc.error_code,
                    retry_after=exc.retry_after,
                ) from exc
            # A continuation is a snapshot token. Discard all accumulated
            # rows, reset to the first page, and retry the complete read.
            restart_count += 1
            requested_cursor = ""


# Keep both resource-oriented names available to callers that import the
# helper directly; the parser uses the plural command-family spelling.
cmd_release_list = cmd_releases_list
cmd_release_get = cmd_releases_get


