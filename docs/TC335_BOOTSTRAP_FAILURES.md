# TC335 CLOSED bootstrap failure inventory

Written before implementation and isolation tests. This operator tool can only
prepare CLOSED authority. It cannot activate a runtime or prove the candidate
image/binary pair, writer drain, or deployment acceptance.

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
instead of discarding uncertain state. No bootstrap receipt proves a complete
writer drain, and the module never stops services or host processes.

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

The hierarchy uses journal format 2, up to 2,048 sequential record pages and 32
sequential index pages. Each index contains at most 64 page descriptors. Every
loaded page must be canonical JSON, match its pinned SHA-256, appear in canonical
order, and fit its bound. The runtime marker remains the same 14-field format 1
prepared marker. Scale journeys retain only counts and hashes, never symlink
targets or source filenames.
