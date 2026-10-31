# TC335 CLOSED bootstrap failure inventory

Written before implementation and isolation tests. This operator tool can only
prepare CLOSED authority. It cannot activate a runtime or prove the candidate
image/binary pair or deployment acceptance. Regular-file metadata changes require
the native access observations and conditional directory barrier described below.

| Failure | Required outcome |
| --- | --- |
| Wrong lane, mutable image, malformed hash/nonce, unknown plan fields | Refuse before changing authority or data. |
| Plan outside root-owned private ancestors; symlink, oversized or permissive plan | Refuse; a boolean claiming backup verification is never accepted. |
| Missing, old, foreign-owned, permissive, symlinked or non-exact backup | Refuse with an actionable exact-snapshot instruction. |
| Corrupt backup SQLite or foreign-key violation, including WAL state | Refuse; never open the live database or checkpoint/reset its WAL. |
| Missing source, foreign ownership, ACL, hardlink, special file, mount boundary, excessive inventory | Refuse without touching source metadata. |
| Rename, inode replacement, file content/mtime drift during scanning | Refuse using descriptor identity checks and bounded streaming hashes. |
| Source owner/mode changes after backup verification | Refuse before overwriting that change; allow private metadata transitions only while this invocation applies them under CLOSED intent. |
| Two first preparations | Atomic security-directory creation admits one; the other refuses or verifies completed identical authority. |
| Empty security directory, missing intent/journal/marker/lock/pages, changed permanent lock | Refuse manual reconciliation; never recreate or reset authority. |
| Different saved plan or stale original inode/content | Refuse; no ownership changes. |
| Interrupted ownership changes | Keep durable CLOSED intent; same plan may resume only original or intended metadata on the same inodes and bytes. |
| Lost marker, active marker, altered journal/pages | Refuse; bootstrap never activates and never repairs missing authority. |
| Concurrent writer after intent or partial chown failure | Preserve CLOSED authority; refuse, never restore bytes or roll metadata back. |
| Populated source with session/config support files | Preserve every original inode, byte and mtime; preserve private owner executable/read/write bits. |
| Repeat prepared invocation | Recheck plan, backup, pinned source and authority; return only a read-only CLOSED receipt. |

The bounded root-private inventory may contain operational relative paths.
Receipts and errors contain counts/digests and fixed reason labels only, never
private file names or candidate credentials. Test fixtures contain synthetic
data only; receipts are retained under `.artifacts/tc335/bootstrap/`.

Operational bounds are 32,768 combined source entries, 2 GiB of combined file
bytes, 64 levels, 4,096 bytes per relative path, 120 seconds per inventory or
SQLite verification, and a snapshot at most 24 hours old. A source-root or
nested mount boundary is refused, including same-device bind mounts. The tool
keeps observed descriptors pinned and does not traverse symlinks. Unsupported
filesystems, descriptor/xattr observations, or insufficient descriptor and
temporary-copy capacity fail CLOSED.

Preview reads source and backup without persistent writes. SQLite integrity
and foreign-key checks run on a private disposable copy containing verified
main/WAL/shared-memory bytes, because SQLite can write shared-memory even with
`mode=ro`. Copy capacity and verification deadlines are bounded; source and
snapshot SQLite are never opened by SQLite. Ordinary reads can update access
times; inode identity, file contents and modification times are preserved.

After source metadata preparation, the permanent marker still has phase
`prepared`, which the runtime guard rejects. Root-private journal pages retain
the original metadata needed for a same-plan retry. Missing or unknown partial
authority requires manual reconciliation. A crash while committing a journal
replacement can leave `journal.next`; this deliberately requires manual review
instead of discarding uncertain state. A receipt alone does not prove the required
execution profiles or an unrestricted host fence. The module never stops services
or host processes.

## Actual lane shape extension: failure inventory before implementation

The read-only operator survey found more than 10,000 entries in each lane and
several external or host-unresolved symlinks. A 4,096-entry regular-only prototype
cannot prepare those lanes. The extension must preserve these links as opaque
metadata without following their targets or changing link ownership or mode.

