"""Audit finding validation and reconciliation decisions."""

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



def _audit_column_id(value: Any) -> str:
    if isinstance(value, dict):
        value = value.get("id", value.get("column_id"))
    return _audit_safe_identifier(value)


def _audit_semantic_value(value: Any) -> str:
    if isinstance(value, dict):
        value = value.get("semantic_state", value.get("state"))
    return value.strip() if isinstance(value, str) else ""


def _audit_submission_ref(value: Any) -> str:
    """Normalize one evidence reference to the server's safe-ref grammar."""

    value = _audit_safe_identifier(value, 512)
    if not value or not re.fullmatch(r"[A-Za-z0-9/][A-Za-z0-9._:/+~-]*", value):
        return ""
    lowered = value.casefold()
    if lowered.startswith(("javascript:", "data:")):
        return ""
    if _AUDIT_SENSITIVE_WORD.search(value):
        return ""
    return value


def _audit_submission_items(raw: Any) -> list[Any]:
    """Extract findings/tasks from an audit JSON context or direct list."""

    value = raw
    if isinstance(value, (str, Path)):
        if str(value) == "-":
            encoded = sys.stdin.read()
        else:
            try:
                encoded = Path(value).read_text(encoding="utf-8")
            except (OSError, UnicodeError) as exc:
                raise HelmError("cannot read audit submission input") from exc
        try:
            value = json.loads(encoded)
        except json.JSONDecodeError as exc:
            raise HelmError("audit submission input is not valid JSON") from exc
    if isinstance(value, dict):
        if isinstance(value.get("tasks"), list):
            value = value["tasks"]
        elif isinstance(value.get("findings"), list):
            value = value["findings"]
        elif isinstance(value.get("audit"), dict):
            return _audit_submission_items(value["audit"])
    if not isinstance(value, list):
        raise HelmError("audit submission input must contain a findings list")
    return value


def _audit_submission_finding(item: Any) -> tuple[dict[str, Any] | None, str | None]:
    if not isinstance(item, dict):
        return None, "finding is not an object"
    task_id = _audit_safe_identifier(item.get("task_id", item.get("id")))
    version = item.get("captured_version", item.get("version"))
    if not task_id or not isinstance(version, int) or isinstance(version, bool) or version <= 0:
        return None, "finding requires a task ID and positive captured version"
    source = item.get("source_column", item.get("source_column_id"))
    if not source:
        source = item.get("current_column")
    source_column = _audit_column_id(source)
    if not source_column:
        return None, "finding requires a source column ID"
    verdict = item.get("verdict")
    if verdict not in {"correct", "needs_attention", "move_proposed"}:
        return None, "finding verdict is invalid"
    destination: str | None = None
    if verdict == "move_proposed":
        destination_value = item.get("proposed_semantic_destination", item.get("suggested_semantic_state"))
        destination = _audit_semantic_value(destination_value)
        if destination not in AUDIT_SEMANTIC_STATES:
            return None, "move proposal requires a valid semantic destination"
    confidence = item.get("confidence")
    if isinstance(confidence, bool):
        confidence = None
    try:
        confidence = float(confidence)
    except (TypeError, ValueError):
        confidence = None
    if confidence is None or not math.isfinite(confidence) or confidence < 0 or confidence > 1:
        return None, "finding confidence must be between 0 and 1"
    reason = _audit_redact_text(item.get("reason"), 2000)
    if not reason:
        return None, "finding reason is required"
    refs: list[str] = []
    raw_refs = item.get("evidence_refs", [])
    if isinstance(raw_refs, list):
        for ref in raw_refs[:AUDIT_MAX_EVIDENCE_REFS]:
            safe_ref = _audit_submission_ref(ref)
            if safe_ref and safe_ref not in refs:
                refs.append(safe_ref)
    body: dict[str, Any] = {
        "task_id": task_id,
        "captured_version": version,
        "source_column": source_column,
        "verdict": verdict,
        "confidence": confidence,
        "reason": reason,
        "evidence_refs": refs,
        # Submission never imports approval state from a file. Human review is
        # a separate versioned API action, while an idempotent replay of this
        # exact pending finding is handled by the server.
        "review_state": "pending",
    }
    if destination is not None:
        body["proposed_semantic_destination"] = destination
    return body, None


def _audit_run_id(payload: Any) -> str:
    if not isinstance(payload, dict):
        raise HelmError("Helm returned an unexpected audit run")
    audit_id = _audit_safe_identifier(payload.get("id"))
    if not audit_id:
        raise HelmError("Helm returned an unexpected audit run")
    return audit_id


def _reconcile_destination_columns(columns: dict[str, dict[str, Any]]) -> dict[str, dict[str, Any]]:
    result: dict[str, dict[str, Any]] = {}
    ordered = sorted(
        columns.values(),
        key=lambda column: (
            column.get("position") if isinstance(column.get("position"), (int, float)) else 0,
            _audit_safe_identifier(column.get("id")),
        ),
    )
    for column in ordered:
        semantic = _audit_semantic_value(column.get("semantic_state"))
        if semantic in AUDIT_SEMANTIC_STATES and semantic not in result:
            result[semantic] = column
    return result


def _reconcile_action_for_destination(semantic: str) -> str | None:
    return {
        "active": "claim_or_resume",
        "blocked": "block",
        "completed": "complete",
    }.get(semantic)


def _idempotency(operation_id: str, action: str, detail: str = "") -> str:
    seed = "\0".join((operation_id.strip(), action, detail)).encode("utf-8")
    # Keep the mutation namespace stable so a migration cannot duplicate a
    # previously submitted operation for the same task and operation ID.
    return "helm-" + hashlib.sha256(seed).hexdigest()


def _mutation_idempotency(operation_id: str, method: str, path: str, detail: Any = "") -> str:
    """Build a deterministic key scoped to one mutation target.

    Operation IDs are intentionally caller-owned and may be reused for a
    whole session. Including the HTTP method/path and canonical body detail
    prevents a retry for one task or endpoint from replaying a different
    mutation that happens to use the same operation ID.
    """

    if not isinstance(detail, str):
        detail = _canonical_json(detail)
    target = method.upper().strip() + " " + path.strip()
    return _idempotency(operation_id, target, detail)


def _validate_operation_id(operation_id: Any) -> str:
    """Apply the server's operation ID contract before any network write."""

    if not isinstance(operation_id, str):
        raise HelmError("operation_id must be between 1 and 128 safe identifier characters")
    value = operation_id.strip()
    if not value or len(value) > 128:
        raise HelmError("operation_id must be between 1 and 128 safe identifier characters")
    ascii_alnum = lambda char: ("a" <= char <= "z") or ("A" <= char <= "Z") or ("0" <= char <= "9")
    if not (ascii_alnum(value[0]) and all(ascii_alnum(char) or char in "-_.:/" for char in value)):
        raise HelmError("operation_id must be between 1 and 128 safe identifier characters")
    return value


