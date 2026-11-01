# Server GitHub audit failure cases

Written before implementation. The real HTTP workflow uses a synthetic provider
and AI account, retaining only a synthetic captured-response artifact.

- Request navigation must not cancel execution; duplicate keys reuse one run.
  Changed bodies with the same key conflict. Another active request returns its ID.
- Capture includes tasks beyond page 50 and freezes IDs, versions, columns,
  descriptions and checklist criteria. Bounded truncation is explicit.
- A branch with no linked PR works. Missing/binary/large source, truncated trees,
  unavailable checks and changing heads produce omissions, never an all-clear.
- A queued restart resumes. An expired fetching/analyzing attempt is interrupted;
  retry is explicit and creates a fresh linked run. Late/fenced results cannot publish.
- Cancellation fences before interruption. Deadline/shutdown outcomes remain
  distinct in jobs and project into existing failed/partial audit statuses.
- Disabled actor, deleted project, deselected repository or disconnected account
  prevents fetch, inference and publication. Evidence access rechecks current access.
- Malformed output, invented tasks/citations, duplicate tasks or missing findings
  cannot publish a complete audit. Findings and terminal state commit together.
- External append/finalize cannot mutate worker-owned runs; external runs retain
  compatibility. Raw prompts/output never enter general model history or job messages.
- Review does not move tasks. Apply requires a prior preview, approval, unchanged
  task/version/source, no claim, Backlog/Ready destination and fresh source/checks.
  Unknown freshness skips. Repeating an applied move never moves twice.
- Additive migration preserves populated tasks and old SQL writes. Operational
  leases stay local and are never portable executable state.

## Retained binary database guards

Before adding rollback triggers: an old binary does not know worker ownership.
It could append findings to a queued job or finalize its legacy run after a
rollback. Preserve queued resume while rejecting those old SQL writes. A worker
finding insert requires the validating stage and a live lease; terminal run
updates require an already terminal job. Publication and cancellation first
settle the job in the same transaction. Normal external audit rows have no job
and continue to accept ordinary legacy writes. Imported provenance must be
inserted after retained findings and then make retained history immutable.

These guards protect a stopped-worker rollback. They are not an authorization
boundary against an administrator writing arbitrary SQL or an old server running
concurrently with a live validating worker; only the new service enforces actor,
project and fencing checks. Deployment must stop the worker before rollback.

Task placement evidence includes captured column semantic state and readable
name, not only its opaque ID. A proposed move to the captured semantic state
is rejected. Historical imported manifests may omit these optional display
fields; imports remain ineligible for Apply.

Source reads must account for a Contents-success/PR-or-check-permission-failure
split. Observed provider denial stops cached reads. Captured PR/check evidence
requires a fresh successful PR/check/review authorization probe; unknown or
partial permission coverage cannot expose cached private details.
