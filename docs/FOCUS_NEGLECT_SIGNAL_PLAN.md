# Server-side neglect signal for Focus and Backlog

Status: TC-377 to TC-381 implemented, uncommitted on `feat/neglect-signal`;
TC-382 validation (rollback, populated database, beta) pending.

Planning date: 8 November 2026. Planning task: TC-376. Builds on the delivered
Focus agent work in [FOCUS_AGENT_PLAN.md](FOCUS_AGENT_PLAN.md) (TC-368 to
TC-373). This plan moves the "meaningful inactivity" rule out of the Python CLI
and into the API so every surface uses one definition of a neglected card.

## Why this exists

Focus is meant to stop cards from being left behind in Backlog. Today the only
thing that can tell a card is neglected is `focus review`, a client-side scan
that an agent must choose to run. That has four consequences:

- The dashboard cannot show, filter, sort or count neglected cards.
- Project attention cannot weigh them; a project of forgotten cards is "quiet".
- Each review costs up to 50 timeline reads and still returns partial results.
- Almost every old card is reported as `unknown`. `changed_fields` on
  `task.updated` shipped with main `1c54493` on 7 November 2026, so every
  earlier edit is ambiguous to classification version 1. The cards most likely
  to be neglected are exactly the ones the review cannot judge.

The dashboard view, attention scoring, specific hook reminders and the
completion sweep all depend on this signal. They are separate later work.

## Outcome

Each task exposes a server-computed `activity` object. A list request can ask
"open cards with no meaningful activity for N days" and receive a complete,
paginated, sortable answer from one indexed query. `focus review` reads that
answer instead of replaying timelines. Old cards with ambiguous history are
classified as provably inactive when the evidence allows it.

Out of scope: any UI, project-attention scoring, notifications, schedulers
that message people, defer/disposition records, and any automatic mutation of
cards. The signal reports facts; it never moves, claims, or closes work.

## Findings that constrain the design

Verified in this checkout at `35f76ab` (one merge commit behind main):

1. SQLite via `modernc.org/sqlite`; `events.cursor` is `INTEGER PRIMARY KEY
   AUTOINCREMENT`, so committed cursors are strictly increasing.
2. Events are written from five places, not one: `insertEvent` in
   `sql_helpers.go`, two direct inserts in `dependencies.go`, one in
   `hierarchy.go`, and the portable import in `portable_import_execute.go`.
   Portable import appends old `created_at` values at new cursors.
3. Retained rollback binaries must keep working against a newer schema
   (`internal/db/migrations.go`). An older binary will write events and know
   nothing about a new activity column.
4. Every qualifying activity already produces an event: task create, update,
   move, lifecycle, dependency, checklist, bug, `comment.created`,
   `task.progressed`, and `attachment.uploaded`. Heartbeats produce none.
5. The task timeline (`timeline.go`, `timeline_task_sources.go`) already merges
   events, comments and `task_agent_work_history` with stable ordering and
   removes the generated progress comment duplicates.
6. Task ETags are `"v<version>"`. A derived field does not change them, as
   with agent-work liveness today.
7. There is precedent for a ticker-driven background worker in `httpapi`
   (`admin_metrics.go`, `attachments_runtime.go`).
8. Migrations on main end at `042_task_attachments.sql`. The next free number
   is expected to be 043; confirm against `origin/main` and `origin/beta`
   immediately before writing it.

## Design decisions

### D1. A derived read model rebuilt from history

Add table `task_activity` (one row per task) and a single-row
`task_activity_projection` holding the processed event cursor. A projector
finds tasks touched by events past that cursor and **rebuilds each touched
task's row from its complete history**. Nothing in the write path changes.

Rejected alternatives:

| Option | Why not |
| --- | --- |
| Compute on every read | Classification replays progress and checklist state in order; it cannot be expressed as a list filter, sort, or count. |
| Maintain a column inside each write | Five insert sites today, more later. A rollback binary would skip it and leave silently wrong data. |
| SQLite trigger marking tasks dirty | Adds a write to every event from every binary and puts logic in schema that migration-safety tests must then freeze. The cursor gives the same coverage for free. |
| Incremental per-event update | Needs stored per-task replay state and breaks when import appends older events. Full rebuild is idempotent and order-proof. Revisit only if the benchmark gate fails. |

