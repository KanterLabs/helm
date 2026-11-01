# Membership integration contract

Status: execution authorized by the owner on 30 October 2026. TC-113 precedes
the existing TC-8 member directory and assignee picker. The consolidation plan
continues; this prerequisite ships as a separate reviewed slice.

## Scope and acceptance

Port the useful behavior from `98e4292` (`backlog/tc113-membership`) into the
current extracted architecture. The historical handoff is source evidence,
not acceptance, a deployed feature, or permission to cherry-pick old monoliths.

- Administrators create/admit, disable/reactivate and manage human members,
  workspace roles and explicit project grants. Preserve human and agent IDs.
- Local users change passwords, inspect their sessions and revoke sessions;
  administrators reset credentials without exposing them in logs or events.
  An admin-created account satisfies the invitation/create requirement; no
  outbound mail service is added.
- Active owner/admin roles manage the workspace. Members work only in granted
  projects. Pending verified identities receive no implicit project access.
  Keep at least one active administrator under concurrent mutations.
- Authentication and authorization apply centrally to all current project,
  task, ticket, Intake, search, view, notification, evidence, audit, settings,
  agent and transfer routes. Apply project ceilings to aggregates and counts.
- Preserve agent/token scope intersections, request admission, CSRF,
  idempotency, version/claim checks and secret-authenticated webhook ingress.
- Keep bounded generic login failures and durable, redacted membership,
  password, role and session events. UI/account failures must not disclose
  passwords, session tokens, hashes or inaccessible project information.

TC-8 additionally needs a project-authorized assignee directory for ordinary
members and server-side eligibility checks. Admin-only member/agent lists do
not satisfy that contract. Disabled or inaccessible identities remain visible
in authorized history but cannot be newly assigned. Assignment is not a claim.

## Verified port hazards

1. Historical migration 013 collides with the current project/column migration.
   Preserve 001–040 unchanged; use the next contiguous migration (currently 041).
2. The old human ceiling helper copies zero grants into a nil slice. Existing
   aggregate queries interpret nil as unrestricted. An admitted member with no
   grants must instead produce an explicitly empty, nonnil ceiling.
3. Retained schema35/40 binaries do not inspect `sessions.revoked_at`. Revoking
   a session must also invalidate it through legacy-compatible expiry or row
   removal while retaining safe audit metadata. New fields alone do not prove
   that a retained binary rejects a revoked cookie.
4. Retained Cloudflare identity reconciliation rewrites the legacy admin bit
   from configured email, and old human readers ignore new membership grants.
   SQL read/write compatibility does not imply authorization parity. Test and
   report this boundary explicitly; never call an unsafe downgrade accepted.
5. Tailnet currently admits only the configured owner after signed,
   request-bound assertion checks. Membership does not expand that ingress
   policy. Preserve subject/email/audience/request binding and consult reviewed
   membership after verification without silently restoring a revoked role.
6. The historical last-admin test is sequential. Establish write serialization
   before counting administrators and test independent database connections.
7. The old HTTP port predates tickets, shared views, notifications, checklists,
   hierarchy, current portable transfer and GitHub routes. A copied old route
   list would miss current authorization surfaces.
8. Current store types and HTTP task modules already exceed the maintained
   file limit. Extract complete responsibilities before changing an oversized
   file; do not copy the old App, server or bundled OpenAPI monoliths.

## Preservation and release boundary

Write failure scenarios and meaningful failing real HTTP/browser journeys
before implementation. Adapt old lifecycle/migration coverage, adding empty
grant, pending identity, revoked membership, actor/token intersection,
independent-connection last-admin, generic login failure and expired/revoked
session cases. Keep all fixtures and artifacts synthetic.

Verify a populated schema40 upgrade preserves IDs, task data, relationships,
claims, histories, credentials and existing owner access. Document how existing
humans and project grants are backfilled; agents keep their existing ceilings.
Test migration retry and integrity/FKs without resetting a database.

Before activation, refresh the verified online backup and exercise the retained
schema35/40 application images against an isolated upgraded copy. Include actual
cookie revocation and authorization behavior, beyond six-column SQL probes.
If an old image would widen access, retain that failed gate and require an
enforcing rollback path before promotion. Never silently accept it, overwrite
the database, restore an older backup or change live ingress to make it pass.

The owner approved implementation, not an authorization bypass or destructive
restore. Production/beta activation requires the preserved-data and runtime
gates. Parent integration owns rollout decisions, cross-cutting authorization,
Opus UI acceptance and the final assignee flow.

Every maintained file stays at or below 20,000 bytes. Measure before edits and
handoff, splitting by responsibility near 16,000 bytes. Shared builds,
migrations and generated OpenAPI refreshes are serialized with other workers.
