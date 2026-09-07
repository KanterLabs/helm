# Canonical Helm hosting and identity roadmap

Planning date: 2026-09-07. Planning task: TC-146.
Status: implementation requested by the owner; TC-147 profile foundation is
committed and locally verified (`af0b1e5`). TC-148 runtime/PWA safeguards are
committed (`6b80df5`); deployment transition validation passes locally. Production
remains in the versioned `legacy` phase. No change has been deployed yet.
Production cutover remains gated on the rehearsal and go/no-go checks below.

Local verification for `6b80df5`: full Go tests and vet, race tests for auth,
configuration and HTTP handlers, 181 frontend tests, zero type-check
diagnostics, production frontend build, six PWA/column browser regressions,
and a mobile migration-notice browser test passed. The latter uses a simulated
HTTPS origin, not live Cloudflare or a physical Home Screen installation.
No schema, database, DNS, Access policy or production runtime changes were made.
The deployment profile, dual-host predicates, bundle-origin gate and deployment
security suite also pass. The sqlite3/system-user backup/restore checks remain
explicit local skips. A same-store runtime rollback test preserves writes and
identifiers, but does not replace retained-binary or provider rollback rehearsal.
Provider before-state output is a sanitized digest/summary, **not** a complete
restore artifact. TC-149 must capture the complete protected provider state,
verify live Access/TLS before DNS publication, and replace the preparatory
CI/publisher guards only after phase-aware rollback is rehearsed.

## Recommendation and scope

Move this instance from `tc.shanekanterman.dev` to
`helm.shanekanterman.dev` on its **existing host and database first**. Ship
portable identity improvements separately. Do not combine the cutover with a
server move, database engine change, project-key rename, authentication-provider
replacement, or mass credential rotation.

Product direction: an operator-owned self-hosted project-management instance,
with local authentication, explicit project permissions, scoped agent access,
and optional OIDC and Cloudflare integrations. This is not a proposal for a
shared multi-tenant SaaS or for moving every customer onto our infrastructure.

Keep project keys/slugs, task IDs/numbers, actor IDs, claims, comments,
dependencies, attachments if present, and historical attribution unchanged.
The hostname is independent of the TC project key. Preserve database paths,
volume names, guest/tunnel identities, and legacy configuration aliases unless
a later separately reviewed migration requires otherwise.

## Evidence and current limitations

- Production's authenticated API reported revision
  `3fbec087bd255cde1b4ff130f4e1f7cec9e5cb43` during planning.
- A read-only public probe found `tc.shanekanterman.dev` resolving with HTTPS
  returning an Access redirect; the proposed hostname returned DNS ENOTFOUND.
  These are observations from this environment, not a Cloudflare inventory or
  proof of readiness to publish the new hostname.
- `README.md` documents Access -> Tunnel -> Debian 12 CT 103 ->
  `helm.service` on loopback -> `/var/lib/roadmap/data/roadmap.db`.
  Beta has a separate guest, database, credentials, and deployment environment.
- `deploy/cloudflare.sh`, `deploy/validate-live.sh`, workflow metadata and
  deployment tests contain the current hostname. The reconciler expects a
  particular route topology: adding DNS manually is not a supported migration.
- `internal/httpapi/server.go` permits browser mutations only from the exact
  configured public origin. `internal/auth/auth.go` uses host-only local
  session cookies; Cloudflare identity is verified independently.
- Human `CanProject` currently permits all projects; token scope/project
  checks already exist. Inviting a team before adding human permissions would
  not produce private project boundaries.
- Existing auth modes are local, Cloudflare, and development-only disabled.
  Generic OIDC and the identity capabilities below are proposed, not shipped.
- Existing per-actor Codex state must stay attached to the same actor; a
  domain or login-provider change must not create replacement user accounts.

Relevant implementation evidence: `internal/config/config.go`,
`internal/auth/auth.go`, `internal/httpapi/agents.go`,
`internal/httpapi/codex_account.go`, `deploy/install-inside-lxc.sh`,
`deploy/helm-deploy-gateway`, `.github/workflows/ci.yml`, and `docs/PWA.md`.

## Non-negotiable release gates

1. One authoritative live database and one canonical browser-write origin.
   Never deploy a second writable copy for hostname validation.
2. Capture exact resource IDs and versioned before-state, not only display
   names. Reject unknown hosts, unexpected Access applications and policy
   widening. Do not use wildcard CORS, parent-domain session cookies, generic
   proxy identity headers, or arbitrary cross-origin credential forwarding.
3. Keep human UI access and machine API access separate. A Cloudflare service
   credential passing the edge does not replace Helm authorization by itself.
