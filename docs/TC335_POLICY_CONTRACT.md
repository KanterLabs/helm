# TC-335 authorization deployment contract

Source implementation only. TC-335/TC-113 remain incomplete for parent
acceptance. **No artifact is accepted and migration041 rollout stays blocked.**
The parent must supply TC-336 runtime evidence, the actual protected observer,
continuing exclusion integration and both protected workflow lanes. See
[the original activation gate](TC113_ROLLBACK_GATE.md),
[failure inventory](TC335_FAILURES.md) and [handoff](TC335_HANDOFF.md).

## Trust boundaries and persistent floor

`deploy/coolify-deploy.py` now always requires membership acceptance, including
first activation and direct requests for historical targets. There is no
legacy-auto-recovery option. This revision contains041; missing policy inputs
reject before any Coolify mutation. Images, backups, mounts and runtime secrets
are retained. Eligibility to serve current data is separate from retention.

Exactly one pre-provisioned, unique, readable, controller-owned Coolify service
environment entry stores `HELM_AUTHORIZATION_FLOOR`. Its JSON value lives in
Coolify's service metadata, outside selected application source/image. It is
not an application runtime permission switch. Bootstrap is a separate parent
operation after verifying actual lane state; the deployer will not create a
missing entry or infer zero from missing/malformed metadata.

The exact target object has these required string fields:

```json
{
  "lane": "beta",
  "service_uuid": "fixture-service",
  "service_name": "fixture-beta",
  "project_uuid": "fixture-project",
  "environment_uuid": "fixture-environment",
  "server_uuid": "fixture-server",
  "site_url": "https://fixture.home.shanekanterman.dev",
  "api_url": "https://coolify.home.shanekanterman.dev/api/v1",
  "data_identity": "fixture-volume/database.sqlite"
}
```

This is clearly synthetic. Parent must choose an exact persistent volume and
database identity, independently resolve it on the host, and bind every writer
using that identity. A display label or path supplied by the candidate is
insufficient. Do not publish real host inspect output or database content.

Initial floor: `{"format":1,"minimum":0,"target":<exact target>}`. The only
supported advance is to41, adding `activation` with `bundle_sha256`, `candidate`
and `recovery`. Pairs are always `{"image":<immutable GHCR digest>,"sha":<full
source SHA>}`. At41 the original activation remains unchanged through subsequent
deployments/recovery. Unknown higher floors, duplicate keys, target mismatch,
hidden/missing values and malformed activation fail closed. The controller
never writes zero or restores floor metadata with image coordinates.

Coolify's env API has no controller CAS/transaction in the verified contract.
The parent must enforce one controller per lane, exclude unrelated API/UI
deployers and manual/queued starts, and protect the floor against other writers.
The enforcing controller must be pinned separately from the selected application
revision; an older app checkout must not reintroduce its unguarded deployer.
The controller does not claim to constrain a host/Coolify administrator who
bypasses those boundaries. An observer snapshot cannot replace that enforcement.

## TC-336 checksum-bound receipt bundle, version1

`HELM_ACCEPTANCE_BUNDLE` is an absolute/local downloaded bundle path;
`HELM_ACCEPTANCE_SHA256` is its exact byte SHA-256 from the protected lane's
reviewed configuration, never taken on trust from candidate output, image labels
or an adjacent checksum file. The protected digest authenticates the trusted
TC-336 producer's complete receipt tree. Checksums alone do not authenticate an
unreviewed producer. No generated synthetic example is a real acceptance.

The bounded root JSON has exactly:

```json
{
  "format": 1,
  "kind": "helm-runtime-acceptance-v1",
  "producer": "tc336-isolated-runtime-v1",
  "issued_at": 1793376000,
  "expires_at": 1793379600,
  "target": "replace with the exact target object above",
  "artifacts": {
    "candidate": "replace with the artifact object below",
    "recovery": "replace with a distinct exact recovery artifact object"
  }
}
```

The strings indicating object replacement are explanatory schema notation;
complete machine-readable synthetic examples are generated with
`python3 test/deployment-policy/export_examples.py OUTPUT_DIRECTORY`.
Every JSON file is at most20,000 bytes. Root and proof symlinks, path traversal,
duplicate keys, unknown fields and malformed JSON are rejected. Times are
integer UTC epoch seconds: no future issuance, maximum age24 hours, expiry
after now and at most24 hours after issuance. Freshness is rechecked before
environment PATCHes and starts. Revalidate/reissue after expiry; do not bypass.

