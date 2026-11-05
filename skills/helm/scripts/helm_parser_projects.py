"""Register projects CLI commands."""

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
    config_check = subparsers.add_parser(
        "config-check", help="Show safe local endpoint diagnostics without network requests"
    )
    config_check.set_defaults(handler=cmd_config_check)

    projects = subparsers.add_parser("projects", help="List visible projects")
    projects.add_argument("--limit", type=int, default=200)
    projects.add_argument("--cursor", default="")
    projects.add_argument("--all", action="store_true", help="Follow every cursor page")
    projects.set_defaults(handler=cmd_projects)

    tasks = subparsers.add_parser("tasks", help="List or search tasks in a project")
    tasks.add_argument("--project", required=True)
    tasks.add_argument("--query", default="")
    tasks.add_argument("--limit", type=int, default=200)
    tasks.add_argument("--cursor", default="")
    tasks.add_argument("--all", action="store_true", help="Follow every cursor page")
    tasks.set_defaults(handler=cmd_tasks)

    releases = subparsers.add_parser("releases", help="List or inspect project product releases")
    release_actions = releases.add_subparsers(dest="release_action", required=True)

    releases_list = release_actions.add_parser("list", help="List releases in a project")
    releases_list.add_argument("--project", required=True, help="Project ID, key, slug, or name")
    releases_list.add_argument("--status", choices=("planned", "released"))
    releases_list.add_argument("--target-from", dest="target_from", help="Earliest target date (YYYY-MM-DD)")
    releases_list.add_argument("--target-to", dest="target_to", help="Latest target date (YYYY-MM-DD)")
    releases_list.add_argument("--limit", type=int, default=50)
    releases_list.add_argument("--cursor", default="")
    releases_list.add_argument("--all", action="store_true", help="Follow every cursor page")
    releases_list.set_defaults(handler=cmd_releases_list)

    releases_get = release_actions.add_parser("get", help="Read one project release")
    releases_get.add_argument("--project", required=True, help="Project ID, key, slug, or name")
    releases_get.add_argument("--release", required=True, help="Release ID or case-insensitive name")
    releases_get.set_defaults(handler=cmd_releases_get)

    release_work = subparsers.add_parser(
        "release-work",
        aliases=("release_work",),
        help="Read a release's dependency-aware work queue",
    )
    release_work.add_argument("--project", required=True, help="Project ID, key, slug, or name")
    release_work.add_argument("--release", required=True, help="Release ID or case-insensitive name")
    release_work.add_argument("--limit", type=int, default=50)
    release_work.add_argument("--cursor", default="")
    release_work.add_argument("--all", action="store_true", help="Follow every queue page")
    release_work.set_defaults(handler=cmd_release_work)

