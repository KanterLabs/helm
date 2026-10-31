# Membership beta rollout, 2026-10-31

Beta is deployed and healthy at source revision
`f045958d6c31e9ca900c4704d166b5e9edc6ed71`, schema41. This was an explicitly
authorized native operator rollout through the existing Coolify service.
Automatic beta deployment remains paused. Production stays at
`c0f119cf330811389dfd38fbcbd7269dbf6680d5`, schema35, and passed readiness at
its original revision afterward. Production membership and the remaining
automatic-controller integration are not approved by this receipt.

## Target and exact artifacts

- Private URL: `https://beta-helm.home.shanekanterman.dev`.
- Coolify project/environment: `ucwlqka3uuzqw8bdvyx8zgxw` /
  `gcvnexqzy7q7eyi0groxzs6e` (`KanterLabs Private / staging`).
- Service: `helm-beta`, UUID `4pn0vrmau0g9nqaecsil0rxq`.
- Server: VM124, UUID `hqk4glwvzgixoh7puycadp5d`.
- Private origin: `10.0.30.21:18446`; existing Tailnet edge, TLS, native
  authentication, networks and ingress were preserved.
- Candidate image:
  `ghcr.io/kanterlabs/helm@sha256:b27ab13aedad3142c56c3a8ac4691627c2f289a6b01d9f4e4d9d840e6a20efa1`.
- Compatible recovery image:
  `ghcr.io/kanterlabs/helm@sha256:2545ed40948dd6017ede3b3b1817df7e644c2417c4b7905332f923ec3fd87020`,
  source `690620c9b9b4a037cf5aa2447cf29d8441a090c9`.
- Both executables hash to
  `09d798d863c19fd86f701c940b312a7162e037f72aace33073d09b2c43dfd355`.
  The images/source revisions differ, but this pair supports an
  authorization-compatible restart, not rollback to different application code.
  A distinct previous-UI recovery baseline remains a production gate.

Authenticated CI checkout/build/upload evidence, head/merge tree equality,
archive hashes, every OCI blob, and the public registry manifest/configuration
were verified independently. Runtime artifacts came from the already-green
candidate/recovery builds. Operator source was separately installed from
`d75bdeb95292b4e35e23e54f47782c716afbd592`, with root-private pinned hashes under
`/opt/helm-admission/d75bdeb95292`.

Secrets stayed in the existing runtime files and Infisical references:
`homelab/lab:/apps/helm/beta/COOLIFY_API_TOKEN`. No value is recorded here.
All config/TLS/secret file bytes matched the pre-upgrade snapshot afterward.

## Acceptance and preservation

Both exact images ran on independent, privately held copies of the actual
schema35 and schema40 databases. All320 real HTTP exchanges and4 current-data
recovery/rollforward exchanges passed, including roles/grants, zero-access
humans, pending/disabled identities, scope intersections, aggregate visibility,
configured-email demotion and cookie reset/revocation. Original input hashes
remained unchanged. Sanitized22-proof bundle checksum:
`be9fa4e27a9d49ac8fad9263df1723ebe6ba89bae7ff98b798cf38558eb94aa2`.
The existing Acceptance reader validated the complete bundle and exact target.
These receipts have bounded validity; renew reviewed acceptance before a future
transition rather than trusting an expired bundle.

The actual app-host kernel6.12 passed14 metadata and30 native operator events
using the measured candidate executable. This complements the DEV evidence;
it is not a claim that the read-only CI observer creates a fence.

The shared backup timer was paused while the service was stopped and metadata
changed; it was restored active/enabled afterward. Trusted root maintenance
must be quiescent during future metadata transitions too.

Final exact offline data/config snapshot:
`/srv/helm/beta/backups/membership-20261031-bc5b91413a43-preflight`.
It covered10,224 objects and155,643,507 regular bytes, with content digest
`9a559a5bfe3685ac90962163120865374761aad7b258cf1137ce237e4317ab7d`.
Snapshot SQLite integrity/FKs were checked on a disposable copy. The source and
snapshot databases were never restored or replaced.

Three root-owned files from a retired agent smoke test initially prevented
the strict ownership preflight. They were retained outside app data under
`/srv/helm/operator/membership-20261031-bc5b91413a43/retained-operator-smoke-files`,
preserving their bytes, inodes, ownership, modes and mtimes. The earlier snapshot
was also retained; no user task, history or credential was deleted.

After live migration, original values across50 tables and all141 tasks were
verified against a disposable copy of the exact offline snapshot. Persistent
request counters legitimately increased with live traffic and were checked
for monotonic preservation. Integrity/FKs passed. All original humans have
explicit membership; the configured owner remains owner. Data directory inode
8129 and database inode41895 on device2051 were preserved.

Post-upgrade backup was independently verified at
`/srv/helm/beta/backups/20261031T233603Z`, schema41,141 tasks, integrity/FKs clean:
database SHA256 `20f6be022ad231cde370568961d40293381a7b55ce96921af4940bbaa803d170`;
runtime archive SHA256 `0ca11285d595a905d9089418a381b916022dede4b3d44eb6a0448c415f02da6b`.

## Enforced runtime and verification

The literal protected profile uses UID/GID70541, no added capabilities,
no-new-privileges, a read-only rootfs, the reserved admission entrypoint and
a read-only authority DIRECTORY bind. Coolify's raw and parsed profiles and
the actual Docker process matched. Native inspection verified the executable
hash, private0700/0600 data modes, original inodes and application-held permanent
exclusive lease. Authority is active at generation3, minimum authorization41;
the immutable original activation and accepted pair are retained.

The canonical private health/readiness endpoints returned the exact candidate
revision. Unauthenticated, invalid-bearer and forged-identity API requests were
denied401. Public DNS/IP with normal TLS served only the connection guide503
for document navigation and403 for API access. No public application route or
new client grant was added.

Actual human Tailnet browser sign-in was not exercised by the operator client.
The configured owner record, shared identity-to-membership backend and preserved
signing/TLS material were checked; this does not substitute for that browser
session. External integrations were preserved but not actively exercised.

Sanitized evidence is under `.artifacts/beta-membership-rollout/` and
`.artifacts/tc336/accepted-beta-20261031-r3/`. Private validation inputs/work
databases were kept outside artifact directories and removed after all324
sanitized exchanges were retained in bounded files.
Historical images, live data and all actual backup snapshots remain retained.

## Next deployment

Use the [native operator protocol](TC335_OPERATOR_TRANSITIONS.md), independently
read-back floor41 and newly reviewed exact artifact acceptance. Stop through
Coolify, pause trusted metadata maintenance, close/rebind under the permanent
lease, require complete native observations, then activate the accepted role.
Recovery keeps current data and the monotonic floor; never restore an older
database or permit a pre-membership image to serve it. Keep automatic deployment
paused until the separately pinned controller/witness workflow is implemented
and accepted. The installed CI observer still reports unknown.
