# TC-308: criterion assessments and column placement

Implementation handoff for TC-309 (application) and TC-310 (UI).
Companions: [plan](AUDIT_COLUMN_RECONCILIATION_PLAN.md),
[UX](AUDIT_COLUMN_RECONCILIATION_UX.md),
[validation](AUDIT_COLUMN_RECONCILIATION_VALIDATION.md), and
[test-first failure inventory](AUDIT_ASSESSMENT_FAILURES.md).

## Version and public shape

New GitHub audit captures explicitly select `assessment_version: 2`. The worker
uses the v2 output schema, decoder and publication method. Missing/unsupported
versions or malformed v2 output never fall back to the legacy decoder. Existing
captures without a version use their original v1 semantics. External finding
submission remains v1; it cannot populate a v2 worker run.

`AuditFinding.assessment` is the immutable v2 observation. Existing finding
fields remain a validated compatibility projection: `column_correct` becomes
`correct`, `correction_recommended` becomes `move_proposed`, and
`unable_to_verify` becomes `needs_attention`. Projection confidence is zero;
it is not a measure of evidence strength. Old rows omit `assessment` and must
be shown as legacy, without invented conditions or reinterpreted approval.

Every selected task and every captured condition must appear exactly once.
Task ID, version, source column, condition ID, quote and origin must match the
immutable capture. Citations must belong to that task/condition. Explanations
and explicit gaps accompany unknown conditions. All required conditions need
appropriate baseline proof before Done; README promises, test source and linked
PR metadata cannot substitute for implementation. Explicit passing-CI conditions
need an observed successful check at the baseline commit. A skipped check does
not establish passing CI. Relevant baseline retrieval gaps prevent conclusive
condition results; unrelated repository omissions do not.

An observed check condition needs commit-specific check evidence rather than an
implementation-file owner. Retrieval records matching baseline observations
directly and avoids unnecessary source planning for that condition. Source-code
conditions retain their implementation evidence requirements.

`AuditRun.assessment_summary` counts `checked`, `corrections`, `correct` and
`unable_to_verify` from persisted assessments. These counts balance independently
of paginated findings. A complete worker run can contain unable-to-verify cards;
completion of a run does not mean every card is correct.

Example response fragment (opaque IDs shortened only for documentation):

```json
{
  "task_id": "task-1",
  "captured_version": 7,
  "source_column": "column-active",
  "verdict": "move_proposed",
  "proposed_semantic_destination": "completed",
  "confidence": 0,
  "assessment": {
    "assessment_version": 2,
    "task_id": "task-1",
    "captured_version": 7,
    "source_column_id": "column-active",
    "conditions": [{
      "id": "criterion:captured-id",
      "text": "Show task dependencies on cards.",
      "origin": "goal",
      "result": "supported",
      "explanation": "The pinned card renderer renders prerequisite labels.",
      "citation_ids": ["github:file:1"],
      "gaps": []
    }],
    "placement": "correction_recommended",
    "target_column_id": "column-done",
    "target_semantic_state": "completed",
    "lifecycle_action": "complete",
    "reason": "Committed implementation supports Done.",
    "basis": [],
    "task_key": "TC-1",
    "task_kind": "task",
    "source_column_name": "In progress",
    "source_semantic_state": "active",
    "baselines": [{
      "repository": "example/synthetic",
      "ref": "main",
      "sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "repository_id": 1
    }],
    "captured_at": "2026-10-28T00:00:00Z",
    "application_eligibility": {
      "status": "review_required",
      "reasons": ["human_confirmation_required", "lifecycle_validation_required"]
    }
  }
}
```

For a correct card, the target is its captured actual column ID/state and
`lifecycle_action` is `none`. For inability to verify, both target fields are
`null`, action is `none`, each unknown condition names its gap, and there is no
proposed legacy destination. Demonstrated partial work in Done targets the
actual Active column with `reopen` (`bug_reopen` for bugs). Into Done uses
`complete` or `bug_resolve`; other corrections use `start`, `block` or `move`.

Active requires a baseline implementation basis and at least one contradicted
required condition. An unknown condition or `work_remaining` assertion alone
cannot confirm Active or reopen Done. Supported plus unknown without a
contradiction is `unable_to_verify`; other unknown conditions do not invalidate
an independently demonstrated contradiction. Affirmative unstarted/readiness
or concrete blocker context retains its separate placement rules.

