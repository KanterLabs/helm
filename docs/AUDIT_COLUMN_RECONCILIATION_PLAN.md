# Audits that reconcile columns with committed work

Status: planning, 28 October 2026. Planning task: TC-305.
Companions: [review UX](AUDIT_COLUMN_RECONCILIATION_UX.md) and
[validation and beta release](AUDIT_COLUMN_RECONCILIATION_VALIDATION.md).

## Product contract

For each card, answer: **Does its current column match the work committed in the
chosen source baseline? If not, which column is supported, and why?** A person
can review the evidence and apply the correction inside Helm.

This replaces the column-audit requirements in the earlier
[beta plan](GITHUB_TOKEN_BETA_PLAN.md) and
[execution design](GITHUB_TOKEN_BETA_EXECUTION.md). In particular, approved Done,
In progress and reopening corrections are core functionality. Run audit and
reviewing a result never move a card. Existing authentication, scoped source
access, isolated inference and explicit application remain requirements.

This is a placement audit, not a general code review. Security findings,
refactoring suggestions and unrelated improvements are not audit results unless
they determine whether this card's stated work meets its completion conditions.

## What is wrong today

The deployed pipeline can authenticate, capture GitHub files and run inference.
Its live health-endpoint example selected audit migration/handler files, missed
the relevant implementation and returned a 15% confidence non-answer.

The implementation has five material gaps:

1. Filename ranking rewards incidental words in the card, then consumes a shared
   whole-file budget. It is not reliable task-specific evidence discovery.
2. The prompt requires `needs_attention` for partial capture, and publication
   rejects `correct` whenever the repository capture is partial. Unrelated unread
   files prevent useful judgments about otherwise verifiable cards.
3. The default scope omits Done, so it cannot find incorrect completion claims.
4. Findings have no structured criterion-by-criterion assessment. A percentage
   and free-text reason obscure what was actually established.
5. Apply only handles Backlog/Ready. It cannot finish the main user journey of
   correcting completed work, partial work or an incorrectly completed card.

TC-293, TC-298 and TC-300 still exist as older open prototype cards even though
parts of their implementation shipped. Do not silently complete or rewrite those
records. The new work items below cover the verified gaps; release validation
must explicitly reconcile overlap with those older cards.

## 1. Judge the card against a defined baseline

### Source and completion meaning

- The run visibly selects repository and branch/ref, defaulting to the project's
  saved selection. Resolve and pin its exact commit before analysis.
- The selected baseline determines which committed implementation is being
  judged. Linked PR heads are additional, separately identified sources. Work
  present only on a PR branch must not be presented as present on the baseline.
- A reviewer can explicitly choose a PR head as the run baseline. Never switch
  the baseline silently to obtain a favorable result.
- Read the goal, explicit acceptance criteria, relevant checklist conditions,
  task kind and project completion policy. If only a clear goal exists, assess
  that quoted goal. If the meaning of Done is ambiguous, request clarification;
  do not invent requirements or declare completion.
- Merge, CI, release and deployment are completion conditions only when the card
  or project requires them. A merge alone never proves implementation; absent
  deployment tracking does not block a code-only task.
- Check evidence must refer to the relevant commit. Do not invent branch
  protection requirements or treat an unrelated passing check as proof.
- Use actual project column IDs, names and semantic meanings. Standard columns
  get sensible defaults. Ambiguous custom columns need a visible mapping or
  clarification; never choose the first matching semantic column arbitrarily.

### Placement decisions

| Supported committed state | Placement judgment |
| --- | --- |
| All completion conditions are supported | Done is appropriate; recommend Done if elsewhere |
| Implementation exists but required work remains | In progress is appropriate; recommend reopening if currently Done |
| Work is established as unstarted, and preparation is complete | Ready, using the project's readiness definition |
| Work is explicitly unstarted and not ready | Backlog, using task/dependency context |
| A concrete unresolved blocker is established | Blocked, with the blocker identified |
| Evidence does not establish a state | Unable to verify; no proposed correction |

No commits found is not proof that work is unstarted. Failed CI normally means
work remains; it does not by itself prove that work is blocked. Missing evidence
does not prove a Done card is wrong. A card already in the supported column is
correct and has no mutation action.

