# GitHub beta API

For activation, backups and the legacy-image rollback boundary, see
[GitHub audit beta operations](GITHUB_BETA_OPERATIONS.md).

Implemented beta routes below are relative to `/api/v1`. The existing
external-agent audit submission, listing and review endpoints remain available.
The [OpenAPI authoring entrypoint](../openapi.yaml) assembles these routes
with the existing contract; servers publish the complete `/openapi.json` document.

## Common rules

Existing authentication, project ceilings, CSRF origin checks, strong `"vN"`
ETags and idempotency apply. Linking requires both `tasks:write` and
`tasks:read` because the response includes private snapshot evidence. Unlinking
requires `tasks:write`. Both use captured task version and normal claim rules;
list/status requires `tasks:read`. Server
audit execution requires a signed-in human with their own AI connection.
Audit analysis never moves tasks. Errors use existing typed error envelopes.

Artifact collections use `{data: [...], next_cursor: "", task_version}`.
Summary and Apply responses use `{data: [...]}`. Empty arrays remain arrays.
Times are UTC RFC3339. Unknown metadata remains unknown; never fabricate
verified titles, check results or deployment state.

## Integration and selected repositories

- `GET /integrations/github`: human-admin diagnostics. Returns
  `{enabled, configured, state, message, allowed_repositories, checked_at}`.
  State: `not_configured|connected|permission_required|unavailable|rate_limited`.
  `allowed_repositories` contains `owner/name` strings from server config.
- `POST /integrations/github/check`: admin connection test, same response.
- `GET /projects/{project}/github`: scoped project repository selections,
  `{version, repositories: [{repository, ref}]}`. Version starts at 1 even
  before a saved selection; default empty. Read does not contact GitHub and
  returns the selection ETag.
- `PUT /projects/{project}/github`: admin + project scope, strong If-Match;
  body `{repositories: [{repository, ref}]}`, selected from server allowlist.
  Validated unique pairs, max 10 repositories. Returns updated selection.
  A selection change invalidates in-flight provider writes and old access.
  Selection writes support the standard optional `Idempotency-Key`.
- `GET /projects/{project}/github/readiness`: scoped human readiness,
  `{ready, github: {state, message}, ai: {state, message}, repositories}`.
  AI state `connected|not_connected|disabled|unavailable`. This endpoint
  does not run inference; an audit attempt still validates actual execution.

Runtime config: `HELM_GITHUB_ENABLED`, `HELM_GITHUB_TOKEN_FILE`,
`HELM_GITHUB_REPOSITORIES` (comma-separated owner/name allowlist). Credential
is read from protected file, never a raw-token environment variable. Fixed
production API origin `https://api.github.com`; fake clients are injected by
test code, not an unrestricted production origin setting.

## PR artifacts and cached summaries

- `GET /tasks/{task}/artifacts`: collection plus `task_version`; current task
  ETag. Each item is a PR artifact described below. No provider fetch.
- `POST /tasks/{task}/artifacts`: requires both `tasks:write` and `tasks:read`;
  body `{url}`, If-Match task ETag; dedupe
  canonical provider/repository/PR identity. Returns `{artifact, task_version}`.
  Returns `201`. A structurally valid allowed URL can persist while verification fails;
  show it unverified and support Refresh. Link records normal task activity.
- `DELETE /tasks/{task}/artifacts/{artifact}`: requires `tasks:write`;
  If-Match task ETag. Detaches
  this task only; returns `{task_version}`. Retain audit snapshots.
- `POST /tasks/{task}/artifacts/{artifact}/refresh`: refresh/join in-flight
  verification; requires `tasks:read` and project access, returns artifact
  with the current task ETag. Does not bump task version or emit activity.
- `GET /artifact-summaries?task_ids=<comma-separated immutable IDs>`: max 200,
  scoped/batched local query; returns `{data: [{task_id, count, label, state}]}`.
  Only permitted tasks; a forbidden requested task rejects the request. No
  per-card upstream calls. `label` is `#N` for one PR or `N PRs`. State is
  `unverified|pending|passing|failing|merged|closed|unavailable|unknown`.

PR artifact:

```ts
type PullRequestArtifact = {
  id: string; task_id: string; kind: 'pull_request'; provider: 'github';
  url: string; repository: string; number: number; created_at: string;
  verification: 'unverified' | 'verified' | 'unavailable';
  error_code?: string; error_message?: string;
  snapshot?: {
    repository_id: number; title: string; state: 'open' | 'closed' | 'merged';
    draft: boolean; head_sha: string; base_sha: string; merge_sha?: string;
    checks_state: 'unknown' | 'pending' | 'passing' | 'failing' | 'unavailable';
    checks: {name: string; state: string; conclusion: string; url: string; sha: string}[];
    review_state: 'approved' | 'changes_requested' | 'commented' | 'none' | 'unknown';
    fetched_at: string; stale: boolean; partial: boolean;
  };
};
```

Permission loss hides cached private snapshot contents. Failure beats pending,
which beats passing; never imply all clear from one successful PR. Merge,
checks and task state remain separate in the drawer. Open PR/Checks uses
validated actual URLs. Keep cache private, bounded, conditional and refreshed
at most every two minutes for linked open PRs, with jitter/backoff and a
fixed work budget. Explicit refresh can update sooner.

## Server audit jobs

- `POST /projects/{project}/github-audits`: idempotent start; body
  `{repositories?: [{repository, ref}], states?: string[], task_ids?: string[]}`.
  Default selected project repositories and backlog/ready/active tasks.
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
- `POST /audits/{audit}/retry`: explicit new run/fresh capture, linked to old
  run, same allowed scope subject to current authorization; returns
  `202 {audit,job}`. Requires a new bounded `Idempotency-Key`, a terminal
  previous job, and the requesting human's own AI connection.
- `GET /audits/{audit}/evidence`: scoped immutable manifest below. Source
  access is revalidated; unavailable evidence is a typed error, not stale
  private content. Capture remains retained even if access is revoked.
- `POST /audits/{audit}/apply`: body `{finding_ids: string[], preview: boolean}`.
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
