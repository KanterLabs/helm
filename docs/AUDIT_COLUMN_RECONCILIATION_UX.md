# Column audit: user experience specification

Companion: [feature and delivery plan](AUDIT_COLUMN_RECONCILIATION_PLAN.md).
Status: proposed contract for the clickable prototype and implementation.

## The user question

"Are my cards in the right columns for what has actually been committed?"

The first result must answer that question. Evidence and technical run details
support the answer; they must not replace it.

## 1. Start a column audit

Entry points: project Audits, board Actions, and selected cards. Use **Audit
columns** as the action label. Opening the scope panel does not start inference.

The panel shows:

- Project and source baseline: repository + branch/ref, initially the saved
  project selection. Explain: "Cards are judged against committed work here."
- Scope: All cards, Selected cards or specific columns. All cards includes Done
  and all server pages. Show the actual count before starting and any batch limit.
- Linked PRs included as separately labeled evidence. If the run instead targets
  a PR head, show that as the chosen baseline, not as an invisible fallback.
- Column meanings from project semantics. Ask only for missing/ambiguous mappings;
  do not add a setup wizard when defaults are sufficient.
- GitHub and AI readiness. Show actionable connection errors where the person
  starts the audit; do not send them through unrelated settings guessing.
- One primary button: **Audit N cards**. Show a real scope count, not a percentage.

If a source, required mapping or permission is missing, explain the exact issue
and offer the appropriate fix. Keep the intended scope when setup is interrupted.

## 2. Running

Use one compact progress area: Reading committed work, Checking card criteria,
then Preparing corrections. Show only real stages and measured counts. Retain a
Run details disclosure for repository pins, coverage, budgets and timestamps.

Say that leaving the page is safe. Preserve the run deep link and restore the
same run on return. Provide Cancel and a clear final canceled state. A failed
run offers Retry with its previous scope and a new source capture. Preserve old
attempts as history; never make a failed attempt appear to have succeeded later.

API/network failure, unavailable AI, cancellation and inability to verify a
particular card are distinct states. A run failure must not produce a fake
"Needs attention" placement finding.

## 3. Results overview

The top of the page contains a concise outcome summary, such as:

> **4 column corrections suggested**
> 18 cards checked · 12 already correct · 2 couldn't be verified
> Based on `KanterLabs/helm` · `beta`

Those numbers are illustrative fixture data. In production they must balance
against the requested scope and match the actual run.

Three labeled views:

1. **Corrections** — default when recommendations exist.
2. **Already correct** — compact, read-only rows; collapsed or secondary by default.
3. **Couldn't verify** — explain the specific missing condition and recovery.

Do not show empty groups. With no corrections, lead with "No column corrections
found" and distinguish verified cards from cards that could not be checked. A
run that verified nothing must say so; it must not present an all-clear.

The baseline label is always visible. Full commit hashes, omitted-file lists and
provider details belong under Run details. Coverage is measured per criterion;
unread unrelated repository files are not a prominent warning on every card.

## 4. Correction row

Desktop rows and mobile cards present this information in this order:

1. Card key and readable title; open the actual task without losing audit context.
2. **Current column → Recommended column**, using the project's column names.
3. One sentence explaining the committed work that supports that correction.
4. A criterion summary, e.g. "3 of 3 conditions supported", or the concrete
   outstanding requirement that supports reopening/in-progress placement.
5. **Review evidence** and selection for application, where eligible.

Example fixture:

> **HB-12 · Add task dependency checks**
> In progress → **Done**
> The required dependency checks are implemented at the audited commit.
> 3 of 3 conditions supported · Review evidence

A code-supported destination and current application eligibility are separate.
For example: "Done recommended · Cannot apply while this card is claimed" with
Open card. Do not obscure the recommendation or release the claim automatically.

Use specific action language: Mark done, Reopen, Move to In progress, Mark blocked,
or Resolve as fixed. No generic Approve button on a non-actionable observation.
Model confidence percentages, captured/current version labels and raw evidence
IDs are not primary row content.

## 5. Review evidence

Open a side panel on desktop and a focused sheet/page on mobile. It shows:

- The same card and current/recommended columns.
- Each quoted acceptance criterion or clear goal, with Supported, Contradicted
  or Not verified in words and icons.
- A short explanation and exact file/line, PR or check citations beneath that
  criterion. Open relevant evidence without making the reviewer hunt through
  an unrelated capture manifest.