Each artifact object has exactly `image`, `sha`, `capabilities`, `proofs`.
The pair must match independently protected recovery coordinates or the exact
candidate build output/source SHA. Candidate and recovery must have distinct
image digests and source revisions; this is also checked when retained release
evidence is read. Capabilities are exactly these three:

- `membership-authority-v1`
- `legacy-session-invalidation-v1`
- `current-data-recovery-v1`

`proofs` must contain all eleven scenario names from `rollback_policy.SCENARIOS`:
restricted-human, zero-grant-human, pending-disabled-human,
configured-email-demotion, bearer-scope-intersection, aggregate-visibility,
session-revoke-reset, disable-reactivation, schema35-populated-upgrade,
schema40-populated-upgrade, current-data-recovery-rollforward.

Every value is `{"file":"candidate-zero-grant-human.json","sha256":<64 hex>}`.
Files are bounded sibling basenames only. Each proof has exactly `format:1`,
`scenario`, exact `target`, exact artifact `subject` pair, `result:"passed"`, and
one to128 `observations`. Each observation has an assertion of at most512
characters, `passed:true`, and a captured `exchange`:

```json
{
  "assertion": "Synthetic zero-grant response remains forbidden",
  "passed": true,
  "exchange": {
    "method": "GET",
    "path": "/api/v1/projects",
    "status": 403,
    "expected_status": 403,
    "response": {"error": "forbidden"},
    "expected_response": {"error": "forbidden"}
  }
}
```

The reader requires actual status/projection to equal expected status/projection;
a success boolean without a captured exchange is insufficient. Paths are
sanitized route templates without queries/fragments; response objects are
allowlisted projections. Never include tokens, cookies, private task text or
raw database rows. Each proof checksum binds captured bytes to the bundle.

The trusted producer must build/resolve each exact image and independently
verify its source provenance, then run that immutable artifact unchanged.
It must compare restricted/zero-grant/pending/disabled humans, configured-email
demotion, bearer ceilings and aggregates; active/revoked/reset sessions and
disable/reactivation; schema35 and40 populated upgrades separately; stable IDs,
relationships, claims, credentials, history, integrity/FKs and preserved writes.
The recovery artifact must write against current migrated data and roll forward
without losing that write. The producer's storage assertions must derive from
actual populated-copy comparisons and retain private full evidence outside
uploaded receipts. These are producer obligations, not claims that JSON parsing
reruns runtime authorization. An unsafe historical artifact cannot receive a
passing proof merely because its SQL/readiness checks pass.

## Protected read-only stop observer

The parent-approved observer is an absolute executable path
`HELM_STOP_OBSERVER`, independently pinned by protected
`HELM_STOP_OBSERVER_SHA256`. It is capped at1MiB, checked before use, invoked
without command arguments, and receives only one JSON request on stdin. It gets
no ambient API token/credentials; environment is limited to PATH/LANG. Timeout
is20 seconds; stdout/stderr are never copied into deployment evidence. There
is no remote shell, daemon, credential provisioning or public endpoint here.
Parent supplies existing authorized private operator transport separately.

Request schema (all fields required):

```json
{
  "nonce": "fresh 64 lowercase hex generated by the controller",
  "target": "exact target object",
  "expected": {
    "container_name": "helm-fixture-service",
    "configured": "exact prior image/sha pair, possibly interrupted coordinates",
    "container_ids": []
  },
  "fence": "empty initially, then the same durable exclusion fence ID"
}
```

Response echoes request fields and adds exactly: `format:1`, `observed_at`,
`expires_at`, `result:"stopped-and-fenced"`, `running_writers:[]`,
`restart_eligible:[]`, `queued_operations:[]`, and `container_ids` (one to16
independently established exact container IDs). `fence` becomes a bounded
nonempty durable exclusion identifier. Observation must occur during this
invocation; expiry is after now and no later than60 seconds after observation.
Unknown/stale/mismatched/extra fields reject. Rechecks require the same IDs
within a stop phase and the same fence throughout activation and recovery.

