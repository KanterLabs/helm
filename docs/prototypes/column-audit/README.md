# Column audit clickable prototype (TC-306)

Standalone HTML/CSS/JS prototype of the column audit flow in
`docs/AUDIT_COLUMN_RECONCILIATION_UX.md`. It opens directly from disk with no
server, uses deterministic synthetic fixtures and makes no network requests.
A CSP meta tag blocks `connect-src`, and the browser test fails on any request
that is not a `file://` URL.

Open `index.html` in a browser. Add `?theme=dark` or `?theme=light` to force a
theme. Runs are kept in `sessionStorage`; run `helmAuditPrototype.reset()` in the
console to start over.

## Files

| Path | Responsibility |
| --- | --- |
| `index.html` | Shell, prototype banner, script order |
| `fixtures/project.js` | Synthetic project, columns, baseline, readiness, apply outcomes |
| `fixtures/corrections.js` | 8 cards whose committed work supports another column |
| `fixtures/other-results.js` | 6 already-correct and 4 couldn't-verify cards |
| `js/dom.js` | Text-only element builder, icons, announcements |
| `js/model.js` | State, scope counts, run stages, preview revalidation, simulated apply |
| `js/view-scope.js` | Home, scope, running, canceled views and Run details |
| `js/view-results.js` | Summary, tabs, correction/correct/unverified rows, review bar |
| `js/view-evidence.js` | Evidence side panel (desktop) and sheet (mobile) |
| `js/view-apply.js` | Read-only preview and per-card receipt |
| `js/app.js` | Hash routing, actions, focus management, theme |
| `css/base.css`, `css/results.css`, `css/panels.css` | Tokens and shell, results, overlays and apply |
| `FAILURE_INVENTORY.md` | Failure modes F1–F28, written before the prototype |
| `tests/column-audit.spec.ts`, `tests/playwright.config.ts` | Focused E2E journey and layout sweep |

## UX decisions

- **The answer comes first.** The heading reads "8 column corrections suggested",
  followed by balanced counts and an always-visible baseline chip
  (`acme-synthetic/harbor · beta @ 5f3c9a1`). Full hashes, budgets, coverage and
  attempt history live under **Run details**.
- **Scope makes the baseline and Done explicit.** It shows the repository plus a
  Branch or ref select with "Cards are judged against committed work here", and
  a PR head is only an explicit choice. All cards states "Includes 6 Done cards,
  2 beyond the board's first page". The button carries the real count ("Audit 18
  cards") and Specific columns starts empty, so you can't start an empty run. The
  unmapped custom column (QA hold) is asked about once, inline, and can be left
  unset. A revoked repository is shown where the audit starts, with its fix.
- **Running** shows three real stages with counts computed from the fixtures,
  "Leaving this page is safe", Cancel and Run details. Canceling gives "Audit
  canceled · No cards were changed" with a retry of the same scope. Results
  are never partial, and attempts stay in history.
- **Correction row order:** key and title (the title opens the card), then
  current → recommended column chips, a one-sentence reason, the condition
  tally, then Review evidence and an action-specific selection such as "Select:
  Mark done" or "Select: Reopen bug". Nothing is preselected and there is no
  per-row move button.
- **Destination is shown apart from blockers.** A claimed card shows "Done
  recommended · Cannot apply while this card is claimed by agent …" with Open
  card and no selection control. A stale card keeps its earlier recommendation,
  muted, with "Card changed after this audit" and Re-audit.
- **Couldn't verify never looks like a placement error.** These rows use a
  dashed, neutral style: the current column plus a "Couldn't verify" pill, no
  arrow, no selection, and "This is not a column change". Recovery matches the
  cause: Review inspected evidence, Refine source, Re-audit, Add acceptance
  criteria, Choose column meaning (jumps to the scope select) or Reconnect
  repository.
- **Evidence is organized by criterion.** Each quoted condition shows Supported,
  Contradicted or Not verified as a word plus an icon. Citations show the kind
  (Implementation / Test source / Check result / Revert / Task fact), `path:lines`
  and `beta @ shortsha` with a bounded, line-numbered excerpt and Open at commit.
  PR-head-only evidence is tagged "PR head · not on beta". Missing evidence says
  which conclusion it prevents. Confidence (as 0.xx, labeled diagnostic) and
  full hashes are only under a collapsed Technical details. A revoked source hides
  private excerpts. Excerpts are inserted as text, so `<script>` renders
  literally.
- **Panel behavior:** a dialog with a focus trap. Escape, Close, the scrim and
  browser Back all close it and return focus to the button that opened it. You
  can select from the panel or from the row, and the two stay in sync.
- **One confirmation.** The sticky "Review N corrections" bar leads to a
  read-only preview that rechecks the selection. HB-22 becomes "Can't apply ·
  not counted below" because beta advanced, so the button reads "Apply 5
  corrections". Bug policy inputs (Resolution, Reopen reason) sit inline. Apply
  is a static footer, so you pass every row before reaching it. There is no
  Approve, Continue or dialog step.
- **The receipt is per card.** It reads "2 of 5 corrections applied", then 1
  already applied · 1 skipped · 1 failed. Retrying the failed card is idempotent
  (same receipt and idempotency key, next attempt), and the skipped card stays
  where it was. Result rows then show "Applied: In progress → Done".

## Fixture coverage

Completion (HB-12), Done → In progress reopen via a revert (HB-7), move out of
Backlog (HB-9), already-correct Done/In progress/Backlog (HB-3, HB-5, HB-8,
HB-2, HB-11), PR-head-only work kept off beta (HB-27), budget-limited unknown
(HB-18), missing criteria (HB-26), ambiguous custom column with two mapping
variants (HB-29), revoked source (HB-24), stale task (HB-15), stale source at
preview (HB-22), foreign claim (HB-21), bug resolve (HB-31) and bug reopen
(HB-34). The mixed apply receipt covers Applied, Already applied, Skipped and
Failed then Applied on retry. All repositories, commits, PR #52 and agents are
invented. Nothing links to a real PR URL.

## Running the test

```bash
cd web   # reuses the existing @playwright/test install
NODE_PATH=$PWD/node_modules npx playwright test \
  -c ../docs/prototypes/column-audit/tests/playwright.config.ts
```

Outputs go to `out/`: `shots/*.png` (desktop light journey, plus 320/390/desktop
in light and dark), `apply-response.json` (simulated apply response),
`results.json`, `test-output.txt` and per-test `test-results/*/trace.zip`.

## Limitations

- This is a prototype only. Open card, Re-audit, Refine source, Reconnect and
  Open at commit show an announcement instead of navigating.
- Results exist for the `beta` baseline only. Choosing `main` or the PR head
  explains this and disables Start.
- Run timing is simulated (about 2.7 s). The deep link and applied state persist
  for the browser session only.
- Only Chromium is exercised. WebKit and Firefox and a screen-reader pass were
  not run.
- Fonts fall back to system fonts because the prototype loads nothing from the
  network.

## Parent acceptance

The parent reviewed the source and saved artifacts, corrected timeout wording so
an unconfirmed request never promises that nothing changed, and independently
passed the focused user-flow E2E in 7.2 seconds. The original worker also passed
the layout/theme journey. This is synthetic design evidence, not a deployed UI.
