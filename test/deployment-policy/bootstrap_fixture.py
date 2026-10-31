"""Synthetic private data/config snapshots for CLOSED bootstrap journeys."""
import hashlib
import json
import os
import shutil
import sqlite3

from bootstrap_native import binding


def snapshot(root):
    result = {}
    for path in [root, *sorted(root.rglob('*'))]:
        s = path.lstat()
        link = path.is_symlink()
        result[str(path.relative_to(root))] = dict(
            dev=s.st_dev, ino=s.st_ino, mtime=s.st_mtime_ns,
            digest=hashlib.sha256(os.fsencode(os.readlink(path)) if link else path.read_bytes()).hexdigest()
            if link or path.is_file() else None,
            link_metadata=(s.st_uid, s.st_gid, s.st_mode) if link else None)
    return result


def save(path, value):
    path.write_text(json.dumps(value, sort_keys=True, separators=(',', ':')) + '\n')
    path.chmod(0o600)


def fixture(output, label):
    base = output / label
    lane = base / 'beta'
    data, config = lane / 'data', lane / 'config'
    data.mkdir(parents=True, mode=0o700)
    config.mkdir(mode=0o700)
    (data / 'sessions').mkdir(mode=0o700)
    (data / 'sessions' / 'synthetic-session').write_bytes(b'synthetic durable session\n')
    (data / 'support').write_bytes(b'#!/bin/sh\nexit 0\n')
    (data / 'support').chmod(0o700)
    (config / 'settings').write_bytes(b'synthetic-config\n')
    (config / 'settings').chmod(0o400)
    with sqlite3.connect(data / 'roadmap.db') as db:
        db.executescript('PRAGMA foreign_keys=ON; CREATE TABLE p(id INTEGER PRIMARY KEY);'
                         'CREATE TABLE c(id INTEGER PRIMARY KEY,p INTEGER REFERENCES p(id));'
                         'INSERT INTO p VALUES(1); INSERT INTO c VALUES(1,1);')
    db.close()
    for path in [data, config, *data.rglob('*'), *config.rglob('*')]:
        os.chown(path, 65532, 65532)
    backup = lane / 'backups' / 'synthetic-snapshot'
    backup.mkdir(parents=True, mode=0o700)
    for name in ('data', 'config'):
        shutil.copytree(lane / name, backup / name)
    for path in [base, lane, lane / 'backups', backup, *backup.rglob('*')]:
        os.chown(path, 0, 0)
        path.chmod(0o700 if path.is_dir() else 0o600)
    binding(base)
    return base, lane, backup
