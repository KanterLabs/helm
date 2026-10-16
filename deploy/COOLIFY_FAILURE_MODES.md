# Coolify deployment failure modes and acceptance

Written before implementing the deploy helper. Deployment changes the selected
service's image and revision only; it never restores a database or modifies a
persistent mount. Schema compatibility with the retained image is a release
requirement, and binary rollback cannot undo a database migration.

| Failure | Required behavior |
| --- | --- |
| Missing token, target, invalid digest/SHA, or non-HTTPS private origin | Fail before mutation; never print credentials. |
| Redirect, oversized/malformed response, HTTP/API failure | Refuse redirects and bound responses; report only operation/status. |
| Wrong service UUID/name, environment membership, server UUID, or unexpected extra application/database | Fail before mutation. |
| Previous image/revision absent, mutable, hidden, duplicated, or malformed | Fail before mutation; bootstrap an accepted immutable release first. |
| Remote branch advances while validation runs | Skip deployment with a successful stale-run result before mutation. |
| Environment update partly commits or response is lost | Treat mutation as started before PATCH; restore both prior values. |
| Coolify reports an old container healthy | Require private `/healthz` and `/readyz` to return requested revision; retry within bounded timeout. |
| Coolify restart/pull/start or private TLS/DNS/readiness fails | Restore previous image and revision; redeploy and verify actual previous revision. |
| Rollback also fails, or interrupted worker leaves uncertain state | Fail visibly; inspect service before retry. No database restore or automated cleanup. |
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
The service must already contain readable, unique `HELM_IMAGE` and
`HELM_RELEASE_SHA` environment entries pointing to its accepted release.
The compose service named `helm` uses `${HELM_IMAGE}` and explicitly injects
`HELM_RELEASE_SHA: ${HELM_RELEASE_SHA}`. Runtime owner configuration remains in
the protected host env file, excludes image/revision overrides, and is never
copied to CI. Persistent lane paths and origin authentication remain host-owned.

Running services use documented `POST /services/{uuid}/restart?latest=true`;
stopped services use `POST /services/{uuid}/start`. Installed Coolify rejects
`/start` on running services, and service operations return queued messages
rather than application deployment UUIDs. Poll service health and the real route.

## Repeatable acceptance on the real private route

1. Run the complete beta workflow; download `helm-coolify-beta-<SHA>` and require
   sanitized deployment evidence with the published digest, exact service UUID,
   requested SHA, Coolify healthy status, and both route revisions matching SHA.
2. Confirm production still has its original digest, UUID, and revision.
3. During a controlled beta drill, request a deliberately unavailable digest with a
   synthetic revision. Require deploy failure and
   evidence showing rollback verified the original digest and revision on both
   real endpoints. This deliberately exercises real service restart and route
   verification; never perform it on production merely to test the helper.
4. Recheck existing private origins and auth. No public DNS, origin exposure,
   restored DB snapshot, or secrets in the artifact is needed.

Compile the Python helper and run actionlint before enabling the workflow.
A compile/syntax check does not substitute for the real deployment acceptance.
