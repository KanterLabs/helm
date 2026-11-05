"""Collection pagination, references, and operation identities."""

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



def _data(payload: Any) -> list[dict[str, Any]]:
    if not isinstance(payload, dict) or not isinstance(payload.get("data"), list):
        raise HelmError("Helm returned an unexpected collection")
    return [item for item in payload["data"] if isinstance(item, dict)]


def _collection_cursor(payload: Any) -> str:
    """Read a collection cursor without treating null as an endless cursor."""

    if not isinstance(payload, dict):
        raise HelmError("Helm returned an unexpected collection")
    cursor = payload.get("next_cursor", "")
    # Older compatible deployments omitted the terminal cursor. The current
    # contract uses an empty string, but treating omission as terminal keeps a
    # read-only audit useful against those deployments.
    if cursor is None:
        return ""
    if not isinstance(cursor, str):
        raise HelmError("Helm returned an invalid collection cursor")
    return cursor.strip()


def _query_path(path: str, params: list[tuple[str, str]]) -> str:
    query = parse.urlencode(params)
    return path + ("?" + query if query else "")


def _paged_collection(
    client: Client,
    path: str,
    params: list[tuple[str, str]],
    *,
    context: str,
) -> list[dict[str, Any]]:
    """Read every page of a cursor collection using GET only.

    The API's cursors are opaque to clients. We therefore pass each cursor
    through verbatim and only guard against a broken server returning the same
    cursor forever.
    """

    result: list[dict[str, Any]] = []
    cursor = ""
    seen: set[str] = set()
    while True:
        page_params = list(params)
        if cursor:
            page_params.append(("cursor", cursor))
        payload, _ = client.call("GET", _query_path(path, page_params))
        try:
            result.extend(_data(payload))
            next_cursor = _collection_cursor(payload)
        except HelmError as exc:
            raise HelmError(f"invalid {context} collection response") from exc
        if not next_cursor:
            return result
        if next_cursor in seen or next_cursor == cursor:
            raise HelmError(f"{context} collection returned a repeated cursor")
        seen.add(next_cursor)
        cursor = next_cursor


def _paged_collection_with_cursor(
    client: Client,
    path: str,
    params: list[tuple[str, str]],
    *,
    context: str,
    initial_cursor: str = "",
    cursor_param: str = "cursor",
    collect_all: bool = False,
) -> tuple[list[dict[str, Any]], str]:
    """Read an offset collection while retaining its terminal cursor.

    ``cursor`` values are opaque to callers.  This helper only transports the
    exact server value and prevents duplicate query parameters when a caller
    starts from an explicitly supplied cursor.
    """

    result: list[dict[str, Any]] = []
    cursor = initial_cursor.strip()
    seen: set[str] = set()
    while True:
        page_params = [(key, value) for key, value in params if key != cursor_param]
        if cursor:
            page_params.append((cursor_param, cursor))
        payload, _ = client.call("GET", _query_path(path, page_params))
        try:
            result.extend(_data(payload))
            next_cursor = _collection_cursor(payload)
        except HelmError as exc:
            raise HelmError(f"invalid {context} collection response") from exc
        if not collect_all:
            return result, next_cursor
        if not next_cursor:
            return result, ""
        if next_cursor in seen or next_cursor == cursor:
            raise HelmError(f"{context} collection returned a repeated cursor")
        seen.add(next_cursor)
        cursor = next_cursor


def _release_project(client: Client, reference: str) -> dict[str, Any]:
    """Resolve one project strictly before reading project-local releases.

    Release names are only meaningful inside a project. Existing project
    lookup is intentionally permissive for compatibility, but release
    commands must not silently choose one project when a human-readable name
    matches more than one visible project.
    """

    normalized = str(reference or "").strip()
    if not normalized:
        raise HelmError("project must not be empty")
    projects, _ = _paged_collection_with_cursor(
        client,
        "/projects",
        [("limit", "200")],
        context="projects",
        collect_all=True,
    )
    folded = normalized.casefold()
    matches = []
    for project in projects:
        candidates = (project.get("id"), project.get("key"), project.get("slug"), project.get("name"))
        if any(isinstance(item, str) and item.casefold() == folded for item in candidates):
            matches.append(project)
    if not matches:
        raise HelmError(f"project not found: {normalized}")
    if len(matches) > 1:
        raise HelmError(f"project reference is ambiguous: {normalized}")
    project = matches[0]
    if not isinstance(project.get("id"), str) or not project["id"].strip():
        raise HelmError("Helm returned an unexpected project")
    return project


def _project_releases(
    client: Client,
    project: dict[str, Any],
    *,
    status: str | None = None,
    target_from: str | None = None,
    target_to: str | None = None,
    cursor: str = "",
    limit: int = 50,
    collect_all: bool = False,
) -> tuple[list[dict[str, Any]], str]:
    """Read a project's release collection with opaque cursor handling."""

    project_id = str(project.get("id") or "").strip()
    if not project_id:
        raise HelmError("Helm returned an unexpected project")
    params: list[tuple[str, str]] = [("limit", str(limit))]
    if status:
        normalized_status = status.strip().lower()
        if normalized_status not in {"planned", "released"}:
            raise HelmError("status must be planned or released")
        params.append(("status", normalized_status))
    if target_from:
        params.append(("target_from", target_from.strip()))
    if target_to:
        params.append(("target_to", target_to.strip()))
    path = "/projects/" + parse.quote(project_id, safe="") + "/releases"
    return _paged_collection_with_cursor(
        client,
        path,
        params,
        context="releases",
        initial_cursor=cursor,
        collect_all=collect_all,
    )


