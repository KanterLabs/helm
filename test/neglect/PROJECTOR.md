# Neglect signal projector: failure modes and budgets

TC-379, 8 November 2026, for plan D4 in
[the neglect signal plan](../../docs/FOCUS_NEGLECT_SIGNAL_PLAN.md). The
failure modes below were written before the projector code. Each is covered
by a Go isolation test in
`internal/store/task_activity_signal_projector*_test.go` or
`internal/httpapi/task_activity_signal_runtime_test.go`, or by a real-flow
case in [FAILURES.md](FAILURES.md); the owner is named on each line.

## Failure modes

1. **Cursor recorded late.** The seed records the processed cursor after (or
   while) rebuilding, so events committed between a task's seed rebuild and
   the cursor write are never revisited. The cursor must be recorded first,
   from `MAX(events.cursor)`, before any seed rebuild. (Go: seed test.)
2. **Seed not resumable.** After a kill mid-seed, a restart misses rows,
   never finishes, or repeats more than the one uncommitted batch. Rows are
   committed per batch and the seed selects only tasks lacking a row at the
   current classification version. (Go: interrupted seed; case 8.)
3. **Startup waits.** Startup or `/readyz` blocks on the seed or on any
   projector work. (Go runtime test; case 16 readiness.)
4. **Race window (case 9).** An event committed between a task's history
   read and the cursor update is skipped because the cursor moves past it.
   The window end is fixed before any history read; later events land in the
   next window. (Go: forced hook between classify and write.)
5. **Cursor regression.** A second projector, or a stale window, writes a
   lower cursor. The cursor update is `MAX(old, new)`. (Go.)
6. **Hard deletion (case 11).** An event whose task was hard-deleted (its
   `task_id` is now NULL) or a task deleted between classification and write
   stalls the tail, fails the window, or recreates a row. (Go.)
7. **Poison history.** A task whose classification returns an error stalls
   the cursor, or is skipped silently. It must be stored as
   `unknown`/`classification_error`, counted, and the cursor must advance.
   (Go; case 10 for real malformed rows.)
8. **Transient failure taken for poison.** A canceled context or closed
   database writes poison rows or advances the cursor past unprocessed
   events. A failed pass writes nothing and is retried. (Go.)
9. **Missing or early wake.** A write through `withTx`, `withImmediateTx` or
   `withImmediateDependencyTx` does not wake the projector, or wakes it
   before the commit is visible so it goes back to sleep until the ticker.
   The wake must never block or fail a writer. (Go: wake test with a one
   hour ticker.)
10. **Foreign writers unseen.** Events written by another process (a retained
    rollback binary, a second server) are never processed. The ticker
    fallback must pick them up. (Go: second connection pool; case 6.)
11. **Version bump ignored.** Rows below the current classification version
    are not reclassified, or the seed rewrites rows already current. (Go.)
12. **Two projectors.** A process starts two projectors. The second attach
    is refused; a second process is safe because rebuilds are idempotent
    upserts and the cursor never regresses. (Go.)
13. **Readers write or hang.** A freshness read creates the projection row,
    writes any row, or waits past its budget; the unprocessed-task query
    misses a task with an event past the processed cursor or a missing or
    outdated row, or reports a current task. (Go.)
14. **Metrics leak.** Metrics carry task ids, text, or unbounded labels.
    Only the bounded `pass` label (`seed`, `tail`) is allowed. (Go runtime.)
15. **Shutdown leak.** The projector outlives graceful shutdown, or a
    canceled batch commits rows without its cursor (or the reverse). Rows and
    cursor commit in one transaction. (Go.)
16. **Writers blocked.** Classification runs inside the write transaction,
    so a seed batch holds SQLite's writer lock for the whole batch. Histories
    are read first; the write transaction only upserts. (Benchmark below.)

## Benchmark budgets

Measured 8 November 2026 on the `make neglect-db` file (20,000 tasks,
500,000 events, 20 projects, seed 377, schema 44) with `make neglect-bench`
(cases 8 and 16), on the development workstation, defaults `SeedBatch=100`,
`TailWindow=1000`, ticker 2 s. Two full runs; the second was on an otherwise
idle machine. Later cards gate on the budget column through
`NEGLECT_BENCH_BUDGETS` in the Makefile (case 16 `store_*` checks).

