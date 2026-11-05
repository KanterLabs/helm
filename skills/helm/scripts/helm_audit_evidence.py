"""Board audit evidence snippets and rubric descriptions."""

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



def _audit_evidence_from_comments(comments: list[dict[str, Any]], limit: int) -> tuple[list[dict[str, Any]], list[str]]:
    if limit <= 0:
        return [], []
    # The API returns comments oldest-first. Keep only the newest bounded
    # window while preserving newest-first order for agent context.
    evidence: list[dict[str, Any]] = []
    for comment in comments[-limit:][::-1]:
        comment_id = _audit_safe_identifier(comment.get("id"))
        if not comment_id:
            continue
        body = comment.get("body", comment.get("text"))
        excerpt = _audit_redact_text(body, AUDIT_MAX_EVIDENCE_TEXT)
        entry: dict[str, Any] = {
            "kind": "comment",
            "id": comment_id,
            "created_at": _audit_safe_identifier(comment.get("created_at")),
        }
        actor_id = _audit_safe_identifier(comment.get("actor_id"))
        if actor_id:
            entry["actor_id"] = actor_id
        if excerpt:
            entry["excerpt"] = excerpt
        evidence.append(entry)
    return evidence, []


def _audit_rubric() -> dict[str, Any]:
    return {
        "version": 1,
        "agent_work_states": list(AUDIT_AGENT_STATES),
        "allowed_verdicts": ["correct", "needs_attention", "move_proposed"],
        "confidence": {
            "type": "number",
            "range": [0.0, 1.0],
            "labels": {"high": ">=0.80", "medium": "0.60-0.79", "low": "<0.60"},
        },
        "rules": [
            {
                "verdict": "move_proposed",
                "when": "A non-active task has an active claim and a fresh working or verifying pulse.",
                "suggested_semantic_state": "active",
            },
            {
                "verdict": "correct",
                "when": "The current column is consistent with claims, live work, and completion metadata.",
                "suggested_semantic_state": "current",
            },
            {
                "verdict": "needs_attention",
                "when": "Signals conflict, a claim is unverifiable, or active work lacks a current owner/pulse.",
                "suggested_semantic_state": "current",
            },
        ],
        "constraints": [
            "A stale or missing pulse is a warning only; never infer completion or abandonment from it.",
            "Do not claim, renew, release, move, complete, block, comment, or emit activity during an audit.",
            "The first release evaluates semantic column placement only and never reorders numeric positions.",
            "Use evidence references and bounded excerpts; do not copy credentials or unrestricted narrative.",
        ],
    }


def _audit_error_context(exc: HelmError) -> dict[str, Any]:
    """Return bounded machine context for a failed audit API call."""

    result: dict[str, Any] = {}
    if exc.error_code:
        result["code"] = exc.error_code[:120]
    if exc.status_code is not None:
        result["status"] = exc.status_code
    return result


