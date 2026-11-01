# Release-bound execution

When the user explicitly says “Work on all tasks needed for release X”, use the
release work queue as a bounded execution plan. This is an orchestration loop,
not a background worker:

1. Resolve exactly one visible project and exactly one `planned` release in
   that project. Names are case-insensitive only within the selected project;
   do not guess when the project or release is missing or ambiguous. Use
   `releases list` to discover a release and `releases get` to publish its
   current name, target date, version, and summary.

The corresponding read commands are:

```sh
python3 scripts/helm.py releases list --project TC [--status planned]
python3 scripts/helm.py releases get --project TC --release 1.4
python3 scripts/helm.py release-work --project TC --release 1.4
```

`release-work` accepts `--limit`, `--cursor`, and `--all`; it returns the
release queue snapshot plus deterministic `state`, `guidance`, and `workflow`
fields. These commands perform GET requests only. The existing task lifecycle
commands (`resume`, `progress`, `complete`, `block`, and `release`/`unclaim`)
remain the only way to mutate work during this loop.

Every valid queue snapshot exits `0` regardless of whether its state is
`ready`, `handoff`, or `waiting`; malformed input, ambiguous scope, API or
transport failure, malformed responses, or exhausted cursor restarts exit
non-zero and do not mutate work.

If the user cancels, interrupts, or restarts the workflow, stop at the current
read/mutation boundary and preserve only the partial progress already recorded
through the task lifecycle. A partial queue read is never an actionable plan:
discard it and re-read from the first page before continuing. Reuse an
operation ID only when retrying that same logical lifecycle mutation; a
different mutation gets a new ID, so retries remain idempotent without
replaying another action.

2. Read `release-work` and report the direct count, required dependency
   closure, completed count, and every non-zero blocker count as the execution
   scope before taking a lease. A dependency-free task is claimable; do not
   use the narrower `dependency=ready` filter as a substitute for this queue.
3. Resume an owned active task first. If the existing two-step `resume` claims
   the task but cannot move it to the Active column, treat the still-owned
   claim as resumable work and recover it; never claim another task in that
   situation. Use the task's goal, acceptance criteria, dependencies,
   checklist, and latest activity as its concrete work contract.
4. When no owned task needs resuming, claim exactly one `claimable` task with
   the existing `resume --task` flow. Never bulk-claim or hold multiple new
   leases. Work and finish that task through the existing versioned progress,
   complete, block, and claim-release actions.
5. After every completion, block, released claim, stale ETag, queue
   invalidation, or scope-changing event, discard the old queue cursor and
   refresh `release-work` from its first page. Include newly added direct
   tasks in the next scope and call out any material scope change.
6. Treat `completed` as done; do not touch `manually_blocked` tasks, override
   `claimed_elsewhere` leases, bypass `dependency_blocked` prerequisites, or
   work an incomplete prerequisite assigned to another planned release
   (`cross_release_conflict`). Checklist warnings require review and do not
   silently turn a task into completed work. Stop and report the blocker and
   its next action when it needs new authority.

The queue may return HTTP 409 `release_queue_changed` while a cursor is being
followed. Restart from the first page and recompute the scope; if contention
continues, leave a handoff rather than acting on stale rows. Keep the outcome
deterministic: report `ready` when every required task is complete, `handoff`
when owned or claimable work remains, and `waiting` when no safe task can
proceed. A ready queue only means “ready to release”; never auto-complete the
product release. Completing or reopening that release is a separate explicit
human-authorized lifecycle action.
