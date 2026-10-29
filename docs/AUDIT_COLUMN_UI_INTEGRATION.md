# Column-audit UI integration

The production UI implements the [reviewed UX](AUDIT_COLUMN_RECONCILIATION_UX.md),
[assessment contract](AUDIT_ASSESSMENT_CONTRACT.md) and
[correction contract](AUDIT_CORRECTION_CONTRACT.md). Opus 5.5 implemented the UI
and its initial browser journeys. Parent integration closed the failures in
[the inventory](AUDIT_COLUMN_UI_FAILURES.md), including scope pagination and
session isolation. Live-model accuracy and beta promotion remain TC-311.

## Behavior and ownership

- Board Actions and selected-card controls open **Audit columns**. Scope counts
  every server page, includes Done/Blocked, and offers the saved branch or an
  explicit PR head. The 1,000-card limit applies to the selected audit batch,
  not to the traversal used to count a narrower column. Counting is bounded to
  100 pages and fails explicitly if it cannot establish the full scope.
- Results use the server's assessment summary and actual column IDs. They show
  Corrections, Already correct and Couldn't verify, with no empty groups or
  confidence-first presentation. Blocked/stale recommendations remain visible.
- Evidence groups quoted conditions and pinned file/check observations. The
  panel traps focus, supports Escape/Back and labels PR-only work separately.
- Select, Review and one final Apply use the v2 endpoint. Bug inputs invalidate
  the previous preview. Unknown outcomes retain the exact request/key in memory
  for safe retry; receipt task snapshots are followed by normal full-task reads.
- Legacy external findings retain their original review path. Session/project
  changes discard protected source content and reviews; late task lookups cannot
  open a drawer in another session.

`web/src/lib/components/audit/columns/` owns the new scope/results/evidence/review
components. `LegacyAuditFindings.svelte` owns the original flow.
`web/src/lib/api/columnAudit.ts` owns counting and correction requests;
`web/src/app/columnAudit.ts` owns board entry points and guarded task opening.

## Repeatable browser validation

With the repository's Go and web dependencies already installed:

```sh
test/e2e/column-audit/run.sh --project=chromium
test/e2e/column-audit/run.sh --project=webkit
cd web
npm run check
```

Set `HELM_GO` to an installed Go executable when needed. The runner compiles a
test-only HTTP harness, starts Vite, runs the selected browser journeys and stops
both processes. `HELM_COLUMN_AUDIT_ARTIFACTS` selects an output directory.
`HELM_COLUMN_AUDIT_DIST` can serve a previously built frontend instead of Vite;
the client-session injection cases require Vite and are skipped with that option.

The harness uses actual Helm routing, supported disabled-mode synthetic human
authentication, SQLite, worker/publication and lifecycle APIs. Only GitHub/model
boundaries are scripted. Browser assertions also inspect independently queried
committed SQLite task/event/receipt state. No test control route is compiled into
the application binary. All cards/source fixtures are synthetic.

The decisive journey proves completion, reopening, bug resolution, a stale skip,
unknown-response recovery with an identical request/key, access revocation,
off-page task opening and real legacy approval. Scope/cancellation/PR-head and
320/390/desktop light/dark keyboard journeys cover presentation and recovery.
Supplemental counting/check-viewer response fixtures are labeled in the specs.
Captured JSON redacts confirmation proofs; screenshots and traces use synthetic
data. These checks prove integration and server enforcement, not model accuracy.

## Release boundaries

TC-311 must verify actual GitHub/model judgments, real beta human authorization,
a fresh online backup, retained release-binary compatibility with schema 42,
exact deployed revision/readiness and preservation of unrelated data. No beta
deployment or production database migration is part of UI acceptance.
