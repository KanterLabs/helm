# Authorized agent credential provisioning

Read this only when the user has authorized administrative credential creation
and ordinary configured access is insufficient. Having root access or receiving
401 does not by itself authorize a new token. When authorization and an approved
admin path already exist, use that path rather than asking the user to provide
a secret you can obtain through it.

## Prefer supported administration

Use the installation's normal authenticated agent/token administration flow when
available. Give an agent only the required project grants and issue a token
within that ceiling. Screenshot comments need `projects:read`, `tasks:read` and
`tasks:write`; existing claimed progress workflows may need additional tracking
scopes. A short-lived example publisher does not need human, workspace-admin,
all-project, or non-expiring credentials.

Use separate origin-bound profiles for beta and production. Do not substitute a
Cloudflare management token, signing key or deployment credential for a Helm
application token. Never synthesize owner headers or Tailnet assertions.

## Guarded native maintenance when required

An explicitly authorized operator may need a native maintenance path when no
usable application-admin credential is available. This is a conditional recovery
procedure, not the normal screenshot upload flow and not a self-running hook.
Follow the current deployment's approved operating contract and identify its
actual service, source revision, schema, data/config paths and permanent lock.
Do not copy a previous session's host IDs, marker generations or helper binary.

Prepare a small helper against the exact application's existing Store API:
`CreateAgent` and `CreateTokenBy` preserve the normal creator attribution and
audit events. Check the enabled workspace owner and intended project. Refuse a
schema mismatch rather than letting a credential operation perform a migration.
Exercise the helper on a disposable populated copy first, including missing or
busy lease, wrong owner/project, duplicate issuance and private receipt failure.

For a guarded installation:

1. Verify a fresh backup and record current service, authority, data identities
   and backup timer state. Pause conflicting backup work.
2. Stop only the selected service and confirm the actual process/container has
   stopped. A queued stop response is insufficient.
3. Acquire its existing permanent writer lease. Keep that lease through all
   writes and validation; pass a verified inherited descriptor to a child helper
   when required. Use approved root maintenance, not an alternate unguarded
   runtime under the reserved application UID.
4. Create or validate a non-admin agent with the intended project grant. Issue
   a narrowly scoped expiring token using the application's normal storage and
   audit functions. Attribute the authorized administrator without impersonating
   a network identity. Keep plaintext only in memory or exclusive0600 temporary
   files; metadata logs may include IDs, scope and expiry, never the value.
5. Verify all prior rows and grants remain, SQLite integrity/foreign keys pass,
   and data ownership, modes, inodes and immutable admission authority remain
   valid. Release the lease, start the unchanged guarded service, check private
   readiness, and restore the original timer state. Recover a partial operation
   explicitly; never reset or restore a database as a token-creation shortcut.

Do not issue duplicates to work around a lost response. Reconcile a protected
receipt or revoke the uncertain issuance through approved administration. Any
maintenance helper and receipt belong to that reviewed operation, not to a
permanent broad credential or an automatic startup action.

Use the new token through the ordinary authenticated API to publish and verify
the requested result. Remove temporary plaintext and helper artifacts after
success. Record expiry and scope so a later session does not depend on an
expired example credential. The published attachment remains available to
other authorized readers after the publisher's token expires.
