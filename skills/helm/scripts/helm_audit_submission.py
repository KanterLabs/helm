"""Audit reads and reviewed finding submissions."""

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



def cmd_audit(client: Client, args: argparse.Namespace) -> Any:
    """Build a bounded, read-only Board Audit context.

    Every network request made by this command is a GET. In particular, an
    audit never claims, renews, moves, comments on, or publishes progress for
    a task; the returned rubric is for an agent or human to review.
    """

    requested_states = getattr(args, "states", None)
    if requested_states is None:
        requested_states = getattr(args, "state", None)
    states = _audit_states(requested_states)
    requested_page_size = getattr(args, "page_size", None)
    if requested_page_size is None:
        requested_page_size = getattr(args, "limit", 200)
    page_size = _audit_page_size(requested_page_size)
    evidence_limit = _audit_evidence_limit(getattr(args, "evidence_limit", AUDIT_MAX_EVIDENCE_ITEMS))
    project = _project(client, args.project)
    project_id = _audit_safe_identifier(project.get("id"))
    if not project_id:
        raise HelmError("Helm returned an unexpected project")
    columns = _audit_columns(client, project_id, page_size)
    tasks = _audit_tasks(client, project_id, states, page_size)
    now = datetime.now(timezone.utc)
    audited: list[dict[str, Any]] = []
    for task in tasks:
        task_id = _audit_safe_identifier(task.get("id"))
        if not task_id:
            # The collection contract requires IDs. Ignore malformed rows so a
            # single bad row cannot produce an unaddressable recommendation.
            continue
        comments, evidence_warning = _audit_comments(client, task_id, evidence_limit)
        evidence, _ = _audit_evidence_from_comments(comments, evidence_limit)
        audited.append(_audit_task_context(task, columns, evidence, evidence_warning, now))

    return {
        "project": project.get("key") if isinstance(project.get("key"), str) else project_id,
        "project_id": project_id,
        "states": list(states),
        "read_only": True,
        "tasks": audited,
        "rubric": _audit_rubric(),
    }


def cmd_submit(client: Client, args: argparse.Namespace) -> Any:
    """Persist agent-reviewed findings as one durable audit run.

    Submission is intentionally separate from ``audit``. The scan remains a
    GET-only operation, while this explicit command records the agent's
    semantic judgment and then closes the run.
    """

    operation_id = _validate_operation_id(args.operation_id)
    project = _project(client, args.project)
    project_id = _audit_safe_identifier(project.get("id"))
    if not project_id:
        raise HelmError("Helm returned an unexpected project")
    scope = _audit_redact_text(getattr(args, "scope", "board"), 200)
    if not scope:
        raise HelmError("audit submission scope is required")
    raw_input = getattr(args, "findings", None)
    if raw_input is None:
        for input_name in ("input_path", "findings_file", "input_file"):
            raw_input = getattr(args, input_name, None)
            if raw_input is not None:
                break
    items = _audit_submission_items(raw_input)
    normalized: list[dict[str, Any]] = []
    local_results: list[dict[str, Any]] = []
    seen_tasks: set[str] = set()
    for item in items:
        finding, error_message = _audit_submission_finding(item)
        if finding is None:
            local_results.append({"status": "skipped", "reason": error_message or "invalid finding"})
            continue
        task_id = str(finding["task_id"])
        if task_id in seen_tasks:
            local_results.append({"task_id": task_id, "status": "skipped", "reason": "duplicate task finding"})
            continue
        seen_tasks.add(task_id)
        normalized.append(finding)

    requested_audit = _audit_safe_identifier(getattr(args, "audit", None))
    if requested_audit:
        run_path = "/audits/" + parse.quote(requested_audit, safe="")
        run, _ = client.call("GET", run_path)
        if not isinstance(run, dict):
            raise HelmError("Helm returned an unexpected audit run")
        if _audit_safe_identifier(run.get("project_id")) != project_id:
            raise HelmError("queued audit belongs to a different project")
        if run.get("status") not in {"queued", "running"}:
            raise HelmError("audit run is already finalized")
        audit_id = _audit_run_id(run)
    else:
        create_path = f"/projects/{parse.quote(project_id, safe='')}/audits"
        create_body = {"scope": scope}
        run, _ = client.call(
            "POST",
            create_path,
            body=create_body,
            idempotency_key=_mutation_idempotency(operation_id, "POST", create_path, create_body),
        )
        audit_id = _audit_run_id(run)
    submitted = list(local_results)
    append_errors = 0
    for finding in normalized:
        path = f"/audits/{parse.quote(audit_id, safe='')}/findings"
        try:
            response, _ = client.call(
                "POST",
                path,
                body=finding,
                idempotency_key=_mutation_idempotency(operation_id, "POST", path, finding),
            )
            finding_id = _audit_safe_identifier(response.get("id")) if isinstance(response, dict) else ""
            submitted.append({"task_id": finding["task_id"], "finding_id": finding_id or None, "status": "appended"})
        except HelmError as exc:
            append_errors += 1
            submitted.append({"task_id": finding["task_id"], "status": "error", "error": _audit_error_context(exc)})

    requested_status = getattr(args, "status", "complete")
    if requested_status not in {"complete", "partial", "failed"}:
        raise HelmError("audit submission status must be complete, partial, or failed")
    terminal_status = requested_status
    if append_errors or any(item.get("status") == "skipped" for item in local_results):
        if terminal_status == "complete":
            terminal_status = "partial"
    finalize_path = f"/audits/{parse.quote(audit_id, safe='')}/finalize"
    finalize_body = {"status": terminal_status}
    finalized, _ = client.call(
        "POST",
        finalize_path,
        body=finalize_body,
        idempotency_key=_mutation_idempotency(operation_id, "POST", finalize_path, finalize_body),
    )
    final_status = finalized.get("status", terminal_status) if isinstance(finalized, dict) else terminal_status
    return {
        "audit_id": audit_id,
        "project": project.get("key") if isinstance(project.get("key"), str) else project_id,
        "status": final_status,
        "findings": submitted,
        "appended": sum(1 for item in submitted if item.get("status") == "appended"),
        "errors": sum(1 for item in submitted if item.get("status") == "error"),
        "skipped": sum(1 for item in submitted if item.get("status") == "skipped"),
    }


