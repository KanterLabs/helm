"""Register project-scoped Focus discovery and lifecycle commands."""
from __future__ import annotations

import argparse


def _focus_positive_int(value):
    try:
        number = int(value)
    except (TypeError, ValueError) as exc:
        raise argparse.ArgumentTypeError("must be a positive integer") from exc
    if number < 1:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return number


def _focus_scope(parser, *, reference=True, optional=False):
    parser.add_argument("--project", required=True, help="Project ID, key, slug, or name")
    if reference:
        parser.add_argument("--focus", dest="release", required=not optional,
                            help="Focus ID or case-insensitive project-local name")


def _focus_pages(parser):
    parser.add_argument("--limit", type=_focus_positive_int, default=50)
    parser.add_argument("--cursor", default="")
    parser.add_argument("--all", action="store_true", help="Follow every cursor page")


def _focus_mutation_flags(parser, *, version=True):
    parser.add_argument("--operation-id", help="Reuse this UUIDv4 when reconciling/retrying one logical operation")
    if version:
        parser.add_argument("--version", type=_focus_positive_int,
                            help="Expected version override; otherwise read the current strong ETag")


def _focus_metadata_flags(parser, *, create=False):
    parser.add_argument("--name", required=create)
    description = parser.add_mutually_exclusive_group()
    description.add_argument("--description", default=None)
    target = parser.add_mutually_exclusive_group()
    target.add_argument("--target-date", help="Target date (YYYY-MM-DD)")
    if not create:
        description.add_argument("--clear-description", action="store_true", help="Set description to null")
        target.add_argument("--clear-target-date", action="store_true", help="Set target date to null")


def register(subparsers):
    focus = subparsers.add_parser("focus", help="Discover and manage project Focus areas")
    actions = focus.add_subparsers(dest="focus_action", required=True)

    listing = actions.add_parser("list", help="List Focus areas in a project (read-only)")
    _focus_scope(listing, reference=False)
    _focus_pages(listing)
    listing.add_argument("--status", choices=("planned", "released"))
    listing.add_argument("--target-from", help="Earliest target date (YYYY-MM-DD)")
    listing.add_argument("--target-to", help="Latest target date (YYYY-MM-DD)")
    listing.set_defaults(handler=cmd_releases_list)

    get = actions.add_parser("get", help="Read a Focus and its summary (read-only)")
    _focus_scope(get)
    get.set_defaults(handler=cmd_releases_get)

    work = actions.add_parser("work", help="Read a Focus dependency-aware work queue (read-only)")
    _focus_scope(work)
    _focus_pages(work)
    work.set_defaults(handler=cmd_release_work)

    review = actions.add_parser("review", help="Review Focus and neglected Backlog evidence (read-only)")
    _focus_scope(review, optional=True)
    for flag, default in (("inactive-days", 14), ("focus-limit", 20), ("task-limit", 200),
                          ("history-limit", 50), ("timeline-limit", 100), ("request-limit", 100),
                          ("time-limit", 30)):
        review.add_argument("--" + flag, type=_focus_positive_int, default=default)
    review.add_argument("--cursor", default="", help="Continue a prior bounded review")
    review.set_defaults(handler=cmd_focus_review)

    create = actions.add_parser("create", help="Create a Focus with an explicit goal/scope")
    _focus_scope(create, reference=False)
    _focus_metadata_flags(create, create=True)
    _focus_mutation_flags(create, version=False)
    create.set_defaults(handler=cmd_focus_create)

    edit = actions.add_parser("edit", help="Edit Focus metadata; omitted fields are preserved")
    _focus_scope(edit)
    _focus_metadata_flags(edit)
    _focus_mutation_flags(edit)
    edit.set_defaults(handler=cmd_focus_edit)

    for action, handler in (("assign", cmd_focus_assign), ("unassign", cmd_focus_unassign)):
        membership = actions.add_parser(action, help=action.capitalize() + " tasks without claiming or moving them")
        _focus_scope(membership)
        membership.add_argument("--task", action="append", required=True,
                                help="Task key or ID; repeat for several tasks (partial success is reported)")
        _focus_mutation_flags(membership)
        membership.set_defaults(handler=handler)

    for action, handler, help_text in (
        ("complete", cmd_focus_complete, "Explicitly complete a ready Focus"),
        ("reopen", cmd_focus_reopen, "Reopen a completed Focus with a reason"),
        ("delete", cmd_focus_delete, "Delete an empty active Focus"),
    ):
        lifecycle = actions.add_parser(action, help=help_text)
        _focus_scope(lifecycle)
        _focus_mutation_flags(lifecycle)
        if action == "reopen":
            lifecycle.add_argument("--reason", required=True)
        lifecycle.set_defaults(handler=handler)
