# TC-113 backend handoff

Implementation slice on `feat/helm-consolidation`, `/home/shane/projects/helm-bugs-beta`.
Parent corrections and retry compatibility are recorded in
[TC113_PARENT_ACCEPTANCE.md](TC113_PARENT_ACCEPTANCE.md). Validation below
describes the original worker handoff before those review fixes.
Parent owns TC-113 operation `helm-consolidation-membership-20261030`, final
acceptance and rollout. This worker did not claim, complete or change tracking,
commit, push, deploy, install dependencies, use live credentials, modify UI,
run global builds/codegen or change historical migrations 001–040.

The authoritative scope remains [TC113_MEMBERSHIP_PORT.md](TC113_MEMBERSHIP_PORT.md).
Failure-first scenarios are in [TC113_BACKEND_FAILURES.md](TC113_BACKEND_FAILURES.md).
Exact owned files and measured bytes are in [TC113_BACKEND_FILES.md](TC113_BACKEND_FILES.md).

## HTTP contract

All paths below have `/api/v1` prefix. Human mutations retain the existing exact
Origin/CSRF policy. Administrative membership writes use existing mutation and
idempotency handling; passwords reject Idempotency-Key, matching auth routes.
Bearer credentials cannot administer membership or local sessions.

| Endpoint | Access and behavior |
| --- | --- |
| `GET /members` | Active human owner/admin; paginated human identities, including pending identities; `disabled=true` includes disabled rows. |
| `POST /members` | Active human owner/admin; create email/name, optional description/password/role/project_ids; default role member; 201 Member. No invitation email. |
| `PATCH /members/{id}` | Active human owner/admin; admit a pending identity or edit name/description/role/project_ids/disabled; 200 Member. Omitted grants stay unchanged; `[]` revokes all. Null fields rejected. |
| `POST /members/{id}/password` | Administrator reset of another member; password required; expires every session; 200 `{ok:true}`. Self-targets require current_password. |
| `POST /auth/password` | Admitted human; current_password/password required; retains the requesting local session and expires other sessions; 200 `{ok:true}`. |
| `GET /auth/sessions` | Admitted human's sessions; optional actor_id requires admin and an admitted human target; up to 200 newest descriptors. |
| `DELETE /auth/sessions/{id}` | Revoke own session, or admin target through actor_id; 204; wrong actor/session is 404. |
| `GET /sessions`, `DELETE /sessions/{id}` | Compatibility aliases for the session routes above. |

`Actor.workspace_role` is optional: owner/admin/member for reviewed humans;
absent means pending. `Member` embeds Actor and optional last_seen_at. Human
project_ids are canonical reviewed grants; absent/empty grants give ordinary
members no projects. Owner/admin role authorizes workspace-wide access.
`Session` exposes id, actor_id, created_at, last_seen_at, expires_at and optional
revoked_at/ip_address/user_agent. It never includes token/hash values. Metadata
and session lists are bounded; session IDs are non-secret audit handles.

See `openapi/paths/membership.yaml`, `openapi/paths/member-sessions.yaml`,
`openapi/schemas/membership.yaml` and the updated Actor schema. Source assembly
has no unresolved references or duplicate operation IDs.

## Authorization and persistence

- `auth.Identity` now consults reviewed role and project grants. Pending humans
  can inspect auth status/me but fail the central workspace gate. Disabled
  humans fail local/verified/bearer authentication. Agent ceilings retain
  actor_projects and existing scope/token intersections. Human bearer readers
  also intersect reviewed grants with token project scope.
- `authorizeWorkspace` runs before protected dispatch and replay. Resource
  descendants, including audit evidence, use live project authorization.
  Soft-deleted task resolution preserves authorized idempotent retries while
  checking the current project grant first. Workspace administration is human
  owner/admin only; claim/version/admission rules remain in current handlers.
- One allocated empty ceiling reaches counts, search, Intake, portable export,
  audit lists, notifications, views and human global Roadmap/Tickets/My Work.
  Recent activity, due counts, upcoming work and project summaries use that
  same ceiling. Existing scoped bearer rules requiring a project on legacy
  Roadmap/Tickets/My Work routes remain unchanged.
- Human notification delivery/read visibility uses reviewed membership. Legacy
  human actor_projects rows cannot widen or narrow reviewed grants. Revoked
  project views and private run history are hidden, including owned views.
  Projectless private AI history is hidden from project-scoped humans because
  its former workspace scope cannot be proven safe.
- Configured email is used only for first verified-owner bootstrap. Pending
  identities cannot consume owner bootstrap eligibility. Existing reviewed
  roles never change through subsequent identity reconciliation. Tailnet's
  configured-owner ingress, signature and request binding were preserved.
