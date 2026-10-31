# Review regression inventory

Written before the corrections to independent review of `3320a93`.

1. An untouched legacy-owned inode can change mode after initial rehash. The
   preparation must refuse without overwriting that mode. The test retains an
   actual legacy-UID descriptor and changes the inode during another inode's
   handover, before the target's per-inode check.
2. A change can occur between a per-inode check and chown. Check mode again
   AFTER ownership transfer, before chmod. Preserve the racing mode and leave
   CLOSED intent if it differs. Ownership may already have transferred; never
   roll it back to make the old runtime work.
3. A previously checked file can be targeted during final verification. Prove
   actual UID65532/CAP_FOWNER0 cannot fchmod a held descriptor after ownership
   becomes70541. Also prove its held writable descriptor can still write: this
   is metadata exclusion, not a substitute for the full FD/mmap drain.
4. Descriptor/mapping evidence can be lost if later maps, cwd or identity checks
   fail. Retain bounded provisional matches with a fixed uncertainty reason.
   Their presence must never suppress unknown, attest current PID identity or
   imply continuing exclusion.
5. An old runtime can have a path-based operation already in the kernel,
   without a visible target FD or cwd. Block target-bound process namespaces
   and native legacy/reserved UID actors as potential writers, even when their
   descriptor/mapping counts are zero. Other lanes' unrelated namespaces must
   remain distinguishable. Mount coordinates use filesystem roots and device
   identities, not container-visible path names.
   Namespace-handle filesystem mounts legitimately have non-path roots; ignore
   those unrelated devices when translating the protected filesystem scope.

The correction closes every data/config directory before any regular-file
handover. Each directory transfers to UID70541, its full mode is checked after
chown, and only then can chmod run. Once every directory is private, complete
native-host observations of both trees must be clean. Regular-file metadata
changes follow that barrier. A missing/foreign external host binding, any
retained access, namespace exposure, native legacy actor, or uncertainty refuses.

Journal format3 binds the original plan, external host context and both access
observation digests. Same-plan resume repeats the barrier; it never reopens
CLOSED authority or restores an older snapshot. The runtime marker stays
format1/prepared and actual Helm refuses it.

Linux serializes chmod/chown through the inode lock, and chmod checks current
ownership/capability. The actual DEV kernel is7.0.0-38-generic. Directory chown
preserved ordinary and special racing modes in the native tests, which refuse
before overwriting them. This is not a general metadata compare-and-swap.
See primary [chmod/chown implementation](https://raw.githubusercontent.com/torvalds/linux/v7.0/fs/open.c)
and [attribute permission checks](https://raw.githubusercontent.com/torvalds/linux/v7.0/fs/attr.c).
Mount translation follows the [kernel proc documentation](https://docs.kernel.org/filesystems/proc.html).

Supported execution requires legacy UID65532 without ownership capabilities,
new UID70541 reserved for guarded runtimes, prepared authority blocking new
runtimes, no bypassing file-only aliases, and trusted root maintenance serialized
under the permanent lane lock. The host binding must be provisioned independently
in verified native context; production code cannot generate it. These premises
still require installation acceptance. Privileged administrators deliberately
bypassing the protected path remain outside the threat boundary.

DEV receipts are under `.artifacts/tc335/`:

- `drain/provisional-evidence-red.txt`: original positive-evidence loss.
- `drain/inflight-namespace-red.txt` and `nsfs-scope-red.txt`: potential actors
  and namespace-handle roots before their fixes.
- `drain/journeys.json`:32 portable journeys, including actual FD/mmap helper
  access and bounded provisional observations.
- `drain-python311-review/`: the same32 journeys on Python3.11.
- `review-security.log`: combined deployment-security and release command flow,
  including11 release failure/recovery scenarios, passes.
- `bootstrap/metadata-7fe4b3c73cea/`: actual legacy-UID metadata races against
  the previous sequence; these initially overwrote modes.
- `bootstrap/metadata-native-3078c6c9a750/`:14 native metadata/binding journeys,
  including descriptor writes still succeeding after chown.
- `bootstrap/parent-native-review.json`: parent basic/scale acceptance.
- `bootstrap/runtime-native-f4b4df366ed7/`: actual target-bound namespace with
  zero protected handles blocks, FD/mmap block, clean drain permits preparation,
  actual Helm refuses prepared authority, and DB bytes/inode/mtimes survive.

Only exclusively created DEV fixtures were changed. No live preparation,
installation, activation, schema change or deployment occurred. These checks
are supporting source evidence, not accepted TC-336 candidate/recovery receipts.
