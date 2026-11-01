# GitHub token prototype for Helm beta

Status: planned, 2026-10-26. Planning: TC-295, revised by TC-299.
Implementation: TC-296, TC-297, TC-291, TC-292, TC-300, TC-293, TC-298,
all unclaimed Backlog work.

## Outcome and boundary

On private beta, a person can attach a GitHub PR to a task, understand its
review/check state without crowding the card, run an audit inside Helm, and
inspect its evidence-backed findings. The design follows the Opus 5.5 concept
from TC-294.

First prototype uses one server-held fine-grained GitHub token and selected
repositories. Initial candidate: `KanterLabs/helm`. GitHub is the mirror;
Gitea remains canonical. A GitHub finding describes that repository and
commit, not an assumed state of Gitea or the deployed application.

User-confirmed direction: Run audit uses GitHub for code, PR and check
evidence, and Helm runs the analysis against task goals and acceptance
criteria. No developer checkout, local agent, copied request or manual
submission is required. The GitHub token grants repository access; Helm's
configured AI connection performs the analysis. Both must be ready.

GitHub App installation, webhooks, Gitea metadata synchronization, automatic
card-to-PR discovery, code execution, CI reruns, merges, deployment inference,
automatic task completion and file uploads are outside this first beta.
Generic artifact/upload work stays on TC-14; this is its PR-link slice.
TC-14's last recorded handoff is historical, not an integrated implementation.

## User experience

### 1. Connect and select repositories

Settings shows a small GitHub beta integration panel: configuration state,
selected repositories, last connection check, and Test connection. An admin
selects repositories from the server's allowed list for each Helm project.
The token is provisioned in beta runtime configuration; the UI never displays
or stores it. Show actionable Not configured, Connected, Permission required,
Unavailable and Rate limited states. Other task work remains usable when
GitHub is unavailable.

### 2. Link work

Task drawer has a Pull requests section with Link PR and Unlink. Pasting a URL
shows the repository and PR number immediately, then a Verifying state, then
verified title, state, reviews, checks and last refresh. Failed verification
keeps a clearly unverified saved link with Retry; it never invents metadata.
The same PR can serve several cards; a card can have several PRs.

Normalize URL variants and deduplicate by provider host, immutable repository
identity and PR number. Preserve drafts and task edits when a link request
fails or the drawer switches tasks. Unlink detaches only that task relation.
GitHub-only metadata is explicit; unsupported providers never receive the
GitHub credential or appear verified.

### 3. Keep the board readable

Show one quiet `#82` or `2 PRs` badge in a reserved metadata slot, outside the
main card button. It opens the drawer's PR section. Keep the current generous
column/card dimensions and title hierarchy; the miniature concept's narrow
four-column frame is not the production layout specification. Do not add a
stack of review/check pills to every card.

Accessible badge text describes the relevant state. Unknown, checking,
running, failed, passed and unavailable are distinct. Multiple PRs cannot
collapse to a false all-clear. Merged PRs can still carry failed checks;
keep both facts visible in details. Colors supplement text and icons.

### 4. Explain PR state in the drawer

Show repo, number, title, Draft/Open/Closed/Merged, current-head check summary,
review observations, last successful refresh, and Refresh. Open PR/Open checks
go to the actual host URL. Raw Actions logs are not imported in this version.

Say "Checks passed", not "Ready to merge", unless mergeability and applicable
rules were actually verified. An approval is a review observation, not proof
that every required reviewer approved. Show missing, skipped, cancelled and
incomplete evidence explicitly. Merged, deployed and task complete remain
separate; deployment is Not tracked by this integration.

### 5. Run and review an audit

Run audit opens a compact scope panel: selected repositories and branches,
linked PRs, task scope, and GitHub/AI connection readiness. Default to the
project's selected branch and linked PRs; make the scope visible before
starting. A person can audit a selected branch even without linked PRs.

Starting creates a durable job. Show Queued, Reading GitHub, Analyzing and
Checking findings only when those stages actually happen. Pin each source to
its exact commit, show measured coverage rather than a made-up percentage,
and let people leave the page and return. Provide Cancel while running and
Retry for failed/interrupted runs, with an explanation of partial evidence.
No terminal command or local-agent handoff appears in this flow.

