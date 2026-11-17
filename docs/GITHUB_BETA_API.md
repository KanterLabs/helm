# GitHub beta API

> Retired in TC-391: this is historical documentation. The GitHub product
> integration and Branches view are removed. See [removal and preservation](GITHUB_REMOVAL.md).

For activation, backups and the legacy-image rollback boundary, see
[GitHub audit beta operations](GITHUB_BETA_OPERATIONS.md). Device-flow
sign-in, its `connection` object and the connect, cancel and disconnect
routes are described in [GitHub sign-in](GITHUB_DEVICE_FLOW.md#api).

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
audit execution uses either a signed-in human's own AI connection or an explicit,
short-lived authorization from a human administrator for an exact agent token.
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
- `GET /projects/{project}/github/readiness`: scoped readiness,
  `{ready, github: {state, message}, ai: {state, message}, repositories}`.
  AI state `connected|not_connected|disabled|unavailable`. This endpoint
  does not run inference; an audit attempt still validates actual execution.
  Agents need `tasks:read` and `?authorization_id=<grant-id>` matching their
  exact agent/token and project. Readiness uses the granting human account;
  humans omit this parameter and check their own account.
- `GET /projects/{project}/github/branches`: requires `tasks:read` and
  project access; optional `?repository=owner/name` must be one the project
  selected and defaults to the first. Contacts GitHub and stores nothing.
  Returns `{state, message, repository, repositories, base, staging, branches,
  partial, fetched_at}`. `base` is `main`, otherwise the default branch:
  `{name, sha, committed_at?}`. `staging` is the `beta` branch measured
  against `base`, or `null` without one. Each branch is `{name, sha,
  committed_at?, ahead, behind, compared, in_staging?}`: `ahead` counts its
  commits missing from `base`, `behind` counts `base` commits it lacks.
  Counts mean nothing unless `compared` is true, so never show an uncompared
  branch as merged. `in_staging` appears only after a successful comparison
  with `staging`. State is `ready|no_repository|not_configured|
  permission_required|repository_not_allowed|rate_limited|unavailable`; any
  state but `ready` carries no branch data. A report compares at most 300
  branches and starts at most 160 new comparisons, so `partial` means repeat
  the request to continue. Finished comparisons are kept in memory by commit
  pair and dropped with every other private cache on permission loss.

Runtime config: `HELM_GITHUB_ENABLED`, `HELM_GITHUB_TOKEN_FILE`,
`HELM_GITHUB_REPOSITORIES` (comma-separated owner/name allowlist). Credential
is read from protected file, never a raw-token environment variable. Without
a token file, sign-in supplies the credential and the allowlist
([GitHub sign-in](GITHUB_DEVICE_FLOW.md#settings)). Fixed
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

## Agent audit authorizations

Historical contract: [audit and portable reference](GITHUB_AUDIT_API_HISTORY.md#agent-audit-authorizations).

## Server audit jobs

Historical contract: [audit and portable reference](GITHUB_AUDIT_API_HISTORY.md#server-audit-jobs).

## Portable v3 retained history

Historical contract: [audit and portable reference](GITHUB_AUDIT_API_HISTORY.md#portable-v3-retained-history).