The cursor approach covers all five insert sites, portable import, and events
written by a rollback binary while the new binary was not running.

### D2. Classification version 2: known, bounded, unknown

Version 1 returns `unknown` whenever an ambiguous event is at or after the
last qualifying one. Version 2 keeps the same qualifying and excluded rules
and adds one inference: meaningful activity cannot be later than the newest
event that is either qualifying or ambiguous. Excluded events never count.

| Field | Meaning |
| --- | --- |
| `status` | `known`, `bounded`, or `unknown` |
| `last_meaningful_at` | Timestamp of the last qualifying activity; set only when `known` |
| `source` | Qualifying source name, as in version 1 (for example `human.comment`) |
| `not_after` | Latest possible time of meaningful activity. Equals `last_meaningful_at` when `known`; the newest ambiguous or qualifying time when `bounded`; null when `unknown` |
| `reason` | For `bounded`/`unknown`: `legacy_update`, `unknown_event_type`, `actor_unavailable`, `unparseable_time`, or `classification_error` |
| `classification_version` | `2` |

A card is **inactive for N days** exactly when `not_after <= now - N days`.
This is true for both `known` and `bounded` rows, so a card last touched by an
ambiguous legacy edit 60 days ago is correctly reported as inactive. A
`bounded` row inside the window is **undetermined**, never "active". `unknown`
is reserved for rows with no usable bound and is always surfaced, never
dropped from a neglect listing silently.

Qualifying and excluded events follow version 1 (see the review rules in the
Focus agent plan and `helm_focus_activity.py`) except for this explicit list,
found by TC-377 because version 1 never named these server events:

| Event | Version 2 | Why |
| --- | --- | --- |
| `comment.updated`, `comment.deleted` | Excluded | Removing the last human comment must fall back to earlier evidence |
| `task.parent_linked`, `task.parent_unlinked` | Excluded | Organisation, like Focus membership |
| `task.deleted`, `attachment.updated`, `attachment.deleted` | Excluded | Not work on the card |
| `task.restored`, `bug.triaged`, `attachment.uploaded` | Qualifying | Real state change or new evidence |
| `task.dependency_state_changed` | Excluded | Caused by another card finishing; must not make a neglected card look fresh |

The last row is the only change to a version 1 `known` result. `bug.updated`
stays ambiguous. A task with complete history and no qualifying event uses
`tasks.created_at`. Assigning a card to a Focus (`task.release_changed`)
still does not reset the clock. Further rules fixed by the TC-377 corpus: an
unparseable time on any row gives `unknown`; with several ambiguous rows the
reason comes from the newest; `reason` is empty on `known` rows; every task
gets a row, including soft-deleted and terminal ones. A malformed payload is
a `classification_error`, never a failed read.

### D3. One classifier, fed by the timeline reader

The Go classifier consumes the same merged item stream the timeline endpoint
serves, through an internal unpaginated iterator over the existing source
queries. This keeps deduplication and ordering identical to what version 1
saw through the API. A shared fixture corpus is run through both the Python
version 1 classifier and the Go classifier; outside the D2 table, every
`known` result must match exactly and every version 1 `unknown` must map to
`bounded` or `unknown`. Corpus and table change together.

### D4. Projector lifecycle

- **Seed.** On first run, record the current maximum event cursor as the
  processed cursor, then rebuild every task that has no row at the current
  classification version, in id-keyset batches. Resumable after restart.
- **Tail.** Read the current maximum cursor `high`; select distinct task ids
  from events in `(processed, high]`, in bounded windows; rebuild them; set
  the processed cursor to the window end in the same transaction as the rows.
- **Wake.** Signal the projector after a store write transaction commits
  (`withTx`, `withImmediateTx`, `withImmediateDependencyTx`), with a ticker
  as fallback for writes made by another process or binary.
- **Reclassify.** Raising the classification version makes older rows
  eligible for the seed pass. No migration rewrites data.
- **Poison rows.** A task whose history cannot be classified is stored as
  `unknown` with `classification_error` and counted; it never blocks the
  cursor.
- **Concurrency.** One projector per process. Rebuild is an idempotent upsert,
  so a second process is wasteful but safe.