Done cards with demonstrated missing/reverted implementation must be reviewable
as reopening recommendations. Cards in Backlog, Ready, In progress or Blocked
can recommend Done when the stated conditions are supported, independently of
whether application is currently permitted.

## 2. Retrieve evidence for criteria, not for the word "audit"

1. Capture all selected cards server-side, including Done by default. Respect
   pagination, filters explicitly chosen for the run, permission ceilings and
   visible batch limits. Never silently use only the current board page.
2. Assign stable criterion IDs within each captured task version. Preserve the
   quoted goal/criterion and its origin rather than rewriting the card.
3. Start discovery from explicit paths, symbols, routes and linked PR changes.
   Use the repository tree to locate likely owners and follow relevant source
   references. Incidental prose such as "audit demonstration" must not outrank
   the subject of a criterion such as `GET /healthz`.
4. Allow bounded discovery/refinement rounds. A model may propose paths from a
   server-supplied index; only the server provider can validate and fetch them.
   Where provider search is available, treat results as discovery hints and
   re-read the selected commit. No arbitrary URLs, model shell, local checkout,
   dependency installation or repository execution is introduced.
5. Capture relevant excerpts with enough surrounding implementation to evaluate
   behavior. Follow-up reads should fill a named criterion gap. A README promise
   alone is not proof that its implementation exists.
6. Use per-card budgets plus a run ceiling, deduplicate shared reads, and prevent
   early cards or large unrelated documents from starving later cards. Keep
   file, byte, request, inference-round and time limits configurable and bounded.
7. Stop when sufficient evidence has been gathered or a budget/access limit is
   reached. Record what was inspected and the specific unresolved gap.

Every excerpt records source repository, ref, commit, blob/path, line range,
evidence ID and the card/criterion it supports. Separate implementation, test
source, observed check results and task/dependency facts. None substitutes for
another without an explicit reason.

Repository coverage and criterion coverage are different. A repository can be
partially read while a particular criterion is sufficiently evidenced. Missing
an unrelated image or unread documentation must not invalidate that criterion.
An actual gap affecting a required criterion prevents a conclusive Done judgment.

## 3. Store a useful, versioned assessment

New assessments need these fields, validated against the immutable capture:

- Card ID/key, captured version, source column ID/name and task kind.
- Goal/criterion text and stable criterion ID.
- Per-criterion result: supported, contradicted or unknown; a concise explanation;
  exact citation IDs; and coverage gaps where relevant.
- Placement result: column correct, correction recommended or unable to verify.
- Recommended column ID/semantic state and lifecycle action, when justified.
- A short explanation connecting the committed facts to the column decision.
- Source baseline and evidence freshness, plus separate application eligibility.

Keep optional model confidence as diagnostic metadata, not the primary decision
signal. The server rejects duplicate/out-of-scope cards, unknown citations,
inconsistent destinations and conclusive assessments missing required evidence.
Validate coverage per card/criterion; replace the blanket partial-capture rule.

Return one result per selected card, including cards that could not be evaluated.
Run summaries count recommended corrections, correct cards and unable-to-verify
cards separately. Failed retrieval/inference is a run problem, not a fabricated
placement finding. The result set must account for the entire requested scope.

Use additive, versioned assessment storage and OpenAPI/types. Preserve existing
runs, decisions, source manifests, external submission and legacy public shapes.
Show older records as legacy assessments; do not invent criterion evidence or
reinterpret old approval as approval of a new correction. A compatibility
projection must not bypass the new assessment validation.

## 4. Apply the correction as a real task lifecycle action

The review separates **the correct destination** from **whether it can be
applied now**. A foreign active claim can prevent applying a valid Done
recommendation; it does not make the committed implementation unfinished.

The normal flow is select recommendation, preview, then confirm application.
Selection records review intent; it does not move the card. Preview is read-only.
The final human confirmation records approval of the exact preview and applies
the lifecycle action transactionally, with idempotent receipts. Existing legacy
approval APIs remain compatible; the new UI does not require a separate Approve,
Continue and confirmation-dialog sequence. The preview resolves
the appropriate normal lifecycle operation for each selected correction:

| Correction | Required operation |
| --- | --- |
| Into Done | Normal completion with checklist/dependency policy |
| Out of Done | Normal reopening with preserved completion history |
| Into In progress | Normal human state transition; do not impersonate or claim as an agent |
| Into Blocked | Normal block operation with the evidenced reason |
| Into Backlog/Ready | Normal state/move rules and explicit target column |
| Bug into/out of Done | Normal resolution/reopening flow, including required resolution/reason |

Extract transaction-aware lifecycle helpers if necessary. Do not copy the
existing audit's raw column update to implement completion or reopening. Keep
`completed_at`, claims, checklists, dependencies, bug resolution, ordering,
versions and normal events consistent with ordinary task actions.

At preview and immediately before mutation, check task/criterion/column versions,
permissions, active claims, completion rules, selected source, cited commit and
material check drift. Review intent binds the exact assessment and destination.
Changed inputs require a fresh assessment; an unknown current source state is
not permission to apply an old result.

Record actor, audit, finding, before/after state, source commit and an idempotent
application receipt. Retrying never duplicates transitions, comments or events.
Batch application reports Applied, Already applied, Skipped/conflict or Failed
per card. One conflicted card does not masquerade as success for the batch.
Do not release another agent's claim or override a lifecycle restriction silently.
Explain the blocker and offer Open card or Re-audit as appropriate.

## 5. Build the UX around the correction

The [UX specification](AUDIT_COLUMN_RECONCILIATION_UX.md) defines the screens,
content hierarchy, interactions and states. Its primary result is:

> A card is in **In progress**; committed implementation supports **Done**.
> See which criteria are satisfied, review the code evidence, and apply completion.

Do not put capture manifests, full hashes, empty groups or confidence percentages
above the answer. Unable-to-verify cards explain the missing evidence and have a
recovery action, never an approval control that suggests a column correction.

First build a clickable prototype with the requested Opus 5.5 design preference,
subject to confirming model availability when that work starts. Use a fixture set
covering all three placement outcomes and lifecycle conflicts. Validate the
interaction before building production screens; an attractive empty state alone
does not satisfy the UX gate.

## Delivery sequence

The linked Backlog items below are implementation work, unclaimed until started.
TC-305 owns this planning contract; its completion does not complete these items.

| Work item | Deliverable | Prerequisites |
| --- | --- | --- |
| TC-306: UX prototype | Clickable run/results/evidence/apply experience | This contract |
| TC-307: Task-focused retrieval | Criterion-linked committed source packs | This contract |
| TC-308: Column evaluator | Versioned criterion assessments and column judgments | TC-307 |
| TC-309: Lifecycle application | Complete, reopen and other eligible corrections | TC-308 |
| TC-310: Production UX | Implement the reviewed prototype against the real API | TC-306, TC-308, TC-309 |
| TC-311: Integrated beta validation | Prove accurate placement and usable correction flows | TC-310 and prior work |

Prototype and retrieval can proceed independently. Shared schemas, migrations,
builds and code generation are serialized. Each implementation item measures
owned files before edits and handoff, extracts near 16,000 bytes and keeps every
maintained file at or below 20,000 bytes. Do not modify oversized general task
modules without a responsibility-based extraction in that item's scope.

## Acceptance and release gate

The [validation plan](AUDIT_COLUMN_RECONCILIATION_VALIDATION.md) defines the
reference scenarios, test-first requirements, data-preservation checks and beta
release gate. Acceptance requires accurate placements and applied completion and
reopening corrections, including a conflict that is correctly not applied.
A successful model response alone is insufficient.

## Code ownership guide

- Retrieval: `internal/httpapi/github_audit_capture.go`, provider tree/file/PR APIs.
- Evaluation/publication: `github_audit_service.go`, `github_audit_output.go`,
  `internal/store/github_audit_publish.go`, versioned finding types/contracts.
- Application: `internal/store/github_audit_apply.go` and focused transaction-aware
  task lifecycle modules. Existing `task_transitions.go` and `task_updates.go`
  establish lifecycle behavior that audit must preserve.
- UX: `AuditReview.svelte`, focused `audit/*` components, types/API and styles.
- Validation: existing real-backend GitHub audit harness and focused browser
  journeys. The current agent grant API remains useful for bounded live tests.
