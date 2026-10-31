# TC335 access snapshot failure inventory

The collector is a read-only, instantaneous Linux access observation. It does
not stop processes, revoke existing descriptors or mappings, prove a fence,
or prove continuing exclusion. A caller must separately establish the private
UID barrier, closed guard authority, and verified host execution. Equal PID
namespace identifiers do not prove the caller is outside a container.

Before implementation, the failure inventory is:

- A legacy process retains a descriptor after ownership changes, or retains a
  shared mapping after closing its descriptor; pathname permissions cannot
  revoke either access. Count all descriptor and mapping access, including
  read-only access, deleted names, and aliases, using device/inode identity.
- A process uses a protected directory as its working directory.
- An input is missing, contains a SQLite-path symlink, special file, hard-linked file,
  extended ACL, nested mount, crosses devices, exceeds inventory bounds, or
  changes identity/metadata during observation: return unknown.

Live metadata discovery before bootstrap found10,229/10,196 entries and8/4
opaque symlinks, with no hardlinks. The larger bounded source stage must inspect
up to32,768 objects. Preserve non-SQLite symlinks as opaque objects without
following targets; reject symlinks at the root database/WAL/SHM paths. This
inventory is metadata only and must not read linked files outside the tree.
- PID visibility is restricted, proc namespace or mount metadata cannot be
  read, Linux/root prerequisites fail, or proc is not the real host proc:
  return unknown; host execution always needs external verification.
- PID, descriptor, status, mapping, or aggregate counts exceed fixed bounds;
  malformed/truncated data and permissions failures return unknown.
- Processes and descriptors exit during scanning. Skip only after independently
  confirming that the specific object has disappeared. A permission error is
  never evidence of disappearance.
- Processes, mappings, and descriptors can change between reads; even a complete
  clean observation is only a snapshot. Metadata rechecks detect some changes,
  never establish monotonic closure or guard authority.
- Scanner-owned directory handles can cause false positives. Close inventory
  handles before scanning and exclude only the collector's own PID.
- Artifacts must contain counts, PID numbers, device/inode identifiers, and
  bounded reason codes only: no file contents, paths, command lines, environment,
  mapping excerpts, or credentials. The actual database remains untouched.
- Many matching processes could exceed the bounded response size. Retain at
  most64 matching records and return unknown with a limit reason if another
  match is found. An incomplete bounded result never becomes a clean claim.

Repeatable verification is `python3 test/deployment-policy/drain_journeys.py`.
It writes sanitized evidence under `.artifacts/tc335/drain/`. Synthetic proc
fixtures exercise refusal and limit cases. An owned real child holds a descriptor
and a shared mapping with its descriptor closed; restricted proc visibility
permits positive observations but cannot produce a clean host claim.

Verified kernel threads are a separate case: require `Kthread: 1` in status,
an empty descriptor directory and an empty maps file. Cwd may be absent or
resolve normally: native DEV inspection found230 kernel tasks with empty
FD/maps and a resolvable cwd. A resolved cwd still counts as access if it names
a protected directory. A permission failure remains unknown.
Missing userspace cwd/maps is never evidence of a kernel thread. Conflicting
kernel-thread metadata returns unknown. Start time and process directory
identity must agree before/after scanning to detect PID reuse.

Parent native-host integration exposed two further regression cases, recorded
before their fixes: procfs kernel-thread cwd links can exist yet resolve ENOENT;
valid kernel-thread cwd links can also resolve an unrelated directory;
and a different process's private proc mount can use hidepid without restricting
the collector's full host proc mount. Validate the collector's own visibility
strictly, while parsing another process's mount metadata without mistaking its
restrictions for the collector's. Real userspace lookup failures stay unknown.

`deploy/admission/drain.py` exposes only `observe(data_root: Path, *,
proc_root=Path('/proc'))`. Results use format 1 and scope
`protected-tree-access-snapshot`, with a target device/inode pair, the number
of completely scanned processes, matching PID/count/cwd records, bounded
reason codes, and namespace equality metadata. Status is `unknown` on any
incompleteness, `access_present` for a complete scan with matches, or
`no_access_observed` for a complete scan without matches. A nondefault proc
root always adds `restricted-proc-visibility`; it never returns a clean claim.
Linux root is mandatory. No process is stopped and no ownership is changed.

Fixed bounds are32,768 protected objects,4,096 positive PIDs,64 matching records,4,096 descriptors
per PID, 1 MiB per maps/mountinfo read, 64 KiB per status/stat read, and 128 MiB
aggregate per-process metadata. Reaching the aggregate limit returns unknown
without further process reads. Directory inventory includes the database, WAL,
and every other regular file/directory present, without opening file contents.
All inventory inspection descriptors close before the scan. The scanner's PID
alone is excluded; the caller must account for the collector's own authority.

The verification artifact includes a real helper with exactly one matching
descriptor and one matching shared mapping. It uses libc mmap through stdlib
ctypes, so Python before3.13 also works without a hidden duplicated descriptor.
Fixture observations remain unknown. Full-host clean behavior
requires a separately verified host-root invocation; these artifacts do not
assert it. The helper exits through its own stdin and is always reaped.
