# TC-113 backend failure inventory

Before implementation, exercise these synthetic failure boundaries:

- A human with zero grants must have a nonnil empty ceiling; pending verified
  identities must see status/me only and cannot access workspace data.
- Revoking a project grant must immediately filter project/task/search/Intake,
  counts, roadmap, portable transfer, audit/evidence, notifications and views.
- Agent ceilings and bearer scope intersections must remain unchanged. Human
  bearer credentials must intersect reviewed membership and token grants.
- Disabled identities must fail cookie, verified ingress and bearer auth.
- Two administrators using independent database connections must not both
  disable/demote the last active administrators. Lock before counting.
- Login failures must be generic for unknown, pending, disabled and incorrect
  credentials; attempts and memory must be bounded before password hashing.
- Self password changes invalidate other sessions; reset invalidates all.
  Revocation must also expire sessions for retained readers ignoring revoked_at.
- Events must be durable but contain no passwords, hashes, cookie values or
  identity assertion, IP or user-agent data.
- Schema40 populated upgrade must preserve rows, IDs, credentials, claims,
  histories, relationships and owner access; retry and integrity/FKs must pass.
- Verified configured owner bootstrap must serialize; reviewed roles must never
  be re-elevated by configured email. Tailnet request-bound owner ingress stays.
- Existing admitted humans receive explicit existing project access on upgrade;
  agents remain in actor_projects. Future grants require review.

Synthetic HTTP artifacts contain only response bodies/status for fixtures,
never request passwords, cookie headers, hashes or authorization material.
Actual retained-image runtime parity and a fresh verified live backup remain
operator/parent release gates. Retained binaries ignore membership and can
re-elevate configured email: rollback is UNSAFE without an enforcing path.

Credential concurrency boundary: an in-flight login must not create a session
using a password snapshot taken before a completed reset or disable. Session
creation must reserve the database writer and recheck the verified hash and
active membership. A self password change must similarly reject a stale
verification snapshot after an administrator reset. These checks serialize
with reset/disable across independent SQLite connections.

Global human Roadmap and legacy Tickets readers must remain useful through an
explicit membership ceiling: a member with one grant sees only that project's
rows/counts/history; no grants yields empty rows and zero counts. Preserve the
existing bearer requirement to select a project on these legacy routes.
# Parent review regressions

Recorded before the parent fixes: cached mutation responses must not disclose
notification, ticket, view or watch data after project grants change; successful
label deletion retries must still acknowledge the original operation; denied
global ticket creation must not create the queue or retarget integrations; and
login limiter capacity must not evict active account lockouts. Real HTTP replay,
denied-write and deletion journeys plus isolated limiter-capacity regressions
must fail before those fixes. Captured responses use synthetic content only.
