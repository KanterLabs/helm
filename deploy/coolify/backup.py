#!/usr/bin/env python3
"""Back up single-primary lanes without stopping writers or changing guarded data."""
import argparse
from contextlib import closing
import datetime
import fcntl
import json
import os
from pathlib import Path
import re
import sqlite3
import tarfile

import attachment_backup
import snapshot_database


def check_database(db):
    if db.execute('PRAGMA integrity_check').fetchone() != ('ok',):
        raise ValueError('backup integrity failed')
    if db.execute('PRAGMA foreign_key_check').fetchall():
        raise ValueError('backup relationships failed')


def backup_lane(base, lane, stamp, container_image=None):
    root = base / lane
    attachment_root = root / 'data/attachments'
    destination = root / 'backups' / stamp
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    # Pin before snapshot creation. GC may unlink a ready object immediately
    # after a SQLite snapshot unless the lease covers snapshot + copy + verify.
    with attachment_backup.gc_pin(attachment_root) as pinned:
        snapshot_database.create(root / 'data', destination / 'roadmap.db', container_image)
        with closing(sqlite3.connect(destination / 'roadmap.db', timeout=30)) as backup:
            check_database(backup)
            if not pinned and (attachment_root.exists() or attachment_backup.ready_rows(backup)):
                raise ValueError('attachment root appeared without a GC pin; retry backup')
            manifest = attachment_backup.capture(backup, attachment_root, destination)
        # Root-only private runtime material retains the pre-attachment format.
        with tarfile.open(destination / 'runtime.tar.gz', 'w:gz') as archive:
            archive.add(root / 'config', arcname='config')
            for name in ('data/codex-users', 'data/data/codex-users', 'data/public-endpoints'):
                if (root / name).exists():
                    archive.add(root / name, arcname=name)
        hashes = {name: attachment_backup.digest(destination / name)
                  for name in ('roadmap.db', 'runtime.tar.gz', 'attachments.json')}
        receipt = {'format': 2, 'lane': lane, 'timestamp': stamp, 'integrity': 'ok',
                   'foreign_keys': 'ok', 'sha256': hashes,
                   'attachment_objects': len(manifest['objects']), 'gc_pin': 'shared-runtime-lease' if pinned else 'legacy-root-absent'}
        # This receipt is the completion marker. Failed backups never have one.
        (destination / 'verified.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(f'{lane}: verified backup {stamp}')
    return destination


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', type=Path, default=Path('/srv/helm'))
    parser.add_argument('--snapshot-container-image', help='explicit read-only container snapshot (disposable tests)')
    parser.add_argument('--lane', choices=('beta', 'production'), action='append')
    parser.add_argument('--stamp', default=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    args = parser.parse_args(argv)
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,79}', args.stamp):
        parser.error('stamp must be a simple directory name')
    os.umask(0o077)
    with (args.base / '.backup.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        for lane in args.lane or ('beta', 'production'):
            backup_lane(args.base, lane, args.stamp, args.snapshot_container_image)


if __name__ == '__main__':
    main()