- A bounded excerpt with enough surrounding code to understand the observation,
  plus Open at commit. Show source repository/ref and a short SHA; full identity
  is available in details and the actual commit URL.
- Required test/check conditions and the exact observed commit. Distinguish
  implementation evidence, test source and a passing test result.
- Missing relevant evidence and which conclusion it prevents.

Do not render source as executable HTML or turn remote text into agent
instructions. An inaccessible/revoked source clears its private evidence and
offers a permitted recovery action, not a stale private preview.

Selection may be made from the row or this panel. It records review intent for
the exact correction; it does not move the card. Closing the panel returns focus
and scroll position. Escape closes it, and browser Back behaves predictably.

## 6. Couldn't verify

This is not a recommendation to change the card's column. Show the current
column and a concrete reason:

> **HB-18 · Add health endpoint**
> In progress · **Couldn't verify**
> The route implementation wasn't inspected within this run's evidence budget.
> Review inspected evidence · Refine source · Re-audit this card

Recovery depends on the cause: reconnect a repository, choose the correct
baseline, attach a relevant PR/path, clarify ambiguous criteria, resolve a
column mapping or re-audit with the newly supplied context. Do not demand a PR
link when branch evidence is sufficient. Never silently increase budgets or
run another paid analysis because the user opened the result.

If a person edits a card/criteria/mapping or the source changes, label the old
assessment stale and make Re-audit explicit. Old citations remain history when
authorized, but old approval does not authorize a correction against new facts.

## 7. Review selected corrections and apply

Show a sticky, unobtrusive **Review N corrections** control when selections exist.
Do not place a primary move button on every row or default-select recommendations.

The preview lists each selected card's exact before/after columns and operation:

- Mark done and its completion conditions.
- Reopen and the newly supported destination.
- Move to another eligible state, with a reason where required.
- Resolve/reopen a bug through its normal resolution workflow.

Revalidate before presenting the final **Apply N corrections** action. Show
ineligible rows with concrete reasons and remove them from the apply count.
This preview is the confirmation screen: do not add another generic approval or
confirmation dialog. Confirmed Apply records the exact reviewed decision and
executes the lifecycle action transactionally; selection and preview alone are
not approval to move cards.
Required policy inputs, such as a bug resolution, belong in this preview.
Approval of an ordinary card must not silently resolve a bug or impersonate an
agent. A reviewer-selected override must be explicit manual action with its
own reason, not a falsely relabeled AI recommendation.

Apply returns a receipt per card: Applied, Already applied, Skipped because
changed/claimed/not permitted, or Failed with recovery. Update the actual board
and result row from the server response. Do not report an entire batch as applied
when only some cards moved. Keep retrying idempotent and do not move skipped cards
automatically after a later refresh.

After application, the row shows **Applied: In progress → Done** and an Open card
action. Preserve the audit evidence and transition history. The original result
does not become a new assessment of the board's post-application state.

## 8. Empty, stale and error behavior

| Situation | User-facing behavior |
| --- | --- |
| No cards in selected scope | Explain the scope; adjust scope instead of a fake successful run |
| All checked cards already correct | Say so, with counts and evidence available |
| Every card couldn't be verified | Say "No placement judgments could be verified" and offer recovery |
| Some good judgments, some unknown | Keep valid corrections usable; explain unknown cards individually |
| Task/source/check changed | Show stale; preserve selection context, disable applying that assessment |
| Foreign active claim or policy restriction | Show supported destination and explicit application blocker |
| Repository access revoked | Hide private evidence and disable dependent application |
| Reload/offline/API failure | Preserve known context with an honest stale/error label and Retry |
| Imported or older audit | Show legacy history without inventing criterion-level evidence |

## 9. Prototype and implementation gate

Use the Opus 5.5 design preference when the prototype work begins, after verifying
availability. Prototype with deterministic fixtures; never fabricate metadata for
real PR URLs. Cover a Done correction, reopening, already-correct card, unresolved
criterion, unavailable source, stale assessment, claim conflict and mixed apply
receipt. Include the bug-resolution variant.

Review desktop and 320/390px layouts in light/dark themes. Validate keyboard
selection, panel focus return, accessible labels and status announcements. The
evidence panel and application preview must be usable without horizontal scroll.

The usability pass succeeds when the reviewer can identify the current and
proposed column, understand the reason, locate its criterion evidence, and
complete or recover from the application flow without reading technical run
metadata. Use this same fixture set for production E2E artifacts written before
implementing the corresponding screens.