| Measure | Run 1 | Run 2 | Budget |
| --- | --- | --- | --- |
| Readiness after start (seed unfinished) | 0.13 s | 0.11 s | 10 s |
| Seed, 20,000 tasks from empty tables | 68.8 s | 46.2 s | 180 s |
| Mean per-task seed rebuild (classify) | 3.1 ms | 2.1 ms | 20 ms |
| Mean steady-state tail rebuild, one touched card | 5.7 ms | 3.6 ms | 20 ms |
| Longest projector write transaction (writer lock held) | 41.6 ms | 21.6 ms | 250 ms |
| Slowest comment write during the seed (one every 250 ms) | 228 ms | 64 ms | 1,000 ms |
| p95 comment write during the seed | 74 ms | 52 ms | none |
| Comment write after the seed, median / max | not recorded | 18 / 39 ms | none |

Case 8 at full size killed the server after the first committed batch (100
rows of 20,000); the restart rebuilt exactly the 19,900 missing tasks, the
cursor ended at `MAX(events.cursor)` and `seeded_version=2`.

### What the benchmark needed: migration 044

The first measurement took about 270 ms per task (a projected 90-minute
seed). The timeline source queries, which the classifier reuses unchanged
(plan D3), exclude progress events and generated comments with
`task_agent_work_history.progress_event_cursor = ?` and
`generated_comment_id = ?`; neither column was indexed, so every event and
comment scanned the whole history table. Migration
`044_task_activity_signal_history_indexes.sql` adds the two indexes
(additive, about 0.2 s on the benchmark file). The timeline endpoint gets the
same speed-up.

### Long comment histories: quadratic until TC-380

`listTimelineComments` found each comment's `comment.created` cursor with a
correlated `json_extract` scan of the task's events, so one rebuild cost
O(comments x comment events) (TC-379: 250 comments 157 ms, 1,000 1.75 s,
2,000 6.6 s per rebuild; the timeline endpoint paid it per page). TC-380
reads the task's `comment.created` events once into a materialized group by
`CAST(json_extract(payload, '$.comment_id') AS TEXT)` (the conversion the
former comparison applied through `comments.id`'s TEXT affinity) and joins
it through SQLite's automatic index. No schema change; a malformed payload
still never matches. `TestTimelineCommentSourceMatchesOriginalQuery` proves
row-for-row identity with the original query (duplicate events, an event on
another task, malformed and unrelated payloads, a numeric id, equal
timestamps, every keyset boundary) and
`TestTimelineCommentSourceReadsEventsOncePerTask` pins the plan. Measured on
the workstation, one task, comment source / whole classification: 250
comments 2.5 / 4.5 ms (original query 79 ms), 1,000 8.9 / 14.6 ms (1.26 s),
2,000 19.9 / 28.0 ms (4.78 s).

### Known gap for TC-382: attachment readiness changes without an event

`SetAttachmentReadiness` (`attachments_gc.go`; called by the attachment
runtime when an object goes missing or reappears on disk, and on a failed
upload) updates `task_attachments.ready` without an event. Timeline hydration
shows only `ready=1` attachments, and the classifier's progress signature
includes the hydrated attachment list, so a readiness flip can turn two
progress rows from "identical" (excluded) into "changed"
(`agent.progress_changed`, qualifying) or back. The projector does not see
it until the task's next event, so the stored row can disagree with a fresh
rebuild (and with v1 replaying the endpoint) in either direction. Practical
reach is small: it needs two progress rows of one task whose attachment lists
differ only by an attachment whose object changed state on disk. Not fixed
in TC-380, because every fix changes a contract: (1) signing progress by
linked attachment ids regardless of readiness diverges from the endpoint the
D3 parity corpus is defined against; (2) an in-process rebuild after the
update races the projector's classify-then-write and needs a guard; (3) an
excluded `attachment.readiness_changed` event changes the write path and the
D2 table. Recommended for TC-382: option 1 with a corpus fixture, or a
reclassification of affected tasks after the attachment runtime pass.
