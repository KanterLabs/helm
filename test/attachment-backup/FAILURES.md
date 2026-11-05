# Attachment backup failure cases (written before implementation)

- An online SQLite snapshot references an original removed by concurrent GC.
  Hold the runtime-created `.gc.lock` shared, including copy and verification;
  a separate GC process must wait for the backup to finish.
- Uploads complete after the database snapshot. Their objects need not appear
  in that backup, while every ready snapshot row must appear, including staged
  and soft-deleted rows. Reservations (`ready=0`) need no object.
- A missing, truncated, modified or symlinked original must fail the backup;
  no success receipt may remain. IDs must not escape the object root.
- A missing runtime lock must fail rather than create anything in guarded data.
  Reading backups must leave source file contents, ownership and lock inode alone.
- Root SQLite can create or update WAL/SHM even with mode=ro. Require a
  kernel read-only source mount; prove running-WAL source identities/content
  remain unchanged and absent sidecars fail without being created. Never run
  an unleased owner writer or use immutable=1 against the live database.
- A legacy database without attachment tables/root must still back up and restore.
- An archive or copied object modified after backup must fail verification before
  restore creates a target. A restore must reject an existing target, even empty.
- Optional thumbnails must be checksummed when present and explicitly marked as
  regenerable when missing. Originals are always mandatory for ready records.
- Runtime config/account/public-endpoint archive behavior, SQLite integrity and
  foreign-key checks, and restrictive destination permissions must survive.

The disposable real-process journey records only synthetic counts, boolean
results and SHA-256 values in its receipt; no credentials or private content.
