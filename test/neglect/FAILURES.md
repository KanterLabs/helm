# Neglect signal failure cases

Recorded before implementation, 8 November 2026 (TC-377), for
[the neglect signal plan](../../docs/FOCUS_NEGLECT_SIGNAL_PLAN.md). The
classifier (TC-378), projector (TC-379) and API (TC-380, wire contract and
failure modes in [API.md](API.md)) and CLI adoption (TC-381, failure modes
in [CLI.md](CLI.md)) have landed. Cases 18 and 19 were red (`feature_absent`)
before the CLI change; every case 1 to 20 now passes and is a guard.

## Running

| Command | Meaning |
| --- | --- |
| `make neglect-red` | Red gate. Passes only while every case not yet landed fails with `feature_absent`, the guard cases pass, and every `store_*` check passes. Since TC-381 every case is a guard, so it is as strict as `neglect-test`. |
| `make neglect-test` | Strict. Passes only when every case passes. |
| `make neglect-corpus` | Parity corpus check ([PARITY.md](PARITY.md)). |
| `make neglect-db` | Full-size benchmark database (20,000 tasks / 500,000 events) under `$TMPDIR`. |
| `make neglect-bench` | Cases 8 and 16 on that database with the recorded budgets ([PROJECTOR.md](PROJECTOR.md)). |
| `python3 test/neglect/run.py --binary B --case N` | One case. |

`NEGLECT_ROLLBACK=<binary>` (runner: `--rollback-binary`) names the retained
pre-signal binary for cases 6, 17 and 20. Since TC-381 the Makefile builds
main `1c54493` once under `$TMPDIR` (`make neglect-rollback`) and uses it by
default; cases 17 and 20 fail if the retained binary returns
`Task.activity`, so they can no longer pass against the candidate. The
runner's own default is still `--binary`. `make neglect-binary` also
builds `.artifacts/neglect-helm-testclock` (`-tags helm_testclock`) for case
14 (runner: `--clock-binary`).

Receipts: `.artifacts/neglect-<run>/case-NN.json` and `result.json`.
Outcomes are `passed`, `feature_absent` (expected red: `Task.activity` or the
collection fields are missing), `failed` (the server answered wrongly) and
`error` (harness fault). Every case performs all of its mutations before it
observes anything, so harness faults appear now and cannot hide behind the
expected red. `Checks` reports a wrong answer ahead of a missing field.

## Harness rules

- **Fixed clock.** Each run fixes `T0` (whole UTC second) and backdates
  owned synthetic rows to exact offsets (`T0-21d`). Only case 14 fixes the
  server's evaluation instant, on the test-clock binary ([API.md](API.md)). Receipts express every fixture time as such an offset, and
  live server times as `live`, so receipts are identical between runs.
- **Backdate, then touch.** A direct `created_at` edit does not advance the
  event cursor, so a projector that already processed the task would never
  revisit it. After backdating, the harness appends excluded events (human
  claim then release, `task.claimed`/`task.released`) at new cursors.
  Excluded events never count, so the touch changes which tasks the tail pass
  revisits, never their evidence.
- **Legacy proxy.** Case 1 removes `changed_fields` from a genuine
  `task.updated` row, which is byte-for-byte what binaries before `1c54493`
  wrote. Case 6 covers a real retained binary.
- **Poison.** Case 10 inserts rows no server writer can produce, at new
  cursors, as a corrupted or future binary would.
- **Contract in one file.** `contract.py` holds every assumption about field
  names, statuses and listing inputs (plan D2, D5, D6). TC-380 kept every
  spelling it assumed; the full wire contract is in [API.md](API.md).
- **Fixture self-checks.** Cases 1, 2, 3 and 12 also run v1 over the real
  server timeline (`fixture_v1_*` checks, passing now), proving each premise
  against real data: the noise really is excluded, each refresh really is
  qualifying, the legacy rows really are ambiguous.
- **Projector guards (TC-379).** Checks named `store_*` read the derived
  tables (`contract.stored_activity`, `projector_settled`) and the private
  `/metrics` path, so the projector is verified before TC-380 exposes
  `Task.activity`. They are guards: a `store_*` check that does not pass
  fails the red gate. The API checks in the same cases stay `feature_absent`.
- **Sanitized receipts.** Ids become stable aliases, free text becomes its
  length, tokens and credentials are never written. Synthetic content only.

## Cases

"Executable" means the real flow runs now and fails with `feature_absent`.

