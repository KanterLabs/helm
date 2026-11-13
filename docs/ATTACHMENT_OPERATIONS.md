# Screenshot retention and recovery

Screenshot originals live beside the SQLite database in
`attachments/objects/<opaque ID>/original`; thumbnails use `thumbnail.png`.
These files are private application data. Access them through authenticated
application routes. Do not expose this directory through a static web server.

The app retains published originals indefinitely. Unpublished staging and
crash remnants expire after 24 hours; soft-deleted originals remain for 30 days.
Retained bytes continue to count toward quota until physical purge. Runtime GC
holds the app-created `attachments/.gc.lock` exclusively around metadata and
physical deletion. Operators must not delete attachment files as root while a
runtime lease is active.

## Backups

The existing daily Coolify backup entry point remains
`deploy/coolify/backup.py`. Install `attachment_backup.py` and `snapshot_database.py` alongside it before
activating attachment uploads. Its default base and lanes are unchanged:
`/srv/helm`, beta and production. Existing systemd timer/service definitions
remain unchanged. A manual filesystem-only example for a disposable lane is:

```sh
python3 deploy/coolify/backup.py --base /tmp/disposable-helm --lane beta --stamp rehearsal
```

Backups take a read-only, shared flock on the existing `.gc.lock` before the
online SQLite snapshot, retaining it through copying and verification. They
never create a lock in guarded data. A missing lock for an existing attachment
root fails closed. If the root appears during a legacy snapshot, retry the
backup so the new lease is held. The permanent `security/runtime.lock` is never
replaced or acquired by this tool.

The SQLite snapshot subprocess enters a private mount namespace, bind-mounts
only the source data directory at a private temporary mountpoint, and remounts
that bind read-only. The helper checks the kernel read-only mount flag before
opening SQLite with `mode=ro`. This matters because `mode=ro` alone can still
write shared-memory files. It never uses `immutable=1` against a live WAL and
never starts an owner-UID writer outside the permanent runtime lease. Snapshot
output stays outside guarded data, is sealed in DELETE journal mode, and is
closed before hashing. The parent retains its shared GC barrier throughout.
A namespace/mount failure or WAL requiring missing sidecars fails closed; the
tool never stops the running writer or retries with a writable mount. The
host needs `unshare`, `mount`, `umount` and the corresponding root mount
capability. Check this in a disposable rehearsal before enabling the timer.

Each completed snapshot contains `roadmap.db`, the existing private
`runtime.tar.gz`, `attachments.json`, `attachments/objects/...`, and a final
`verified.json`. SQLite integrity and foreign-key checks must pass. Every
`ready=1` row in the database snapshot requires an original with matching size
and SHA-256, including staged and soft-deleted rows. Reservations with
`ready=0` need no object. Concurrent uploads absent from the snapshot need no
object in that backup. Available object directories without snapshot linkage
are retained as explicitly marked orphans, without guessing links. This also
preserves files across an older schema41 binary writing a schema42 database.
A table-less legacy database remains supported.

The manifest records original checksums and thumbnail checksums when present.
Thumbnails are marked regenerable: a missing thumbnail does not invalidate an
otherwise complete original backup. Originals are mandatory. A success receipt
is written last; a directory without `verified.json` is incomplete and must not
be treated as a recovery point. Backups retain their existing root-only parent
permissions and have no automatic expiry. Monitor space and keep verified
copies off the host. Retention GC does not prune backup copies.

## Offline recovery rehearsal

`restore_attachments.py` reconstructs a snapshot only into a **new** directory.
It rejects any existing target, including an empty directory. It checks the
receipt, SQLite integrity/relationships, originals and thumbnails before
creating the target, then verifies the copied attachment tree again.

```sh
python3 deploy/coolify/restore_attachments.py /tmp/disposable-helm/beta/backups/rehearsal /tmp/helm-restored-rehearsal
```

This creates `data/roadmap.db`, `data/attachments`, and the archived
config/runtime paths. It copies only manifest-listed objects. Runtime archive
paths must belong to config, codex-users or public-endpoints and contain only
regular files/directories; unsafe archive paths and links fail verification.
The Gitea connection's `data/forge` directory is not archived, so a restored
lane signs in to Gitea again ([GITEA_OAUTH.md](GITEA_OAUTH.md#restores-moves-and-downgrades)).
No service is activated, and no source lock, staging files, runtime lease,
security profile, authorization floor or production ownership is changed.
The app creates its own GC lock when a separately authorized recovery is
activated. Old snapshots without attachment metadata can recover databases
without ready attachment records; a legacy snapshot that references ready
attachments but lacks their manifest is rejected.

An offline reconstruction is a rehearsal artifact. Production replacement,
security admission, UID70541 ownership, existing native guard/inode guarantees,
and any image change still require the established deployment recovery
workflow. Never lower an authorization floor to start an older image. Keep
current data and backup copies until an explicitly authorized recovery is
verified.

## Repeatable verification

The test uses disposable files, synthetic SQLite records, a real backup
process, and a separate GC process. On unprivileged workstations it uses the
explicit `--snapshot-container-image` test option with an existing Python
image, a read-only source bind, no network and no capabilities. This runs the
same source-read-only check; it is not an automatic deployment fallback. It pauses object copying after the snapshot,
proves exclusive GC waits, changes live metadata, then restores the exact older
snapshot. It also rejects missing/corrupt/symlinked originals, a missing GC lock,
invalid object IDs, existing restore targets and tampered backup objects. A
legacy/orphan recovery verifies rollback preservation. A running-WAL journey
compares source inode, UID, mode and content before/after snapshot. Closing its
last writer removes sidecars; another attempt must fail without creating any
source files. It writes a sanitized,
checksummed receipt, containing no credentials or user screenshots:

```sh
HELM_BACKUP_TEST_IMAGE=fe3da92a5cb2 python3 test/attachment-backup/run.py --receipt /tmp/helm-attachment-backup-receipt.json
```

The failure inventory was written before implementation in
[test/attachment-backup/FAILURES.md](../test/attachment-backup/FAILURES.md).