4. Require populated-data migration tests for schema work, a verified online
   pre-upgrade backup (including actor-isolated Codex state), and a restore
   drill in an isolated environment. Preserve
   row counts, relationships, stable IDs and concurrent writes. Copying a live
   SQLite file without its supported backup procedure is not a backup.
5. Roll back runtime/configuration while retaining the live database. No
   automatic restore, reset, import-overwrite, or replay of uncertain writes.
6. A retained binary must remain compatible with both schema **and security
   semantics**. A pre-RBAC binary that exposes all projects to all humans is
   unsafe after restricted membership has been enabled, even if it can read
   the new schema. Maintain a compatible secure rollback release first.
   Extend the existing restore credential-revocation overlay to new identity
   tables; a disaster-recovery restore must not silently revive old sessions
   or revoked credentials. A restore remains a separate authorized operation.
7. Every stage ends with recorded go/no-go evidence. Implementation and the
   production cutover require a separate execution decision from the owner.

## Track A: canonical hostname

### A1 — Inventory and parameterize (TC-147)

Create a validated per-environment deployment profile. Keep the current
hostname as the preparatory release's default. Make rendering, Access/Tunnel
reconciliation, bundle validation, rollback, health checks and CI environment
links consume the same profile. This is not permission to accept arbitrary
provider resources or loosen the existing deployment identity.

Inventory DNS records and proxy mode, certificate coverage, UI/API application
IDs and audiences, issuer, policy IDs, Tunnel ingress, current release,
database/backup locations, and rollback configuration. Record references to
secrets, never their values. Inventory clients: installed skill URLs and
canonical/legacy environment aliases, agents, CI probes, integrations,
monitoring, bookmarks and installed PWAs. Both aliases must agree if set.

Acceptance: deterministic profile rendering; wrong-host, wrong-environment,
duplicate-resource and policy-drift failures; unchanged beta behavior;
current production still works after the preparatory release. Jobs created or
modified use `homelab` or `homelab-heavy` according to workload.

### A2 — Implement and rehearse transition behavior (TC-148)

Use disposable beta/test names with isolated populated fixtures. First deploy
code capable of representing both approved hosts while retaining exactly one
canonical browser origin. Stage new Access/TLS/Tunnel routing before making
the new hostname discoverable. New-host browser writes need not work before
promotion; document and test that state rather than weakening CSRF to hide it.

| Request | Before promotion | During compatibility window |
| --- | --- | --- |
| Old UI navigation | Normal application | Temporary redirect or migration page to the fixed new host |
| New UI | Controlled readiness testing | Canonical application |
| Old-origin browser mutation | Normal authenticated write | Explicit moved/reload error; no automatic replay |
| Old machine API | Existing authentication | Same backend, existing authorization, no cross-host redirect |
| New machine API | Controlled authenticated validation | Canonical API |
| Unknown host, issuer, audience or identity | Deny | Deny |

Redirect only approved UI GET/HEAD navigations; explicitly exclude API, auth
callbacks, service workers and compatibility assets. Do not blindly carry
sensitive query parameters to another origin. Define path/query handling,
open-redirect rejection and cache headers. Never redirect API POST/PATCH/DELETE
requests, rely on clients forwarding Authorization across hosts, or retry a
write whose commit status is unknown. Redirects remain temporary until the
rollback window passes.

