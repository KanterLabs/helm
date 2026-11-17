# GitHub audit API history

> Retired in TC-391: these are historical contracts. See [feature removal](GITHUB_REMOVAL.md).

## Agent audit authorizations

A human administrator with project access can authorize bounded audit inference
using their own connected AI account. Ordinary project read access alone does
not authorize AI spending. See [agent audit setup](GITHUB_AGENT_AUDITS.md) for
owner-authorized operator provisioning with `helm audit-access`.

- `POST /projects/{project}/github-audit-authorizations`: human-admin only;
  body `{agent_id, token_id, expires_at, max_tasks?}`. Returns `201` with the
  grant. `expires_at` must be a future RFC3339 time within 24 hours.
  `max_tasks` defaults to 10 when omitted or zero; otherwise it is 1–100.
  The human owner is inferred from authentication and cannot be supplied.
  Supports the standard optional `Idempotency-Key`.
- `GET /projects/{project}/github-audit-authorizations`: human administrators
  see all project grants. Agents need `tasks:read` and see only grants bound
  to their exact actor/token and project, including expired/revoked records.
  Returns `{data: [...]}`.
- `DELETE /projects/{project}/github-audit-authorizations/{authorization}`:
  only the granting human administrator may revoke. Returns the grant;
  repeated revocation keeps the original timestamp. Revocation fences active
  jobs before interrupting inference and preserves retained evidence. Each
  request revalidates access rather than replaying a cached mutation response.

Grant fields are `id`, `project_id`, `agent_id`, `token_id`,
`account_owner_id`, `selection_version`, `max_tasks`, `created_at`,
`expires_at` and optional `revoked_at`. Token IDs are public identity metadata,
never bearer secrets. Creation requires an enabled agent/token with
`tasks:read` and project access, plus selected repositories. Changing the
selection invalidates existing grants. Expiry, revocation, disabled actors,
expired/deleted tokens and lost project/source/account access fail closed;
execution rechecks authorization before publishing results.

## Server audit jobs

- `POST /projects/{project}/github-audits`: idempotent start; body
  `{repositories?: [{repository, ref}], states?: string[], task_ids?: string[], authorization_id?: string}`.
  Human starts default to selected project repositories and backlog/ready/active
  tasks. Agents require `tasks:read`, a matching unexpired `authorization_id`,
  and explicit nonempty `task_ids` within the grant limit; they cannot select
  states. Humans must omit `authorization_id`.
  Repository overrides must remain selected/allowed. Task IDs and states are
  alternative scopes; capture all matching IDs up to a visible bounded limit.
  Scope captures at most 1,000 tasks; excess state-selected tasks appear as
  omissions. Captured task JSON is limited to 192 KiB, and an empty scope is
  rejected. `task_ids` allows at most 1,000 unique IDs; `states` at most five.
  Returns `202 {audit, job}`. A required non-blank `Idempotency-Key` of at
  most 200 characters identifies the request. Same request key returns the same
  run; one active
  actor/project job is allowed. A different start while active returns typed
  conflict with the authorized existing audit ID.
- `GET /audits/{audit}/job`: returns job below; `404` for an external run.
- `POST /audits/{audit}/cancel`: idempotent cancellation by originating human
  or project-authorized admin; fence attempt before interrupt; returns job.
  An agent with project access and `tasks:read` may cancel only a job started
  by that exact token, including after its grant expires or is revoked.
- `POST /audits/{audit}/retry`: explicit new run/fresh capture, linked to old
  run, same allowed scope subject to current authorization; returns
  `202 {audit,job}`. Requires a new bounded `Idempotency-Key`, a terminal
  previous job, and the requesting human's own AI connection. Retry is human
  only, including for an agent-started job; it clears the old agent grant.
- `GET /audits/{audit}/evidence`: scoped immutable manifest below. Source
  access is revalidated; unavailable evidence is a typed error, not stale
  private content. Capture remains retained even if access is revoked.