Findings show task title, explanation, uncertainty, suggested action and
clickable PR/check/file evidence tied to exact commits. Fetch task identity
independently of the board's first page and filters. Preserve deep links,
browser Back, drafts and visible loading/error/retry states (coordinate with
TC-190). A changed PR head marks repository evidence stale even when the Helm
task version is unchanged.

Approval records a decision. A separate preview lists eligible Backlog/Ready
moves; explicit Apply reports Applied, Skipped or Needs follow-up per item.
Recheck task versions, source column, claims and evidence freshness. Starting,
blocking and completing tasks continue through their normal lifecycle.
The concept's automatic follow-up-card creation is deferred; this beta offers
Open task/normal creation with its evidence, not a new audit mutation type.
Missing remote evidence never proves that local work is absent or complete.
The [execution design](GITHUB_TOKEN_BETA_EXECUTION.md) defines durable jobs,
AI isolation, source selection and failure handling for TC-300.

## Credential and provider boundary

Proposed runtime settings: `HELM_GITHUB_ENABLED`, `HELM_GITHUB_TOKEN_FILE`,
and a bounded repository allowlist. Names become contractual in TC-297.
Use an owner-protected file in the existing beta configuration mount, readable
by service UID 65532. Keep the token only in the provider client; never put it
in SQLite, browser storage, portable archives, prompts, child-process env,
command arguments, logs or diagnostic payloads. Existing embedded agent
processes inherit environment, so an environment-valued PAT is unsuitable.

Use a short-expiry fine-grained token for the selected organization/repos:

| Repository permission | Use |
| --- | --- |
| Contents: read | Commit-pinned source evidence |
| Pull requests: read | PR identity, files and review observations |
| Checks: read | Check results associated with a commit |
| Commit statuses: read | Legacy CI status contexts |
| Metadata: read | Repository identity |

Organization approval may be required. Connection testing validates actual
endpoint access, not the token's spelling. No Actions-log, write, merge or
webhook permissions are needed. Obtain the credential through the secure
secret flow when implementing the connection, not during planning; use the
Infisical helper for any configured secret-store access.

GitHub API requests originate from the server to a fixed API origin. Validate
repository selections before fetching, restrict redirects, cap payloads and
pagination, and treat remote titles/bodies/check text as untrusted evidence.
Never fetch a user-supplied arbitrary URL with the token. Permission loss or
repository deselection immediately stops exposing cached private evidence;
cache reuse never grants project access.

## Storage, API and refresh design

Keep four responsibilities distinct:

1. Durable task artifacts: explicit PR relationships, canonical URL, provider,
   repository identity and PR number. Reuse this boundary for TC-14 later;
   do not repurpose internal `task_links`, which stores task-to-task relations.
2. Project repository configuration and a disposable current-metadata cache.
   Normal task reads use a batched summary; they never wait on GitHub.
3. Immutable audit evidence snapshots. Retain the bounded evidence actually
   cited by findings even after current cache entries change or links detach.
4. Durable audit jobs and attempts, separate from immutable findings and
   review decisions. A browser disconnect does not cancel server work.

Proposed API ownership, finalized with the OpenAPI changes:

| Surface | Behavior and authorization |
| --- | --- |
| `/integrations/github` | Human-admin sanitized diagnostics |
| `/projects/{project}/github` | Admin repository selection, version checked |
| `/tasks/{task}/artifacts` | Scoped list/add; PR subtype; versioned, idempotent writes |
| `/tasks/{task}/artifacts/{id}` | Scoped detach; preserve audit references |
| `/projects/{project}/github/refresh` | Admin bounded refresh, with counts/errors |
| Existing audit context/run routes | Scoped immutable evidence and drift state; preserve external submission compatibility |
| Explicit server audit start/cancel/retry surface | Authorized, idempotent job lifecycle; no task moves |

