# Agent token management UX plan

Status: implemented and verified (2026-11-01).

Planning record, 2026-11-01. Tracking: TC-345. Scope: Helm-issued agent
credentials in Settings, based on the supplied screenshot and this checkout.
No application changes or credential operations are part of this planning task.

## Outcome

Make Settings answer: which agent has access, to which projects, with which
permissions, and until when? Creating a token should finish with a clear
one-time handoff and practical storage instructions. Keep Helm's purple accent,
existing typography, light and dark themes, and familiar dialog interactions.

Existing credential-lifecycle work remains on TC-154. This plan supplies bounded
UI slices and a manual replacement experience; it does not create a second
automatic rotation system. Agent metadata management overlaps TC-8 and guided
first-agent setup overlaps TC-176; keep those broader outcomes on their cards.

## Verified foundation

| Area | Current behavior | Design consequence |
| --- | --- | --- |
| Inventory | Every agent receives a card, even with no tokens; scope strings occupy most token rows. | Use a compact searchable token inventory and a separate agent directory. |
| Expiry | Creation API accepts a future RFC3339 `expires_at`; the form does not expose it. | Add expiry controls without a schema change. |
| Usage | Responses include `created_at` and `last_used_at`; usage writes are throttled for five minutes. | Show approximate last use and exact timestamps on demand; do not imply live connection health. |
| Status | Token lists include expired rows; revocation deletes the token row and emits an event. | Derive expiry accurately; do not invent a persistent Revoked inventory or historical counts. |
| Projects | Empty token project IDs inherit the actor ceiling; an unrestricted actor makes this workspace-wide. Explicit token IDs intersect the actor ceiling. | Require an explicit Selected projects or All agent projects choice and explain future coverage. |
| Permissions | Form initially enables project read/write and task read/write; six options omit the two supported notification scopes. | Use documented presets and expose all eight supported scopes in Advanced. |
| Generation | Issuance returns plaintext once with `Cache-Control: no-store` and rejects idempotency keys. | Never auto-retry an ambiguous issuance response. Refresh metadata and help retire an orphan before retrying. |
| Storage | Tokens are random bearer secrets with only a SHA-256 hash persisted. | Metadata remains visible; secret recovery is impossible. Do not add a vault of recoverable secrets. |
| Handoff | Reveal already supports copy and manual selection; closing clears its state. | Improve completion and recovery while retaining this boundary. |
| Revocation | A named accessible confirmation already exists. | Replace the ambiguous cross icon with a labeled action and retain confirmation. |
| Sources | Alert intake creates agent-kind attribution actors with a deterministic intake ID. The UI offers them token creation too. | Classify source identities using server-owned provenance, not name, description, or lack of tokens. |

Evidence: `web/src/App.svelte` (`scopeOptions`, `loadAgents`, `createToken`,
`deleteToken`, `copyRevealedToken`, settings markup and reveal dialog),
`web/src/lib/api.ts` (agent/token client), `web/src/lib/types.ts` (`ApiToken`),
`internal/httpapi/agents.go`, `internal/store/auth.go`, and
`internal/store/alert_intake.go` (`intakeActorID`, `IngestAlerts`). Existing
revocation accessibility coverage is in `web/e2e/board-a11y.spec.ts`.

## Proposed screens and interactions

### Inventory

Keep the Agents and tokens heading and Add agent action. Replace the large
explanatory aside with a small inline help disclosure, giving the inventory
the available width. Use compact agent headers with name, description, project
ceiling and an explicitly named New token action. A true automation agent with
no tokens gets one short row, not a tall empty card.

Show search, status and project filters above the collection. Search covers
agent and token names; filtering must not hide that an agent has additional
tokens. Start with All current tokens, including expired tokens, so the
screenshot's stale credentials remain visible. Offer Active, Expiring soon,
Expired and Never used filters. Never used is a usage condition independent
of expiry. Use a count summary, not decorative dashboard cards.