- `POST /audits/{audit}/apply`: body `{finding_ids: string[], preview: boolean}`.
  Apply and preview are human only; an agent grant does not authorize either.
  Returns `{data: [{finding_id, task_id, outcome, message, task?}]}`; outcomes
  `eligible|applied|skipped|needs_follow_up`. Preview has no task mutations.
  Both `finding_ids` (1–200 unique IDs) and an explicit boolean `preview`
  are required. Recheck approval, task version/column, claim, safe destination
  and source
  freshness even for non-preview; each eligible move has deterministic
  idempotency. HTTP idempotency responses are not cached for this endpoint;
  every preview and apply rechecks freshness. Lifecycle destinations remain
  normal task actions.

```ts
type GithubAuditJob = {
  audit_id: string; project_id: string;
  requested_by: string; account_owner_id: string; authorization_id?: string;
  stage: 'queued' | 'fetching' | 'analyzing' | 'validating' | 'finished';
  outcome: '' | 'complete' | 'partial' | 'failed' | 'cancelled' | 'interrupted';
  created_at: string; updated_at: string; finished_at?: string;
  task_count: number; captured_files: number; omitted_files: number;
  message: string; error_code?: string; retry_of?: string;
};
type GithubAuditEvidence = {
  audit_id: string; captured_at: string; partial: boolean; omissions: string[];
  repositories: {repository: string; ref: string; sha: string; repository_id: number}[];
  items: {id: string; kind: 'file' | 'pull_request' | 'check'; title: string;
    url: string; repository: string; sha: string; path?: string;
    blob_sha?: string; start_line?: number; end_line?: number; text: string}[];
  tasks: {id: string; key: string; title: string; description: string;
    version: number; column_id: string; criteria: string[]}[];
};
```

Findings use existing audit finding fields and `evidence_refs` containing
manifest item IDs in the existing safe reference namespace. Return captured
task identity independently of current board pagination. New review UI loads
findings through existing detail/collection clients, job and evidence through
these new endpoints. Existing external run listing and review remain available.
Reading or reviewing findings from a protected GitHub audit additionally
revalidates current source access; unavailable access returns `409
evidence_unavailable` before private reasons or review responses are exposed.

## Portable v3 retained history

`GET /export` and `GET /projects/{project}/export` retain their default v2
response. Explicitly request `?version=3` to include durable GitHub history;
`?version=2` selects the compatibility format. Other export versions, repeated
version parameters and invalid integer values return `400`. The
`X-Helm-Portable-Version` header reports the selected version. Export still
requires `projects:read`, `tasks:read` and `events:read`, plus the normal
project ceiling. If any retained manifest or protected finding has unavailable
source access, the whole v3 export returns `409 evidence_unavailable`; no
partial private archive is returned.

A v3 archive retains the existing portable fields and adds `github`:

- `artifacts`: canonical PR identities with project, repository, PR number,
  URL, creation time, and immutable repository ID when known.
- `task_artifacts`: durable task/PR relationships and creation provenance.
- `audit_runs` and `audit_findings`: retained run metadata, review decisions,
  captured versions, evidence references and reasons.
- `audit_evidence`: immutable evidence manifests, including captured tasks.

Version 3 does not export provider credentials, project repository selections,
disposable caches, executable jobs, or apply receipts. Import accepts v1, v2
and v3. A `github` payload requires version 3. Core and retained-history
references are validated and remapped in one additive transaction; dry-run
returns a report without writing. Imported history does not authorize source
access, resume a job, or make imported findings eligible for old Apply receipts.
Equal records are skipped; incompatible stable-ID conflicts follow the chosen
`remap` or `fail` policy. Existing v1 release normalization remains unchanged.

The store caps each v3 collection and validates canonical GitHub identities,
project/task ownership, timestamps, evidence references and retained manifest
bounds. Evidence manifests are limited to 8 MiB. Private history remains
subject to source authorization; revoking a repository does not delete its
retained evidence.
