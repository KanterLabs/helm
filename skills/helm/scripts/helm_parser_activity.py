"""Register activity CLI commands."""

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
    notifications = subparsers.add_parser(
        "notifications", aliases=("notification",), help="List, read, or configure notifications"
    )
    notification_actions = notifications.add_subparsers(dest="notification_action", required=True)

    notifications_list = notification_actions.add_parser("list", help="List notification inbox items")
    notifications_list.add_argument("--unread", action="store_true", help="Only show unread items")
    notifications_list.add_argument("--cursor", default="")
    notifications_list.add_argument("--limit", type=int, default=50)
    notifications_list.add_argument("--all", action="store_true", help="Follow every cursor page")
    notifications_list.set_defaults(handler=cmd_notifications_list)

    notifications_read = notification_actions.add_parser("read", help="Mark notification items read")
    notification_target = notifications_read.add_mutually_exclusive_group(required=True)
    notification_target.add_argument("--notification", help="One notification ID")
    notification_target.add_argument("--id", dest="ids", action="append", help="Notification ID (repeatable)")
    notification_target.add_argument("--all", action="store_true", help="Mark all visible notifications read")
    notifications_read.add_argument(
        "--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)"
    )
    notifications_read.set_defaults(handler=cmd_notifications_read)

    notifications_preferences = notification_actions.add_parser(
        "preferences", help="Read or update notification preferences"
    )
    for field in ("assignments", "mentions", "blockers", "state_changes"):
        option = "--" + field.replace("_", "-")
        option_names = (option, "--" + field) if "_" in field else (option,)
        notifications_preferences.add_argument(
            *option_names,
            type=_parse_strict_bool,
            metavar="true|false",
            help=f"Enable or disable {field.replace('_', ' ')} notifications",
        )
    notifications_preferences.add_argument(
        "--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)"
    )
    notifications_preferences.set_defaults(handler=cmd_notification_preferences)

    watches = subparsers.add_parser("watches", aliases=("watch",), help="List or mutate notification watches")
    watch_actions = watches.add_subparsers(dest="watch_action", required=True)
    watches_list = watch_actions.add_parser("list", help="List owned watches")
    watches_list.add_argument("--project", help="Filter by project ID, key, or slug")
    watches_list.add_argument("--task", help="Filter by task key or opaque ID")
    watches_list.set_defaults(handler=cmd_watches_list)

    watches_add = watch_actions.add_parser("add", help="Follow one project or task")
    watch_target = watches_add.add_mutually_exclusive_group(required=True)
    watch_target.add_argument("--project", help="Project ID, key, or slug")
    watch_target.add_argument("--task", help="Task key or opaque ID")
    watches_add.add_argument("--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)")
    watches_add.set_defaults(handler=cmd_watches_add)

    watches_remove = watch_actions.add_parser("remove", help="Remove an owned watch")
    watches_remove.add_argument("--watch", required=True, help="Watch ID")
    watches_remove.add_argument("--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)")
    watches_remove.set_defaults(handler=cmd_watches_remove)

    auth_check = subparsers.add_parser(
        "auth-check",
        aliases=("auth", "auth_check"),
        help="Validate the configured agent token without mutating Helm",
    )
    auth_check.set_defaults(handler=cmd_auth_check)

    events = subparsers.add_parser(
        "events", aliases=("event", "events-poll"), help="Poll events after a monotonic cursor"
    )
    events.add_argument("--after", type=int, default=0, help="Last event cursor observed (default: 0)")
    events.add_argument("--project", help="Restrict events to a project ID, key, or slug")
    events.add_argument("--limit", type=int, default=50)
    events.add_argument("--all", action="store_true", help="Follow every event page")
    events.set_defaults(handler=cmd_events)

    timeline = subparsers.add_parser(
        "timeline",
        aliases=("activity", "task-timeline", "project-timeline"),
        help="Read a task or project timeline",
    )
    timeline_target = timeline.add_mutually_exclusive_group(required=True)
    timeline_target.add_argument("--task", help="Task key or opaque task ID")
    timeline_target.add_argument("--project", help="Project key, slug, or opaque project ID")
    timeline.add_argument("--before", default="", help="Opaque cursor from a prior page")
    timeline.add_argument(
        "--kind", choices=("agent_progress", "comment", "task_change"), help="Optional activity kind filter"
    )
    timeline.add_argument("--limit", type=int, default=50)
    timeline.add_argument("--all", action="store_true", help="Follow every opaque cursor page")
    timeline.set_defaults(handler=cmd_timeline)

    renew = subparsers.add_parser("renew", help="Renew the current owner's active task lease")
    renew.add_argument("--task", required=True)
    renew.add_argument("--lease-seconds", type=int, choices=range(30, 604801), default=1800)
    renew.add_argument("--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)")
    renew.set_defaults(handler=cmd_renew)

    release = subparsers.add_parser(
        "release",
        aliases=("unclaim",),
        help="Release (unclaim) the current owner's active task lease",
    )
    release.add_argument("--task", required=True)
    release.add_argument("--operation-id", help="UUIDv4 for deterministic replay (generated when omitted)")
    release.set_defaults(handler=cmd_release)