| Desktop column | Presentation |
| --- | --- |
| Token | Name; created timestamp and abbreviated immutable token ID distinguish duplicates. The ID is metadata, not a secret suffix. |
| Status | Active, Expiring soon within 14 days, or Expired; text plus icon, not color alone. Disabled agent is a separate access condition. |
| Access | Read only, Task worker, Maintainer or Custom; expandable exact scope names. Derive preset labels by exact set equality. |
| Projects | Named project chips and a disclosure for additional projects, or All agent projects with its effective ceiling. |
| Last used | Never or approximate relative time, with exact recorded time available; explain the five-minute update interval. |
| Expiry | Relative date plus exact timestamp on demand, or an explicit No expiry label. |
| Actions | Labeled accessible menu with Replace and Revoke. No bare cross icon. |

Default sort: expiring soon first, then active, then expired; stable tie-break
by creation time and token ID. Sorting is presentation only. Invalid dates
show Unknown rather than Active. Count every fetched page, and distinguish
load failure from an empty collection. Keep filters visible after no matches.

Below the automation collection, place a collapsed Alert sources section with
compact attribution rows and links to the existing integration management
destination. Resolve the actual route during implementation; do not invent
one from the screenshot. No token issuance action belongs here. Server-owned
classification must also reject new issuance for these identities. Preserve
any existing source tokens and expose them as a needs-review exception with
explicit revocation; do not hide usable credentials or revoke them silently.
Do not claim a source cannot authenticate while such credentials still exist.

At narrow widths, stack each token into name/status, access/projects and
usage/expiry lines. Keep a 44px action target and no horizontal scroll at
320px. Preserve readable text and contrast in both themes.

### Generate a token

Open New token for [agent] as one scrollable dialog, becoming a full-screen
sheet on a phone. Keep four sections and a concise review summary above the
Generate token action; avoid a multi-page wizard for this amount of input.

1. Name: ask where the token will be used, such as Codex on work laptop.
   Warn about duplicate names without prohibiting them.
2. Access: show Read only, Task worker, Maintainer and Custom presets.
3. Projects: explicitly choose Selected projects or All agent projects.
4. Expiry: choose 7, 30 or 90 days, one year, a custom date, or No expiry.

| Preset | Exact scopes |
| --- | --- |
| Read only | `projects:read`, `tasks:read` |
| Task worker, default | `projects:read`, `tasks:read`, `tasks:write`, `tasks:claim`, `events:read` |
| Maintainer | Task worker plus `projects:write` |
| Custom | Any nonempty supported set; show all eight scopes grouped by resource, including both notification scopes. |

Explain that Task worker can change tasks and claim work. It deliberately omits
project modification; task-workflow acceptance must verify that intended agent
operations work with this set. Custom changes switch the preset to Custom.
Do not silently add read privileges when someone selects a write scope; keep
the API's ability to express exact grants and explain dependencies instead.

Default to Selected projects with no implicit global grant. Preselect a project
only when the creation action carries an explicit project context. Require
at least one selected project. Show only projects in the actor ceiling, and
reject outside-ceiling projects server-side instead of issuing a zero-access
token. All agent projects explains that the grant follows the agent ceiling;
when that ceiling is unrestricted it includes future workspace projects.
Show stored restriction and effective access separately for legacy tokens.

Default new tokens to 90 days; this is a proposed product default, not a
server-enforced policy or a change to existing tokens. Show the resolved exact
expiry and timezone before submission. Duration choices use an explicit
creation-time instant; custom dates use the displayed local end of day and
convert to RFC3339. Validate past dates and daylight-saving boundaries.
No expiry remains available with clear persistent explanatory text.

Example summary: Task worker can read and update tasks, claim work and read
events in Test and helm-beta until the displayed expiry. Creation stays
disabled until valid and while submitting. Reset all draft grants when
switching to another agent; prevent stale async responses from moving the
secret to another identity or session.

If the POST response is lost, do not automatically retry. Refresh token
metadata and explain that issuance may have succeeded. Identify candidates by
agent, creation interval, name and token ID, without assuming a name is unique.
Offer explicit retirement of the inaccessible candidate and fresh issuance.
If the refresh also fails, preserve the uncertainty until it can be resolved.
A successful issuance followed by list-refresh failure must retain the reveal
and report only the refresh problem; it is not a failed creation.

### One time handoff and storage guidance

The reveal dialog owns plaintext only in memory. Show the agent, token name,
projects and expiry next to a selectable read-only token field. Copy announces
Copied through a live region. On clipboard failure, keep the value selectable
and show platform-appropriate manual copy instructions. Never gate manual use
on successful browser clipboard access.

