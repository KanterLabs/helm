# Closed authority bootstrap and access snapshot

Source stage only. Neither tool is installed on a live target; no live
ownership, schema, image or service configuration changed. The installed CI
metadata collector still returns unknown. The stopped-and-fenced contract is
unchanged. These tools do not authorize activation.

`deploy/helm-admission-bootstrap.py` exposes only preview and prepare. The
operator CLI requires root and fixes lane paths under `/srv/helm`. It has no
environment path override, activation, restore, reset or process-stop command.
Before live use, independently pin and install reviewed root-owned source,
complete artifact acceptance and obtain a fresh verified offline snapshot.
Preparing ownership while an old runtime serves would disrupt that runtime;
the live transition must wait for the remaining gates.

## Preparation contract

Preview checks source identity/content and an existing exact data/config
snapshot under the lane's private backups directory. SQLite integrity and
foreign-key checks run on a disposable copy of verified main/WAL/SHM bytes.
Neither live SQLite nor snapshot SQLite is opened. The saved plan binds source
directory identities, inventory digest, snapshot digest and coordinates.
Candidate coordinates are labels, not accepted candidate/recovery proof.

Prepare independently verifies the root-private plan and backup. Atomic mkdir
and a permanent exclusive lock serialize first creation. CLOSED intent, marker,
inventory and journal are fsynced before source ownership changes. Lost or
unknown partial authority is refused, never reset. Same-plan completed replay
is read-only. Interrupted ownership changes can resume only on the same
inodes/content with original/intended metadata. Unexpected data or owner/mode
drift is preserved and refused, never overwritten or restored from backup.

Prepare also requires an externally provisioned `/srv/helm/operator/host.json`
in a root:root0700 directory, with a root-private0600 file. Its exact format1
keys are `format`, `boot_id`, `pid_namespace`, and `mnt_namespace`. The bound
boot and both native init namespaces must match the caller. The bootstrap
does not write this binding; a container cannot bless its own proc view through
an environment flag or generated binding. Preview needs no binding. The
installer and its actual host-execution verification remain an open gate.

Metadata handover has two phases. First, transfer every data/config directory
root-to-leaf, checking its full mode after chown and before chmod. Any racing
mode is preserved and refused. Once all directories are private, require fresh
complete native-host observations of both trees before transferring regular
files. Existing descriptors, closed-descriptor mappings, target-bound process
namespaces, or native legacy/reserved-UID actors block this step. The journal
records the host-context and observation digests. Same-plan replay rechecks
the host binding and observations. Only process-set churn with no matches
receives a bounded three-attempt observation retry; other uncertainty refuses.

This sequence requires verified supported execution profiles: legacy UID65532
without ownership capabilities, UID70541 reserved for guarded runtimes, no
file-only bypass aliases, and trusted root maintenance serialized by the lane
lock. Those are installation requirements, not consequences of a snapshot.

Directories and regular files move to UID/GID70541. Directories become0700;
roadmap.db becomes0600; other regular files retain private owner permissions,
including executable bits. Source bytes, inodes and mtimes are checked. Opaque
symlinks retain inode, target, owner, mode and mtime: O_PATH pins the link itself
and its targets are never followed or modified. SQLite main/WAL/SHM must be
direct regular files. External hardlinks, special files, ACLs and source mount
boundaries are refused.

Journal format3 authenticates up to32 index pages, each indexing at most64
sequential record pages. Index/record pages are each at most16,000 bytes, with
at most2,048 record pages. Load checks canonical schema/order, hashes, counts
and the exact authority file set. Runtime marker format1 retains its14 fields
and phase prepared, which Helm rejects. The lock is never replaced/recreated.

Bounds are32,768 source objects,2GiB regular bytes,64 levels,4,096-byte paths and
120 seconds per inventory/SQLite verification; snapshot age is at most24 hours.
Only the current operator process soft descriptor limit may increase toward
65,792, bounded by its unchanged hard limit. Actual capacity exhaustion refuses;
small trees work under a1,024 hard limit.

## Snapshot contract

`deploy/admission/drain.py:observe` only reads metadata. It compares protected
identities against host descriptors, mappings and working directories, including
shared mappings with closed descriptors. It reads no descriptor contents,
process environment or command line. Output contains bounded counts, PID and
device/inode metadata, and fixed reasons.

Status is access_present, no_access_observed or unknown. Inaccessible, changed,
malformed or bounded-out observations remain unknown. Linux/root and matching
init namespaces are checked; trusted host execution requires external proof.
Another process's private proc mount does not restrict the collector's host
view. Verified kernel tasks require empty FD/maps; protected cwd still counts.
At most64 matches are retained; overflow is unknown. Process churn can produce
unknown while positive matching records remain useful evidence that blocks.
Matches found before a later inspection failure are retained as provisional,
with a fixed uncertainty reason; unknown still takes precedence. Process
namespace exposure uses device identity and filesystem-root coordinates,
including direct descendant binds. It does not depend on the mount's visible
container path. Unrelated namespace-handle filesystem roots are ignored.

A clean snapshot is instantaneous. It does not establish continuing exclusion,
kill a writer, revoke a mapping, sign evidence or activate authority. The private
identity barrier, complete initial drain and guarded execution paths must prove
why supported legacy access cannot reappear afterward.

## Evidence and remaining integration

Read-only live metadata found about10,200 objects per lane,165/224MB regular
bytes and8/4 opaque links. Parents are root-owned; lane/backups are0700. Both
data/config storage records are directories without chown/chmod overrides.
No source or target contents were published by these inspections.

Retained evidence under `.artifacts/tc335/` includes38 native-root basic
journeys; a10,331-object partial-resume journey with155 pages/3 indexes;14
native metadata/credential regressions;32 portable scanner journeys; and
combined actual Helm startup with native DEV preparation. The combined journey
blocks regular-file handover while a target-bound namespace, writable FD or
closed-FD mmap remains, stops only test holders, then obtains a complete clean
host snapshot and prepares. Actual Helm rejects prepared authority. Replay
retains the lock; a missing marker is refused. Synthetic DB bytes, inode and
mtimes survive. These are not TC-336 accepted artifacts.

The existing deployment-security command runs the portable scanner journeys.
Privileged bootstrap/scale/runtime acceptance remains a separate explicit DEV
command, not silently skipped or claimed by ordinary CI.

Still required: reviewed installation; interrupted deployment and activation/
recovery transitions; execution-time profile enforcement; an honest new witness
contract; independently pinned protected workflow wiring; TC-336 exact immutable
candidate/recovery preservation; and fresh live backups. A bootstrap receipt or
clean snapshot cannot substitute for these gates.