Task evidence requires `tasks:read` and the existing project ceiling. Link
mutations require `tasks:write`, the project ceiling, and existing claim /
concurrency rules. Admin credentials do not turn every project into a reader
of every selected GitHub repository.

Start with refresh on explicit action or stale drawer opening, plus one
bounded server scheduler for linked open PRs. Target a two-minute refresh
interval with jitter; use ETags, deduplication and backoff. This is a prototype
freshness target, not a promise of instant delivery. Bound concurrency and
work per cycle. Manual refresh joins an in-flight refresh. A provider outage
must not affect `/readyz`, task reads, or unrelated application features.

Record head/base SHA, merge SHA when present, upstream times, fetch time,
coverage/truncation and error category. Read the head again before committing
a combined result; a changed head invalidates mixed results. Check both check
runs and legacy statuses. Cap pagination with explicit partial coverage.
Retain stale successes after transient failures, but label them stale.

Use connection/config generation guards so disconnect, token rotation,
repository deselection or task deletion invalidates in-flight writes.
Background cache refreshes must not bump task versions or create activity
spam; explicit link/unlink actions do record activity and collection changes.
Audit evidence drift includes source commit and material review/check changes.
If freshness cannot be established, skip the evidence-dependent move.

The server worker receives task-scoped evidence packs from the GitHub client.
Binary, large, missing or inaccessible files are reported as incomplete
evidence. The model receives bounded content and verified citation IDs, never
the token, arbitrary filesystem access or repository execution capability.
Source files are evidence, not instructions. Keep provider and executor
interfaces separate so a later GitHub App can reuse the same audit records.
Existing external-agent submission remains compatible but is not part of the
new beta flow; no local helper changes are required for this prototype.

## Implementation order

| Card | Deliverable | Prerequisites |
| --- | --- | --- |
| TC-296 | Split touched oversized modules; preserve behavior/contracts | None |
| TC-297 | Token file, provider client, selected repos and connection UX | TC-296 |
| TC-291 | Durable PR artifacts, Link/Unlink, board badge and drawer | TC-296 |
| TC-292 | Verified PR/review/check summaries, cache and refresh UX | TC-291, TC-297 |
| TC-300 | Immutable evidence capture and durable server-run GitHub audits | TC-292 |
| TC-293 | Run/progress/cancel/retry, evidence review and guarded apply UX | TC-300 |
| TC-298 | Integrated validation and private beta release | TC-293 |

The link slice can be built with fake provider fixtures while the connector
is implemented. Keep public interfaces stable during extraction. Measured
oversized candidates include API route dispatch (69 KB), API/types modules
(50/37 KB), audit component (40 KB), portable store
(179 KB), and OpenAPI source/embedded contract (523/734 KB). Touching them
requires responsibility-based extraction first, not appending or minifying.
Author contract domain fragments and assemble the same public document; do
not evade the 20,000-byte rule with a generated/vendor label. Measure intended
files before editing and changed/untracked files before every handoff.

Use new focused GitHub client/config/sync modules, artifact and snapshot store
modules, and drawer/PR/audit components. Shared builds, migrations, lockfiles
and code generation run serially. No implementation card is claimed by this
planning workstream.

## Validation and release gate

[Validation and rollout](GITHUB_TOKEN_BETA_VALIDATION.md) defines the real API
end-to-end scenarios, secret-leak checks, populated migrations, portable
archive compatibility, verified backups, retained-image tests and private
Coolify release. All gates apply before TC-298 is complete.

## Source references

- [GitHub token capabilities and organization approval](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
- [Contents permission and commit references](https://docs.github.com/en/rest/repos/contents#get-repository-content)
- [PR review read permission](https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request)
- [Check runs at a Git reference](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference)
- [Legacy commit status permission](https://docs.github.com/en/rest/commits/statuses#get-the-combined-status-for-a-specific-reference)
- Existing contracts: [API](API_CONTRACT.md), [agent workflows](AGENT_WORKFLOWS.md).
- Opus concept and checks: TC-294; local review artifact
  `/tmp/helm-pr-audit-ux-opus/index.html` (synthetic, not the production app).
