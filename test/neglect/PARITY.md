# Classifier parity corpus (plan D3, case 15)

The corpus in `corpus/` is the shared input for the Python version 1
classifier (`skills/helm/scripts/helm_focus_activity.py`) and the Go
version 2 classifier (`internal/store/task_activity_signal_classifier.go`). Content is synthetic; there are no ids, names or text
from real tasks.

| File | Holds |
| --- | --- |
| `corpus/qualifying.json` | Each qualifying source v1 recognises |
| `corpus/excluded.json` | Each excluded kind after a qualifying comment |
| `corpus/ambiguous.json` | Legacy updates, unavailable actors, unknown row kinds and every unlisted event type the server writes that v2 still leaves ambiguous |
| `corpus/edges.json` | Unparseable times, ties, ordering, offsets, reason precedence |
| `corpus/reclassified.json` | One fixture per row of the plan D2 version 2 table |
| `corpus/reference_v1.json` | v1 output per fixture, recorded by `corpus_run.py --record` |

## Fixture shape

```json
{"id": "a-legacy-only",
 "covers": ["ambiguous:task.updated:legacy"],
 "note": "Legacy-only history: plan case 1.",
 "task": {"created_at": "2026-08-01T00:00:00Z"},
 "expected_v2": {"status": "bounded", "last_meaningful_at": null, "source": null,
                 "not_after": "2026-09-05T00:00:00Z", "reason": "legacy_update"},
 "rows": [{"id": "row0001", "kind": "task_change", "created_at": "...",
           "actor": {"kind": "human"},
           "change": {"event_type": "task.updated", "payload": {"version": 2}}}]}
```

`rows` use the task timeline item shape (`kind` is `comment`,
`agent_progress`, `task_change` or an unknown kind; `actor` may be null).
Every history is complete: v2 always rebuilds from full history, so v1's
`lower_bound` (truncated history) has no v2 counterpart and is not in the
corpus. Row order in the file is deliberately not always chronological.

## The plan D2 table: the only departures from v1

Version 2 reclassifies exactly these event types (plan D2), mirrored in
`TABLE_EXCLUDED` / `TABLE_QUALIFYING` in `corpus_run.py` and in the Go
classifier. Each has one fixture in `reclassified.json` (`r-*`, a human
comment then the event).

| Event | v1 | v2 | Fixture result |
| --- | --- | --- | --- |
| `comment.updated`, `comment.deleted` | ambiguous | excluded | v1 `unknown` becomes `known` (earlier comment) |
| `task.parent_linked`, `task.parent_unlinked` | ambiguous | excluded | as above |
| `task.deleted`, `attachment.updated`, `attachment.deleted` | ambiguous | excluded | as above |
| `task.restored`, `bug.triaged`, `attachment.uploaded` | ambiguous | qualifying, source = event type | v1 `unknown` becomes `known` at the event |
| `task.dependency_state_changed` | qualifying | excluded | v1 `known` at the event becomes `known` from earlier evidence |

The last row is the only permitted change to a v1 `known` result. Event
types that stay ambiguous in v2 (`comment.created` without a surviving
comment row, `task.progressed` without a history row, `bug.updated`,
`bug.created`, `bug.duplicate_of`, `task.artifact_linked`,
`task.artifact_detached`, future types) keep their `a-unlisted-*` fixtures.

## Contract the Go classifier must satisfy

For every fixture, `v1` is the recorded result in `reference_v1.json` (a
true recording of v1, never edited by hand) and `baseline` is v1 rerun with
only the table applied (`run_v1_table`: table-excluded rows with a parseable
time are dropped; table-qualifying rows are replayed as a v1 lifecycle event
and keep their own name as source). Outside the table `baseline == v1`, so
parity there is exact. With `v2` the Go output:

0. **Table guard.** If `v1.status == "known"` and `baseline != v1`, the
   history must contain `task.dependency_state_changed`.
1. **Known is exact.** If `baseline.status == "known"` then
   `v2.status == "known"`, `v2.source == baseline.source` and
   `v2.last_meaningful_at` is the same instant as `baseline.at`.
2. **Unknown widens only.** If `baseline.status == "unknown"` then
   `v2.status` is `bounded` or `unknown`.
3. **Shape (D2).** `known`: `not_after` equals `last_meaningful_at`, reason
   null or empty. `bounded`: `last_meaningful_at` and `source` null,
   `not_after` is the time of a row in the history, reason in the D2 set.
   `unknown`: `not_after` null, reason in the D2 set.
4. **Expected values.** `v2` equals the fixture's `expected_v2` on all five
   fields. Times compare as instants (`2026-09-05T02:00:00+02:00` equals
   `2026-09-05T00:00:00Z`); reason null and empty are equal.

`expected_v2` was set by hand from plan D2 and is itself checked against
rules 1 to 3 on every run. The resolutions it encodes where the plan is
silent:

- Any row with an unparseable or missing time, including an excluded kind
  (v1 checks time before kind), makes the result `unknown` with
  `unparseable_time`.
- An unparseable `tasks.created_at` with no qualifying row is `unknown`,
  `unparseable_time`; a later qualifying row still makes it `known`, as in v1.
- `bounded.not_after` is the newest ambiguous time (which is at or after the
  last qualifying time); `reason` comes from that row, ties broken by
  (time, id) as v1 sorts. Ambiguous row reasons: legacy `task.updated`
  (no `changed_fields`) is `legacy_update`; a comment whose actor is null or
  neither human nor agent is `actor_unavailable`; an unlisted event type or
  unknown row kind is `unknown_event_type`.
- An ambiguous row older than the last qualifying row is ignored (`known`).
- A malformed payload (not a JSON object or null, or a field of the wrong
  type where v1 reads it) or an undecodable stored progress row is `unknown`,
  `classification_error`, after the unparseable-time check. The corpus is
  JSON, so this is covered by Go store tests, not fixtures.

Changing any of these is allowed, but only in the same change that updates
the affected `expected_v2` values and this list. Reclassifying another event
type means adding a row to the plan D2 table, to both table constants and to
the Go classifier, and moving its fixture to `reclassified.json`.

## Running

```sh
python3 test/neglect/corpus_run.py                     # check (exit 1 on violation)
python3 test/neglect/corpus_run.py --record            # after an intended v1 change only
python3 test/neglect/corpus_run.py --v2-results out.json --receipt .artifacts/x/corpus.json
```

`--v2-results` takes a JSON object mapping fixture id to the Go output
(`status`, `last_meaningful_at`, `source`, `not_after`, `reason`) and applies
rules 0 to 4. `internal/store/task_activity_signal_parity_test.go` (TC-378)
feeds each fixture's rows to the Go classifier, checks rule 4 and the shape
rules itself, and with `HELM_NEGLECT_V2_RESULTS=<path>` writes the map; it
then runs this script with `--v2-results` when `python3` is available, so
`go test ./internal/store` enforces rules 0 to 4 against
`reference_v1.json`.
