# Agent credential lifecycle implementation plan

Status: proposed (2026-10-04). Not started; planned after v0.2.0.

## Goal and scope

Deliver TC-4 as a reviewed operator workflow for one named Helm agent:

- `provision`
- `status`
- `rotate`
- `finalize-rotation`
- two-phase `revoke`

The workflow must manage the Cloudflare Access service credential and its
nonsecret Helm principal mapping as one lifecycle without ever printing or
persisting a one-time secret outside an approved sink.

This plan does not replace the existing owner login, release-signing,
deployment SSH, tunnel, or Cloudflare management credentials. It also does not
make arbitrary Cloudflare resources configurable; the current fixed-account,
fixed-application allowlist remains the provider boundary.

## Required prerequisite

TC-4 depends on TC-3, **Authenticate agents directly with Cloudflare Access
service principals**.

Today the edge uses one shared `Helm agents` Cloudflare service token while the
application authenticates each agent with a second Helm bearer token. TC-4's
one-principal provision/rotate/revoke contract is only safe and coherent after
TC-3 makes a dedicated Cloudflare service principal sufficient for normal Helm
API calls.

Before TC-4 implementation starts:

1. Complete TC-3's signed service-principal authentication and nonsecret
   principal-to-agent mapping.
2. Keep legacy Helm bearer tokens and the shared Cloudflare token compatible
   during the migration.
3. Add TC-3 as a formal prerequisite of TC-4 and move TC-4 out of Ready if that
   prerequisite remains incomplete.

## Architecture decisions

### Trust boundaries

Use three deliberately separate actors:

1. **Lifecycle controller** — a manually bootstrapped, narrowly scoped service
   principal allowed to manage agent principals. It cannot grant itself more
   scope or widen its project ceiling.
2. **Target agent principal** — the named Cloudflare service token mapped by
   TC-3 to one Helm agent, its scopes, and its project ceiling.
3. **Human administrator** — retains UI control, emergency disablement, and
   bootstrap authority.

Add an `agents:manage` control-plane permission only to the principal-management
routes. Do not add it to normal task agents by default, and do not treat an
agent actor's `admin` bit as authority.

### Application state

Add an append-only migration for a provider-principal mapping with at least:

- immutable mapping ID and agent actor ID;
- provider (`cloudflare_access`) and provider principal ID;
- state (`staged`, `active`, `retiring`, `disabled`);
- scopes and project ceiling;
- optimistic version;
- creation, verification, activation, retirement, and disable timestamps;
- current rotation operation ID where applicable.

Provider secrets never enter SQLite. The existing agent actor remains as the
durable historical identity; revocation disables it and removes its live
provider mapping rather than deleting audit history.

Expose a narrow, OpenAPI-documented control API for lifecycle automation:

- read/reconcile a named agent principal;
- create a staged principal mapping;
- verify and activate a staged mapping;
- mark the old mapping retiring during overlap;
- disable/remove a mapping and disable the agent;
- revoke any legacy Helm bearer tokens owned by the agent.

Every mutation requires an idempotency key and an exact ETag. Responses contain
only nonsecret metadata. Concurrent lifecycle operations on the same agent
must fail with a stable conflict instead of racing.

### Cloudflare provider boundary

Keep `deploy/cloudflare.sh` as the sole Cloudflare API boundary. Extend it with
narrow internal operations for a named agent token and the existing API Access
application policy:

- inspect one exact token and policy membership;
- create one bounded-lifetime token and write its one-time value directly to a
  preflighted sink;
- add or remove one token from the exact Service Auth policy;
- revoke one exact token;
- verify the expected fixed application and policy topology.

Do not introduce generic Terraform/Pulumi state or arbitrary account, zone,
hostname, application, or policy inputs. Preserve the existing TLS-only curl
configuration, mode-0600 temporary headers, duplicate-resource rejection,
one-year maximum lifetime, and cleanup trap.

### Operator command

Add a thin lifecycle command in `deploy/helm-agent-credentials.py`. Python is a
good fit for the state machine and fake-provider tests; Cloudflare mutations
remain delegated to the allowlisted shell boundary above.

Common arguments:

```text
--agent NAME
--scope SCOPE                 repeatable
--project PROJECT             repeatable
--expires-in DURATION
--controller-config FILE      owned by the user, mode 0600
--sink-file FILE              explicit file sink
--sink-profile NAME           approved secret-manager adapter
--operation-id ID
--json
```