Access path precedence matters: the more specific API application does not
inherit the UI application's policies. Test the base API path as well as its
children, trailing slashes and auth bootstrap. Validate exact host/application
audience binding at the edge and origin; do not accept beta or unrelated
application audiences during overlap. See [Cloudflare application paths](https://developers.cloudflare.com/cloudflare-one/access-controls/policies/app-paths/).

PWA acceptance:

- Warn users to save drafts and expect to authenticate at the new origin.
- Install a new Home Screen app, visit boards online, then verify saved boards
  after a full offline relaunch. Offline remains read-only with no write queue.
- Keep the old service worker and necessary assets reachable during the
  transition; test existing controlled clients rather than just fresh browsers.
- Explain that the new origin cannot read or delete the old origin's IndexedDB.
  Do not copy tokens or private snapshots via URLs, iframes or messages to
  work around origin isolation. Users can explicitly clear the old saved data
  and remove the old Home Screen shortcut after validating the replacement.
- Old offline devices cannot receive a retirement notice or be remotely wiped.
  Offline snapshots remain subject to the existing expiry/storage policy.

The origin-storage boundary is documented by [MDN IndexedDB](https://developer.mozilla.org/en-US/docs/Web/API/IndexedDB_API/Using_IndexedDB#security).

### A3 — Controlled production promotion (TC-149)

Preflight: compare live provider state to the reviewed profile, confirm trusted
TLS and unchanged negative-access behavior, take and verify the online backup,
record current schema/release and before-state counts, and prove the retained
rollback release. Rehearse configuration rollback including writes made after
the switch. Define a maintenance window and measured recovery target; aim for
a five-minute configuration rollback only if rehearsal demonstrates it.

With explicit owner go/no-go approval:

1. Publish only the pre-protected new hostname to the existing Tunnel/backend.
2. Verify new-host owner authentication, API service authentication and denial
   without credentials; do not create another owner or database.
3. Promote the canonical public origin and the tested legacy behavior as a
   coordinated, reversible release. Expect stale tabs to need reload.
4. Update client base URLs explicitly, one client class at a time. Verify both
   Cloudflare edge credentials and Helm bearer authorization without rotating
   unrelated deploy/signing/Tunnel keys. Record each client migration.
5. Prove task create/read/update/claim/progress/complete using designated test
   fixtures, events and comments, logout/reconnect, stable existing actor IDs,
   and the physical iPhone checklist. Do not mutate unrelated user tasks.
6. Compare integrity, FK checks, identifiers and counts with the baseline,
   accounting for known legitimate writes. Record live revision and health.

Stop/rollback for owner lockout, unauthorized access, persistent auth/write
failure, data-invariant mismatch, redirect loops or failed origin/TLS checks.
Restore the captured application origin, route profile, Access policy/audience
configuration and compatible binary as needed. Keep the current database and
verify post-cutover writes still exist. A provider outage is not resolved by
restoring yesterday's data. New hostname requests should get a controlled
maintenance response during rollback, not oscillating redirects.

### A4 — Observe and retire deliberately (TC-150)

Proposed windows for approval: at least seven healthy days before permanent
UI redirects and 30 days of legacy agent API compatibility. These are policy
proposals, not platform guarantees or automatic timers.

Require migrated client inventory, no unexplained old API use for the agreed
observation period, successful owner/iPhone acceptance, clean monitoring and
current recovery evidence. Count traffic without logging credentials or
task/query contents. Then retire only the old API permission/audience entries
and transitional credentials no longer in use, with explicit migration errors.
Keep useful browser bookmarks working through exact-host HTTPS redirects.
Do not delete a shared Access object or credential merely because its name
contains "Roadmap". Legacy retirement is separately approved from cutover.

## Track B: identity for self-hosted teams

### B1 — Authorization and stable identities (TC-151)

Separate authentication (who signed in) from project authorization (what that
actor may access). Proposed minimal permissions, subject to owner review:

| Role | Intended authority |
| --- | --- |
| Instance Owner/Admin | Instance settings and identity administration; explicit instance-wide authority |
| Project Manager | Manage the assigned project's configuration and membership within defined limits |
| Project Member | Work on assigned projects; no identity administration |
| Project Viewer | Read allowed projects; no mutations |
| Agent | Explicit scopes intersected with its project ceiling; never human-admin powers |

Use one centralized authorization policy across every route and collection,
including counts/search, exports, event streams, audit findings, dependencies,
hierarchy, assignees and AI context. Avoid side-channel disclosure through
errors and identifiers. Validate assignment eligibility independently from
claim ownership. Existing human access needs an explicit reviewed backfill;
do not quietly grant every future member every project.

Add actor-linked external identity records. OIDC identity uses issuer plus
subject, not email as its stable key; changing an email must not create a new
actor or transfer someone else's account. Account linking requires an
authenticated linking ceremony or administrator-approved migration with audit
evidence. See [OpenID Connect identity stability](https://openid.net/specs/openid-connect-core-1_0.html#ClaimStability).

Preserve actor IDs, ownership history, agent claims and per-user Codex state.
Test at least two humans with disjoint projects, viewer write denial, an
out-of-project agent and attempted self-escalation. Protect the last owner.

### B2 — Human lifecycle and directory (TC-152, reuse TC-8)

Implement closed-by-default invitations with one-time expiring tokens,
membership administration, disablement and visible account status. Reuse TC-8
for the human/agent directory and searchable assignee picker; do not duplicate
or claim that existing implementation card during planning.

Add own-session inventory/revocation and constrained admin revocation. Define
absolute/idle expiry, password-reset and offboarding invalidation, and clear
online cached data on observed access loss. A disabled user must not regain
access via a lingering session, linked provider or background AI session.
Retain history and attribution rather than deleting actors. Explicitly handle
active agent claims and Codex processes; do not silently reassign work.

Recovery needs rate limiting, non-enumerating responses, securely stored
single-use reset tokens and optional SMTP configuration. Provide an audited
operator-console recovery procedure when SMTP/IdP fails. Do not switch the
public instance to disabled authentication or add a permanent remote bypass.
Offline devices cannot immediately honor server-side revocation; document
trusted-device requirements and the existing snapshot expiry limitation.

### B3 — Optional OIDC/SSO (TC-153)

Use a maintained protocol implementation with Authorization Code + PKCE,
state/nonce, exact callbacks, issuer/audience/signature/expiry validation and
key rotation handling. Validate against a disposable provider before selecting
an operational IdP. Keep the local recovery path controlled and audited.

Enrollment and provider/group claims must not silently grant owner privileges.
Test duplicate/changed email, disabled accounts, provider outages, logout,
session expiry, bad callbacks and issuer confusion. Make MFA enforcement an
explicit IdP policy for SSO; native local passkeys/MFA are a later separately
scoped design, not implied shipped functionality.

Do not change the production IdP during Track A. Confirm provider choice,
membership model and recovery custody before executing this phase.

### B4 — Agent credential lifecycle (TC-154)

Keep provider-neutral per-agent scoped credentials in the product. Add named
credential metadata, expiry, last-used visibility and rotation as staged new
credential -> verify -> bounded overlap -> retire old. Plaintext is returned
once to an approved sink; hash credentials at rest and redact them everywhere.
Preserve idempotency/ETag behavior without replay-caching plaintext secrets.

The current Cloudflare deployment has an edge credential layer plus a Helm
bearer layer. A dedicated Cloudflare service-principal adapter may later map
verified machine identity to the same actor, but must not become mandatory for
generic self-hosters. Cloudflare's [service-token documentation](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/)
describes the edge credential mechanism; Helm project permissions remain a
separate responsibility in the current implementation.

Reconcile `docs/AGENT_CREDENTIAL_LIFECYCLE_PLAN.md` before coding. Its TC-3/TC-4
references were unavailable through current task reads; do not restore cards,
assume their readiness, or silently replace their proposed trust boundary.
That pre-existing document was not modified during this planning work.

Normal task agents remain unable to mint credentials, expand permissions or
override another actor's active claim. Any lifecycle controller needs a
separate, human-approved, bounded administrative capability. Test partial
provider failure, concurrent rotations, undeliverable secrets and revoke races.

## Track C: productize the hosting recipe (TC-155)

Publish a generic binary/container installation using an operator-owned domain,
persistent database and actor-isolated Codex volumes, local auth and a documented
reverse proxy. Keep the app private behind its proxy and make proxy trust
explicit. Cloudflare and OIDC are optional integrations, not prerequisites.

Separate homelab deployment profiles from generic instructions. A clean
installation must not need Shane's DNS zone, Cloudflare account, Proxmox host
or GitHub secrets. Preserve pinned artifacts, safe upgrades, backup/restore
drills, retention guidance and health diagnostics. Reuse TC-119's backup work
and TC-118's diagnostics where applicable; portable import is not full recovery.

Acceptance: another operator can perform first setup, invite a user, scope an
agent, persist data over restart, upgrade populated data, restore to an isolated
instance and perform a compatible rollback from the written recipe. Do not
advertise unimplemented SSO as required for the initial local-auth recipe.

## Delivery order and ownership

| Card | Deliverable | Required predecessor |
| --- | --- | --- |
| TC-147 | Validated hostname/profile | None; preparatory behavior only |
| TC-148 | Origin/API/PWA transition | TC-147 |
| TC-149 | Rehearsal and production cutover | TC-148; owner go/no-go |
| TC-150 | Observation and retirement | TC-149; owner retirement approval |
| TC-151 | Human authorization + stable identity foundation | None; ship independently of cutover |
| TC-152 | Invitations, sessions, recovery | TC-151 |
| TC-153 | Optional OIDC | TC-152 |
| TC-154 | Portable agent credential lifecycle | TC-151; reconcile prior proposal |
| TC-155 | Provider-neutral installation/recovery recipe | TC-147, TC-152 |
| TC-8 (existing) | Member directory and assignee picker | Coordinate with identity policy; do not duplicate |

The nine implementation cards have formal prerequisites. TC-147 is active;
the remaining new cards stay unclaimed Backlog work. TC-146 covers only
producing this plan. TC-109's existing
authentication review remains untouched; obtain its relevant findings before
identity implementation rather than assume a clean security verdict.

Recommended execution: finish Track A through stable cutover first; do not
mix Track B schema/permission changes into that deployment. Track C and
identity design can proceed independently, but production promotions are
serialized and individually verified.

## Decisions to confirm before execution

- Approve keeping the existing host/data/Cloudflare edge for the hostname move.
- Approve the maintenance/recovery target and legacy compatibility windows.
- Confirm intended project-role powers and existing-human membership backfill.
- Choose an IdP only if SSO is wanted; decide MFA and recovery custodians.
- Reconcile the unavailable TC-3/TC-4 references and delegated-agent
  administration policy with the existing credential proposal.

No domain or project-key rename, infrastructure migration, account creation,
token rotation, or identity-policy expansion was performed during planning.
Track actual execution evidence on the corresponding implementation cards.