- Migration 041 creates human-only workspace/project membership tables and
  session metadata columns. Existing admin humans become owners, other humans
  members; every existing human receives every existing project explicitly,
  preserving pre-membership readers' actual unrestricted access. Agents keep
  their existing actor_projects unchanged. Future projects are not auto-granted
  to members. No actor, task, history, claim, credential or project IDs change.
- Membership mutation reserves SQLite's writer before inspecting administrators;
  independent connection pools cannot both remove the last active admin.
  Session issuance and self password changes recheck the verified credential
  snapshot under that same writer serialization, preventing stale login or
  self-change after reset/disable.
- Revoke/logout/reset/disable record redacted events and set expires_at to a
  past date in addition to revoked_at. Reactivation cannot revive cookies.
  Self changes preserve only the current local session. Events contain role,
  actor/session handles, counts and reasons; passwords, hashes, IPs, user-agent
  and assertion/cookie/token values never enter these event payloads.
- Local failures remain generic `401 invalid_credentials`, including unknown,
  wrong-password, disabled and attempt-limited accounts. The limiter reserves
  before bcrypt, allows five attempts/account/immediate-peer/15 minutes, and
  bounds its in-memory key count to 1024; forwarded headers are not trusted.

## Validation and evidence

Use the installed Go 1.26.10 toolchain. These commands are focused package
checks, not global builds:

```sh
export GOMODCACHE=/home/shane/.cache/helm-release-gomod
export PATH=/home/shane/.cache/helm-release-gomod/golang.org/toolchain@v0.0.1-go1.26.10.linux-amd64/bin:$PATH
export HELM_TEST_ARTIFACT_DIR="$PWD/.artifacts/tc113-membership-backend"
go test ./internal/db ./internal/store ./internal/auth ./internal/httpapi -count=1
go test -race ./internal/store -run '^TestMembership(IndependentConnectionsLastAdmin|CredentialSnapshotCannotOutliveResetOrDisable|RevocationExpiresForLegacyReaders)$' -count=10
go test ./internal/httpapi -run '^TestMembershipHTTPJourney$' -count=1 -v
check-file-sizes
```

The first real-listener HTTP journey failed before implementation with
`POST /members = 404, want 201`. The populated migration tests failed because
041 was absent. Additional regression receipts cover private readers, empty
portable export, global aggregates and stale credential snapshots. Existing
fixtures were updated to explicit grants/roles rather than weakening access.

The final focused four-package run passes. Repeated race-enabled independent
connection checks pass. The real-listener journey captures only synthetic
response bodies/statuses and covers create, grant/revoke, filtered readers,
audit/evidence denial, password change, reset, revoke, logout and disabled/
reactivated cookies. Schema40 upgrade coverage checks stable rows/IDs/claims/
credentials/history, owner role, session SQL shape, integrity/FKs, retry, and
transaction rollback after an injected duplicate-column failure.

Local evidence is under `.artifacts/tc113-membership-backend/`: red receipts,
`focused-green.log`, `concurrency-race.log`, `http-journey.log`, sanitized
`membership-http-responses.json`, `schema40-upgrade-proof.json` and
`openapi-source-check.json`. No production data or credentials are included.

## Parent integration and release gates

1. Parent explicitly chose to regenerate embedded OpenAPI during integration.
   Three JSON fragments are missing (`paths/membership`, `paths/member-sessions`,
   `schemas/membership`) and `schemas/auth.json` is stale. After the shared lock
   is released, run the normal OpenAPI generator/check and documentation checks.
   Served discovery remains the previous embedded contract until then. No
   frontend API/types module was authored; that ownership was not approved.
2. TC-8 still needs a project-authorized, redacted assignee directory and server
   assignment eligibility. Reuse reviewed Actor roles/grants, IsWorkspaceAdminRole,
   Identity.CanProject and scopedProjectIDs. Admin-only `/members` and `/agents`
   do not implement the picker. Preserve disabled/inaccessible history identities;
   prohibit new assignment to them in TC-8. Assignment remains distinct from claims.
3. Parent owns Opus UI integration, browser acceptance, full integration checks
   and final TC-113 acceptance. TC-113 remains active.
4. **Unsafe downgrade gate remains open.** Actual retained schema35/40 images
   have not been exercised by this worker. Six-column session SQL compatibility
   and expiry proof do not establish runtime authorization parity: old human
   readers ignore memberships and retained Cloudflare reconciliation can rewrite
   the legacy admin bit from configured email. A retained image may widen member/
   pending access or regrant privileges. Do not promote or call rollback safe
   without an enforcing rollback path and actual isolated-image tests.
5. A fresh verified live pre-upgrade backup, populated production-shaped copy
   migration, exact retained-image cookie/role/grant/ingress checks, and operator
   rollout approval remain parent/operator release gates. Do not reset/recreate
   the database or restore an older backup as an upgrade/rollback procedure.
