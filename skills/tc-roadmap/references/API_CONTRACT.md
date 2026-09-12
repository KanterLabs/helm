# TC Roadmap compatibility API reference

This compatibility reference mirrors the release-bound read contract shipped
with `skills/helm/references/API_CONTRACT.md`. The legacy helper keeps its
`tc_roadmap.py` command name and `tc-roadmap` user-agent label, but uses the
same `/api/v1` routes and JSON shapes.

## Release discovery

The read routes are:

```text
GET /api/v1/projects/{project}/releases
GET /api/v1/releases/{release}
GET /api/v1/releases/{release}/work-queue
```

`{project}` accepts an ID, key, slug, or name. The helper resolves it through
`GET /projects?limit=200`, rejects an ambiguous match, then uses the opaque
project ID. Release names are resolved case-insensitively only inside that
project. `releases get` performs a second direct GET by the resolved release
ID, so a same-named release in another project cannot be returned.

```sh
python3 scripts/tc_roadmap.py releases list --project TC [--status planned]
python3 scripts/tc_roadmap.py releases get --project TC --release 1.4
python3 scripts/tc_roadmap.py release-work --project TC --release 1.4
```

Release lists accept `status=planned|released`, `target_from`, `target_to`,
`cursor`, and `limit` (1–200). The list result is
`{ "project": "TC", "releases": [...], "next_cursor": "..." }`; `--all`
follows every page and returns an empty terminal cursor. Get returns the
release object directly. Each release object has `id`, `project_id`, `name`,
`description`, nullable ISO `target_date`, `status` (`planned` or `released`),
nullable `released_at` and `released_by`, `version`, `created_at`,
`updated_at`, and a `summary` containing `task_count`, `completed_count`,
`blocked_count`, `claimed_count`, `checklist_warning_count`,
`required_task_count`, `required_completed_count`,
`cross_release_conflict_count`, and `ready_to_release`.

## Release work queue

The queue is read-only and returns:

```json
{
  "release": {"id": "release_14", "name": "1.4", "version": 3},
  "snapshot": {"project_revision": 812, "read_at": "2026-08-27T10:00:00Z"},
  "summary": {
    "direct": 12, "required": 15, "completed": 9, "claimable": 1,
    "owned": 1, "dependency_blocked": 2, "manually_blocked": 1,
    "claimed_elsewhere": 1, "cross_release_conflicts": 1
  },
  "data": [{
    "task": {"id": "task_42", "key": "TC-42", "version": 8},
    "relationship": "direct", "disposition": "claimable", "blocked_by": []
  }],
  "next_cursor": ""
}
```

Queue data includes direct members and transitive same-project prerequisites,
including dependency-free tasks. Dispositions are `completed`, `owned`,
`claimable`, `dependency_blocked`, `manually_blocked`, `claimed_elsewhere`,
and `cross_release_conflict`. Owned work sorts first, followed by claimable
work in dependency order. A queue cursor captures release and project/task
collection revisions; HTTP 409 `release_queue_changed` with
`details.restart=true` means discard partial data and restart from page one.
The helper performs at most two automatic restarts before returning the safe
409 error.

`release-work` adds read-only `state`, `guidance`, and `workflow` fields. State
is `ready` when every required task is complete, `handoff` when owned or
claimable work remains, and `waiting` when no safe task can proceed. Guidance
names dependency/manual/foreign/cross-release blockers and checklist warnings.
The helper never claims, reassigns, completes, or blocks a task while reading
the queue.

For a valid queue snapshot, including `ready`, `waiting`, or `handoff`, the
CLI prints compact JSON to stdout and exits `0`. Invalid arguments, an
ambiguous or missing project/release, transport/API errors, malformed API
shapes, repeated cursors, or an exhausted queue-restart budget print an error
to stderr and exit non-zero; no mutation is attempted.

The task lease compatibility command remains singular; `unclaim` is only an
alias and does not address the product release:

```sh
python3 scripts/tc_roadmap.py release --task TC-42
python3 scripts/tc_roadmap.py unclaim --task TC-42
```
