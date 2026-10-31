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

Directories and regular files move to UID/GID70541. Directories become0700;
roadmap.db becomes0600; other regular files retain private owner permissions,
including executable bits. Source bytes, inodes and mtimes are checked. Opaque
symlinks retain inode, target, owner, mode and mtime: O_PATH pins the link itself
and its targets are never followed or modified. SQLite main/WAL/SHM must be
direct regular files. External hardlinks, special files, ACLs and source mount
boundaries are refused.

Journal format2 authenticates up to32 index pages, each indexing at most64
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

A clean snapshot is instantaneous. It does not establish continuing exclusion,
kill a writer, revoke a mapping, sign evidence or activate authority. The private
identity barrier, complete initial drain and guarded execution paths must prove
why supported legacy access cannot reappear afterward.

## Evidence and remaining integration

Read-only live metadata found about10,200 objects per lane,165/224MB regular
bytes and8/4 opaque links. Parents are root-owned; lane/backups are0700. Both
data/config storage records are directories without chown/chmod overrides.
No source or target contents were published by these inspections.

Retained evidence under `.artifacts/tc335/` includes38 basic root Docker
journeys; a10,331-object partial-resume journey with155 pages/3 indexes; a parent
late-metadata regression; portable scanner journeys; and combined actual Helm
startup with native DEV host observation. That combined journey retains a
writable FD and closed-FD mmap through preparation, observes both, rejects
Helm against prepared authority, stops only test holders, and obtains a complete
host no-access snapshot. Replay retains the lock; missing marker is refused.
Synthetic DB bytes and inode survive. These are not TC-336 accepted artifacts.

The existing deployment-security command runs the portable scanner journeys.
Privileged bootstrap/scale/runtime acceptance remains a separate explicit DEV
command, not silently skipped or claimed by ordinary CI.

Still required: reviewed installation; interrupted deployment and activation/
recovery transitions; execution-time profile enforcement; an honest new witness
contract; independently pinned protected workflow wiring; TC-336 exact immutable
candidate/recovery preservation; and fresh live backups. A bootstrap receipt or
clean snapshot cannot substitute for these gates.
