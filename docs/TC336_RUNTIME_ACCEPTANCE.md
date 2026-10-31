# TC-336 retained-image runtime acceptance

The focused producer is `test/deployment-policy/runtime_acceptance.py`.
It starts the two retained CI image IDs unchanged on isolated populated copies,
using their normal entrypoint and nonroot image user. There are no host ports.
The Docker bridge is internal; requests run through a tiny static HTTP client
inside each container's loopback namespace. The client does not replace the
application executable. Disposable local credentials and a disposable HTTPS
JWKS/RS256 issuer exercise real authentication rather than mocked app responses.

Failure inventory: [TC336_RUNTIME_FAILURES.md](TC336_RUNTIME_FAILURES.md).
This producer is separate from deployment: it does not PATCH Coolify, publish
images, mutate source inputs, write protected lane settings or start live code.

## Exact artifact provenance

`runtime_acceptance_provenance.py` independently reads authenticated GitHub run,
artifact and Git-object metadata, verifies the downloaded artifact ZIP digests,
checks every OCI blob digest, examines the checks binary's embedded VCS fields,
and ties the container CI log's checkout/build/upload to those same bytes.
Image labels are not the source authority. PR runs use merge commits:

| Role | Requested head | Actual PR checkout | Exact equal Git tree |
| --- | --- | --- | --- |
| Candidate | `f045958d6c31e9ca900c4704d166b5e9edc6ed71` | `bbed78128e35fa74117c334329c0f996ed99d251` | `6023eab9932286495da1c6e52e3e8bc1e10d4aea` |
| Recovery | `690620c9b9b4a037cf5aa2447cf29d8441a090c9` | `2ae94304beb445245d9c543558b0274c2a9dda33` | `5ebbf0beec579b9114fd33de1ac790660411d611` |

CI run IDs are41238074080 and41227731125. Their image archives are respectively
`helm-bbed78128e35fa74117c334329c0f996ed99d251.tar.gz` and
`helm-2ae94304beb445245d9c543558b0274c2a9dda33.tar.gz`.

The immutable registry manifest coordinates, independently matching the archives:

- Candidate: `ghcr.io/kanterlabs/helm@sha256:b27ab13aedad3142c56c3a8ac4691627c2f289a6b01d9f4e4d9d840e6a20efa1`
- Recovery: `ghcr.io/kanterlabs/helm@sha256:2545ed40948dd6017ede3b3b1817df7e644c2417c4b7905332f923ec3fd87020`

The corresponding OCI config digests are56154c78495c93b69586ed6f9c08a5609f7df14f48ec7c106c452e4f4e86816a
andc2792cccf4725a4ea90d0585d257286b76f5ef75ed5f82471e4ed0ca14c09f15.
Docker's containerd image store exposes manifest digest as `inspect.Id` here;
it must not be described as the config digest.

Both retained image Helm executables have identical bytes with SHA256
`09d798d863c19fd86f701c940b312a7162e037f72aace33073d09b2c43dfd355`.
The images and full source revisions remain distinct. Checks executables contain
PR merge VCS revision fields; image executables have no VCS fields because
`.dockerignore` excludes `.git`. Checks executables therefore have different
hashes. The trusted GitHub checkout/build/upload chain and equal Git trees bind
image source; an environment release SHA or image label alone would not.

PR CI skips GHCR publishing. The operator can tag the already verified loaded
image and push it unchanged, then independently GET/hash the registry manifest
and verify its config digest against the retained OCI archive. Rebuilding from
source, inventing a digest from the config hash, or accepting a mutable tag does
not preserve this artifact identity. Existing historical images remain retained.

## Running safely

Use a private output directory outside uploaded artifacts when the inputs
contain real user data. Supply both independently captured schema copies:

```sh
python3 test/deployment-policy/runtime_acceptance.py \
  --candidate sha256:b27ab13aedad3142c56c3a8ac4691627c2f289a6b01d9f4e4d9d840e6a20efa1 \
  --recovery sha256:2545ed40948dd6017ede3b3b1817df7e644c2417c4b7905332f923ec3fd87020 \
  --candidate-sha f045958d6c31e9ca900c4704d166b5e9edc6ed71 \
  --recovery-sha 690620c9b9b4a037cf5aa2447cf29d8441a090c9 \
  --schema35 /PRIVATE/schema35.sqlite --schema40 /PRIVATE/schema40.sqlite \
  --output /PRIVATE/tc336-run
```

Inputs are opened SQLite read-only and backed up into fresh per-role/schema
copies. The original input files never become runtime mounts. The private parent
directory is0700; application-copy access is limited to the invoking group and
image UID65532. The only added group grants that private fixture access. Live
configuration, integration token files, account directories and credentials must
never be mounted. The parent must confirm captured database fields contain no
live plaintext outbound integration secrets before providing copies. Existing
credential hashes and task/event relationships are compared only in memory.

For each image and prefix the producer checks existing rows, credentials,
claims, history, memberships, SQLite integrity and foreign keys. Migration036's
documented column position/version/timestamp changes are allowed for schema35;
all other retained values must survive. HTTP last-seen timestamps are the only
other omitted volatile fields. New columns and additive rows are allowed.

Real HTTP checks cover restricted/zero-grant humans, verified pending and
disabled identities, configured-email demotion, human/agent bearer ceilings,
aggregate hiding, active legacy and new cookies, explicit revoke, reset and
disable/reactivation. Recovery then writes via HTTP on the migrated current
copy; candidate starts on that same file and must read the write while all
membership/grant/history rows remain. No database restore occurs.

Diagnostic runs cannot authorize deployment. With no schema inputs the harness
creates explicitly labelled generated populated fixtures. The bounded exporter
`runtime_acceptance_bundle.py` rejects those runs. After reviewing a complete
private-copy run, provenance and target, it emits22 checksum-bound proof files
and a root accepted by the actual `rollback_policy.Acceptance` reader. Each
exchange comes from the exact subject runtime. Authorization proof projections
use the schema40 repetition; complete local diagnostics retain both prefixes.
Proof files use only status codes, error codes, zero-grant collection counts,
forbidden-project-presence booleans and synthetic recovery keys. Even failed
zero-grant responses never copy private collection rows into evidence.

## Verification status

Final private-copy run `tc336-private-final-20261031-r3` passed both exact
images on independently captured schema35 and schema40 databases. All320
actual HTTP exchanges and4 same-current-data recovery/rollforward exchanges
passed. Existing values across41/50 original tables survived; integrity and
foreign keys passed and both original input hashes remain unchanged. Strong
aggregate checks scan every ungranted actual project ID privately and emit
only booleans/counts. No user rows, passwords, cookies or bearer values enter
sanitized evidence.

The reviewed22-proof bundle is under
`.artifacts/tc336/accepted-beta-20261031-r3/`, with checksum
`be9fa4e27a9d49ac8fad9263df1723ebe6ba89bae7ff98b798cf38558eb94aa2`.
The parent independently validated the real Acceptance reader, input hashes,
OCI manifests/configuration and the producer code. Disposable private input/work databases were removed after exporting all324
sanitized exchanges into bounded files under `.artifacts/tc336/final-real-copy-exchanges/`. Live profile/authority installation and private
identity/readiness remain separate operator gates; these tests alone do not
claim deployment.