Credential values are never accepted as command-line arguments. Controller and
Cloudflare management credentials come from private files or inherited process
configuration. The target agent secret goes from the provider response to the
sink without entering argv, logs, task data, or command output.

Commands return sanitized JSON by default, including the agent, operation ID,
provider resource ID, state, expiry, and required next action. They never
return the service client secret or its containing filesystem path.

## Secret sink contract

Define a small transactional sink interface:

1. `preflight` — prove the destination is writable and safe before creating a
   provider token.
2. `stage` — capture the one-time credential under the lifecycle operation ID.
3. `commit` — atomically make the staged version current after live validation.
4. `abort` — remove a staged value after a failed provision/rotation.
5. `retire` — remove or archive the superseded version only after finalization.

The initial file sink must:

- require an absolute path outside the repository;
- reject symlinks, existing unexpected files, broad parent permissions, and
  non-user ownership;
- create parent directories as mode 0700 and values as mode 0600;
- use a same-directory temporary file, `fsync`, and atomic rename;
- preserve the previous valid value until the replacement is verified;
- never echo the value during cleanup or error handling.

Secret-manager adapters are selected by a named profile in a private config,
not by an arbitrary command supplied on the CLI. Pass the value on stdin to an
allowlisted adapter, require a nonsecret version handle in response, and bound
and sanitize all adapter diagnostics.

## Lifecycle state machines

### Provision

1. Acquire the server-side lifecycle lock/version for the named agent.
2. Reconcile Helm and Cloudflare state; return success without mutation when
   the requested active principal already exists.
3. Preflight the sink and exact fixed Cloudflare policy topology.
4. Create the Cloudflare service token with bounded expiry and capture its
   one-time secret as a staged sink version.
5. Create the staged Helm mapping with the requested scopes/project ceiling.
6. Add the token to the Service Auth policy without removing existing tokens.
7. Probe a read-only Helm auth-check endpoint using the new credential and
   verify the expected agent identity, scopes, projects, audience, and revision.
8. Activate the mapping, commit the sink version, and emit lifecycle events.
9. On failure, disable/remove the staged application mapping first, remove the
   provider policy member, revoke the newly created provider token, and abort
   the staged sink version. Report any incomplete cleanup as `action_required`.

### Status

Read Helm, Cloudflare, and sink metadata without mutation. Classify the result
as `healthy`, `rotation_pending`, `revocation_pending`, `expired`, `drifted`, or
`action_required`, with a bounded next action. Status must detect duplicate
provider names/IDs, policy drift, expiry policy violations, missing mappings,
disabled actors, and a sink version that does not correspond to the active
provider ID.

### Rotate

1. Reject a second rotation while another operation is pending.
2. Preflight the sink and create/capture a new provider token under a unique
   rotation name.
3. Add a staged Helm mapping and add the new token to the Service Auth policy.
4. Validate the new credential end to end.
5. Activate the new mapping, mark the old mapping `retiring`, and commit the new
   sink version while retaining the old one.
6. Return `rotation_pending` with the rotation ID and earliest safe finalize
   time. Both credentials work during the bounded overlap window.

If any pre-activation step fails, the old credential remains authoritative and
the new resources are cleaned up. No automatic failure path may remove the old
credential.

### Finalize rotation

1. Re-read the rotation ID, ETag, overlap deadline, and both provider mappings.
2. Revalidate the new credential.
3. Disable/remove the old Helm mapping first and prove the old identity is
   rejected while the new identity still succeeds.
4. Remove the old token from the Cloudflare policy, revoke it at the provider,
   then retire its sink version.
5. Mark the rotation complete. A provider-side failure leaves the application
   mapping disabled and returns an idempotently recoverable next action.

### Two-phase revoke

`revoke --prepare` creates a short-lived, single-use confirmation record after
showing nonsecret impact: agent, projects, scopes, active claims, token expiry,
and provider resource ID. It performs no destructive action.

`revoke --confirm CONFIRMATION_ID` then:

1. validates that the principal/version and impact snapshot have not changed;
2. disables the Helm mapping and agent, revokes legacy Helm bearer tokens, and
   verifies that application authorization fails;
3. removes the provider policy member and revokes the Cloudflare token;
4. retires the sink value and records completion.

If Cloudflare is unavailable after application disablement, retrying the same
confirmation continues cleanup without re-enabling the principal. Provide a
separate human-admin emergency-disable path that stops application access even
when the provider API is unavailable.

## Delivery slices

### Slice 0 — prerequisite and contract freeze

