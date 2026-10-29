# TC-309 correction API handoff

Companions: [assessment](AUDIT_ASSESSMENT_CONTRACT.md),
[UX](AUDIT_COLUMN_RECONCILIATION_UX.md),
[failure inventory](AUDIT_CORRECTION_FAILURES.md).
Production UI is TC-310; beta human-auth and deployment validation is TC-311.

## Exact flow for TC-310

`POST /api/v1/audits/{audit}/apply` has two explicit contracts. Set
`assessment_version: 2` for v2. Unknown/null/unsupported versions and malformed
v2 bodies return 400; they never fall back. No version retains the old
`finding_ids`/`preview` contract, including its separate approval and persisted
preview, for legacy findings only. V2 findings cannot use legacy application.
Imported history has no operational job and cannot use either application path.
Agent tokens/grants cannot apply: the route requires a current enabled human
session and normal project access. It never creates a session, claims as another
actor, overrides a foreign active claim or releases one to make a correction fit.

1. Select findings locally; selection has no mutation/approval effect.
2. Request preview, with any required bug inputs:

```json
{
  "assessment_version": 2,
  "preview": true,
  "items": [{"finding_id": "finding-id"}]
}
```

3. Render each result's exact `source_column_id`, `target_column_id`,
   `target_semantic_state`, `action`, `captured_version`, `baselines` and
   `message`. Map IDs to project column names and task identity. Recommended
   destination remains present when application is blocked. `required_inputs`
   names missing inputs. `bug_resolve` requires explicit `resolution: "fixed"`;
   `bug_reopen` requires a nonblank `reason` (max 10,000 bytes). Re-preview after
   changing inputs; policy inputs are part of the proof. Other resolutions are
   separate manual actions. No separate Approve request is needed.
4. Only an `eligible` result supplies `confirmation_token`, `expires_at` and
   `fingerprint`. This is the confirmation screen; one final **Apply** submits
   the reviewed item(s), identical inputs and those tokens:

```json
{
  "assessment_version": 2,
  "preview": false,
  "items": [{"finding_id": "finding-id", "confirmation_token": "from-preview"}]
}
```

   Final Apply requires a new bounded `Idempotency-Key` (1–200 characters) for
   this logical batch. Preserve its exact body/key until its outcome is known.
   Both calls allow 1–200 unique items and return results in request order.
   Preview performs SELECTs in a read transaction: it does not change tasks,
   findings, run state, approval, history/events or persisted preview/receipt rows.
   The five-minute proof is HMAC-authenticated, bound to the enabled human,
   audit, finding, exact context and inputs. Its random key exists only in this
   process. Restart or expiry requires refreshed preview if no receipt exists.
   Do not log/store/export the token or send it to analytics.

## Receipts and recovery

```json
{
  "data": [{
    "audit_id": "audit-id", "actor_id": "reviewer-id",
    "finding_id": "finding-id", "task_id": "task-id",
    "outcome": "applied", "outcome_known": true,
    "message": "Reviewed correction applied", "action": "complete",
    "source_column_id": "active-column", "target_column_id": "done-column",
    "target_semantic_state": "completed", "captured_version": 7,
    "request_key": "logical-batch-id",
    "task": {
      "id": "task-id", "key": "TC-1", "project_id": "project-id",
      "kind": "task", "column_id": "done-column", "version": 8,
      "position": 3, "completed_at": "2026-10-28T23:00:00Z",
      "claimed_by": null, "claim_expires_at": null
    }
  }]
}
```

`applied` and `already_applied` carry the original committed lifecycle snapshot;
`actor_id` identifies the original applying human, including when another human
has already applied that same immutable finding. Fetch the normal task endpoint
for other fields or subsequent changes. Approval, normal lifecycle mutation,
normal history/events, provenance event and receipt commit together. Receipts
bind the assessment, source, actor, task, action, preview hash and request identity.
They are operational records and are absent from portable archives.

Detailed source/model explanations remain in the protected audit. Ordinary move
events use a generic audit provenance reason; block comments reuse the existing
public task blocker or prerequisite context. They never copy protected finding
prose into general task activity. A user-entered bug reopening reason follows the
normal shared-comment lifecycle.

`skipped` gives concrete recovery: open the card, finish prerequisites/checklist,
wait for the claim owner, restore source access, or re-audit changed facts.
A business conflict does not undo another item's committed result. `failed` with
`outcome_known: true` is a durable local failure; refresh/review and use a new
logical key for a new attempt. Skipped/failed items stay that way on same-key
replay, so retry never silently applies them after facts change.

