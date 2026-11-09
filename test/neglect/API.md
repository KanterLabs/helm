# Neglect signal API: failure modes and wire contract

TC-380, 8 November 2026, for plan D5 and D6 in
[the neglect signal plan](../../docs/FOCUS_NEGLECT_SIGNAL_PLAN.md). The
failure modes were written before the API code. Each names its owner: a Go
isolation test (`internal/store/task_activity_signal_listing*_test.go`,
`internal/httpapi/task_activity_signal_api_test.go`) or a real-flow case in
[FAILURES.md](FAILURES.md).

## Failure modes

1. **Boundary off by one.** `not_after = E - N days` is not returned, or
   `E - N days + 1ms` is. Inactive is `not_after <= E - N*24h`, compared as
   fixed-width UTC text. (Go: boundary; case 14.)
2. **Page chain drift.** A continuation page evaluates against a new instant,
   so a card crossing the threshold between pages is skipped or repeated.
   `evaluated_at` is the cursor's read instant; offset cursors cannot carry it
   and are rejected with neglect inputs. (Go; case 14.)
3. **Pending listed as inactive.** A task with an unprocessed event (or no
   current row) is returned with its stale stored status, or coverage says
   complete while one is pending. (Go: pending; cases 4, 9.)
4. **Readers write.** A read creates the projection row, a signal row, or
   waits beyond its budget. (Go.)
5. **Plain reads slowed.** A read without neglect inputs waits for the
   projector. Only neglect listings wait; plain reads do one progress
   statement and one batched row read, and add one batched unprocessed
   lookup only while the projector is behind. (Go.)
6. **N+1.** Activity is read per task instead of one batch per page. (Go:
   statement count is independent of page size.)
7. **Terminal or deleted tasks in neglect filters.** Completed tasks (in a
   completed column or with `completed_at`) or soft-deleted tasks match
   `inactive_days` or `activity_status`. (Go; case 11.)
8. **`unknown` dropped silently.** An unknown row is missing from a plain
   `inactive_days` listing. Plan D2 requires it to be surfaced. (Go.)
9. **Undetermined misread.** A bounded row newer than the threshold is
   reported inactive or omitted from `activity_status=undetermined`, or
   `undetermined` is accepted without `inactive_days`. (Go; case 1.)
10. **NULL ordering unstable.** `sort=not_after` with unknown or missing rows
    skips or repeats across pages, in either direction. (Go.)
11. **Existing contract changed.** ETags, existing filters, sorts, cursors
    or portable archives change; mutation responses gain `activity`;
    `activity` leaks to a caller without `tasks:read` or outside the
    project ceiling. (Go; case 13; existing suites.)
12. **409 lost.** A project write between pages of a neglect listing does
    not return `409 task_collection_changed`. (Go.)
13. **Clock seam reachable.** The fixed evaluation clock can be set by a
    request, configuration, or environment in a production build. It exists
    only in binaries built with `-tags helm_testclock`. (Go: build-tag file
    absent from default builds; case 14 uses the tagged binary.)
14. **Index-less plan.** The filtered listing scans tasks outside the
    project, `events`, or the whole signal table. (Case 16 plan check.)

## Wire contract (what TC-381 consumes)

`Task.activity` on `GET /tasks/{id}`, `GET /projects/{id}/tasks`,
`GET /my-work` and `GET /issues` (never on mutation responses):

| Field | Value |
| --- | --- |
| `status` | `known`, `bounded`, `unknown`, or transport-only `pending` |
| `last_meaningful_at` | RFC 3339 UTC; set only when `known` |
| `source` | qualifying source (`human.comment`, ...); empty when not `known` |
| `not_after` | RFC 3339 UTC; null when `unknown` or `pending` |
| `reason` | D2 reason for `bounded`/`unknown`; empty otherwise |
| `classification_version` | `2`; null when `pending` |

`pending` means the projector has not processed every event of the task (or
the task has no row at the current version yet); its other fields are
null/empty. Times use RFC 3339 UTC with trailing zero fractions trimmed, like
every other API time, so compare them as instants: `...:00.001Z` sorts
before `...:00Z` as text.

`GET /projects/{id}/tasks` neglect inputs, combinable with every existing
filter (notably `state` and `release`):

- `inactive_days=N` (1 to 3650): open tasks with `known`/`bounded` rows and
  `not_after <= evaluated_at - N*24h`, plus every open `unknown` row.
- `activity_status`: `known`, `bounded`, `unknown` narrow by stored status
  (with `inactive_days`, `known`/`bounded` also apply the threshold);
  `undetermined` (requires `inactive_days`) selects `bounded` rows newer
  than the threshold. Missing `inactive_days` with `undetermined` is `400`.
- `sort=not_after` (`order=asc|desc`): key `(not_after IS NULL, not_after,
  number, id)`. Rows without a bound (unknown, or no row yet) sort after
  every time ascending and before every time descending.
- Activity filters exclude terminal and soft-deleted tasks; a sort alone
  does not filter.

Any neglect input adds `evaluated_at` (the page chain's instant) and
`activity_coverage` to the collection:

```json
{"complete": true, "lag": 0, "pending": 0}
```

`complete` is true only when the projector has processed every event of the
project up to the request's snapshot, every task of the project has a row at
the current version, and no returned task is `pending`. `lag` is the event
cursor distance still to process for this project; `pending` counts pending
tasks on this page. Neglect listings wait up to 250 ms for the projector
first. A chain whose first page is complete is gap- and duplicate-free; any
project write between pages is `409 task_collection_changed`. While coverage
is incomplete the seed may write rows between pages, so a restart after
`complete` is the only full answer.

A client detects support by `activity` on a task read, or by
`evaluated_at` on a neglect listing; an older server answers
`sort=not_after` with `400 sort must be ...` and ignores the new filters
(no `evaluated_at`).

## Fixed clock (test builds only)

`go build -tags helm_testclock` adds a file reading
`HELM_TEST_EVALUATION_TIME` (RFC 3339, not in the future) into the store's
evaluation clock and logging that it did. Default builds do not contain that
file, so no request, configuration or environment can fix the clock.
`make neglect-binary` builds `.artifacts/neglect-helm-testclock` for case 14.