- Complete TC-3 and add its dependency edge to TC-4.
- Freeze identity claims, the auth-check response, lifecycle states, conflict
  codes, provider naming, default expiry, overlap bounds, and sink profiles.
- Document the one-time bootstrap of the lifecycle controller.

### Slice 1 — application principal administration

- Add the migration, store methods, lifecycle events, ETags, and concurrency
  guard.
- Add the narrow control API and `agents:manage` authorization.
- Preserve old binaries by keeping the migration additive and legacy bearer
  authentication unchanged.

### Slice 2 — provider and sink foundations

- Add allowlisted Cloudflare agent-token operations to `cloudflare.sh`.
- Implement file and named secret-manager sink protocols.
- Add sanitized command output and operation metadata.

### Slice 3 — provision and status

- Implement idempotent reconciliation, staged capture, live validation,
  activation, drift reporting, and compensation.
- Pilot only with a disposable, least-privilege test agent.

### Slice 4 — rotation

- Implement overlap, pending-rotation state, finalize, old-credential rejection
  checks, and retryable partial cleanup.

### Slice 5 — revocation and emergency response

- Implement prepare/confirm, stale confirmation rejection, application-first
  disablement, provider cleanup, and emergency disable.
- Add expiry monitoring and actionable status output.

### Slice 6 — rollout and documentation

- Run the complete fake-provider, application, migration, security, and live
  pilot gates.
- Document normal operation, scheduled rotation, expired credentials, provider
  outage, partial cleanup, emergency disablement, and rollback ownership.
- Keep the shared legacy service-token path until every retained client and
  rollback release has a verified compatibility route.

## Verification matrix

### Unit and contract tests

- Fake Helm and Cloudflare providers for every HTTP status, malformed response,
  timeout, duplicate resource, missing resource, and partial mutation.
- Table-driven state transitions for provision, no-op reconciliation, rotation,
  finalize, revoke prepare/confirm, stale confirmation, and retry.
- Sink tests for ownership, mode, symlink, existing file, full disk, failed
  `fsync`/rename, adapter timeout, and cleanup.
- Stable sanitized JSON/error contracts and explicit assertions that synthetic
  secrets never appear in stdout, stderr, argv captures, operation state, or
  logs.

### Store and API tests

- Populated-database migration, foreign keys, uniqueness, retained rollback
  binary compatibility, and exact row/event counts.
- Human-admin versus controller versus ordinary-agent authorization.
- Project ceiling/scope narrowing, disabled principals, wrong audience,
  unknown principal, stale ETag, idempotent retry, and opposing concurrent
  operations.
- Application-first revocation and legacy bearer compatibility.

### Provider and integration tests

- Extend `deploy/test-deployment-security.sh` with a fake Cloudflare API for
  exact policy membership, one-time secret behavior, expiry bounds, and cleanup
  after every injected failure point.
- Validate that deployment bundles, images, workflow artifacts, and the guest
  environment never contain agent service credentials.
- Run repository Go tests, shell syntax/security tests, OpenAPI generation
  checks, and the Helm skill tests.

### Live rollout gate

1. Verify a read-only `status` against current production without mutation.
2. Provision a disposable agent with one project and read-only scope.
3. Confirm correct identity and deny wrong audience, project, and scope.
4. Rotate with overlap and prove both credentials work.
5. Finalize and prove only the replacement works.
6. Run two-phase revoke and prove both Helm and Cloudflare reject the old
   principal.
7. Inspect logs, task activity, CI output, temporary files, and secret-manager
   history for leakage before enabling broader use.

## Rollback and data preservation

- Take and verify the normal pre-upgrade backup before deploying the additive
  principal-mapping migration.
- Retain the previous binary and prove it can run against the migrated,
  populated database without modifying the new table.
- Deploy all new authentication and management behavior disabled-first.
- During rotation, rollback always keeps the previously verified credential;
  it never revokes credentials automatically.
- A binary rollback does not restore an older database and does not delete new
  provider tokens or sink versions. Operators use `status` to reconcile them.
- Do not remove the shared Cloudflare token or legacy Helm bearer support in
  TC-4. Their eventual retirement is a separate, explicitly reviewed change
  after the retained rollback window closes.

## Completion gate

TC-4 is complete only when all five commands are idempotent, the live pilot
passes provision/overlap/finalize/revoke, every injected partial failure has a
documented recovery path, application access is removed before provider
revocation, and synthetic-secret leakage tests cover output, logs, state,
artifacts, and temporary files.
