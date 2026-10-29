# TC-310: column-audit UI failure inventory

Written before the production UI, and before its browser journeys. Companions:
[UX](AUDIT_COLUMN_RECONCILIATION_UX.md), [assessment contract](AUDIT_ASSESSMENT_CONTRACT.md),
[correction contract](AUDIT_CORRECTION_CONTRACT.md) and the
[prototype inventory](prototypes/column-audit/FAILURE_INVENTORY.md) (F1–F28).

Decisive journeys run the real Helm router, auth (disabled-mode synthetic human),
store, worker and correction lifecycle through
`internal/httpapi/column_audit_browser_test.go`. Only GitHub and the model are
scripted. Each failure lists the spec that covers it: `L` is
`web/e2e/column-audit-lifecycle.spec.ts`, `S` is `web/e2e/column-audit-scope.spec.ts`.

## Scope and start

- U1. The action is still "Run audit", or Done/Blocked are left out of the
  default scope. Audit columns must default to every card, including Done and
  Blocked. [S]
- U2. The count comes from the cards loaded on the board (50 per page) rather
  than server pagination, so Done cards beyond the first page go missing. [S, L]
- U3. The baseline repository/ref is hidden, a PR head silently becomes the
  baseline, or a branch is offered that the API cannot honor. A PR head is an
  explicit `baseline:{repository,pull_request}` choice. [S]
- U4. Opening repository settings or leaving the page loses the intended scope
  (mode, columns, selected cards, baseline). [S]
- U5. Missing AI/GitHub readiness is reported without an exact fix where the
  audit starts. [S]

## Running

- U6. Progress invents percentages or stages, or names unread unrelated files as
  failed conditions. Only server stages and measured counts. [L]
- U7. Leaving the page cancels or loses the run; the deep link must restore it. [L]
- U8. Cancel or failure looks like an empty successful audit; retry loses scope. [S]

## Results

- U9. Counts come from the loaded findings page instead of
  `assessment_summary`, or do not balance. [L]
- U10. Empty groups render, or a run with no verified judgments reads as all
  clear. [L]
- U11. The row leads with confidence/hash/IDs instead of current → recommended
  column, reason and criterion coverage. The compatibility `confidence: 0` is
  displayed as a confidence. [L]
- U12. Couldn't-verify or already-correct rows offer a selection or mutation. [L]
- U13. A foreign claim hides the recommendation, or allows selection. Server
  publication eligibility is presented as permission to apply. [L]
- U14. A card changed after the audit keeps a selectable correction or has its
  target silently replaced. It must stay visible, muted, with Re-audit. [L]
- U15. Open card loses the audit, or fails for a card beyond the board's first
  page. [L]
- U16. Legacy/external findings are rendered with invented v2 criteria, or lose
  their original approve/preview flow. [L, legacy mocked spec]

## Evidence

- U17. Criterion results rely on colour only, citations lack path:line/ref/short
  commit, or source renders as HTML. PR-only evidence looks like baseline proof. [L]
- U18. Escape, outside click or browser Back do not close the panel, or focus is
  not returned to the opener. [L]
- U19. Revoked source access leaves protected excerpts or confirmation proofs in
  memory/DOM. [L]

## Review and apply

- U20. Corrections are preselected, or a chain of Approve/Continue/confirm
  dialogs precedes the one final Apply. The old finding decision API is called
  for v2 findings. [L]
- U21. Preview is not read-only, or the apply count includes stale/blocked items
  removed by the authoritative preview. [L]
- U22. A bug resolves without explicit `resolution:"fixed"`, a bug reopens without
  a reason, or a changed input reuses an older proof. [L]
- U23. An unknown transport outcome is reported as nothing changed, or a retry
  sends a different body/key. Proofs are logged or persisted. [L]
- U24. A whole batch is reported as applied when only some cards moved. Skipped
  or failed same-key replays become applied later. [L]
- U25. A partial receipt task snapshot replaces the full board task and erases
  fields, or a later refresh failure relabels a confirmed Applied result. [L]
- U26. A late response from another project/session updates this page. [L]

## Layout and access

- U27. Horizontal scroll at 320/390 px, missing dark-theme contrast, or panels
  unusable with the keyboard. [L, S]

## Parent integration regressions

`web/e2e/column-audit-boundaries.spec.ts` was written before these fixes:

- U28. Counting stops once the project exceeds the audit limit, then silently
  excludes later cards from a smaller column selection. Count the complete
  project, or return an explicit counting error without starting an audit.
- U29. A session change retains the earlier review's selected findings/proofs.
  Reset protected state synchronously and remount the review for the new session.
- U30. An off-page task lookup finishes after a session change and opens the old
  card. Discard late session/project responses before opening the drawer.
- U31. A check citation shows its name but hides the captured check outcome.
  Display the observed outcome beside its immutable source identity.

The oversized-count and check-viewer cases use labeled supplemental response
fixtures. Lifecycle mutations, scope/run flows and session isolation still use
the real local backend. Legacy setup must fail on an unsuccessful finding POST;
the lifecycle journey also verifies a real external finding can be approved.
