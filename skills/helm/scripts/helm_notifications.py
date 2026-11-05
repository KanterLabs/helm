"""Notification preferences, inbox, and watch operations."""

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



def cmd_notifications_list(client: Client, args: argparse.Namespace) -> Any:
    """List the authenticated actor's notification inbox."""

    params: list[tuple[str, str]] = []
    if bool(getattr(args, "unread", False)):
        params.append(("unread", "true"))
    params.append(("limit", str(_validate_limit(getattr(args, "limit", 50)))))
    items, next_cursor = _paged_collection_with_cursor(
        client,
        "/notifications",
        params,
        context="notifications",
        initial_cursor=str(getattr(args, "cursor", "") or "").strip(),
        collect_all=bool(getattr(args, "all", False)),
    )
    return {"data": items, "next_cursor": next_cursor}


def _notification_read_selection(args: argparse.Namespace) -> tuple[str, list[str], bool]:
    """Validate and normalize the mutually-exclusive read targets."""

    raw_notification = getattr(args, "notification", None)
    notification = _optional_text(args, "notification")
    notification_supplied = raw_notification is not None
    if notification_supplied and notification is None:
        raise HelmError("notification must not be empty")
    raw_ids = getattr(args, "ids", None)
    if raw_ids is None:
        raw_ids = getattr(args, "id", None)
    ids_supplied = raw_ids is not None
    if raw_ids is None:
        ids: list[Any] = []
    elif isinstance(raw_ids, (list, tuple)):
        ids = list(raw_ids)
    else:
        ids = [raw_ids]
    normalized_ids: list[str] = []
    for value in ids:
        if not isinstance(value, str) or not value.strip():
            raise HelmError("notification IDs must be non-empty")
        normalized_ids.append(value.strip())
    if ids_supplied and not normalized_ids:
        raise HelmError("at least one --id is required")
    if len(normalized_ids) > 200:
        raise HelmError("at most 200 notification IDs may be selected")
    if len(set(normalized_ids)) != len(normalized_ids):
        raise HelmError("notification IDs must be unique")
    all_notifications = bool(getattr(args, "all", False))
    selected = int(notification_supplied) + int(ids_supplied) + int(all_notifications)
    if selected != 1:
        raise HelmError("exactly one of --notification, --id, or --all is required")
    if notification is not None:
        return notification, [], False
    return "", normalized_ids, all_notifications


def cmd_notifications_read(client: Client, args: argparse.Namespace) -> Any:
    """Mark one or more visible notifications read."""

    notification, ids, all_notifications = _notification_read_selection(args)
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    if notification:
        path = "/notifications/" + parse.quote(notification, safe="") + "/read"
        payload, _ = client.call(
            "POST",
            path,
            idempotency_key=_new_mutation_idempotency(operation_id),
        )
        return {"notification": payload, "operation_id": operation_id}

    path = "/notifications/read"
    body: dict[str, Any] = {"all": True} if all_notifications else {"ids": ids}
    payload, _ = client.call(
        "POST",
        path,
        body=body,
        idempotency_key=_new_mutation_idempotency(operation_id),
    )
    if isinstance(payload, dict):
        result = dict(payload)
        result["operation_id"] = operation_id
        return result
    return {"result": payload, "operation_id": operation_id}


def _parse_strict_bool(value: str) -> bool:
    normalized = str(value).strip().lower()
    if normalized == "true":
        return True
    if normalized == "false":
        return False
    raise argparse.ArgumentTypeError("value must be true or false")


def cmd_notification_preferences(client: Client, args: argparse.Namespace) -> Any:
    """Read or update explicitly selected notification preference categories."""

    fields = ("assignments", "mentions", "blockers", "state_changes")
    body: dict[str, bool] = {}
    for field in fields:
        value = getattr(args, field, None)
        if value is None:
            continue
        if not isinstance(value, bool):
            raise HelmError(f"{field} must be true or false")
        body[field] = value
    path = "/notification-preferences"
    if not body:
        payload, _ = client.call("GET", path)
        return {"preferences": payload}

    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    payload, _ = client.call(
        "PATCH",
        path,
        body=body,
        idempotency_key=_new_mutation_idempotency(operation_id),
    )
    return {"preferences": payload, "operation_id": operation_id}


# Keep the command-family spelling available to callers that dispatch by the
# top-level ``notifications`` resource name.
cmd_notifications_preferences = cmd_notification_preferences


def cmd_watches_list(client: Client, args: argparse.Namespace) -> Any:
    """List watches owned by the authenticated actor."""

    params: list[tuple[str, str]] = []
    project = _optional_text(args, "project")
    task = _optional_text(args, "task")
    if getattr(args, "project", None) is not None and project is None:
        raise HelmError("project must not be empty")
    if getattr(args, "task", None) is not None and task is None:
        raise HelmError("task must not be empty")
    if project:
        params.append(("project", project))
    if task:
        params.append(("task", task))
    payload, _ = client.call("GET", _query_path("/watches", params))
    return {"data": _data(payload), "next_cursor": _collection_cursor(payload)}


def cmd_watches_add(client: Client, args: argparse.Namespace) -> Any:
    """Follow exactly one project or task through its canonical watch route."""

    project = _optional_text(args, "project")
    task = _optional_text(args, "task")
    if (project is None) == (task is None):
        raise HelmError("exactly one of --project or --task is required")
    if getattr(args, "project", None) is not None and project is None:
        raise HelmError("project must not be empty")
    if getattr(args, "task", None) is not None and task is None:
        raise HelmError("task must not be empty")
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    if project is not None:
        path = "/projects/" + parse.quote(project, safe="") + "/watch"
    else:
        path = "/tasks/" + parse.quote(task or "", safe="") + "/watch"
    payload, _ = client.call(
        "POST",
        path,
        idempotency_key=_new_mutation_idempotency(operation_id),
    )
    return {"watch": payload, "operation_id": operation_id}


def cmd_watches_remove(client: Client, args: argparse.Namespace) -> Any:
    """Remove one owned watch."""

    watch = _optional_text(args, "watch")
    if watch is None:
        raise HelmError("watch must not be empty")
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    path = "/watches/" + parse.quote(watch, safe="")
    payload, _ = client.call(
        "DELETE",
        path,
        idempotency_key=_new_mutation_idempotency(operation_id),
    )
    result: dict[str, Any] = {"operation_id": operation_id}
    if payload is not None:
        result["watch"] = payload
    return result


