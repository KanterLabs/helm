"""Verify an existing exact operator snapshot without opening live SQLite."""
import hashlib
import os
from pathlib import Path
import re
import shutil
import sqlite3
import tempfile
import time

from .filesystem import (BootstrapError, Inventory, MAX_SECONDS, bounded_names, content,
                         digest, identity, open_directory)

BACKUP_NAME = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]{0,95}\Z')
MAX_AGE_SECONDS = 24 * 60 * 60


def validate_name(name):
    if not isinstance(name, str) or not BACKUP_NAME.fullmatch(name) or name in ('.', '..'):
        raise BootstrapError('strict backup basename required')


def verify(lane_path, name, source):
    validate_name(name)
    parent = open_directory(Path(lane_path) / 'backups', private=True)
    fd = None
    try:
        fd = open_directory(Path(lane_path) / 'backups' / name, private=True)
        s = os.fstat(fd)
        age = time.time() - s.st_mtime
        if age < -60 or age > MAX_AGE_SECONDS:
            raise BootstrapError('fresh exact operator snapshot required (maximum age 24 hours)')
        if identity(os.stat(name, dir_fd=parent, follow_symlinks=False)) != identity(s):
            raise BootstrapError('backup identity changed')
        if bounded_names(fd, 3) != ['config', 'data']:
            raise BootstrapError('exact snapshot must contain only data and config trees')
        with Inventory(fd, owners=(0,), backup=True) as backup:
            if content(backup.records) != content(source.records):
                raise BootstrapError('backup is not exact; create a fresh offline data/config snapshot including WAL')
            validate_sqlite(backup)
            backup.rehash()
            source.rehash()
            if identity(os.stat(name, dir_fd=parent, follow_symlinks=False)) != identity(s):
                raise BootstrapError('backup identity changed')
            return digest(content(backup.records))
    finally:
        if fd is not None:
            os.close(fd)
        os.close(parent)


def validate_sqlite(backup):
    records = {record['path']: record for record in backup.records}
    main = records.get('data/roadmap.db')
    if not main or main['kind'] != 'file':
        raise BootstrapError('snapshot SQLite database required')
    paths = [path for path in ('data/roadmap.db', 'data/roadmap.db-wal', 'data/roadmap.db-shm')
             if path in records]
    if any(records[path]['kind'] != 'file' for path in paths):
        raise BootstrapError('unsupported snapshot SQLite sidecar')
    # SQLite may write shared-memory even with mode=ro. Its disposable private
    # copy includes the exact main/WAL bytes, leaving snapshot and source alone.
    required = sum(records[path]['size'] for path in paths)
    if shutil.disk_usage(tempfile.gettempdir()).free < required + 16 * 1024 * 1024:
        raise BootstrapError('insufficient temporary verification capacity')
    deadline = time.monotonic() + MAX_SECONDS
    with tempfile.TemporaryDirectory(prefix='helm-bootstrap-verify-') as directory:
        os.chmod(directory, 0o700)
        for path in paths:
            source = backup.fds[path]
            os.lseek(source, 0, os.SEEK_SET)
            size, checksum = 0, hashlib.sha256()
            target = os.open(str(Path(directory) / path.rsplit('/', 1)[-1]),
                             os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            try:
                while True:
                    if time.monotonic() > deadline:
                        raise BootstrapError('snapshot verification deadline exceeded')
                    chunk = os.read(source, 1024 * 1024)
                    if not chunk:
                        break
                    size += len(chunk)
                    if size > records[path]['size']:
                        raise BootstrapError('snapshot changed during verification')
                    checksum.update(chunk)
                    view = memoryview(chunk)
                    while view:
                        view = view[os.write(target, view):]
            finally:
                os.close(target)
            if size != records[path]['size'] or checksum.hexdigest() != records[path]['sha256']:
                raise BootstrapError('snapshot changed during verification')
        try:
            uri = (Path(directory) / 'roadmap.db').as_uri() + '?mode=ro'
            with sqlite3.connect(uri, uri=True, timeout=1) as connection:
                connection.set_progress_handler(lambda: int(time.monotonic() > deadline), 1000)
                connection.execute('PRAGMA trusted_schema=OFF')
                if connection.execute('PRAGMA integrity_check').fetchmany(2) != [('ok',)]:
                    raise BootstrapError('snapshot SQLite integrity check failed; replace the snapshot')
                if connection.execute('PRAGMA foreign_key_check').fetchone() is not None:
                    raise BootstrapError('snapshot SQLite foreign-key check failed; reconcile before retry')
        except sqlite3.Error:
            raise BootstrapError('snapshot SQLite validation failed; supply a consistent exact snapshot') from None
