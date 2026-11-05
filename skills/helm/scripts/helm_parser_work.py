"""Register work CLI commands."""

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
    start = subparsers.add_parser("start", help="Create and claim an active task")
    start.add_argument("--project", required=True)
    start.add_argument("--title", required=True)
    start.add_argument("--goal", required=True)
    start.add_argument("--step", action="append", default=[])
    start.add_argument("--priority", choices=("low", "normal", "high", "urgent"), default="normal")
    start.add_argument("--lease-seconds", type=int, choices=range(30, 604801), default=604800)
    start.add_argument("--operation-id", help="UUIDv4 for new work; explicit legacy IDs remain deterministic")
    start.set_defaults(handler=cmd_start)

    backlog = subparsers.add_parser("backlog", help="Create an unclaimed backlog task")
    backlog.add_argument("--project", required=True)
    backlog.add_argument("--title", required=True)
    backlog.add_argument("--goal", required=True)
    backlog.add_argument("--step", action="append", default=[])
    backlog.add_argument("--priority", choices=("low", "normal", "high", "urgent"), default="normal")
    backlog.add_argument("--operation-id", help="UUIDv4 for new work; explicit legacy IDs remain deterministic")
    backlog.set_defaults(handler=cmd_backlog)

    resume = subparsers.add_parser("resume", help="Claim or renew an existing task")
    resume.add_argument("--task", required=True)
    resume.add_argument("--lease-seconds", type=int, choices=range(30, 604801), default=604800)
    resume.add_argument("--operation-id", help="UUIDv4 for new work; explicit legacy IDs remain deterministic")
    resume.set_defaults(handler=cmd_resume)

    notes = subparsers.add_parser("notes", aliases=("agent-notes",), help="Read or curate bounded agent task notes")
    note_actions = notes.add_subparsers(dest="note_action", required=True)
    note_list = note_actions.add_parser("list", help="List active agent notes")
    note_list.add_argument("--task", required=True); note_list.add_argument("--include-resolved", action="store_true"); note_list.set_defaults(handler=cmd_note_list)
    note_add = note_actions.add_parser("add", help="Add verified reusable task knowledge")
    note_add.add_argument("--task", required=True); note_add.add_argument("--category", required=True, choices=("known_issue", "rejected_approach", "constraint", "workaround")); note_add.add_argument("--body", required=True); note_add.add_argument("--evidence", action="append", default=[]); note_add.add_argument("--operation-id"); note_add.set_defaults(handler=cmd_note_add)
    note_update = note_actions.add_parser("update", help="Update an active agent note")
    note_update.add_argument("--task", required=True); note_update.add_argument("--note", required=True); note_update.add_argument("--category", choices=("known_issue", "rejected_approach", "constraint", "workaround")); note_update.add_argument("--body"); note_update.add_argument("--evidence", action="append", default=None); note_update.add_argument("--operation-id"); note_update.set_defaults(handler=cmd_note_update)
    note_resolve = note_actions.add_parser("resolve", help="Resolve an obsolete agent note")
    note_resolve.add_argument("--task", required=True); note_resolve.add_argument("--note", required=True); note_resolve.add_argument("--operation-id"); note_resolve.set_defaults(handler=cmd_note_resolve)

    heartbeat = subparsers.add_parser("heartbeat", help="Refresh agent-work liveness")
    heartbeat.add_argument("--task", required=True)
    heartbeat.add_argument("--operation-id", required=True)
    heartbeat.set_defaults(handler=cmd_heartbeat)

    progress = subparsers.add_parser("progress", help="Post a meaningful progress comment")
    progress.add_argument("--task", required=True)
    progress.add_argument("--message", required=True)
    progress.add_argument("--state", choices=("working", "waiting", "verifying", "handoff"))
    progress.add_argument("--phase")
    progress.add_argument("--completed", type=int)
    progress.add_argument("--total", type=int)
    progress.add_argument("--next", dest="next_step")
    progress.add_argument("--checkpoint-ref", action="append", dest="checkpoint_refs", default=[])
    progress.add_argument("--operation-id", help="UUIDv4 for new work; explicit legacy IDs remain deterministic")
    add_image_args(progress)
    progress.set_defaults(handler=cmd_progress)

    complete = subparsers.add_parser("complete", help="Complete a claimed task")
    complete.add_argument("--task", required=True)
    complete.add_argument("--comment", required=True)
    complete.add_argument("--operation-id", help="UUIDv4 for new work; explicit legacy IDs remain deterministic")
    complete.set_defaults(handler=lambda client, args: _action(client, args, "complete", "comment"))

    block = subparsers.add_parser("block", help="Block a claimed task")
    block.add_argument("--task", required=True)
    block.add_argument("--reason", required=True)
    block.add_argument("--operation-id", help="UUIDv4 for new work; explicit legacy IDs remain deterministic")
    block.set_defaults(handler=lambda client, args: _action(client, args, "block", "reason"))
