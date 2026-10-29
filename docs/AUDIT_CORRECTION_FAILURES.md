# TC-309 failure inventory (before implementation)

The real HTTP/store journeys are written before implementation. Synthetic human
identity uses the existing audit HTTP fixture; bearer/agent identity is explicitly
rejected. Production human authorization is a TC-311 integration gate.

- Preview mutates task, finding approval/version, run, comments, events or preview
  rows; missing/forged/expired/cross-actor proofs authorize mutation.
- Completion loses completed_at/history, bypasses checklist or dependency policy,
  or releases a foreign claim. Reopening leaves completion or bug resolution set.
- Bug resolution silently defaults required input; bug reopening omits its reason.
- A semantic duplicate selects an arbitrary target instead of the captured ID.
  Changed task, criterion/checklist, policy, dependency, source/target column or
  mapping is silently rebased onto old approval.
- Relevant evidence is rejected because unrelated repository files were unread.
  Conversely unavailable/changed branch, PR/check, selection, provider/account or
  delegated authorization permits stale application.
- A later failed/conflicted item hides earlier committed outcomes. Concurrent
  retries duplicate comments/events. A lost response followed by source advance
  hides an existing durable receipt; changed request-key reuse rewrites authority.
- Legacy approval/Backlog/Ready APIs break or malformed v2 falls back to v1.
  Imported history acquires operational job/receipt authority.
- Additive migration damages populated history or retained-binary read/write and
  readiness compatibility. Receipts leak into portable archives.

Expected evidence: sanitized per-item HTTP receipts, preview before/after
snapshots, focused red/green logs, populated migration integrity and rollback
compatibility observations under `/tmp/helm-tc309-handoff`.
