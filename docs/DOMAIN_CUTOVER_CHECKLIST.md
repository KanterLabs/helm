# Domain cutover evidence checklist

Workstream: TC-148 implementation, TC-149 rehearsal/promotion, TC-150 retirement.
This checklist is **not evidence that a cutover has occurred**. Leave unchecked
items unchecked until the named observation has actually been recorded.

## Release sequence

Use separate source revisions for preparation, staging and promotion. A retained
release binds its signed environment to its revision; do not repurpose one SHA
with a different public origin. Beta remains an independent single-host instance.
Every phase uses the existing production backend and database.

The preparatory CI workflow deliberately permits only `legacy` production
deployments and rollbacks, and the publisher rejects dual-host DNS publication.
TC-149 must replace those guards with live pre-publish Access/TLS validation and rehearsed
phase-aware recovery before a source change can stage or promote the domain.
Do not bypass the guard with an ad-hoc DNS change or manually edited runtime
environment.

- [ ] Preparatory release: legacy phase; existing hostname and clients unchanged.
- [ ] Staged release: both exact host routes protected; browser-write origin
      remains the old hostname. Rejection of new-origin browser writes is expected.
- [ ] Canonical release: new browser-write origin; legacy UI migration behavior
      enabled; existing authenticated legacy machine API remains available.
- [ ] Retirement: separately approved after the observation window; not part of
      an ordinary deployment or automated cleanup.

## Before any provider write

- [ ] Record release SHA, phase, runtime origin, schema version, tunnel ID,
      application IDs/audiences, policy IDs, service-token ID and DNS record IDs.
      Record identifiers only, never secret values or authorization headers.
- [ ] Capture complete provider before-state in access-controlled storage and
      its digest. Ensure no other operator/reconciler is changing these resources.
      A read/compare/write sequence is not an atomic provider transaction.
- [ ] Verify current owner access and a designated agent's existing authorization.
- [ ] Take the supported online backup and verify its checksum, integrity and
      foreign keys. Record the actor-isolated Codex-state backup reference.
- [ ] Record stable project/task/actor IDs and data counts; allow for known live
      writes when comparing later. Do not copy a live SQLite file as a backup.
- [ ] Rehearse rollback with populated isolated data and a write performed after
      simulated promotion. That write must remain after rollback.

## Staging acceptance

- [ ] Each hostname has its own exact UI/API application and audience pair.
      The existing service-token identity is reused, not rotated.
- [ ] Tunnel ingress is exactly the two approved names to the same loopback
      backend plus the final deny rule; no wildcard or third-host route.
- [ ] New DNS is published only after Access and tunnel protection are ready.
- [ ] Trusted HTTPS, unauthenticated denial, wrong-audience denial, owner login,
      agent authorization, `/api/v1` and child-path policy behavior are verified.
- [ ] Old browser writes work; new-origin browser writes fail without replay.
- [ ] A retained legacy release can be restored without losing database writes.
      Provider topology and validation must match the rollback phase, not merely
      whichever phase happens to be checked out on main.

## Promotion acceptance

- [ ] Owner gives go/no-go approval based on recorded staging/recovery evidence.
- [ ] New-origin login and designated fixture create/read/update/claim/progress
      operations succeed with the same existing actor and project IDs.
- [ ] Old browser mutations fail with an actionable moved error; spoofing the
      new Origin on the old Host does not permit a cookie-authenticated write.
- [ ] Old authenticated agent API requests are not redirected or replayed.
- [ ] Approved old UI navigations use temporary fixed-origin redirects without
      forwarding sensitive queries. API, auth and static/PWA paths are excluded.
- [ ] Existing controlled PWA clients can fetch their worker and compatibility
      assets; migration UI protects drafts and does not automatically clear data.
- [ ] Physical iPhone: sign in and install from the new origin, visit a board,
      fully relaunch offline, verify read-only data, then reconnect successfully.
      Desktop emulation alone does not satisfy this check.
- [ ] Explicit old-origin cleanup works and explains its local scope. Removing
      the old Home Screen icon and signing out are separate user actions.
- [ ] Integrity, relationships, stable IDs, health and release revision match
      expectations after accounting for legitimate writes.

## Stop conditions and recovery

Owner lockout, unauthorized access, unexplained data differences, redirect loops,
or persistent auth/write failures stop promotion. Restore the compatible retained
runtime/configuration and reviewed provider route/policy state; verify both hosts
behave as intended and post-promotion writes still exist. Do **not** automatically
restore the database. Keep newly created provider resources for review rather
than deleting shared objects during emergency recovery.

Record the measured recovery time, commands/revisions used, final hostname
behavior and remaining client migrations. Mark completion only after required
checks pass; missing physical-device or owner verification remains explicit.
