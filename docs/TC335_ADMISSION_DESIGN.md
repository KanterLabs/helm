# Proposed application-owned deployment admission

Revised after independent review; not accepted for rollout. PR58 at
189e5ae has green required checks. The installed metadata collector still
returns unknown. Live beta/schema40 and production/schema35 are unchanged.

## Evidence and scope

Actual DEV Docker tests used both exact retained beta/prod image digests and
synthetic populated SQLite data. Both images default to65532:65532. Owner70541,
directory0700 and DB0600 deny their fresh read/write opens. UID0 with all
capabilities dropped also fails. Explicitly forcing a legacy image to70541
bypasses DAC, and an already-open legacy descriptor survives chown. SQLite
bytes, IDs, relationships and integrity were preserved. These are kernel
observations, not candidate runtime or production acceptance.

Evidence: `/tmp/helm-data-identity-proof/run-fdc5e7b5591c/summary.json` and its
failure inventory, runner and limitations. A separate installed-source trace
is under `/tmp/helm-start-permission-discovery/`. Normal starts do not infer
an image UID and chown populated data to it. Non-root SSH command rewriting
can recursively chown Coolify workdirs. The actual server uses non-root SSH
user `shane`; protected data/config binds are outside those workdirs under
`/srv/helm`. Both live data directories are65532:65532 mode0750; config is
65532:65532 mode0700. These directories are not symlinks and have no extended
ACL. Config files have private0400/0600 modes. LocalFileVolume type/ownership
flags still require exact verification before relying on automatic starts.

Protect supported Helm deployment/recovery, including stale queued starts and
controller interruption. Deliberate privileged host/Docker/Coolify bypass and
an already compromised application are outside this boundary. Do not patch
Coolify globally, add a Docker authorization daemon, delete historical images,
restore data, or grant CI a general host command to implement this proposal.

## Decisions after independent review

The review at `/tmp/helm-admission-review/findings.md` rejects activation from
the original proposal. Its composite delayed-start/open-descriptor risk is
credible; writable descriptors and mappings need actual proof. A signed
snapshot alone never creates continuing exclusion.

Implement the runtime boundary first, without any live setup or activation.
Embed the check in Helm itself, before configuration or database-touching CLI
dispatch, whenever the reserved private mount exists. No environment enable
flag, default entrypoint or old Compose profile can omit this check. Exempt
only nonmutating healthcheck and embedded migration-info. Public deployments
without the reserved mount keep their normal behavior. Hold a CLOEXEC lease
in the server process itself, removing helper-exec descriptor inheritance.
Child-writer/drain behavior remains an explicit external acceptance gate.

Remove automatic first bootstrap from queued initialization. Establish the
permanent root-owned authority/lock once through the existing operator path,
with explicit durable identity and resumable closed state. That operator action
still needs a bounded implementation, fresh backup and data-preservation proof;
it has not happened. Later initialization must refuse absent/lost authority.
The approved CI SSH observer remains read-only. Do not infer permission for a
new general host-write channel or introduce a signing service as installed fact.

Before any candidate image selection, both the integrated runtime check and
the private identity transition must exist, followed by complete legacy access
drain. Test the full image/profile cross product. Candidate under an old profile
must refuse before migrations even during bootstrap; old binary under the new
profile must fail its missing reserved entrypoint. Any unproved gap stays closed.

## Integrated guard and fixed protected profiles

Add `/usr/local/bin/helm-admission` as a second copy of the same static Helm
binary in accepted images. Its admission code also runs through the ordinary
`/usr/local/bin/helm` entrypoint. Public image defaults remain unchanged. The
protected runtime profile explicitly
uses that entrypoint, UID/GID70541, all capabilities dropped, read-only rootfs,
no-new-privileges and fixed literal directory binds. Legacy images lack the new
entrypoint and cannot start through this profile. Historical profiles run65532
and cannot open data after the identity transition. A user override alone is
NOT evidence of safe code; the forced guard entrypoint and admission check are
mandatory together.

Helm verifies its actual executable hash against the admitted role before any
database access. The controller/host still verify the
exact immutable OCI digest and source SHA externally; an in-image binary hash
must not be described as self-attestation of an entire OCI image. Require an
accepted admission capability and measured binary hash in the TC-336 bundle. Reject
unknown guard/floor formats. Do not trust only mutable release environment labels.

The reserved private data mount `/var/lib/roadmap/data` always requires guard
state, even when an old environment omits an enable flag. Missing state there
must never fall back to public/unfenced startup. Public default `/data` use must
remain compatible; the guard must not silently migrate protected data through
an old profile before the barrier exists.

## Root-owned persistent authority and exclusive lease

Use a separate host directory `/srv/helm/<lane>/security`, mounted into runtime
read-only. Proposed ownership: root:70541 directory0750, marker/lock0640. The
application can read authority but cannot replace it. Mount the DIRECTORY, not
a marker file whose bind would retain an old inode across atomic replacement.

The marker binds exact lane/target/data device+inode, minimum authorization41,
format/guard version, monotonic generation, operation/nonce, accepted pair and
measured executable hash, desired role, phase, observer trust binding and
the permanent lock inode. Atomic writes preserve the original activation;
unknown/missing/decreased state never becomes zero. Never replace the lock file.

