"""Read-only board audit evidence and scoring."""

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



def _audit_states(value: Any) -> tuple[str, ...]:
    if value is None:
        return AUDIT_DEFAULT_STATES
    if isinstance(value, str):
        values: list[Any] = [part for part in value.split(",") if part.strip()]
    elif isinstance(value, (list, tuple)):
        values = list(value)
    else:
        raise HelmError("audit states must be semantic state names")
    states: list[str] = []
    for item in values:
        if not isinstance(item, str):
            raise HelmError("audit states must be semantic state names")
        state = item.strip()
        if state not in AUDIT_SEMANTIC_STATES:
            raise HelmError("audit states must be semantic state names")
        if state not in states:
            states.append(state)
    if not states:
        raise HelmError("audit requires at least one semantic state")
    return tuple(states)


def _audit_page_size(value: Any) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise HelmError("audit page size must be between 1 and 200") from exc
    if parsed < 1 or parsed > 200:
        raise HelmError("audit page size must be between 1 and 200")
    return parsed


def _audit_evidence_limit(value: Any) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise HelmError("audit evidence limit must be between 0 and 5") from exc
    if parsed < 0:
        raise HelmError("audit evidence limit must be between 0 and 5")
    # A caller may ask for more, but the output contract stays bounded.
    return min(parsed, AUDIT_MAX_EVIDENCE_ITEMS)


# Text fields are read-only API data, but descriptions, progress summaries,
# and comments can still contain accidental credentials. Redact common secret
# forms before they become agent context. Evidence comments are omitted when
# they contain a sensitive marker that cannot be safely isolated.
_AUDIT_PRIVATE_KEY = re.compile(
    r"-----BEGIN [^-\n]*PRIVATE KEY-----.*?-----END [^-\n]*PRIVATE KEY-----",
    re.IGNORECASE | re.DOTALL,
)
_AUDIT_BEARER = re.compile(r"\bBearer\s+[A-Za-z0-9._~+/=-]+", re.IGNORECASE)
_AUDIT_SECRET_ASSIGNMENT = re.compile(
    r"(\b(?:[A-Za-z][A-Za-z0-9-]*[_-])*(?:authorization|bearer|token|secret|password|passwd|api[_-]?(?:key|token)|access[_-]?token|client[_-]?(?:secret|token))\b\s*[:=]\s*)([^\s,;]+)",
    re.IGNORECASE,
)
_AUDIT_QUERY_SECRET = re.compile(
    r"([?&](?:[A-Za-z][A-Za-z0-9-]*[_-])*(?:token|secret|password|passwd|api[_-]?(?:key|token)|access[_-]?token|sig|signature)=)[^&#\s]+",
    re.IGNORECASE,
)
_AUDIT_TOKEN_PREFIX = re.compile(r"\b(?:sk|rk|gh[pousr]|xox[baprs])[-_][A-Za-z0-9._-]+", re.IGNORECASE)
_AUDIT_SENSITIVE_WORD = re.compile(
    r"\b[^\s,;]*(?:authorization|bearer|token|secret|password|passwd|api[_-]?(?:key|token)|access[_-]?token|private\s+key)[^\s,;]*",
    re.IGNORECASE,
)


def _audit_redact_text(value: Any, limit: int) -> str:
    if not isinstance(value, str):
        return ""
    text = value.strip()
    if not text:
        return ""
    text = _AUDIT_PRIVATE_KEY.sub("[REDACTED]", text)
    text = _AUDIT_BEARER.sub("Bearer [REDACTED]", text)
    text = _AUDIT_SECRET_ASSIGNMENT.sub(r"\1[REDACTED]", text)
    text = _AUDIT_QUERY_SECRET.sub(r"\1[REDACTED]", text)
    text = _AUDIT_TOKEN_PREFIX.sub("[REDACTED]", text)
    # Remove marker words as a final guard against leaking a marker's value in
    # an otherwise unstructured sentence (for example, "secret abc").
    text = _AUDIT_SENSITIVE_WORD.sub("[REDACTED]", text)
    if len(text) > limit:
        text = text[: max(0, limit - 1)].rstrip() + "…"
    return text


def _audit_safe_identifier(value: Any, limit: int = 128) -> str:
    if not isinstance(value, str):
        return ""
    value = value.strip()
    if not value:
        return ""
    # IDs and operation IDs are contract-safe identifiers. Still apply a
    # length bound, and never copy a value containing control characters.
    if any(ord(char) < 0x20 or ord(char) == 0x7F for char in value):
        return ""
    return value[:limit]


