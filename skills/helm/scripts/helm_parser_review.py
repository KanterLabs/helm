"""Register review CLI commands."""

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



def register(subparsers):
    def add_dependency_args(parser: argparse.ArgumentParser, *, mutation: bool) -> None:
        parser.add_argument("--task", required=True, help="Dependent task key or opaque ID")
        if mutation:
            parser.add_argument(
                "--prerequisite",
                "--depends-on",
                required=True,
                help="Prerequisite task key or opaque ID",
            )
            parser.add_argument(
                "--operation-id",
                help="UUIDv4 for deterministic replay (generated when omitted)",
            )

    dependencies = subparsers.add_parser(
        "dependencies", aliases=("dependency",), help="List or mutate direct task dependencies"
    )
    dependency_actions = dependencies.add_subparsers(dest="dependency_action", required=True)
    dependency_list = dependency_actions.add_parser("list", help="List prerequisites and dependents")
    add_dependency_args(dependency_list, mutation=False)
    dependency_list.set_defaults(handler=cmd_dependency_list)
    dependency_add = dependency_actions.add_parser("add", help="Add a prerequisite edge")
    add_dependency_args(dependency_add, mutation=True)
    dependency_add.set_defaults(handler=cmd_dependency_add)
    dependency_remove = dependency_actions.add_parser("remove", help="Remove a prerequisite edge")
    add_dependency_args(dependency_remove, mutation=True)
    dependency_remove.set_defaults(handler=cmd_dependency_remove)

    for name, handler, mutation in (
        ("dependency-list", cmd_dependency_list, False),
        ("dependency-add", cmd_dependency_add, True),
        ("dependency-remove", cmd_dependency_remove, True),
    ):
        dependency_command = subparsers.add_parser(name, help=f"{name.replace('-', ' ').capitalize()}")
        add_dependency_args(dependency_command, mutation=mutation)
        dependency_command.set_defaults(handler=handler)

    def add_issue_filter_args(parser: argparse.ArgumentParser) -> None:
        parser.add_argument("--project")
        parser.add_argument("--state")
        parser.add_argument("--column")
        parser.add_argument("--priority")
        parser.add_argument("--severity")
        parser.add_argument("--label")
        parser.add_argument("--assignee")
        parser.add_argument("--reporter")
        parser.add_argument("--resolution")
        parser.add_argument("--agent-state")
        parser.add_argument("--action-needed", choices=("true", "false"))
        parser.add_argument("--query", "-q")
        parser.add_argument("--updated-after")
        parser.add_argument("--cursor", default="")
        parser.add_argument("--limit", type=int, default=50)
        parser.add_argument("--all", action="store_true", help="Follow every cursor page")

    issues = subparsers.add_parser(
        "issues",
        aliases=("issue", "bug-list", "issue-list"),
        help="List visible bug tasks with lifecycle filters",
    )
    add_issue_filter_args(issues)
    issues.set_defaults(handler=cmd_issues)

    def add_bug_report_args(parser: argparse.ArgumentParser) -> None:
        parser.add_argument("--project", required=True)
        parser.add_argument("--title", required=True)
        parser.add_argument("--actual-behavior", required=True, dest="actual_behavior")
        parser.add_argument("--expected-behavior", dest="expected_behavior")
        parser.add_argument("--reproduction-steps", dest="reproduction_steps")
        parser.add_argument("--environment")
        parser.add_argument("--affected-version", dest="affected_version")
        parser.add_argument("--severity", choices=("s1", "s2", "s3", "s4"))
        parser.add_argument("--description")
        parser.add_argument("--priority", choices=("low", "normal", "high", "urgent"))
        parser.add_argument("--column")
        parser.add_argument("--assignee")
        parser.add_argument("--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)")

    bug_report = subparsers.add_parser(
        "bug-report", aliases=("report-bug", "report"), help="Create a structured bug task"
    )
    add_bug_report_args(bug_report)
    bug_report.set_defaults(handler=cmd_bug_report)

    def add_bug_action_args(parser: argparse.ArgumentParser, action: str) -> None:
        parser.add_argument("--task", required=True, help="Bug key or opaque task ID")
        parser.add_argument("--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)")
        if action == "triage":
            parser.add_argument("--severity", required=True, choices=("s1", "s2", "s3", "s4"))
            parser.add_argument("--priority", choices=("low", "normal", "high", "urgent"))
            parser.add_argument("--assignee")
            parser.add_argument("--column")
        elif action == "resolve":
            parser.add_argument(
                "--resolution",
                required=True,
                choices=("fixed", "duplicate", "not_planned", "cannot_reproduce", "works_as_designed"),
            )
            parser.add_argument("--duplicate-of", dest="duplicate_of")
            parser.add_argument("--note")
        elif action == "duplicate":
            parser.add_argument("--duplicate-of", required=True, dest="duplicate_of")
            parser.add_argument("--note")
        elif action == "reopen":
            parser.add_argument("--reason", required=True)

    bug_action_specs = (
        ("bug-triage", cmd_bug_triage, "triage"),
        ("bug-resolve", cmd_bug_resolve, "resolve"),
        ("bug-duplicate", cmd_bug_duplicate, "duplicate"),
        ("bug-reopen", cmd_bug_reopen, "reopen"),
    )
    for name, handler, action in bug_action_specs:
        bug_action = subparsers.add_parser(name, help=f"{action.capitalize()} a bug")
        add_bug_action_args(bug_action, action)
        bug_action.set_defaults(handler=handler)

    bug = subparsers.add_parser("bug", aliases=("bugs",), help="Run a bug report, list, or lifecycle command")
    bug_actions = bug.add_subparsers(dest="bug_action", required=True)
    bug_list = bug_actions.add_parser("list", help="List visible bugs")
    add_issue_filter_args(bug_list)
    bug_list.set_defaults(handler=cmd_issues)
    bug_report_nested = bug_actions.add_parser("report", help="Create a structured bug")
    add_bug_report_args(bug_report_nested)
    bug_report_nested.set_defaults(handler=cmd_bug_report)
    for action, handler in (("triage", cmd_bug_triage), ("resolve", cmd_bug_resolve), ("duplicate", cmd_bug_duplicate), ("reopen", cmd_bug_reopen)):
        nested = bug_actions.add_parser(action, help=f"{action.capitalize()} a bug")
        add_bug_action_args(nested, action)
        nested.set_defaults(handler=handler)

    audit = subparsers.add_parser("audit", help="Read-only audit of board column placement")
    audit.add_argument("--project", required=True)
    audit.add_argument(
        "--state",
        "--semantic-state",
        dest="states",
        action="append",
        choices=AUDIT_SEMANTIC_STATES,
        help="Semantic state to inspect; repeat to select more than one (default: backlog and active)",
    )
    audit.add_argument("--limit", "--page-size", dest="page_size", type=int, default=200)
    audit.add_argument(
        "--evidence-limit",
        type=int,
        default=AUDIT_MAX_EVIDENCE_ITEMS,
        help=f"Maximum recent comments per task to include (0-{AUDIT_MAX_EVIDENCE_ITEMS})",
    )
    audit.set_defaults(handler=cmd_audit)

    submit = subparsers.add_parser(
        "submit",
        aliases=("audit-submit", "submit-audit"),
        help="Persist an audit context as a durable run and findings",
    )
    submit.add_argument("--project", required=True)
    submit.add_argument(
        "--audit",
        help="Append to and finalize an existing queued/running audit instead of creating a run",
    )
    submit.add_argument("--input", "--findings", "--findings-file", dest="input_path", required=True)
    submit.add_argument("--scope", default="board")
    submit.add_argument("--status", choices=("complete", "partial", "failed"), default="complete")
    submit.add_argument("--operation-id", required=True)
    submit.set_defaults(handler=cmd_submit)

    reconcile = subparsers.add_parser(
        "reconcile",
        help="Preview approved audit moves; use --apply to mutate explicitly",
    )
    reconcile.add_argument("--audit", required=True)
    reconcile.add_argument("--apply", action="store_true", help="Apply eligible approved moves")
    reconcile.add_argument("--limit", "--page-size", dest="page_size", type=int, default=200)
    reconcile.add_argument(
        "--operation-id",
        help="Stable idempotency namespace (default: audit-reconcile-<audit-id>)",
    )
    reconcile.set_defaults(handler=cmd_reconcile)

