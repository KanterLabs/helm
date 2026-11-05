#!/usr/bin/env python3
"""Verify a backup and reconstruct it into a NEW offline, disposable directory.

This tool never activates a service or restores over an existing lane. Guarded
production data, security state, ownership, runtime leases and authorization
floors require their existing separately authorized recovery workflow.
"""
import argparse
from contextlib import closing
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import sqlite3
import tarfile

import attachment_backup
from backup import check_database

ALLOWED = ('config', 'data/codex-users', 'data/data/codex-users', 'data/public-endpoints')


def runtime_members(archive):
    members = archive.getmembers()
    for item in members:
        path = PurePosixPath(item.name)
        if path.is_absolute() or '..' in path.parts or not (item.isfile() or item.isdir()):
            raise ValueError('unsafe runtime archive member')
        if not any(item.name == name or item.name.startswith(name + '/') for name in ALLOWED):
            raise ValueError('unexpected runtime archive member')
    return members


def verify(source):
    receipt = json.loads((source / 'verified.json').read_text())
    required = {'roadmap.db', 'runtime.tar.gz'}
    if receipt.get('format') == 2:
        required.add('attachments.json')
    hashes = receipt.get('sha256', {})
    if set(hashes) != required:
        raise ValueError('unexpected backup hash manifest')
    for name, expected in hashes.items():
        attachment_backup.regular(source / name)
        if attachment_backup.digest(source / name) != expected:
            raise ValueError(f'backup checksum mismatch: {name}')
    with closing(sqlite3.connect(f'file:{source}/roadmap.db?mode=ro', uri=True)) as db:
        check_database(db)
        if 'attachments.json' in required:
            attachment_backup.verify(db, source)
        elif attachment_backup.ready_rows(db):
            raise ValueError('legacy backup cannot recover ready attachment originals')
    with tarfile.open(source / 'runtime.tar.gz', 'r:gz') as archive:
        runtime_members(archive)
    return receipt


def restore(source, target):
    if target.exists() or target.is_symlink():
        raise ValueError('restore target must not exist; live lane restore is unsupported')
    verify(source)
    # Verification finishes before any target creation. Do not copy a GC lease
    # from a running source: a new app initializes its own lock on recovery.
    target.mkdir(mode=0o700)
    try:
        (target / 'data').mkdir(mode=0o700)
        shutil.copyfile(source / 'roadmap.db', target / 'data/roadmap.db')
        if (source / 'attachments.json').exists():
            manifest = json.loads((source / 'attachments.json').read_text())
            for item in manifest['objects']:
                origin = source / 'attachments/objects' / item['id']
                folder = target / 'data/attachments/objects' / item['id']
                folder.mkdir(mode=0o700, parents=True)
                attachment_backup.copy_blob(origin / 'original', folder / 'original',
                                            (item['size_bytes'], item['sha256']))
                thumb = item['thumbnail']
                if 'sha256' in thumb:
                    attachment_backup.copy_blob(origin / 'thumbnail.png', folder / 'thumbnail.png',
                                                (thumb['size_bytes'], thumb['sha256']))
        with tarfile.open(source / 'runtime.tar.gz', 'r:gz') as archive:
            archive.extractall(target, members=runtime_members(archive), filter='data')
        with closing(sqlite3.connect(f'file:{target}/data/roadmap.db?mode=ro', uri=True)) as db:
            check_database(db)
            if (source / 'attachments.json').exists():
                # Verify the restored tree against the same immutable manifest.
                staging = target / 'data/attachments.json'
                shutil.copyfile(source / 'attachments.json', staging)
                try:
                    attachment_backup.verify(db, target / 'data')
                finally:
                    staging.unlink()
    except BaseException:
        shutil.rmtree(target)
        raise
    return target


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('snapshot', type=Path)
    parser.add_argument('target', type=Path)
    args = parser.parse_args(argv)
    os.umask(0o077)
    restore(args.snapshot, args.target)
    print('Verified offline restore completed; no service activated.')


if __name__ == '__main__':
    main()
