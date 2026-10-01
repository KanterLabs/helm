# Helm release CI

Release CI is a manual, immutable promotion path for Helm `v0.1.1`. The
workflow packages one exact commit into both Linux archives, Debian/RPM
packages, and per-platform OCI outputs, validates the downloaded artifacts,
and only then publishes through the selected forge adapter. A later tag push may be added
after the manual path has been exercised; this workflow has no tag trigger and
never force-moves a tag. If publication creates a missing release tag, the
shared tool targets the planned commit explicitly.

## Failure cases and required evidence

The release path is accepted only when each failure case below is covered by a
failed command or an explicit required job result. The lower-level artifact
cases are also recorded in [`ci/release-failure-cases.md`](../ci/release-failure-cases.md).

| Case | Failure prevented | Required evidence or fail-closed behavior |
| --- | --- | --- |
| CI-1 | A fork or an untrusted ref runs a privileged release job. | The workflow requires the canonical repository, the protected release ref, and a full immutable commit SHA before any build or credential is available. |
| CI-2 | The requested version, release configuration, and source commit disagree. | The plan job checks semantic version text, the checked-in release metadata, and `git rev-parse HEAD`; a mismatch stops the run. |
| CI-3 | A mutable tag is silently moved to a different commit. | Existing tags are read before publishing and are accepted only when they already resolve to the planned commit; a different target fails. |
| CI-4 | One architecture contains the other architecture's executable. | Native amd64 and arm64 jobs run the downloaded artifact harness, including ELF and package metadata checks, and the release gate requires both results. |
| CI-5 | A package or archive is absent, empty, or not represented in the release receipt. | The artifact bundle is checked against the complete v0.1.1 matrix: two archives, four native packages, and two OCI platform manifests. Missing files or receipts fail the gate. |
| CI-6 | A package passes file inspection but the service cannot start, serve the embedded UI, or preserve SQLite state. | `ci/test-release-artifacts.sh` downloads the archive, starts the amd64 binary without optional companions, probes health/readiness/UI, and checks upgrade/removal state preservation. |
| CI-7 | An OCI image has the wrong target or helper binary. | Each build parses its OCI receipt, requiring the requested version, one matching Linux platform, and the named non-empty OCI archive; the release gate requires both platform receipts. |
| CI-8 | Build output from one commit is published as another commit. | The source SHA is carried from plan through every build, gate, and publisher invocation; the publisher rejects a receipt whose commit differs. |
| CI-9 | A network failure leaves a release half-published or a retry duplicates it. | The shared publisher writes a durable per-artifact receipt and retries each artifact independently. Existing matching digests are idempotent; conflicting names or digests fail without overwrite. |
| CI-10 | A failed validation can be bypassed by publishing directly from a build job. | Only the publisher job has `contents: write`/`GITEA_RELEASE_TOKEN`; every publisher step depends on the complete release gate and re-runs the shared receipt validation. |
| CI-11 | A broad or personal access token leaks into a build job. | Build and validation jobs receive no publish credential. Gitea maps only the repo-scoped built-in `GITEA_TOKEN` to `GITEA_RELEASE_TOKEN` in the publisher job; GitHub uses its job-scoped `GITHUB_TOKEN`. |
| CI-12 | GitHub and Gitea both publish the same release. | The public GitHub mirror is a no-op when `CANONICAL_RELEASE_FORGE=gitea`; the canonical Gitea adapter is a no-op otherwise. A non-canonical fork is rejected before this selector is honored. |
| CI-13 | Gitea cannot transfer an artifact because the instance has imported actions disabled. | The Gitea adapter uses its built-in checkout and separately pinned upload/download Gitea forks with the v3 API for the ephemeral bundle. No imported GitHub action is referenced. |
| CI-14 | A long build consumes the short runner budget or a publish hangs indefinitely. | Metadata and publish jobs run on `homelab` with a ten-minute timeout; native builds and runtime E2E run on `homelab-heavy` with a twenty-five-minute timeout. |
| CI-15 | RPM coverage is overstated. | The workflow validates RPM metadata and native package contents. It does not claim Fedora runtime coverage until the parent-wired Fedora harness is ready. |

## Source and forge rules

`packaging/release.json` is the version and artifact naming source. The
checked-in `make release-*` product builders create the archive, native
package, and OCI bytes. The vendored shared release tool and its lock file
generate and verify the release manifest, then reconcile publication; workflow
YAML supplies the forge-specific runner, identity, artifact transfer, and
credential boundary.

The manual dispatch accepts an exact 40-character commit and a semantic
version (defaulting to `0.1.1` for this release). It must run from the
canonical protected release branch. The plan records the commit, version, and
selected forge, then every job checks out that commit. A tag can be introduced
later only if it points at this same commit.

GitHub is the public mirror and uses immutable action pins. Gitea 1.27.3 is the
canonical executor when `CANONICAL_RELEASE_FORGE=gitea`; its workflow avoids
GitHub imported actions and uses separately pinned Gitea-compatible artifact
forks with the v3 API for the short-lived bundle. The Gitea publisher maps the built-in, repository
scoped `GITEA_TOKEN` only in the publisher job as `GITEA_RELEASE_TOKEN` and
grants that job `contents: write`. No job accepts a PAT or broad credential.

The existing `.github/workflows/ci.yml` required checks and deployment lock
remain in force. Its production mutation job honors `[skip deploy]` in the
commit message, so release-CI integration commits can run the required checks
without starting a deployment. Release publication is a separate manual path
and never invokes deployment.

## Job shape

Both adapters use the same phases:

1. **Plan** on `homelab`: validate the canonical repository/ref, resolve the
   exact commit and requested version, and write a bounded source receipt.
2. **Build** on `homelab-heavy`: invoke the product release builders for amd64
   and arm64 archives and OCI outputs, invoke the native package adapter on each
   matching builder, and bundle the complete output with checksums and source
   metadata.
3. **Validate** on `homelab-heavy`: download the bundle through the forge
   artifact store, run `ci/test-release-artifacts.sh` with no partial allowance,
   inspect OCI platform receipts, and invoke the shared manifest/verify
   commands.
4. **Publish** on `homelab`: re-download the exact validated bundle and call
   the shared `ensure-release` command. It runs only after every matrix target is
   green and is the sole job with write permission.

The two native package builders are intentionally architecture-specific. A
builder may produce only its own Debian/RPM pair; the final gate joins both
bundles and rejects an incomplete matrix. The OCI job produces one platform
archive and receipt per target, and the shared tool records their exact bytes.
The archive and package checks are consumer-facing downloaded-artifact checks,
not checks of a local build directory.

## Operator procedure

Dispatch `Release` from the protected canonical branch with:

```text
release_ref: <40-character commit SHA>
version:     0.1.1
publish:     false   # validation rehearsal
```

Review the plan receipt, downloaded-artifact evidence, and release-gate result.
Run the same dispatch with `publish: true` only after the rehearsal is green.
The publisher is retry-safe: a failed run may be re-run with the same source
commit, while a tag or artifact name pointing at another digest remains a hard
failure. Publishing does not deploy Helm or alter the existing CI/deployment
workflow.

Use `[skip deploy]` in the merge commit title for packaging-only changes. All
required main checks still run, while the automatic production mutation is
skipped. A normal manual deploy dispatch remains a separate explicit action.
