# Unified Intake read contract

TC-320 adds `GET /api/v1/intake` (`listIntake`); `/tickets`, `/issues`, task mutations and
filing retain their existing behavior. No migration or copied entity is needed.

Before implementation, the failure inventory is:

- Joining separate ticket and bug collections can duplicate the same task and
  make page sizes, cursors and counts disagree.
- A queue grant inferred from another project can expose private incoming work;
  actor ceilings must intersect token ceilings, including an empty intersection.
- Filtering only after pagination can hide visible records or leak counts.
- Filed or completed tickets can disappear; classification can discard ordinary
  requests, aliases, comments, typed bug fields or source/repeat correlation.
- A read can create the hidden queue, rewrite routing, or modify task history.
- Invalid filters/cursors can silently return unfiltered data; empty results can
  serialize as null or advertise a nonexistent next page.
- Equal timestamps or mutable priority can reorder unchanged incoming records;
  offset pages can skip visible work when an earlier item leaves triage.
- A continuation can be reused with different filters or reader authority, or
  rows and counts can observe different collection revisions.
- Query errors or enrichment errors can produce partial success.

`scope=needs_triage` is the default: ticket members in a Backlog semantic column,
or unresolved bugs with no severity outside completed columns. A triaged bug in
Backlog is included only when it also has ticket membership. `scope=all` includes
all ticket members and bugs, including filed and completed records. Both scopes
exclude deleted tasks and archived projects. Archival history remains available
through existing authorized project/task routes.

`kind=task|bug`, `project` (ID/key/slug), `severity=s1|s2|s3|s4|untriaged|none`, and
`q` constrain both rows and counts. Severity selects bugs only; `none` and
`untriaged` mean a null bug severity. Search matches title, description, typed
bug details, current key and preserved key aliases. Counts `{all,needs_triage}`
ignore the selected scope and pagination but apply every other filter and the
same authorization ceiling. One task appears once, even with both memberships.

Requires `tasks:read`. Global scoped-token reads use only the effective actor /
token project intersection. Explicit unauthorized projects return 403. Queue
metadata is returned only if the caller can read its existing project; a read
never creates that project or adds a grant. Ordinary tickets remain ordinary
tasks until an explicit guarded classification action.

Pagination uses a bounded opaque `ic1` keyset cursor and `limit` (default 50,
maximum 200), ordered by immutable task creation time then ID. Cursors retain
the filters, effective actor/token authority fingerprint, original read time,
and authorized project event/task-collection snapshot. Reusing a cursor with
different filters or reader authority returns 400. A collection mutation since
the previous page or during rows/counts/enrichment returns
`409 task_collection_changed` with `details.restart=true`; restart from page one.
Unrelated unreadable project changes do not invalidate a scoped cursor. The new
endpoint accepts only its own keyset cursors. Empty `next_cursor` ends the
collection. Responses reuse enriched
Task records, preserving canonical IDs, ticket membership, bug details, comments
counts, source facts and occurrence counts. Existing guarded actions remain the
only way to classify, triage, file, resolve or reopen.

Repeatable proof: run the focused `TestIntake*HTTP` Go tests with
`HELM_TEST_ARTIFACT_DIR` set to an empty temporary directory. The real HTTP
journeys write synthetic response JSON there, without authorization headers.
