# Server-run GitHub audits for beta

Scope decision: TC-299. Implementation: TC-300; user flow/review: TC-293.
Companions: [plan](GITHUB_TOKEN_BETA_PLAN.md) and
[validation and rollout](GITHUB_TOKEN_BETA_VALIDATION.md).

## Contract

Run audit compares Helm task goals, acceptance criteria and captured task
state with the selected GitHub branch and linked PR evidence. Helm manages
the analysis. No developer checkout, local agent, terminal command, clone,
dependency installation or repository execution is needed. GitHub supplies
source evidence; it does not provide the AI analysis through the PAT.

The first beta uses the requesting human's configured AI connection. Check
GitHub access and AI readiness separately before enqueueing, and again before
execution. Honor the AI kill switch, actor/project ceilings and repository
selection. An unavailable connection has an actionable setup/retry state;
it does not leave a run pretending to be active. Do not silently borrow
another person's account. Pin the selected model and effort per attempt.

## Current code and required adapter

`internal/httpapi/audits.go` currently creates audit records without launching
a worker. `internal/store/audits.go` supplies immutable findings and review
guards. `cmd/helm/main.go` creates a server AI manager, but no durable audit
dispatcher. Add focused job, evidence and executor modules, with explicit
startup/shutdown ownership and additive storage.

`internal/codexruntime/runtime.go` already supports structured output and
best-effort turn interruption. Its current read-only sandbox is not proof
that commands are disabled or credential files are inaccessible. Child
processes inherit most environment variables, and a same-UID process could
read a PAT file. Do not directly reuse this execution profile for audits.

TC-300's first gate is a demonstrated evidence-only inference adapter: no
shell, arbitrary file reads, repository execution, MCP tools or arbitrary
network calls available to the model. The configured inference transport
may reach its model service; GitHub access belongs only to the provider
client. Reuse the actor connection and structured-output abstractions only
behind an execution profile whose tool and filesystem restrictions have
been verified on beta. If the current runtime cannot enforce this boundary,
stop that adapter's integration; a prompt saying "do not use tools" or a
read-only flag does not satisfy the gate.

Do not automatically reuse the draft feature's prompt/output history helpers
(`internal/httpapi/task_draft.go`, `luna_runs.go`). Private source evidence
belongs in the project-scoped snapshot store with revocation checks. Job
history contains safe phase/error metadata, not raw source or prompts.

## Evidence capture

1. Capture the chosen task IDs, versions, columns, goals and criteria. Record
   any scope limits explicitly; do not silently audit only the visible board
   page. A configured branch can be audited without any PR links.
2. Resolve each selected repository/ref and linked PR to immutable repository
   identity and exact commit SHAs. Capture base/head/merge identities and the
   time-sensitive review/check observations. Recheck changing heads before
   publishing a combined capture; never label mixed-SHA data consistent.
3. Fetch bounded tree/file content through the fixed GitHub API client. Use
   linked PR paths, explicit task paths and bounded tree/path relevance to
   select source, with configured byte/file/request/time limits. Do not fetch
   arbitrary URLs suggested by a task or model. Treat symlinks, submodules,
   binaries, inaccessible or oversized files as explicit omissions.
4. Freeze a manifest with task snapshots, commits, paths/blob hashes, line
   ranges, checks/reviews, fetch times, coverage and omissions. The model gets
   bounded content and opaque citation IDs; provider credentials never enter
   that input. Source text is untrusted evidence, never agent instructions.
5. Validate returned task IDs, verdicts, destinations and citation references
   against the manifest. Resolve citation URLs server-side from captured
   identities, not model-produced links. Missing coverage produces uncertainty
   or a partial result; it cannot establish that work is absent or complete.

## Durable jobs and publication

Use an additive `audit_jobs` boundary linked to the existing audit run, with
attempt identity, request fingerprint, stage, lease owner/expiry/fencing
token, heartbeat, cancellation, bounded error category, actor, model/effort
and manifest identity. Preserve existing audit status values from migration
010: queued/running/complete/partial/failed. Keep more specific job outcomes
(cancelled/interrupted) separate, with failed/partial legacy run projections;
do not rewrite that migration or expose unsupported statuses to old clients.

- **Start:** authorize a signed-in human; create run, job and captured task
  selection transactionally. An idempotency key is scoped to actor, project
  and request fingerprint. Double clicks/replayed requests reuse the run.
  Start with one active job per actor/project and a bounded global queue and
  worker concurrency. A conflicting scope returns the existing run clearly.
- **Work:** acquire a database compare-and-set lease. Use server lifetime,
  not the HTTP request, for execution. Persist real stages: Queued, Reading
  GitHub, Analyzing, Checking findings, then a terminal outcome. Include a
  deadline and coverage counters. Waiting on another AI turn stays queued.
- **Publish:** stage validated results, then atomically write findings and
  the terminal outcome under the current fencing token. Reject unauthorized
  legacy append/finalize calls for worker-owned runs. The existing external
  create/submit path remains unchanged for external runs. Do not trust the
  old per-task duplicate check to reconcile nondeterministic retry output.
- **Cancel:** durably revoke the attempt before interrupting inference.
  Late results cannot publish. A browser navigation alone does not cancel.
- **Restart:** recover queued jobs. Mark expired active attempts interrupted;
  do not silently repeat inference that may have completed remotely. No
  automatic paid/model retry in this beta. Explicit Retry creates a linked
  new run with a fresh snapshot; preserve the old evidence and decisions.
- **Shutdown/rollback:** stop new claims, interrupt/fence active attempts and
  settle worker-owned runs before an old binary starts. Test abrupt shutdown
  recovery as well as graceful draining. Retained binaries must keep ordinary
  task reads/writes working without discarding new rows. Imports of portable
  evidence/history never resume jobs or restore leases.

Recheck actor/project/repository authorization and connection generations
before fetching, inference and publication. Logout, disconnect, deselection
and revocation fence attempts when observed; retained private evidence is
not accessible through cached permissions. A check failure is a finding;
an unavailable provider or incomplete source capture is a coverage limit.

## Review and application

Only validated published findings enter review. Approval changes no tasks.
The separate Apply preview rechecks task version/column, active claims,
destination lifecycle, permissions, source commits and material check/review
drift. Unknown freshness skips the evidence-dependent move. Never equate
merged, deployed and task complete. Follow-ups use ordinary task creation.

TC-300 owns the worker, capture, executor and API contract. TC-293 owns scope,
readiness, progress/cancel/retry UI, citation rendering, review and guarded
apply integration. TC-298 proves the complete flow on beta, including data
preservation and the executor isolation gate. All future cards stay unclaimed
until implementation starts.

Before editing, run `check-file-sizes` for intended ownership. The current
audit store is 31,946 bytes and must be extracted by responsibility; the HTTP
audit module (16,890 bytes) and runtime (15,777 bytes) are near the extraction
threshold. Keep every maintained file at or below 20,000 bytes, including
tests and docs. Write failure scenarios and end-to-end coverage before code;
retain only synthetic, credential-free test evidence.
