#!/usr/bin/env python3
"""Back up both single-primary Coolify lanes without stopping their writers."""
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import tarfile

os.umask(0o077)
base = Path('/srv/helm')
lock = (base / '.backup.lock').open('a')
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
for lane in ('beta', 'production'):
    root = base / lane
    destination = root / 'backups' / stamp
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    source = sqlite3.connect(f'file:{root}/data/roadmap.db?mode=ro', uri=True, timeout=30)
    backup = sqlite3.connect(destination / 'roadmap.db', timeout=30)
    source.backup(backup, pages=256, sleep=0.05)
    assert backup.execute('PRAGMA integrity_check').fetchone() == ('ok',), 'backup integrity failed'
    assert not backup.execute('PRAGMA foreign_key_check').fetchall(), 'backup relationships failed'
    source.close()
    backup.close()
    # Runtime files include private material: root-only permissions and directory.
    with tarfile.open(destination / 'runtime.tar.gz', 'w:gz') as archive:
        archive.add(root / 'config', arcname='config')
        for name in ('data/codex-users', 'data/data/codex-users', 'data/public-endpoints'):
            if (root / name).exists():
                archive.add(root / name, arcname=name)
    hashes = {name: hashlib.file_digest((destination / name).open('rb'), 'sha256').hexdigest()
              for name in ('roadmap.db', 'runtime.tar.gz')}
    (destination / 'verified.json').write_text(json.dumps({
        'lane': lane, 'timestamp': stamp, 'integrity': 'ok', 'foreign_keys': 'ok', 'sha256': hashes,
    }, indent=2) + '\n')
    print(f'{lane}: verified backup {stamp}')