The helper must attest actual stopped state for every writer sharing the exact
database/volume, exact old image/source/container identity, disabled restart
eligibility, and absence of queued/concurrent unsafe operations. It must resolve
the actual container identity or a retained authoritative operator inventory
when Coolify has already removed the stopped container. Mixed env PATCHes are
configuration coordinates, not evidence that such an image/SHA pair ever ran.
If identity or the queue cannot be established, return unknown/fail.

`stopped-and-fenced` means **continuing enforced exclusion**, not a stale
snapshot: a parent-provisioned durable interlock must prevent old/surviving
containers, restart policies, worker retries and pending asynchronous operations
in supported deployment paths from starting an incompatible writer. Deliberate
bypass by a privileged host/Coolify administrator is outside this guarantee.
It must survive
controller interruption and only admit the reviewed candidate/recovery. It must
remain held through queued start completion, readiness and any recovery; it
must not expire with the60-second observation receipt. The read-only helper
verifies this existing interlock; it cannot establish one through commands.
If the platform cannot provide that guarantee, rollout remains blocked.

The deployer requires fresh witness observations after stop, immediately before
floor advancement, before coordinate writes and immediately before each start.
A new stop phase resolves the failed candidate's IDs while retaining the same
exclusion fence. Cached Coolify `exited` only avoids a redundant stop request;
it never satisfies any witness requirement.

## Supported API and transition

Official primary references inspected on2026-10-30:

- [Stop service](https://coolify.io/docs/api/endpoints/services/stop-service-by-uuid)
  documents POST and queued response, with `docker_cleanup` default true.
- [ServicesController source](https://raw.githubusercontent.com/coollabsio/coolify/b3ec26c3b1771654cae4b4ebc06d14a164bd8824/app/Http/Controllers/Api/ServicesController.php)
  `action_stop` rejects already stopped/exited with400, reads `docker_cleanup`,
  and dispatches StopService; `action_deploy` rejects running and queues start.
- [StopService source](https://raw.githubusercontent.com/coollabsio/coolify/b3ec26c3b1771654cae4b4ebc06d14a164bd8824/app/Actions/Service/StopService.php)
  marks applications exited after remote stop/remove; errors are caught, and
  remote processing uses `throwError:false`. This is why cached status is weak.
- [Start service](https://coolify.io/docs/api/endpoints/services/start-service-by-uuid),
  [list envs](https://coolify.io/docs/api/endpoints/services/list-envs-by-service-uuid),
  [update env](https://coolify.io/docs/api/endpoints/services/update-env-by-service-uuid)
  support the existing start and per-key GET/PATCH contract.

These are upstream semantics, not verification of the installed Coolify build.
Parent subsequently inspected the installed build; see the bounded findings in
[TC335_PARENT_ACCEPTANCE.md](TC335_PARENT_ACCEPTANCE.md). Parent must confirm
the installed version honors `docker_cleanup=false` and
the helper's actual host/queue/fence proof before rollout, and that the floor
entry survives compose parsing/restarts. No live API was used.

Transition: validate acceptance and exact target/floor; stop old service with
`POST /services/{uuid}/stop?docker_cleanup=false`; verify continuing exclusion;
advance/read back floor41; PATCH candidate image then SHA; verify exact readback
and fresh witness; start; require exact private HTTPS health/readiness revision.
No restart endpoint can silently run legacy coordinates. A floor PATCH error
or lost response stops at the barrier without starting anything. Coordinate
PATCH/start/readiness failure stops the failed candidate and selects only the
accepted recovery pair, rechecks floor/fence and verifies exact readiness.
Recovery never restores a database. Verified recovery still exits nonzero and
retains the original failure. Failed/uncertain recovery attempts stop and leave
operator inspection required; never fall through to a historical legacy image.
Loss of floor/configuration after startup immediately fails verification. A
stop-only fallback still validates the exact Coolify target but does not require
readable release metadata. It can request stop without certifying exclusion or
readiness, and can never PATCH coordinates or start any process.

Same accepted, healthy candidate at an activated floor verifies exact revision
without repeated mutations. Parent's continuing host interlock remains required
for this replay and after controller exit. It is not replaced by readiness.