The migration only creates empty tables. Startup and `/readyz` do not wait
for the seed; a slow backfill must not fail a deployment.

### D5. Freshness on reads

Readers never write. Only a neglect listing (any D6 input) notes the
maximum event cursor at its start and waits up to 250 ms for the projector;
plain task reads never wait. Either way, tasks with unprocessed events are
returned with `status: "pending"`, and a neglect listing reports
`activity_coverage.complete: false` with the lag. A filtered listing
that is incomplete says so; it is never presented as the full neglected set.
`pending` is a transport status only and is never stored.

### D6. API contract (additive)

- `Task.activity`: the object from D2, present on task and collection reads.
- `GET /projects/{id}/tasks` gains `inactive_days` (positive integer),
  `activity_status` (`known`, `bounded`, `unknown`, `undetermined`), and sort
  value `not_after`. `inactive_days` combines with existing `state` and
  `release` filters, so "unfocused Backlog inactive 14 days" is one request.
- Collection responses that use these inputs include `evaluated_at` (one UTC
  instant per page chain, carried in the keyset cursor; offset cursors are
  rejected) and `activity_coverage`. Delivered contract:
  [test/neglect/API.md](../test/neglect/API.md).
- Existing filters, sorts, cursors, ETags, and portable archives are
  unchanged. Derived rows are never exported or imported.
- Update `openapi/` sources and regenerate; `make openapi-check` must pass.

### D7. CLI adoption with fallback

`focus review` requests the server signal when the API advertises it and
drops per-task timeline reads. Against an older server it keeps the version 1
path unchanged. Output keeps existing fields and adds `not_after`, the new
statuses, and which path produced the result. The skill reference documents
`bounded` and `undetermined` so agents do not describe them as active.
Delivered: detection adds no request (older servers ignore `inactive_days`),
so their output stays byte-identical; `activity_signal` marks the server path;
see [test/neglect/CLI.md](../test/neglect/CLI.md).

## Schema sketch

```sql
CREATE TABLE IF NOT EXISTS task_activity_signal (
    task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('known', 'bounded', 'unknown')),
    last_meaningful_at TEXT,
    source TEXT NOT NULL DEFAULT '',
    not_after TEXT,
    reason TEXT NOT NULL DEFAULT '',
    classification_version INTEGER NOT NULL,
    computed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS task_activity_signal_projection (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    event_cursor INTEGER NOT NULL DEFAULT 0,
    seeded_version INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
```

"Task activity" already names agent progress history in this codebase, so
implementation uses `task_activity_signal` and
`task_activity_signal_projection` for tables and `task_activity_signal_*.go`
for files; the API field stays `activity`. Neglect listings keep the existing
`409 task_collection_changed` behaviour of plain listings.

No index on `not_after` was needed (case 16 plan check). Migration 044 adds
two `task_agent_work_history` indexes (`progress_event_cursor`,
`generated_comment_id`) that the timeline replay needed on the populated
database. New Go files live in `internal/store` under responsibility names
(classifier, projector, reads); do not append to `timeline.go` or
`task_listing.go` beyond a call site.

## Failure cases

Written before implementation, per the repository testing policy. Each needs
a failing real-flow test first and a retained, sanitized artifact.

1. **Legacy history.** A card whose only post-creation events are legacy
   `task.updated` rows 60 days old is `bounded` and returned by
   `inactive_days=14`. The same card edited yesterday by a legacy binary is
   `undetermined`, not inactive and not active.
2. **Noise does not refresh.** Heartbeats, claim/renew/release, assignment,
   Focus membership, checklist reorder, identical progress and agent comments
   leave `not_after` unchanged on a 21-day-old card.
3. **Real activity refreshes.** A human comment, a scope edit with
   `changed_fields`, a column move, a checklist completion, a dependency
   change and changed progress each make the card `known` and current.
4. **Read-your-writes.** A comment followed immediately by a filtered list
   shows the card as active, or `pending` with incomplete coverage. It is
   never returned as inactive.
5. **Every writer is covered.** Events inserted by the dependency, hierarchy
   and portable-import paths update the signal without any change to them.
6. **Rollback binary.** A retained binary writes events against the new
   schema while the new binary is stopped. After restart the tail pass
   reconciles them. The old binary reads and writes normally throughout.
