# Neglect signal: gaps in the plan

Moved unchanged from [FAILURES.md](FAILURES.md) by TC-380 (size limit);
TC-380 resolutions are appended to the items they close.

## Gaps the plan leaves for TC-378 onward

Gaps 1, 2, 3 and 8 are resolved by TC-378: the plan D2 table (mirrored in
PARITY.md and `corpus/reclassified.json`), the tolerant iterator, and the
`task_activity_signal` names.

1. **v1 does not list many real event types.** The store writes
   `task.deleted`, `task.restored`, `comment.updated`, `comment.deleted`,
   `task.parent_linked`, `task.parent_unlinked`, `bug.created`,
   `bug.triaged`, `bug.updated`, `bug.duplicate_of`, `task.artifact_linked`,
   `task.artifact_detached`, `attachment.uploaded`, `attachment.updated` and
   `attachment.deleted`. v1 treats every one as ambiguous. Under "rules
   unchanged from v1" each restore, comment deletion or hierarchy link bounds
   the card at that instant, and case 12 cannot pass: `comment.deleted` is
   itself ambiguous at deletion time. Plan finding 4 also calls
   `attachment.uploaded` and `task.progressed` qualifying, which v1 does not
   do. TC-378 must classify these explicitly. Reclassifying an ambiguous
   type as excluded or qualifying turns a v1 `unknown` into a v2 `known`,
   which the D3 contract forbids; amend D3 with an explicit allowlist and
   update the `a-unlisted-*` fixtures in the same change.
2. `task.dependency_state_changed` counts as qualifying in v1 (prefix match)
   although another card's completion causes it.
3. **Malformed payload JSON makes the existing task timeline return 500**
   (case 10 receipt, `existing_timeline_status`). D3 feeds the classifier
   from the timeline sources; the iterator must yield `classification_error`
   instead of aborting.
4. v1 checks time before kind, so an excluded event with an unparseable time
   is ambiguous. The corpus records `unknown`/`unparseable_time`.
5. The reason when several ambiguous rows exist is unspecified. The corpus
   uses the newest ambiguous row, ties broken by (time, id).
6. `reason` on `known` rows: the schema default is `''`, D2 says only
   bounded/unknown carry one. The corpus accepts null or empty.
7. Rows for soft-deleted and terminal tasks: case 8 expects a row for every
   task, matching open decision 3 and restore without special casing.
8. The plan's `task_activity` name collides in spirit with
   `internal/store/task_activity_types.go` and migration
   `011_task_activity_timeline.sql`, which already mean agent progress
   history. Consider a distinct prefix for the new files.
9. **No fixed server clock.** Case 14's exact instant needs TC-380 to choose
   an injection point (a test-only clock option, or an `as_of` input bound
   into the page cursor). The harness otherwise depends on wall-clock time
   only through `T0` offsets of whole days. *TC-380:* a `helm_testclock`
   build tag; default builds cannot fix the clock ([API.md](API.md)).
10. Plain task listings already return `409 task_collection_changed` when
    the collection changes mid-read (seen in case 9's reader). D5 must state
    whether neglect listings restart or report `pending` under concurrency.
    *TC-380:* both. A project write during a request or between pages is
    `409` as before; unprocessed events at read time give `pending`.
11. `activity_status=undetermined` is assumed to combine with
    `inactive_days`; D6 does not say whether it can stand alone. *TC-380:*
    it cannot (`400`).
12. Migrations on `origin/main` and `origin/beta` both end at
    `042_task_attachments.sql` on 8 November 2026; 043 is still free.
