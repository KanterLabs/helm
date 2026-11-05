"""Authentication diagnostics, activity, leases, and dependencies."""

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



def cmd_auth_check(client: Client, _args: argparse.Namespace) -> Any:
    """Validate the configured agent credential without mutating Helm."""

    try:
        # ``/auth/status`` intentionally treats a bearer header as an edge
        # navigation hint rather than authenticating it.  ``/auth/me`` is the
        # protected, non-mutating identity check for API clients.
        payload, _ = client.call("GET", "/auth/me")
    except HelmError as exc:
        return {
            "ok": False,
            "authenticated": False,
            "error": {**_safe_error_for_client(exc, client), "hint": _auth_failure_hint(exc)},
        }
    if not isinstance(payload, dict):
        error = HelmError("Helm returned an unexpected auth identity")
        return {
            "ok": False,
            "authenticated": False,
            "error": {
                **_safe_error_for_client(error, client),
                "hint": "Retry against a Helm API endpoint that supports /auth/me.",
            },
        }
    return {"ok": True, "authenticated": True, "actor": _safe_actor(payload)}


def _events_page(client: Client, after: int, project: str, limit: int) -> tuple[list[dict[str, Any]], str]:
    params: list[tuple[str, str]] = [("after", str(after))]
    if project:
        params.append(("project", project))
    params.append(("limit", str(limit)))
    payload, _ = client.call("GET", _query_path("/events", params))
    try:
        return _data(payload), _collection_cursor(payload)
    except HelmError as exc:
        raise HelmError("invalid events collection response") from exc


def cmd_events(client: Client, args: argparse.Namespace) -> Any:
    """Poll the append-only event feed using its monotonic cursor."""

    after = _validate_nonnegative_int(getattr(args, "after", 0), "after")
    limit = _validate_limit(getattr(args, "limit", 50))
    project = str(getattr(args, "project", "") or "").strip()
    collect_all = bool(getattr(args, "all", False))
    events: list[dict[str, Any]] = []
    seen: set[int] = set()
    while True:
        page, next_cursor = _events_page(client, after, project, limit)
        events.extend(page)
        if not collect_all or not next_cursor:
            return {"data": events, "next_cursor": "" if collect_all else next_cursor}
        next_after = _validate_nonnegative_int(next_cursor, "event next_cursor")
        if next_after <= after or next_after in seen:
            raise HelmError("events collection returned a repeated cursor")
        seen.add(next_after)
        after = next_after


def _timeline_page(
    client: Client,
    path: str,
    *,
    before: str,
    kind: str,
    limit: int,
) -> tuple[list[dict[str, Any]], str]:
    params: list[tuple[str, str]] = []
    if before:
        params.append(("before", before))
    if kind:
        params.append(("kind", kind))
    params.append(("limit", str(limit)))
    payload, _ = client.call("GET", _query_path(path, params))
    try:
        return _data(payload), _collection_cursor(payload)
    except HelmError as exc:
        raise HelmError("invalid timeline collection response") from exc


def cmd_timeline(client: Client, args: argparse.Namespace) -> Any:
    """Read task or project activity while preserving opaque keyset cursors."""

    task = str(getattr(args, "task", "") or "").strip()
    project = str(getattr(args, "project", "") or "").strip()
    if bool(task) == bool(project):
        raise HelmError("exactly one of --task or --project is required")
    if task:
        path = "/tasks/" + parse.quote(task, safe="") + "/timeline"
    else:
        path = "/projects/" + parse.quote(project, safe="") + "/timeline"
    before = str(getattr(args, "before", "") or "").strip()
    kind = str(getattr(args, "kind", "") or "").strip()
    limit = _validate_limit(getattr(args, "limit", 50))
    collect_all = bool(getattr(args, "all", False))
    items: list[dict[str, Any]] = []
    seen: set[str] = set()
    while True:
        page, next_cursor = _timeline_page(client, path, before=before, kind=kind, limit=limit)
        items.extend(page)
        if not collect_all or not next_cursor:
            return {"data": items, "next_cursor": "" if collect_all else next_cursor}
        if next_cursor in seen or next_cursor == before:
            raise HelmError("timeline collection returned a repeated cursor")
        seen.add(next_cursor)
        before = next_cursor


def _task_action_with_uuid(
    client: Client,
    args: argparse.Namespace,
    action: str,
    body: dict[str, Any] | None,
) -> Any:
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    current = _task(client, str(getattr(args, "task", "")))
    path = "/tasks/" + parse.quote(str(current["id"]), safe="") + "/" + action
    request_kwargs: dict[str, Any] = {
        "if_match": current["version"],
        "idempotency_key": _new_mutation_idempotency(operation_id),
    }
    if body is not None:
        request_kwargs["body"] = body
    payload, _ = client.call("POST", path, **request_kwargs)
    return {"task": payload, "operation_id": operation_id}


def cmd_renew(client: Client, args: argparse.Namespace) -> Any:
    """Renew only the current owner's active lease through the exact route."""

    lease_seconds = getattr(args, "lease_seconds", 1800)
    return _task_action_with_uuid(client, args, "renew", {"lease_seconds": lease_seconds})


def cmd_release(client: Client, args: argparse.Namespace) -> Any:
    """Release the current owner's active lease through the exact route."""

    result = _task_action_with_uuid(client, args, "release", None)
    if isinstance(result, dict):
        _clear_matching_session(result.get("task"), str(result.get("operation_id", "")))
    return result


def cmd_dependency_list(client: Client, args: argparse.Namespace) -> Any:
    task = _task(client, str(getattr(args, "task", "")))
    path = "/tasks/" + parse.quote(str(task["id"]), safe="") + "/dependencies"
    payload, headers = client.call("GET", path)
    if not isinstance(payload, dict):
        raise HelmError("Helm returned an unexpected dependency collection")
    result = dict(payload)
    result["task"] = task.get("key", task["id"])
    etag = None
    if isinstance(headers, dict):
        etag = next((value for key, value in headers.items() if str(key).lower() == "etag"), None)
    if etag:
        result["etag"] = etag
    return result


def cmd_dependency_add(client: Client, args: argparse.Namespace) -> Any:
    prerequisite = str(getattr(args, "prerequisite", "") or "").strip()
    if not prerequisite:
        raise HelmError("prerequisite must not be empty")
    return _task_action_with_uuid(client, args, "dependencies", {"prerequisite": prerequisite})


def cmd_dependency_remove(client: Client, args: argparse.Namespace) -> Any:
    prerequisite = str(getattr(args, "prerequisite", "") or "").strip()
    if not prerequisite:
        raise HelmError("prerequisite must not be empty")
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    current = _task(client, str(getattr(args, "task", "")))
    path = (
        "/tasks/"
        + parse.quote(str(current["id"]), safe="")
        + "/dependencies/"
        + parse.quote(prerequisite, safe="")
    )
    payload, _ = client.call(
        "DELETE",
        path,
        if_match=current["version"],
        idempotency_key=_new_mutation_idempotency(operation_id),
    )
    return {"task": payload, "operation_id": operation_id}