Runtime acquires an exclusive nonblocking flock on that permanent lock, checks
the marker, then holds the read-only CLOEXEC descriptor within Helm. This
enforces one guarded runtime even if duplicate current-image jobs execute.
Verify actual Go finalizer lifetime and child process behavior; descendants
must not inherit the lease after exec. Health checks
are read-only commands and must not compete for the server's exclusive lease.
Direct CLI/bootstrap variants and unexpected arguments need explicit treatment.

## Narrow root initializer through the existing deployment platform

Prefer two exact execution profiles for the SAME existing one-app service.
Initialization uses the same accepted image and guard entrypoint, UID0,
no network/ports, restart=no, disabled application health check, read-only
rootfs, no-new-privileges and only the filesystem capabilities needed for
ownership/mode changes (provisionally CHOWN/FOWNER/DAC_OVERRIDE). It has
RW data/config/security binds; runtime has RW data, RO config/security. The
initializer never invokes Helm, opens its database for mutation, executes
caller-selected commands, or serves application traffic. Legacy images cannot
execute this entrypoint. CI SSH access remains read-only.

The controller switches exact raw Compose between initialization and runtime
with instant_deploy=false and checks the parsed result before starting it.
Validate canonical protected fields and actual parsed profile; do not accept
arbitrary user/group/entrypoint/capability or bind interpolation. Current
one-app target validation stays narrow. Review whether queued execution and
Coolify parsing make this safe; a two-service alternative would need explicit
oneshot health/model-count and profile handling, rather than broad relaxation.

First bootstrap is operator-owned and cannot be requested by such a plan.
Later initialization requires existing pinned permanent authority. An initializer
plan comes from the pinned controller after exact artifact
acceptance. It binds expected current marker hash, a fresh operation/generation,
target, role and accepted hashes. The initializer takes the same exclusive
lock and applies compare-and-swap transitions. Stale queued plans cannot change
newer state. Replays may return an old receipt read-only but cannot reopen an
earlier phase. No init command should trust arbitrary environment instructions.

## Transition and signed observation

1. Validate artifacts/profile/target and fresh backup. Capture actual inventory.
   Stop the current service through Coolify.
2. Run initialization only. Persist intent before mutation, change protected
   data ownership/modes to70541 and prepare root-owned authority with admission
   closed. Preserve bytes/inodes and all current writes. Treat symlinks, external
   hardlinks, ACLs, unexpected writers and partial ownership changes explicitly.
   Do not infer success or restore old permissions after an uncertain operation.
3. Stop again and verify all processes that could have opened data before the
   ownership change are gone. A changed path permission does not revoke an FD
   or mmap. Inspect complete bounded Docker/cgroup and FD/mapping evidence;
   unknown/incomplete inspection fails closed.
4. The existing read-only host observer signs a fresh exact-target observation
   binding the closed marker hash, data identity/permissions, lock identity,
   profile and absence of old readers/writers. Use a separately protected
   Ed25519 signing key and pin its public key. No private key is given to CI or
   runtime. Metadata access alone never produces this assertion.
5. The root initializer verifies that signature, nonce/time and exact expected
   closed-state hash before admitting ONE desired application/guard hash. It
   changes authority atomically under the lease. Start runtime; the guard checks
   the active authority under that same lease before exec.
6. Verify actual image/source, process credentials, lease and exact private
   readiness. Recovery repeats prepare/observe/admit for the accepted recovery
   role, against current data. It never reverts the authorization floor or
   restores a database. Successful recovery still reports the original failure.

Fresh prepare generations invalidate older activation proofs/tokens. Queued
old guard runtimes fail the current marker; queued pre-guard runtimes fail DAC
or the missing entrypoint. Duplicate admitted runtimes compete for one lease.
Queued initializers must obey hash CAS and monotonic phases, not current
environment guesses. Controller interruption leaves explicit persistent state
for reconciliation instead of selecting a historical binary.

## Compatibility and validation obligations

Ownership is a one-time private runtime identity transition; actual app TLS,
Codex state, backups and origin renewal must still work. Current private origin
certificates are manually renewed before2027-09-09. Config file access must be
verified for70541; preserve certificate bytes/CA/SNI and runtime secrets. Keep
public Docker defaults and all non-container Helm interfaces working.

Before implementation, write bounded failure inventory and real Docker/HTTP
journeys. Cover missing/foreign/old guard, forged hashes/signature/time/target,
stale init and activation replays, unknown marker, changed lock inode, open FD
and mmap, duplicate runtime, lease inheritance/health, old profiles/new images,
partial ownership failure and preserved writes through recovery. Do not accept
a fake status string as an enforced fence. TC-336 must run exact immutable
candidate/recovery and retained artifacts on populated isolated copies; kernel
probes or scripted model fixtures do not replace those gates.

This review should identify the smallest defensible version and any ordinary
queued/startup path that defeats it. Avoid expanding the threat model to a
privileged administrator deliberately bypassing the protected pipeline.
