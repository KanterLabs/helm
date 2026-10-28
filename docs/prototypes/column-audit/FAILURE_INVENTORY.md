# Column audit prototype: failure inventory

Written before the prototype so the browser journey tests the
product question, not the markup. Each entry is a way the review flow could
mislead someone about the question "are my cards in the right columns for what
has been committed?". The `tests/column-audit.spec.ts` assertion that covers
each entry is listed in brackets.

## Scope and baseline

- F1. Starting an audit without seeing which repository and branch are judged.
  The baseline must be visible before the run and on every result. [scope, results]
- F2. Done is silently left out, so wrong completion claims can never be found.
  All cards must include Done, including cards beyond the first board page. [scope]
- F3. The button shows a percentage or a stale count instead of the real number
  of cards to audit. Changing the scope must update "Audit N cards". [scope]
- F4. A PR head is used as the baseline without the person choosing it, or
  PR-head-only work is presented as if it were on the selected branch. [evidence]
- F5. An ambiguous custom column is mapped by position and judged anyway. [scope, unverified]

## Running

- F6. Progress invents stages or percentages, or gives no way to cancel. [running]
- F7. Cancel looks like a completed run with no findings. [cancel]

## Results

- F8. The first screen leads with metadata (hashes, manifests, confidence) rather
  than "N column corrections suggested". [summary]
- F9. Counts do not balance: corrections + already correct + couldn't verify must
  equal the audited scope. [summary]
- F10. "Couldn't verify" is rendered like a placement error: it has an arrow to
  another column, a selection control or a correction verb. [unverified]
- F11. A valid recommendation is hidden because it cannot be applied now (foreign
  claim), or the claim is released automatically. The destination and the
  blocker must both be shown. [claim]
- F12. A stale assessment (card edited after the run) can still be selected. [stale]
- F13. Corrections are preselected, or a primary "move" button sits on each row. [summary]
- F14. Confidence percentages or full commit hashes appear in main rows. [summary]

## Evidence

- F15. The reviewer cannot tell which criterion is supported, contradicted or not
  verified without colour alone. Words and icons are both required. [evidence]
- F16. Citations have no file, line, ref and short commit, or the excerpt can
  execute as HTML. [evidence]
- F17. Escape does not close the panel, or focus is lost instead of returning to
  the Review evidence button that opened it. [evidence]

## Preview and apply

- F18. Selection, or opening preview, moves cards. [preview]
- F19. A chain of Approve → Continue → "Are you sure?" dialogs. The preview is the
  only confirmation and has one Apply button. [preview]
- F20. A card whose source changed after the audit is still counted in Apply N.
  It must be listed as ineligible with a reason and removed from the count. [preview]
- F21. A bug is closed through a column move without its resolution, or an ordinary
  completion silently resolves a bug. [preview]
- F22. The receipt says "5 applied" when only some cards moved. Each card needs
  its own Applied / Already applied / Skipped / Failed status. [receipt]
- F23. Retrying a failed card duplicates a transition, or skipped cards move later
  without review. [receipt]
- F24. After application the row still offers the old correction instead of
  "Applied: before → after". [receipt]

## Layout and access

- F25. Horizontal scrolling at 320 or 390 px, or in the evidence/preview views. [layout]
- F26. Dark theme loses status contrast or hides the baseline chip. [layout]
- F27. The prototype makes a network request or links real PR URLs to invented
  facts. Only `file://` requests are allowed. [network]
- F28. Leaving the page loses the run; the deep link must restore it. [deep link]