| Additional failure | Required outcome |
| --- | --- |
| Over 10,229 source entries, including arbitrary external/dangling links | Bounded scan succeeds; every original inode/content/mtime/link target and link metadata survives. |
| SQLite main, WAL or SHM is a symlink | Refuse before authority creation; critical SQLite paths must be regular. |
| Link target text, owner, mode or mtime changes | Refuse on scan/resume; never inspect or restore the target. |
| Symlink ACL cannot be observed safely without following it | Refuse; no target-following fallback. |
| Record-page count exceeds a single root journal | Bound 16,000-byte record pages and index pages; root journal hashes at most 32 indexes, each indexing at most 64 pages. |
| Corrupt/missing/reordered index or page; unknown authority file | Refuse; preserve uncertain state for manual reconciliation. |
| Descriptor soft limit too low | Raise only the current operator process soft limit toward 65,792, never change the hard limit or host settings. |
| Existing hard limit cannot support this particular source and backup | Refuse on actual exhaustion with an actionable fixed reason; small fixtures still work. |
| App-writable directory grows during enumeration | Count a bounded scandir stream and refuse; never collect an unbounded listdir result. |

The hierarchy introduced in journal format 2, and retained in format 3, uses up
to 2,048 sequential record pages and 32
sequential index pages. Each index contains at most 64 page descriptors. Every
loaded page must be canonical JSON, match its pinned SHA-256, appear in canonical
order, and fit its bound. The runtime marker remains the same 14-field format 1
prepared marker. Scale journeys retain only counts and hashes, never symlink
targets or source filenames.

## P1 continuing exclusion: failure inventory before implementation

1. A legacy file descriptor can chmod a still-owned regular file between a
   per-inode check and chown. Regular-file chown can also erase special bits.
   Therefore no regular-file metadata mutation is permitted until all source
   directories have transferred ownership and a complete native host drain
   observes no retained access to either data or config.
2. Transfer directories root-to-leaf. Immediately before chown, require original
   or supported partial metadata. Immediately after directory chown, require
   unchanged full mode and mtime before chmod. A racing ordinary or special mode
   must survive and cause refusal; never repair it or roll ownership back.
3. A legacy actor retaining a directory descriptor must be unable to perform a
   fresh path open after directory ownership and mode become 70541:70541/0700.
   Any existing file, O_PATH, mapping or cwd access still refuses the drain before
   any regular file is changed. Closing only the synthetic holder permits a
   same-plan retry if source content and metadata remain unchanged.
4. Missing or mismatched root-private external host binding refuses before
   authority or source changes. Binding compares actual native boot ID and
   self/init PID and mount namespaces; namespace equality alone is insufficient.
   Production never creates a binding, accepts caller trust booleans or bypasses
   this check through environment settings.
5. Native observation errors, positive/provisional access, and unknown results
   refuse. Only a bounded retry of pure process-set churn without access evidence
   is permitted. Snapshot hashes attest observations, never execution profiles.
6. Prepared authority and held permanent lane lock block guarded UID70541
   execution. Supported legacy actors must use UID65532 with effective,
   permitted, inheritable and ambient capabilities zero and no privilege gain;
   trusted root maintenance must serialize under that lock.
   Installer verification of these execution profiles is still required before
   any live use. Privileged administrators deliberately bypassing the contract
   remain outside this boundary.

Journal format 3 records the directory-handover/native-drain mechanism, trusted
host context hash and both clean snapshot hashes. Every preparing retry obtains
fresh host observations; stored evidence never authorizes a later mutation.
Native test fixtures alone provision their own binding under a new root-private
fixture. Tests run through native `sudo /usr/bin/python3 -B`, never an inner PID1
container, and retain bounded synthetic receipts under the existing artifact root.

The fixed production binding is `/srv/helm/operator/host.json`, root:root mode
0600 inside a root:root mode0700 directory. Its strict bounded dictionary is
`{format:1, boot_id, pid_namespace, mnt_namespace}`. A trusted native installer
provisions it; production code never does. It attests context only. It does not
prove legacy credentials, exclusive use of guarded UID70541, root maintenance
serialization, or the absence of alternate file-only bind aliases that bypass
private directories. The future installer must audit those aliases, including
stopped or read-only Docker file bindings. Normal directory paths are closed by
the ownership barrier; an alternate file alias can bypass that barrier before
regular-file handover. These execution/profile/alias checks remain required
installation gates, and this source stage remains uninstalled.
