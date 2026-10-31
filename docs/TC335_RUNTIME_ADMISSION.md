# Private runtime admission, source stage

Implemented in `internal/deployadmission` and invoked at the beginning of
`cmd/helm/main.go`, before configuration, migrations or database-touching CLI
handling. This is one source-stage safety boundary, not rollout acceptance.
The installed observer still returns unknown; beta and production are unchanged.

Whenever `/var/lib/roadmap/data` exists, startup requires admission. This is
independent of environment enable flags and applies through either image path:
`/usr/local/bin/helm` or the reserved `/usr/local/bin/helm-admission`. The latter
contains the same binary; retained legacy images lack that reserved path.
Healthcheck and embedded migration-info do not touch data or take the lease.
Other CLI commands require admission when the protected mount exists. Public
usage without that mount preserves its existing behavior.

Linux private runtime requires real/effective UID/GID70541, only group70541,
zero permitted/effective capabilities and NoNewPrivs1. The data directory is
70541:70541 mode0700 with an existing regular roadmap.db mode0600. Authority
at `/etc/helm-admission` is a read-only directory mount, root:70541 mode0750;
marker.json and permanent runtime.lock are regular root:70541 mode0640.
Final symlinks, unavailable observations, unknown formats and writable authority
fail closed. Host ancestors, alternate mounts and the actual Compose profile
remain externally verified deployment responsibilities.

Marker v1 is bounded to4096 bytes and has exactly these fields:

| Field | Required value |
| --- | --- |
| format / minimum_authorization | 1 / 41 |
| phase / lane | active / beta or production |
| generation | positive uint64 |
| operation_nonce | 64 lowercase hex characters |
| data_device / data_inode | exact protected directory identity |
| lock_device / lock_inode | exact permanent lock identity |
| binary_sha256 | SHA256 of the running executable via /proc/self/exe |
| role | candidate or recovery |
| image | ghcr.io/kanterlabs/helm@sha256: plus64 lowercase hex characters |
| release_sha | 40 lowercase hex characters |

Duplicate, missing, unknown, case-aliased, null or malformed fields are rejected.
The binary hash is checked against actual bytes. The image and release fields
are coordinates supplied by trusted external authority: this process cannot
self-attest the entire OCI image or prove those coordinates are running.

The process acquires an exclusive nonblocking flock through a read-only,
CLOEXEC descriptor on the permanent lock, reads the marker under that lock,
checks identities and executable hash, and retains the lease until server exit.
The database closes before the deferred lease release. Runtime never creates,
changes or replaces authority. Main's retained lease and duplicate exclusion
are verified by actual Helm startup, healthcheck and restart journeys.

## Evidence

`test/deployment-policy/runtime_admission_e2e.py --binary PATH` runs a labeled
DEV hybrid fixture: a built Helm binary mounted into an exact retained base
image, synthetic populated SQLite, no network or ports. It is not an accepted
candidate image. It captures bounded receipts and cleans only its own containers.

- RED: `.artifacts/tc335/runtime-journey/helm-admission-e2e-90fdf838d0/receipt.json`
  showed nine missing rejection boundaries plus missing duplicate exclusion;
  public startup already passed. Old-profile startup actually changed the
  synthetic database, demonstrating why the early guard matters.
- GREEN: `.artifacts/tc335/runtime-journey/helm-admission-e2e-3068678e5b/receipt.json`
  passes all11 cases. Rejections preserve database bytes; admitted/public runs
  preserve stable synthetic IDs, foreign keys and integrity. Admitted health,
  duplicate rejection and lease reacquisition pass. No owned containers remain.
- Parent `go test ./cmd/helm ./internal/deployadmission` passes. Worker focused
  race/vet and Darwin stub compilation evidence is under
  `.artifacts/tc335/runtime-admission/`.

Separate actual kernel probes at `/tmp/helm-legacy-writer-proof/` prove that
writable descriptors and shared mappings with closed descriptors still change
synthetic bytes after ownership changes. Fresh opens are denied. A complete
legacy process/FD/mapping drain is therefore still required before activation.

## Still required

No authority bootstrap, privileged initializer, observer signing service,
guarded Compose activation, paired artifact acceptance or workflow integration
is provided by this stage. Private deployment of this binary intentionally
fails without authority; do not enable rollout around the check. Complete the
operator bootstrap/drain and persistent interrupted-operation protocol, exact
profile/image cross-product tests, child/process-tree lifecycle proof and
TC-336 exact candidate/recovery preservation gates first. Never restore a live
database automatically or delete a retained artifact to make admission pass.