## Captured context and actual destinations

Capture includes active project columns and their versions, task kind,
checklist completion policy, checklist facts, claims and direct prerequisite
IDs/versions/states. The normal current block-with-reason event/comment pair is
captured as a blocker fact; an arbitrary old comment is never substituted.
Explicit description context is also recognized:

```text
Work status: unstarted
Readiness: ready
Blocker: Waiting for vendor approval.
```

`Readiness: not_ready` supports Backlog rather than Ready. These are affirmative
context declarations, not implementation proof. Arbitrary unlabeled readiness
prose remains unknown. Unmet captured prerequisites can establish a blocker;
failed CI and absent commits cannot. Missing completion meaning or ambiguous
actual destinations produce inability to verify. The default Backlog and Bugs
columns share a semantic state: selecting a destination among duplicates needs
clarification. A correct card can retain its already-known actual source ID.

The internal inference schema asks for a semantic destination rather than a
duplicated actual target ID. Before full validation, the server binds absent
target metadata from the immutable capture: a correct card retains its exact
current column/state; a correction requires an explicit semantic state and a
unique actual destination. Explicit conflicts are never overwritten, duplicate
Backlog/Bugs mappings remain ambiguous, and unknown cards retain null targets.
The persisted/public v2 shape and all identity, proof and lifecycle validation
remain unchanged.

`application_eligibility` is computed by the server, never accepted from the
model. It records `not_applicable`, `review_required` or `blocked`; reasons can
include an active claim, changed/unavailable task, required open checklist or
unmet dependencies. It is a publication-time observation, **not permission to
apply**. TC-309 must recheck live task/column/criteria/source/check identities,
actor permissions, claims, policy and lifecycle rules in its preview and final
transaction. The recommendation remains visible when application is blocked.
No new lifecycle operation or production UI is included in TC-308.

## Explicit PR-head baseline and semantic discovery

Start input can add:

```json
{"baseline":{"repository":"example/synthetic","pull_request":7}}
```

The repository must already be selected for the project. This explicitly pins
`refs/pull/7/head` as that repository's baseline, recording its PR number and
exact commit in `repositories`. Omission preserves the saved branch/ref.
Ordinary linked PRs remain `linked_pr` evidence and never silently switch the
baseline. Baseline/PR/check identities are rechecked before publication.

Literal paths/routes/symbols retain TC-307 retrieval. When no literal owner is
available, a bounded evidence-only model request selects paths from a
server-supplied pinned index. The server validates every requested path/range,
reads only the exact captured SHA and records criterion-linked excerpts. A
planner may refine using bounded inspected prefixes and a bounded server-extracted
signature/line index: compact declaration names retain their exact source line
numbers within the existing bounded signature envelope, with whole-file sampling
for larger owners. Large owners get a second bounded read-planning round. This is
semantic planning,
not generic word matching or repository execution. It shares TC-307 card/run
file, byte, request and deadline ceilings: at most two file-refinement rounds per
condition plus one directory-routing round for large trees, four discoveries per
card, 48 per run and a 90-second retrieval window. The global production-source
index excludes supplementary tests/docs unless explicitly named or linked by a
PR hint. When that production index exceeds the existing ceiling, discovery
first exposes owning directories, then their pinned files, so late-sorting
owners remain reachable within the routing budget. Each index is capped at 512 candidates/48 KiB of path names;
prompts and responses are separately bounded. Round-robin card scheduling and
shared-read deduplication remain in force. Sufficient literal retrieval adds no
planning call. Durable attempt, authorization/grant, account/provider generation
and source freshness fences surround extra inference and exact reads.

For ambiguous ownership the planner can request safe literal concept searches,
each naming its criterion evidence question. These consume the same discovery
budget; default-branch hits must belong to the pinned tree before reading. Search
hits remain visible beyond the initial index. Refinement sees selected excerpts
before generic prefixes, within the same 12-KiB observed-text allowance. Semantic
follow-ups explicitly request required helpers instead of chasing every call.

