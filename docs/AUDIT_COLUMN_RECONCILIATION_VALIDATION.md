# Column audit: validation and beta release gate

Companions: [feature plan](AUDIT_COLUMN_RECONCILIATION_PLAN.md) and
[review UX](AUDIT_COLUMN_RECONCILIATION_UX.md).

## Acceptance and release gate

Write the failing user journeys and synthetic fixtures before implementing each
feature. Prefer real HTTP/auth/store/worker and browser flows over mocked UI-only
tests. If isolation is required, document failure modes first and write its tests
before the implementation. Each E2E journey saves a sanitized response, trace or
screenshot. Reuse coverage; do not introduce repeated benchmark matrices.

The fixture set must prove:

1. The health-endpoint example finds its actual committed handler/test even when
   no likely filename contains the endpoint and audit prose is a distractor.
2. Complete work in In progress recommends Done; correct Done remains correct.
3. Partial implementation in Done recommends reopening; partial work already
   In progress stays there. A demonstrated revert is distinguished from no evidence.
4. A missing or ambiguous criterion, exhausted budget or unavailable relevant
   source gives Unable to verify, with no correction or apply action.
5. Relevant criteria can be verified despite unrelated repository omissions;
   one card's uncertainty does not contaminate another card's judgment.
6. Unmerged PR evidence, selected branch evidence and commit-specific checks stay
   distinct. Required failing/unknown checks affect only the applicable conditions.
7. Readiness and genuine blockers use task/dependency facts; missing commits and
   failed CI do not manufacture Backlog or Blocked placement.
8. Done cards beyond the board's first page are audited; all scope counts balance;
   multiple custom destination columns never resolve by arbitrary position.
9. Complete/reopen and bug resolve/reopen preserve IDs, comments, labels, links,
   history, timestamps and dependency/checklist behavior. Active claims and roles
   are honored, concurrent changes are skipped, repeated Apply is idempotent.
10. Source/task/criterion/check drift invalidates approval/preview; revoked access,
    cancellation, restart and malformed or invented citations fail safely.
11. Run, leave/return, evidence review, selection, preview, application and conflict
    recovery work with keyboard and at 320/390px, desktop, light and dark themes.
12. A reviewer can identify the card, its current/recommended column, the reason,
    supporting evidence and the available next action without reading a manifest.

All decisive reference cases must match the approved expected placements. There
must be no unsupported Done/reopen recommendation and no hidden scope omissions.
An API 202, successful model response or generic low-confidence finding is not
the acceptance criterion.

For additive storage changes, prove populated-data migration, legacy history,
portable export behavior and retained-binary compatibility. Verify an online
backup before beta deployment; use exact revision readiness and image rollback
without a database restore. Keep production unchanged during beta validation.

On beta, use clearly labeled synthetic cards with known committed implementations
deliberately placed in wrong columns. Complete at least one approved Done
correction and one reopening correction, then demonstrate one stale/conflicted
selection is not applied. Record expected card deltas, preserve unrelated data,
and revoke temporary test access. Finish only when those user outcomes are shown.

