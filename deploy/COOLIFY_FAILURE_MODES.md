# Coolify deployment failure modes and acceptance

Original deployment inventory, extended by TC-335. Deployment changes the
selected service's image/revision and monotonic authorization floor; it never
restores a database or modifies persistent mounts. Runtime authorization
acceptance is required for both selected artifacts. See the pre-implementation
[TC-335 inventory](../docs/TC335_FAILURES.md) and
[receipt/stop-witness contract](../docs/TC335_POLICY_CONTRACT.md).

| Failure | Required behavior |
| --- | --- |
| Missing token, target, invalid digest/SHA, or non-HTTPS private origin | Fail before mutation; never print credentials. |
| Redirect, oversized/malformed response, HTTP/API failure | Refuse redirects and bound responses; report only operation/status. |
| Wrong service UUID/name, environment membership, server UUID, or unexpected extra application/database | Fail before mutation. |
| Previous image/revision absent, mutable, hidden, duplicated, or malformed | Fail before mutation; bootstrap an accepted immutable release first. |
| Remote branch advances while validation runs | Skip deployment with a successful stale-run result before mutation. |
| Missing/stale/malformed/mismatched runtime acceptance | Reject before any mutation, including first041 activation. |
| Missing/hidden/duplicate/unknown floor | Reject; never infer zero or lower an activated floor. |
| Old writer, surviving/restart-eligible container, queued operation or uncertain stop witness | No floor advance/start; cached exited/queued response is insufficient. |
| Floor PATCH fails or its response is lost | Keep stopped; do not start any image or revert the floor. |
| Image/SHA update partly commits or response is lost | Stop/prove exclusion, select only the accepted recovery pair, retaining current data/floor. |
| Coolify reports an old container healthy | Require private `/healthz` and `/readyz` to return requested revision; retry within bounded timeout. |
| Candidate start or private TLS/DNS/readiness fails | Stop failed candidate, verify exclusion, then verify exact accepted recovery revision. Failure remains visible. |
| Recovery also fails, or interrupted worker leaves uncertain state | Attempt stop and fail visibly; operator inspection required. Never select legacy code, restore data or prune artifacts. |
| Credentials/private content in deployment responses | Capture allowlisted status/revision fields only; never save full API bodies. |
| Legacy beta candidate workflow dispatch | Fail with migration explanation; no LXC controller mutation. |

## Hosted runner compatibility exception

All jobs retain the repository's intentional hosted-runner cutover (`92615c2`)
on `ubuntu-24.04`. The live `ci-runner-partition-arc-cap` admission policy
forces all seven retired runner pools, including `homelab-heavy`, to zero
minimum and maximum replicas; ARC controllers are deliberately scaled to zero.
Reactivating runners requires the separate `single-job20261011B1H1` platform
migration and cannot be repaired by changing this repository's runner label.

Each protected deployment job therefore uses the existing digest-pinned
Tailscale action with `TS_OAUTH_CLIENT_ID`, `TS_OAUTH_SECRET`, and
`tag:gha-helm-deploy`. The action supplies `--accept-routes` itself. Existing
private Helm lane ACL access and narrowly scoped Coolify API access let these
jobs verify the same private origins without exposing services publicly.

## Protected environment contract

Each GitHub environment (`beta`, `production`) supplies variables
`COOLIFY_API_URL` (HTTPS URL ending `/api/v1`), `COOLIFY_PROJECT_UUID`,
`COOLIFY_ENVIRONMENT_UUID`, `COOLIFY_SERVICE_UUID`, `COOLIFY_SERVICE_NAME`,
`COOLIFY_SERVER_UUID`, and `HELM_SITE_URL` (the existing private `.home` HTTPS
origin). It supplies secret `COOLIFY_API_TOKEN`, with `read`, `read:sensitive`,
`write`, and `deploy` abilities. Sensitive read is necessary to recover prior
service environment values; complete service responses must remain private.
Coolify tokens are team scoped; exact target validation and GitHub environments
are additional boundaries, rather than token-level resource isolation.

CI supplies `HELM_IMAGE=ghcr.io/kanterlabs/helm@sha256:<64 lowercase hex>`,
`HELM_RELEASE_SHA=<40 lowercase hex>`, and `HELM_DEPLOY_EVIDENCE=<artifact path>`.
The service must already contain readable, unique `HELM_IMAGE`,
`HELM_RELEASE_SHA` and controller-owned `HELM_AUTHORIZATION_FLOOR` entries.
Protected membership acceptance/recovery/observer inputs and pending workflow
wiring are specified in [TC-335's handoff](../docs/TC335_HANDOFF.md).
The compose service named `helm` uses `${HELM_IMAGE}` and explicitly injects
`HELM_RELEASE_SHA: ${HELM_RELEASE_SHA}`. Runtime owner configuration remains in
the protected host env file, excludes image/revision overrides, and is never
copied to CI. Persistent lane paths and origin authentication remain host-owned.

Replacement explicitly stops the old service using documented
`POST /services/{uuid}/stop?docker_cleanup=false`, requires an independent
continuing-exclusion witness, then advances the floor before any candidate
start. Stopped services use `POST /services/{uuid}/start`. Coolify returns queued
messages; parent must verify installed stop semantics and the real host
interlock before activation. Poll service health and the real route.

## Repeatable acceptance on the real private route

1. Run the complete beta workflow; download `helm-coolify-beta-<SHA>` and require
   sanitized deployment evidence with the published digest, exact service UUID,
   requested SHA, Coolify healthy status, and both route revisions matching SHA.
2. Confirm production still has its original digest, UUID, and revision.
3. Before any live drill, complete TC-336 artifact acceptance on isolated
   populated copies and parent verification of the actual host stop/fence
   integration. An unavailable/unaccepted digest must reject before mutation.
   Accepted-candidate failure must recover only the accepted membership pair
   against current data and remain a failed job. Live drills require separate
   parent rollout authorization; these source tests perform no live actions.
4. Recheck existing private origins and auth. No public DNS, origin exposure,
   restored DB snapshot, or secrets in the artifact is needed.

Compile the Python helper and run actionlint before enabling the workflow.
A compile/syntax check does not substitute for the real deployment acceptance.
