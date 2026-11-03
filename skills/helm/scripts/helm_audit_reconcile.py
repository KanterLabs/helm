"""Versioned board audit reconciliation."""

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



def cmd_reconcile(client: Client, args: argparse.Namespace) -> Any:
    """Preview or explicitly apply approved, unchanged audit move proposals."""

    requested_audit = getattr(args, "audit", None)
    if requested_audit is None:
        requested_audit = getattr(args, "audit_id", None)
    audit_id = _audit_safe_identifier(requested_audit)
    if not audit_id:
        raise HelmError("audit ID is required")
    page_size = _audit_page_size(getattr(args, "page_size", getattr(args, "limit", 200)))
    apply_changes = bool(getattr(args, "apply", False))
    operation_id = getattr(args, "operation_id", "")
    if not operation_id:
        operation_id = "audit-reconcile-" + audit_id
    operation_id = _validate_operation_id(operation_id)

    run_payload, _ = client.call("GET", "/audits/" + parse.quote(audit_id, safe=""))
    if not isinstance(run_payload, dict):
        raise HelmError("Helm returned an unexpected audit run")
    project_id = _audit_safe_identifier(run_payload.get("project_id"))
    if not project_id:
        raise HelmError("audit run has no project")
    run_status = run_payload.get("status") if isinstance(run_payload.get("status"), str) else ""
    review_ready = run_status in {"complete", "partial"}
    findings_path = "/audits/" + parse.quote(audit_id, safe="") + "/findings"
    findings = _paged_collection(client, findings_path, [("limit", str(page_size))], context="audit finding")
    columns = _audit_columns(client, project_id, page_size)
    by_semantic = _reconcile_destination_columns(columns)
    now = datetime.now(timezone.utc)
    results: list[dict[str, Any]] = []
    for finding in findings:
        finding_id = _audit_safe_identifier(finding.get("id")) or None
        task_id = _audit_safe_identifier(finding.get("task_id"))
        result: dict[str, Any] = {
            "finding_id": finding_id,
            "task_id": task_id or None,
            "status": "skipped",
            "review_state": finding.get("review_state") if isinstance(finding.get("review_state"), str) else None,
            "verdict": finding.get("verdict") if isinstance(finding.get("verdict"), str) else None,
        }
        if not task_id:
            result["reason"] = "finding has no task ID"
            results.append(result)
            continue
        if not review_ready:
            result["reason"] = "audit run must be complete or partial before reconciliation"
            result["action_required"] = "wait_for_audit" if run_status in {"queued", "running"} else "rerun_audit"
            results.append(result)
            continue
        try:
            current, _ = client.call("GET", "/tasks/" + parse.quote(task_id, safe=""))
            if not isinstance(current, dict) or not isinstance(current.get("version"), int):
                raise HelmError("unexpected task")
        except HelmError as exc:
            result["reason"] = "current task unavailable"
            result["error"] = _audit_error_context(exc)
            result["action_required"] = "refresh_task"
            results.append(result)
            continue

        current_version = current.get("version")
        current_column_id = _audit_safe_identifier(current.get("column_id"))
        if current.get("project_id") and current.get("project_id") != project_id:
            result["reason"] = "current task is outside the audit project"
            result["action_required"] = "inspect_scope"
            results.append(result)
            continue
        source_column_id = _audit_column_id(finding.get("source_column", finding.get("source_column_id")))
        captured_version = finding.get("captured_version")
        changed = bool(finding.get("changed_since_audit")) or (
            not isinstance(captured_version, int)
            or current_version != captured_version
            or not source_column_id
            or current_column_id != source_column_id
        )
        result.update(
            {
                "captured_version": captured_version if isinstance(captured_version, int) else None,
                "current_version": current_version,
                "source_column_id": source_column_id or None,
                "current_column_id": current_column_id or None,
                "changed_since_audit": changed,
            }
        )
        if changed:
            result["reason"] = "task changed since audit; refresh the finding before reconciling"
            result["action_required"] = "rerun_audit"
            results.append(result)
            continue
        if finding.get("review_state") != "approved":
            result["reason"] = "finding is not approved"
            result["action_required"] = "review_finding"
            results.append(result)
            continue
        if finding.get("verdict") != "move_proposed":
            result["reason"] = "finding is not a move proposal"
            results.append(result)
            continue

        destination_semantic = _audit_semantic_value(finding.get("proposed_semantic_destination"))
        result["destination_semantic_state"] = destination_semantic or None
        lifecycle_action = _reconcile_action_for_destination(destination_semantic)
        if lifecycle_action:
            result["reason"] = f"{destination_semantic} requires its lifecycle action"
            result["action_required"] = lifecycle_action
            results.append(result)
            continue
        destination = by_semantic.get(destination_semantic)
        destination_id = _audit_safe_identifier(destination.get("id")) if destination else ""
        if destination_semantic not in {"backlog", "ready"} or not destination_id:
            result["reason"] = "destination semantic column is unavailable"
            result["action_required"] = "review_destination"
            results.append(result)
            continue
        result["destination_column_id"] = destination_id
        result["current_column_semantic_state"] = _audit_semantic_value(
            columns.get(current_column_id, {}).get("semantic_state")
        ) or None
        if destination_id == current_column_id:
            result["reason"] = "task is already in the proposed destination column"
            results.append(result)
            continue
        current_semantic = _audit_semantic_value(columns.get(current_column_id, {}).get("semantic_state"))
        if current_semantic == "completed" or current.get("completed_at"):
            result["reason"] = "completed tasks require an explicit lifecycle action"
            result["action_required"] = "reopen"
            results.append(result)
            continue
        claim = _audit_claim(current, now)
        if claim["status"] == "active":
            result["reason"] = "task has an active claim; release it before moving"
            result["action_required"] = "release_claim"
            result["claim"] = claim
            results.append(result)
            continue
        if claim["status"] == "unknown":
            result["reason"] = "task claim lease is not verifiable"
            result["action_required"] = "inspect_claim"
            result["claim"] = claim
            results.append(result)
            continue

        move_path = "/tasks/" + parse.quote(task_id, safe="") + "/move"
        reason = _audit_redact_text(finding.get("reason"), 2000) or "Approved Board Audit move proposal"
        body = {
            "destination_column_id": destination_id,
            "expected_source_column_id": source_column_id,
            "source": "board_audit",
            "reason": reason,
        }
        result["request"] = {
            "path": move_path,
            "if_match_version": current_version,
            "body": body,
        }
        if not apply_changes:
            result["status"] = "preview"
            result["reason"] = "approved unchanged move proposal; apply explicitly to mutate"
            results.append(result)
            continue
        try:
            moved, _ = client.call(
                "POST",
                move_path,
                body=body,
                if_match=current_version,
                idempotency_key=_mutation_idempotency(operation_id, "POST", move_path, body),
            )
            result["status"] = "applied"
            result["reason"] = "approved move applied"
            if isinstance(moved, dict):
                result["resulting_version"] = moved.get("version")
        except HelmError as exc:
            result["status"] = "conflicted" if exc.status_code == 409 else "error"
            result["reason"] = "task changed during reconciliation" if exc.status_code == 409 else "move was not applied"
            result["error"] = _audit_error_context(exc)
        results.append(result)

    summary = {
        "total": len(results),
        "preview": sum(1 for item in results if item.get("status") == "preview"),
        "applied": sum(1 for item in results if item.get("status") == "applied"),
        "skipped": sum(1 for item in results if item.get("status") == "skipped"),
        "conflicted": sum(1 for item in results if item.get("status") == "conflicted"),
        "errors": sum(1 for item in results if item.get("status") == "error"),
    }
    return {
        "audit_id": audit_id,
        "project_id": project_id,
        "audit_status": run_status or None,
        "apply": apply_changes,
        "read_only": not apply_changes,
        "operation_id": operation_id,
        "summary": summary,
        "findings": results,
    }