7. **Import of older history.** Importing events older than a task's current
   signal produces the same row as a from-scratch rebuild.
8. **Interrupted seed.** Killing the process mid-seed and restarting finishes
   with no missing rows, no duplicate work beyond one batch, and no cursor
   regression.
9. **Concurrent writes during rebuild.** Events arriving between a task's
   history read and the cursor update are processed in the next window.
10. **Poison history.** Malformed payload JSON, an unparseable timestamp and
    an unknown event type each produce a recorded `unknown`/`bounded` row and
    do not stall the projector.
11. **Deletion and restore.** Soft-deleted tasks are excluded from listings;
    a restored task is reclassified. Hard deletion removes the row.
12. **Comment removal.** Deleting the human comment that was the last
    activity falls back to the previous evidence, matching the timeline.
13. **Scope and permissions.** `activity` and the new filters respect project
    ceilings and Tickets visibility exactly as the task listing does.
14. **Boundary.** A card exactly at the threshold instant is inactive; one
    millisecond newer is not. One `evaluated_at` governs a whole page chain.
15. **Parity.** The shared fixture corpus agrees between Python version 1 and
    Go version 2 as defined in D3.
16. **Scale.** On a populated database (target: 20,000 tasks, 500,000 events)
    seed completes within a recorded budget without blocking writers beyond a
    recorded bound, steady-state rebuild stays under a recorded per-task
    budget, and the filtered listing uses an index-backed plan.
17. **Older server.** The updated CLI against a server without the signal
    produces version 1 output unchanged.

## Implementation queue

| Card | Work | Prerequisites |
| --- | --- | --- |
| TC-377 | Failure-case fixtures, parity corpus and populated benchmark database | None |
| TC-378 | Migration, classifier (version 2) and full-history rebuild | TC-377 |
| TC-379 | Projector: seed, tail, wake, reclassify, metrics | TC-378 |
| TC-380 | API: `Task.activity`, filters, sort, coverage, OpenAPI | TC-379 |
| TC-381 | CLI and skill adoption with version 1 fallback | TC-380 |
| TC-382 | Rollback, populated-database and beta validation | TC-380, TC-381 |

Each card carries its full acceptance criteria in Helm. Cards stay unclaimed
in Backlog until their work starts.

## Verification gates

- `go test ./...`, `make openapi-check`, `make focus-test` and the web check
  pass; no web source changes are expected in this work.
- A real isolated-server flow for every failure case above, with sanitized
  JSON receipts under `.artifacts/` and a fixed clock where time matters.
- Migration safety: additive-only, contiguous number, passes the existing
  prefix, preflight, rollback and retained-binary suites, plus a new
  populated-database test asserting unchanged task, event and comment counts.
- A verified pre-upgrade backup exists before any production migration.
  Rollback leaves the two new tables in place and unused; no restore.
- Operational metrics on the existing private metrics path: processed cursor
  lag, seed remaining, rebuild duration, and classification-error count, with
  no task text, ids, or unbounded labels.
- Beta soak: seed completes on the real beta database, lag returns to zero,
  and a hand-checked sample of 20 cards (including legacy-only cards) matches
  the reported status and bound.
- Every maintained file stays at or below 20,000 bytes; run
  `check-file-sizes` during implementation and before handoff.

## Open decisions for Shane

1. **Focus assignment and the clock.** Version 1 says adding a card to a
   Focus does not count as activity. That keeps the signal honest, but a card
   you just triaged into a Focus still shows as inactive. Recommended: keep
   the rule, and let the later dashboard work treat "has a Focus" as a
   separate dimension from "inactive".
2. **Default threshold.** Keep 14 days as the client default and let callers
   pass `inactive_days`. The server stores no threshold.
3. **Terminal tasks.** Recommended: classify them too (cheap, and reopen then
   needs no special case) but exclude them from neglect filters.

## Success criteria

- One request returns the complete set of unfocused Backlog cards inactive
  for N days in a project, with explicit coverage, in place of up to 100
  client requests that today return a partial answer.
- On beta, the share of open cards that review reports as `unknown` drops to
  a recorded figure near zero, with the remainder explained by `reason`.
- `focus review` output for `known` cards is unchanged from version 1.
- No write path, ETag, portable archive, or existing client behavior changes.