Use Done after a successful copy or explicit acknowledgement that the token
has been saved. Before then, Escape or another intentional close requests a
Close without saving confirmation; backdrop clicks do nothing. Always offer a
deliberate discard path. After copying, Done closes without claiming the token
is installed or tested. Restore focus to the exact initiating agent action.

Provide concise setup disclosures for Helm skill, Environment and CI. Follow
the current `skills/helm/references/authentication.md` contract: canonical
`~/.config/helm/credentials.json`, owner-only mode 0600, `base_url` and `token`,
or `HELM_URL` and `HELM_TOKEN` supplied by the user's secret manager. Confirm the
authorized deployment origin rather than trusting a forwarded host value.
Use placeholders in examples; do not embed plaintext in downloadable config,
commands, URLs or shell history. Offer the existing `auth-check` as a
user-run verification step. One token per installation makes revocation clear.

Clear plaintext on completion, explicit discard, logout and component teardown.
Guard in-app navigation while unsaved, use best-effort browser unload
protection and block PWA activation while generation/reveal is open. There is
no browser persistence or recovery after a reload or crash. Metadata lists,
logs, traces, exports and background caches must never receive plaintext.

### Revocation and manual replacement

Retain the existing named confirmation and immediate API revocation. Show
agent, token ID, project access and last use; initial focus belongs on Cancel.
A recently used token receives a clear warning that its client may fail
immediately. The destructive button reads Revoke token. Reuse this terminology
for expired credentials rather than making deletion seem recoverable.

Replace opens generation prefilled with the old stored project restriction
and exact scopes, a distinguishable name and a fresh expiry. Recheck the
current ceiling and show changed effective access; never silently widen it.
Generate and save the replacement, update the client, run a user-directed
verification, then explicitly revoke the old token in its own confirmation.
Offer Keep both for now. Copy success alone never triggers old-token revocation.
Existing API calls support this manual flow. Persistent replacement pairing,
scheduled retirement, secret-sink delivery and automated rotation stay on
TC-154; do not invent a session-only Replaced status that disappears on reload.

## Delivery sequence

Opus 5.5 supplied the UI review. The accepted design above keeps its compact
inventory, presets, expiry, guarded reveal and manual replacement. Parent
review retained exact Custom grants, avoided a stored secret hint and made
source classification a server responsibility. The file-size tool is installed
on PATH and was run; it need not be present in the repository.

1. Bounded frontend extraction must pass first. It is larger than moving the
   token section alone because original touched entry points must comply too.
2. Inventory and source classification, including server issuance denial and
   preserved existing-source-token visibility.
3. Generation form and outside-ceiling validation, using existing expiry fields.
4. Reveal and storage guidance with navigation/PWA protection. Define its
   transient result interface alongside generation so successful issuance
   remains usable throughout integration.
5. Usage-aware revocation and manual replacement, building on generation and
   reveal. Keep automated lifecycle work on TC-154.

The first deployable UI milestone includes inventory, generation and reveal
together after extraction. Manual replacement can follow. Do not ship a new
creation path without the guarded reveal. Search and metadata use existing
list responses; source classification and outside-ceiling rejection are
additive backend behavior changes. No token suffix, recoverable secret store,
durable revoked-token state or replacement-link migration is needed.

| Card | Implementation outcome | Prerequisites |
| --- | --- | --- |
| TC-346 | Bounded frontend modules, preserving existing behavior | None |
| TC-347 | Compact inventory and server-classified alert sources | TC-346 |
| TC-348 | Explicit permissions, project access, expiry and issuance recovery | TC-346 |
| TC-349 | Guarded reveal, manual copy and storage guidance | TC-346 |
| TC-350 | Usage-aware revoke and safe manual replacement | TC-347, TC-348, TC-349 |

TC-346 through TC-350 are implemented and verified. Generation and reveal
share the transient result contract, and their release is coupled.
TC-154 remains the broader credential-lifecycle record.

## Implementation and validation

See [the implementation record](AGENT_TOKEN_UX_IMPLEMENTATION.md) for shipped
behavior and passing evidence.

See [Agent token UX validation](AGENT_TOKEN_UX_VALIDATION.md) for file-size
extraction, data preservation, failure scenarios and end-to-end acceptance.