| # | Mode | Flow and exact expected observable |
| --- | --- | --- |
| 1 | Executable | Two cards created `T0-61d`, each with one legacy `task.updated` (`T0-60d`, `T0-1d`). Old: `bounded`, `reason=legacy_update`, `not_after=T0-60d`, returned by `inactive_days=14`. Recent: `bounded`, `not_after=T0-1d`, absent from `inactive_days=14`, present with `activity_status=undetermined`. |
| 2 | Executable | Card with history at `T0-21d`, then: agent heartbeat, renew, identical progress (new operation id), agent comment, agent release, human claim/release, assignment, priority change, Focus membership, checklist reorder. `known`, `not_after=T0-21d`, still in `inactive_days=14`. |
| 3 | Executable | Six cards at `T0-21d`; one each: human comment, description edit, column move, checklist completion, dependency added, changed progress. Each `known`, `not_after >= T0`, sources `human.comment`, `task.scope_or_state_changed`, `task.moved`, `task.checklist_completed`, `task.dependency_added`, `agent.progress_changed`; none in `inactive_days=14`. |
| 4 | Executable | Card at `T0-30d`; human comment; the very next `inactive_days=14` page either omits it or returns it `pending` with `activity_coverage.complete=false`. Task read shows `not_after >= T0`. |
| 5 | Executable; store guards | Dependency add on a `T0-30d` card, hierarchy link (event lands on the child only), portable import into a new project. Dependency carrier `not_after >= T0`; the hierarchy carrier's row is rebuilt after the link but keeps `not_after = T0-30d`, because plan D2 (TC-378) excludes `task.parent_linked` (the original "every carrier `>= T0`" contradicted D2; corrected in TC-379); every imported task has a non-pending row. Since TC-379 the same is asserted on stored rows (`store_*`). |
| 6 | Executable; store guard | Candidate creates a `T0-30d` card and stops; retained binary starts on the same file, adds a human comment, reads and lists normally, stops; candidate restarts. `known`, `human.comment`, `not_after >= T0`; listing reaches complete coverage. Stored row checked since TC-379. |
| 7 | Executable; store guard | Export a project holding a card created `T0-90d` with a human comment `T0-45d`; import with remap. Imported row equals the source row on `status`, `last_meaningful_at`, `source`, `not_after`, `reason`; `not_after=T0-45d`. |
| 8 | Executable; store guards | Populated copy; start; SIGKILL as soon as the seed has committed a batch but not finished (`seeded_version=0`, `0 < rows < tasks`); restart. Store guards: the kill landed mid-seed (state and `helm_task_activity_signal_seed_complete=0` scraped just before it); `task_activity_signal` rows at version 2 equal `tasks` rows; `event_cursor = MAX(events.cursor)`, never below the cursor at the kill, and `seeded_version=2`; the restarted seed rebuilt no task that had a committed row (`rebuilds_total{pass="seed"}` <= missing rows <= seed + tail rebuilds), so lost work is at most the one uncommitted batch. Every project's listing then reaches complete coverage (TC-380). `--kill-after-ms` is now extra time allowed for the first batch. |
| 9 | Executable; Go guard | Six `T0-30d` cards each get three comments from parallel writers while a reader lists `inactive_days=14`. No card whose comment was acknowledged before a listing began is returned non-`pending`; after quiescence none is inactive. The exact window (an event committed between a task's history read and the cursor update) is forced by `TestTaskActivitySignalTailProcessesEventsCommittedDuringRebuild` (store hook between classify and write): the first window stops at its end, the racing event is processed by the next. `store_go_race_window_test_present` checks it exists. |
| 10 | Executable; store guards | Inserted rows on `T0-30d` cards: malformed JSON `task.updated` (`T0-5d`), `task.updated` at `not-a-timestamp`, `task.synthetic_future_kind` (`T0-5d`). Malformed: `unknown`/`bounded` with a D2 reason. Unparseable: `unknown`, `unparseable_time`. Unknown type: `bounded`, `unknown_event_type`, `not_after=T0-5d`. A later human comment on a neighbour is reflected (projector not stalled). |
| 11 | Executable; store guard | Soft-delete a `T0-30d` card: absent from `inactive_days=14`. Restore: stored row `task.restored`, `not_after >= T0`, `computed_at >= restored updated_at` (`store_*`), then activity present (TC-380). No API hard-deletes a task; hard deletion is `TestTaskActivitySignalHardDeleteAndRestore` (cascade removes the row, events with a NULL `task_id` or a task deleted between classify and write never stall the cursor or recreate a row). |
| 12 | Executable | Card created `T0-40d`, human comment `T0-20d`: `known`, `not_after=T0-20d`. Delete the comment: `known`, `source=task.created`, `not_after=T0-40d`. The `fixture_v1_after_delete` check shows v1 says `unknown` here; see gap 1. |
| 13 | Executable | Scoped agent token: neglect listing ids are a subset of the plain listing; foreign project returns the same status and code as the plain listing (403); a foreign task read leaks no `activity`; a token without `tasks:read` sees no `activity`. "Tickets visibility" is not defined further by the plan; this case pins parity with the plain listing. |
| 14 | Executable (own test-clock server) | Evaluation instant fixed at `E = T0`. Three cards `T0-30d..T0-32d`; `inactive_days=14&limit=1` page chain: one `evaluated_at` (= `T0`) on every page, no card skipped or repeated; `sort=not_after` ascending by parsed instant (RFC 3339 text with trimmed fractions does not sort lexically). A card at `T0-14d` is inactive; one at `T0-14d+1ms` is not. Moved to `cases_boundary.py` by TC-380. |
| 15 | Guard since TC-378 | Python half: `corpus_run.py`. Go half: `internal/store/task_activity_signal_parity_test.go` exists and enforces [PARITY.md](PARITY.md) rules 0 to 4 under `go test ./internal/store`. Passes; must keep passing. |
| 16 | Executable; store guards | Populated copy (`--scale-tasks`/`--scale-events`, default 300/6,000; `--populated-db` for `make neglect-db`). Store guards: readiness <= 10 s while the seed is provably unfinished; a comment every 250 ms during the seed, slowest <= `--write-budget-ms`; seed <= `--seed-budget`; mean per-task seed and steady-state tail rebuild <= `--rebuild-budget-ms`; longest projector write transaction <= `--lock-budget-ms`. Measured numbers and budgets: [PROJECTOR.md](PROJECTOR.md). Since TC-380: filtered listing (`inactive_days=14&state=backlog&release=unassigned`) <= 500 ms with complete coverage, and `EXPLAIN QUERY PLAN` of the exact SQL the store builds, on this case's populated copy, searches tasks by project and the signal row by key and scans neither `tasks`, `events` nor the signal table (`TestTaskActivityListingPlanOnPopulatedDatabase`). |
| 17 | Guard | CLI `focus review` against the retained binary (checked pre-signal): activity equals v1 recomputed from the timeline, keys exactly `status, at, source, classification_version=1`, and the `inactive` finding is present. |
| 18 | Guard since TC-381 (own server) | Cards A `T0-30d`, B legacy `T0-60d`, C legacy `T0-1d`, D unparseable event, E fresh, F `T0-12d`, Focus G `T0-30d`, K depends on G, O owned. `activity_signal.path=server`; no timeline reads; A/B/G/O `inactive`, C `undetermined` with `activity_undetermined` and no `inactive`, D `unknown`; E and F not listed; F inactive with `--inactive-days 10`; `--timeline-limit` reported unused and without effect; `--history-limit 1` chain covers every card once; `--request-limit 3` restarts; `--focus` works; no events written. |
| 19 | Guard since TC-381 (own server) | An unprocessed event held behind a write lock: `activity_coverage.complete=false`, `coverage.partial`, warning `activity_coverage_incomplete`, the card `pending` (never `inactive`); after release the rerun is complete and the card inactive. |
| 20 | Guard (own retained server) | The version 1 CLI from `git archive 1c54493` and the candidate CLI give byte-identical stdout, stderr and exit codes (wall-clock `evaluated_at` aside) in seven modes, including a cursor continuation and `--inactive-days 5000`. |

## TC-378 store isolation failure modes

Written before the classifier, iterator, rebuild and migration code (plan D1
to D3). Each is a Go test in `internal/store/task_activity_signal_*_test.go`
or `internal/db/task_activity_signal_migration_safety_test.go`.

1. The Go classifier disagrees with the corpus (parity test, rules 0 to 4).
2. The iterator diverges from the endpoint: a generated progress comment, a
   progress event or a `comment.created` event with a live comment row
   appears twice, or a soft-deleted comment still counts.
3. The iterator truncates long history (a page limit leaks in): an old task
   with more than 200 timeline items loses its newest evidence.
4. Malformed event payload JSON fails the rebuild or the endpoint changes
   behaviour; it must give `unknown`, `classification_error`, and the
   existing timeline read must behave exactly as before.
5. An undecodable stored progress row (`checkpoint_refs`) fails the rebuild
   instead of giving `classification_error`.
6. A soft-deleted or terminal task gets no row (rows are for every task).
7. Rebuild is not idempotent: a second run changes anything but
   `computed_at`, or adds a row.
8. Hard deletion of a task leaves an orphan row (`ON DELETE CASCADE`).
9. Stored times do not compare as instants: `not_after` text must sort
   chronologically (fixed-width UTC), whatever offset or precision the
   source row used.
10. Comment removal does not fall back (case 12 at store level).
11. Rebuild of an unknown task id writes a row or reports success.
12. Migration 043 changes existing data, is not additive, breaks the prefix,
    preflight, rollback or retained-binary suites, or rejects the D2 values.

The gaps the plan left for TC-378 onward, and how each was resolved, are in
[GAPS.md](GAPS.md).