A transport timeout or `outcome_known: false` means **unknown outcome**. Retry
the identical body/key; never tell the person that nothing changed. Durable
receipts are checked under current human/project authorization before source
freshness or proof expiry, so an already committed result remains available
after source advance, disconnect or restart. Same-key changed bodies return
409. Concurrent retries/humans produce one lifecycle change and return the
original receipt. Each selected item has its own outcome; 200 is not batch
success. If an interrupted batch has unprocessed items and previews expired,
replaying first recovers any known outcomes; refresh the remaining items with a
new logical request and explicit final confirmation.

## Revalidation and normal lifecycle

Local checks run in preview and again after acquiring the SQLite writer lock:
enabled human, project, job/outcome/source selection and delegated authorization,
exact immutable assessment, captured task/version/source column, quoted criteria,
checklist state, current checklist policy, claim identity/expiry, dependency
versions/states and entire column mapping IDs/versions. Task ordering revisions
alone do not invalidate independent items. The captured actual target ID is used;
a missing or duplicated semantic mapping never resolves by first match.

Remote checks run for every item at preview and apply: source access, baseline
repository ID/ref/SHA, exact PR state/head/base/merge/review/check observations,
account authorization/connection and provider generation. Cancellation and a
45-second request deadline are honored. Provider generation and proof expiry are
checked again under the writer lock. Remote services and SQLite cannot share a
distributed transaction: observations are rechecked immediately before the local
transaction; unknown remote state always prevents a new mutation.

Relevant v2 evidence remains usable with `Partial=true` caused by unrelated
unread files. Immutable publication eligibility is display history and never
application authority. Actual baseline advance invalidates dependent corrections.
Normal checklist/dependency guards still enforce lifecycle inside the transaction.

| Action | Shared normal writer |
| --- | --- |
| complete / block | `transitionTaskTx`: completed_at, claim release, checklist, notes, normal events/dependency history |
| reopen / start | `updateTaskColumnTx`: normal human column-only PATCH, completion clearing, destination tail, claim/policy/dependency checks and task.updated |
| move | `moveTaskTx`: normal Backlog/Ready move, captured exact column, append order and full move event |
| bug_resolve | `resolveBugTx`: fixed resolution, timestamps, checklist/dependencies, duplicate-link cleanup and bug.resolved |
| bug_reopen | `reopenBugTx`: required reason/comment, resolution/duplicate metadata clearing and bug.reopened; accepts captured Active destination |

## Module map and preservation

- `internal/httpapi/github_audit_apply.go`: explicit contract dispatch; legacy path.
- `internal/httpapi/github_audit_corrections.go`: bounded human preview/apply,
  source revalidation, per-card responses and receipt-first replay.
- `internal/httpapi/github_audit_correction_proof.go`: process-local HMAC proof.
- `internal/store/github_audit_correction_types.go`: public request/result types.
- `internal/store/github_audit_correction_guards.go`: read-only/local transaction
  inspection, exact context fingerprint, lifecycle blockers.
- `internal/store/github_audit_corrections.go`: transactional composition and
  savepoint rollback of a failed item before durable outcome recording.
- `internal/store/github_audit_correction_receipts.go`: authorized replay,
  logical-request fencing and immutable operational receipts.
- `internal/store/task_column_lifecycle.go`: shared column-only human lifecycle;
  other focused lifecycle modules expose transaction helpers to their public APIs.
- Migration 042 adds request/receipt tables and one-applied-finding uniqueness;
  it does not rewrite existing history or schema 041's assessment trigger.
- `openapi/schemas/github-audit-corrections.yaml` and apply path: exact API schema.
- `internal/httpapi/github_audit_correction_*test.go`: synthetic real HTTP,
  provider HTTP, worker/publication, SQLite lifecycle and receipt scenarios.
- `internal/db/audit_receipt_migration_test.go`: populated upgrade and retained SQL.

Test receipts, focused red/green logs, synthetic populated backup/migration
artifacts and the parent handoff index live in `/tmp/helm-tc309-handoff`.
No production database is upgraded by this task. TC-311 must obtain and verify
a current online beta backup before promotion and verify retained actual release
binaries against the candidate database; database restore is not rollback.