Ambiguous unanchored criteria with multiple production owners now start with
a schema-enforced search-only stage. Go refinement uses exact server-indexed
declaration selectors; receiver identity stays distinct from the provider's
lexical query. A selected caller cannot shadow a requested helper in the same
file. Required definitions resolve only when their complete pinned bodies are
captured within the existing shared window/excerpt bounds. Explicit paths,
linked-PR hints and single-owner captures retain their compatibility route.
An explicit `source_complete` result distinguishes no further retrieval from
a named unresolved question; it never establishes criterion support or clears
missing helpers or budget failures.

Assessment must match the observed subject and timing: a check after modifying
a copy cannot establish that the original already passed. User/operator-facing
requirements need an invocation/result surface connected to the behavior. A
CLI or an appropriately linked candidate check can be valid; automatic traffic
or deployment gating is required only when explicitly stated by the card.
Discovery output schemas enforce literal query format. Malformed generated
searches are `invalid_model_output` with a closed diagnostic, not GitHub outages.

When a concept exactly names a Go function, its pinned syntax-tree declaration
supplies body windows before earlier field/metric mentions. At most three
101-line windows share the existing whole-line byte/excerpt limits; truncation
remains a gap. Explicit model windows retain every requested whole line. For
literal-anchor v2 Go retrieval, bounded AST/import/local-declaration provenance
distinguishes standard-library calls from owned or unknown references, including
SQL factories, typed parameters/fields, aliases and lexical shadowing. This
loads no packages or dependencies. Non-Go/malformed source and v1 retain the
original reference fallback. Cached pinned files can be reconsidered without
another provider read. These mechanisms improve capture, not placement proof:
missing behavior or required owned source must prevent unsupported completion.

## Module map

| Responsibility | Modules |
| --- | --- |
| Versioned public/store types | `internal/store/github_audit_assessment_types.go`, `github_audit_types.go` |
| Context and source columns | `internal/store/github_audit_context.go`, `github_audit_capture.go` |
| Inference contract and prompt | `internal/httpapi/github_audit_assessment_output.go`, `github_audit_assessment_prompt.go`, `github_audit_service.go` |
| Semantic planning / exact reads | `internal/httpapi/github_audit_semantic*.go`, existing `github_audit_retrieval*.go` |
| Explicit PR baseline | `internal/httpapi/github_audit_baseline.go`, `github_audit_capture.go` |
| Proof and placement validation | `internal/store/github_audit_assessment_proof.go`, `github_audit_assessment_validate.go` |
| Atomic publication / eligibility | `internal/store/github_audit_assessment_publish.go` |
| Additive persistence | `internal/db/migrations/041_github_audit_assessments.sql`; nullable `audit_findings.assessment_json` |
| Legacy views / summary | `internal/store/audits_findings.go`, `audits_runs.go` |
| Portable history | `internal/store/github_audit_assessment_archive.go`, `portable_v3_*` |
| Authored OpenAPI | `openapi/schemas/github-audit-{assessments,retrieval,evidence,jobs}.yaml`, finding/audit/portable fragments |

Migration 41 does not rewrite history or older migrations. Retained named-column
SQL continues to work. Versioned destination/assessment JSON is immutable;
review state remains mutable. Portable v3 includes assessments and pinned
manifests, validates their projection and preserves imported-history guards.
Imported history is stale and cannot restore operational jobs or source access.

## Validation and remaining boundaries

Focused real HTTP/provider/store/worker scenarios cover placements, strict
identity/proof validation, irrelevant omissions, ordinary-language discovery
with/without a linked PR, explicit PR baseline, planning cancellation/revocation,
legacy behavior and portable history. Sanitized receipts, red logs, a populated
migration backup and retained-binary readiness/history receipts are indexed at
`/tmp/helm-tc308-handoff/index.json`. Fixtures use scripted model responses;
these prove the model boundary and server enforcement, not live-model accuracy.
TC-311 still owns real-model/beta accuracy and integrated reviewed application.

No deployment or production migration occurred. The verified pre-upgrade backup
is synthetic test data; a verified production online backup remains required
before any later beta/production upgrade. TC-309 owns lifecycle application and
its live eligibility checks. TC-310 owns production presentation/types and should
use the new assessment, actual destination and summary fields directly.

TC-309 implements the versioned [correction preview/apply contract](AUDIT_CORRECTION_CONTRACT.md), including UI examples, receipts, lifecycle module map and retry recovery.