def _audit_timestamp(value: Any) -> datetime | None:
    if not isinstance(value, str) or not value.strip():
        return None
    try:
        parsed = datetime.fromisoformat(value.strip().replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def _audit_liveness(work: dict[str, Any] | None, now: datetime) -> tuple[str, bool]:
    if work is None:
        return "missing", False
    stale = work.get("stale")
    if isinstance(stale, bool):
        return ("stale" if stale else "fresh"), stale
    updated = _audit_timestamp(work.get("updated_at"))
    if updated is not None:
        stale = (now - updated).total_seconds() >= AUDIT_PULSE_STALE_SECONDS
        return ("stale" if stale else "fresh"), stale
    return "unknown", False


def _audit_safe_agent_work(work: Any) -> dict[str, Any] | None:
    if not isinstance(work, dict):
        return None
    result: dict[str, Any] = {}
    for field in ("operation_id", "actor_id", "state", "started_at", "updated_at"):
        value = work.get(field)
        if isinstance(value, str) and value.strip():
            result[field] = _audit_safe_identifier(value)
    for field in ("phase", "summary", "next_action"):
        value = _audit_redact_text(work.get(field), AUDIT_MAX_AGENT_TEXT)
        if value:
            result[field] = value
    refs = work.get("checkpoint_refs")
    if isinstance(refs, list):
        safe_refs = []
        for ref in refs[:AUDIT_MAX_EVIDENCE_REFS]:
            safe_ref = _audit_redact_text(ref, 128)
            if safe_ref:
                safe_refs.append(safe_ref)
        if safe_refs:
            result["checkpoint_refs"] = safe_refs
    for field in ("checkpoint_completed", "checkpoint_total"):
        value = work.get(field)
        if isinstance(value, int) and not isinstance(value, bool):
            result[field] = value
    for field in ("stale", "action_needed"):
        value = work.get(field)
        if isinstance(value, bool):
            result[field] = value
    return result


def _audit_claim(task: dict[str, Any], now: datetime) -> dict[str, Any]:
    owner = task.get("claimed_by")
    expires = task.get("claim_expires_at")
    nested = task.get("claim")
    if isinstance(nested, dict):
        if not isinstance(owner, str) or not owner.strip():
            owner = nested.get("owner_id", nested.get("claimed_by"))
        if not isinstance(expires, str) or not expires.strip():
            expires = nested.get("expires_at", nested.get("claim_expires_at"))
    owner_id = _audit_safe_identifier(owner)
    expires_at = _audit_safe_identifier(expires)
    parsed_expiry = _audit_timestamp(expires_at)
    if not owner_id:
        status = "unclaimed"
    elif parsed_expiry is None:
        status = "unknown"
    elif parsed_expiry > now:
        status = "active"
    else:
        status = "expired"
    return {
        "owner_id": owner_id or None,
        "expires_at": expires_at or None,
        "status": status,
        "active": status == "active",
    }


def _audit_goal(description: str) -> str:
    for line in description.splitlines():
        match = re.match(r"^\s*goal\s*:\s*(.*?)\s*$", line, re.IGNORECASE)
        if match and match.group(1):
            return _audit_redact_text(match.group(1), AUDIT_MAX_TEXT)
    paragraphs = [part.strip() for part in re.split(r"\n\s*\n", description) if part.strip()]
    if paragraphs:
        return _audit_redact_text(paragraphs[0], AUDIT_MAX_TEXT)
    return ""


def _audit_acceptance_criteria(description: str) -> list[str]:
    result: list[str] = []
    in_acceptance_section = False
    for raw_line in description.splitlines():
        line = raw_line.strip()
        if re.match(r"^acceptance criteria\s*:?\s*$", line, re.IGNORECASE):
            in_acceptance_section = True
            continue
        checked = re.match(r"^[-*+]\s+\[[ xX]\]\s+(.+?)\s*$", line)
        bullet = re.match(r"^[-*+]\s+(.+?)\s*$", line)
        value = checked.group(1) if checked else (bullet.group(1) if in_acceptance_section and bullet else "")
        if value:
            safe_value = _audit_redact_text(value, AUDIT_MAX_CRITERION_TEXT)
            if safe_value and len(result) < AUDIT_MAX_EVIDENCE_REFS:
                result.append(safe_value)
            continue
        if in_acceptance_section and line and not line.startswith(("-", "*", "+")):
            # A subsequent heading starts a different section.
            in_acceptance_section = False
    return result


def _audit_current_column(task: dict[str, Any], columns: dict[str, dict[str, Any]]) -> dict[str, Any]:
    column_id = task.get("column_id")
    if not isinstance(column_id, str) or not column_id.strip():
        nested = task.get("column")
        if isinstance(nested, dict):
            column_id = nested.get("id")
    column_id = _audit_safe_identifier(column_id)
    column = columns.get(column_id, {})
    semantic = column.get("semantic_state")
    if not isinstance(semantic, str) or not semantic.strip():
        semantic = task.get("semantic_state")
    if not isinstance(semantic, str) or not semantic.strip():
        nested = task.get("column")
        semantic = nested.get("semantic_state") if isinstance(nested, dict) else ""
    name = column.get("name")
    if not isinstance(name, str) or not name.strip():
        name = task.get("column_name")
    if not isinstance(name, str) or not name.strip():
        nested = task.get("column")
        name = nested.get("name") if isinstance(nested, dict) else ""
    return {
        "id": column_id or None,
        "name": _audit_redact_text(name, 200) or None,
        "semantic_state": semantic.strip() if isinstance(semantic, str) and semantic.strip() else None,
    }


def _audit_confidence_label(score: float) -> str:
    if score >= 0.8:
        return "high"
    if score >= 0.6:
        return "medium"
    return "low"


def _audit_classification(
    semantic: str | None,
    claim: dict[str, Any],
    work: dict[str, Any] | None,
    liveness: str,
    completed_at: Any,
) -> tuple[str, str | None, float, str, list[str]]:
    """Return a conservative verdict and destination for one task."""

    state = work.get("state") if isinstance(work, dict) else None
    warnings: list[str] = []
    if liveness == "stale":
        warnings.append("agent pulse is stale (warning only)")
    elif liveness == "missing":
        warnings.append("agent pulse is missing (warning only)")
    elif liveness == "unknown":
        warnings.append("agent pulse liveness is unknown (warning only)")
    if claim.get("status") == "expired":
        warnings.append("claim lease is expired")
    elif claim.get("status") == "unknown":
        warnings.append("claim lease is not verifiable")
    if state in {"waiting", "handoff"}:
        warnings.append(f"agent work is {state}")

    if semantic not in AUDIT_SEMANTIC_STATES:
        return "needs_attention", semantic, 0.25, "current semantic column is unavailable or unknown", warnings

    # Terminal lifecycle metadata is authoritative over historical agent-work
    # pulses. A final verifying/working pulse is expected after completion.
    if semantic == "completed" and completed_at and claim.get("status") != "active":
        return "correct", semantic, 0.95, "completion metadata matches the completed column", warnings
    if semantic == "blocked" and claim.get("status") != "active" and state in {"waiting", "handoff"}:
        return "correct", semantic, 0.92, f"blocked lifecycle metadata matches the {state} handoff state", warnings

    fresh_active_work = (
        claim.get("status") == "active"
        and state in {"working", "verifying"}
        and liveness == "fresh"
    )
    if semantic != "active" and fresh_active_work:
        return (
            "move_proposed",
            "active",
            0.92,
            "an active claim and fresh working pulse indicate this task belongs in active",
            warnings,
        )

    if semantic == "active":
        if fresh_active_work:
            return "correct", "active", 0.96, "active claim and fresh working pulse match the active column", warnings
        if state in {"waiting", "handoff"}:
            return "needs_attention", "active", 0.86, f"active work is {state}; keep the task active and inspect the handoff", warnings
        if claim.get("status") in {"active", "expired", "unknown"}:
            return "needs_attention", "active", 0.74, "active placement needs an owner or a current agent pulse", warnings
        if liveness in {"stale", "missing", "unknown"}:
            return "needs_attention", "active", 0.68, "active placement has no current agent pulse; this is a warning only", warnings
        return "needs_attention", "active", 0.62, "active placement is not supported by a current claim or pulse", warnings

    if claim.get("status") == "active" or state in {"working", "verifying", "waiting", "handoff"}:
        return "needs_attention", semantic, 0.78, "work signals do not match this non-active column", warnings
    if liveness == "stale":
        return "needs_attention", semantic, 0.65, "stale agent pulse needs review; it does not prove abandonment", warnings
    # Missing pulses are expected for unclaimed backlog work. Keep the warning
    # in the machine context without proposing a move based on absence.
    if semantic == "backlog" and claim.get("status") == "unclaimed" and work is None:
        return "correct", semantic, 0.93, "unclaimed work is in the backlog; no active pulse is required", warnings
    return "correct", semantic, 0.9, "no contradictory placement signal was observed", warnings