def _release_reference(
    client: Client,
    project_reference: str,
    release_reference: str,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """Resolve one release ID or case-insensitive name within one project."""

    normalized = str(release_reference or "").strip()
    if not normalized:
        raise HelmError("release must not be empty")
    project = _release_project(client, project_reference)
    releases, _ = _project_releases(client, project, limit=200, collect_all=True)
    folded = normalized.casefold()
    matches = []
    for release in releases:
        candidates = (release.get("id"), release.get("name"))
        if any(isinstance(item, str) and item.casefold() == folded for item in candidates):
            matches.append(release)
    if not matches:
        raise HelmError(f"release not found: {normalized}")
    if len(matches) > 1:
        raise HelmError(f"release reference is ambiguous: {normalized}")
    release = matches[0]
    if not isinstance(release.get("id"), str) or not release["id"].strip():
        raise HelmError("Helm returned an unexpected release")
    return project, release


def _validate_limit(value: Any, *, default: int = 50) -> int:
    """Validate the API collection limit before making a request."""

    if value is None:
        return default
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise HelmError("limit must be an integer between 1 and 200") from exc
    if parsed < 1 or parsed > 200:
        raise HelmError("limit must be an integer between 1 and 200")
    return parsed


def _validate_nonnegative_int(value: Any, label: str) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise HelmError(f"{label} must be a non-negative integer") from exc
    if parsed < 0:
        raise HelmError(f"{label} must be a non-negative integer")
    return parsed


def _new_operation_id(value: Any = None) -> str:
    """Resolve a new-workflow mutation ID to a UUIDv4.

    Existing Helm/TC compatibility commands intentionally retain their
    deterministic ``helm-<sha256>`` namespace.  New workflow commands use a
    UUIDv4 so the value itself is a valid API idempotency key.  Re-running a
    logical operation is deterministic when the caller supplies the emitted
    UUID again.
    """

    if value is None or not str(value).strip():
        return str(uuid.uuid4())
    candidate = str(value).strip()
    if not UUID4_PATTERN.fullmatch(candidate):
        raise HelmError("operation_id must be a UUIDv4 for this mutation")
    return candidate.lower()


def _new_mutation_idempotency(operation_id: str) -> str:
    """Use one UUIDv4 for one new logical mutation and safe retries."""

    return _new_operation_id(operation_id)


def _derived_uuid4(seed: str) -> str:
    """Derive a deterministic UUIDv4-shaped key for a mutation sub-step."""

    raw = bytearray(hashlib.sha256(seed.encode("utf-8")).digest()[:16])
    raw[6] = (raw[6] & 0x0F) | 0x40
    raw[8] = (raw[8] & 0x3F) | 0x80
    return str(uuid.UUID(bytes=bytes(raw)))


def _command_operation_id(value: Any = None) -> str:
    """Use UUIDv4 by default while retaining explicit legacy operation IDs."""

    if value is None or not str(value).strip():
        return str(uuid.uuid4())
    candidate = str(value).strip()
    if UUID4_PATTERN.fullmatch(candidate):
        return candidate.lower()
    # Existing Helm commands accepted safe human-readable IDs. Keep those
    # deterministic for callers that explicitly continue to use them, while
    # making every newly-created logical operation a UUIDv4 by default.
    return _validate_operation_id(candidate)


def _command_mutation_id(operation_id: str, method: str, path: str, detail: Any = "") -> str:
    """Return a per-mutation UUID key or the legacy deterministic key."""

    if UUID4_PATTERN.fullmatch(operation_id):
        if not isinstance(detail, str):
            detail = _canonical_json(detail)
        seed = "\0".join((operation_id, method.upper().strip(), path.strip(), detail))
        return _derived_uuid4(seed)
    return _mutation_idempotency(operation_id, method, path, detail)


def _safe_actor(actor: Any) -> dict[str, Any]:
    """Keep auth-check output limited to non-secret actor identity fields."""

    if not isinstance(actor, dict):
        return {}
    result: dict[str, Any] = {}
    for field in ("id", "kind", "name", "admin"):
        if field in actor and isinstance(actor[field], (str, bool)):
            result[field] = actor[field]
    for field in ("project_ids", "scopes"):
        values = actor.get(field)
        if isinstance(values, list) and all(isinstance(value, str) for value in values):
            result[field] = [value[:200] for value in values[:200]]
    return result


def _auth_failure_hint(exc: HelmError) -> str:
    if exc.status_code == 401:
        return "Check HELM_URL and HELM_TOKEN; the token may be expired or invalid."
    if exc.status_code == 403:
        return "Check the token scopes and project ceiling, then verify Cloudflare edge credentials if configured."
    if exc.status_code in {429, 503}:
        return "Retry after the server's backoff window; no mutation was attempted by auth-check."
    if exc.status_code is None:
        return "Check HELM_URL, network access, and the API TLS certificate."
    return "Inspect the returned API code and verify the Helm endpoint configuration."


