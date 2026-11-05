#!/usr/bin/env python3
"""Snapshot SQLite only through a kernel-enforced read-only source mount.

Even SQLite mode=ro can create/write SHM on a writable source. A root observer
must not become a second unleased writer inside native guarded data.
"""
import argparse
from contextlib import closing
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
import sys
import tempfile


def run_checked(command):
    subprocess.run(command, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def snapshot(source, output):
    if not os.statvfs(source).f_flag & os.ST_RDONLY:
        raise ValueError('SQLite source must be on a kernel read-only mount')
    database = source / 'roadmap.db'
    if not stat.S_ISREG(database.lstat().st_mode):
        raise ValueError('SQLite source must be a regular file')
    with closing(sqlite3.connect(database.as_uri() + '?mode=ro', uri=True, timeout=30)) as incoming:
        with closing(sqlite3.connect(output, timeout=30)) as outgoing:
            incoming.backup(outgoing, pages=256, sleep=.05)
            outgoing.execute('PRAGMA journal_mode=DELETE')
    descriptor = os.open(output, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def mounted_snapshot(source, output):
    # The caller entered a private mount namespace with propagation disabled.
    # Never recursively remove a mountpoint, even if unmounting fails.
    private = Path(tempfile.mkdtemp(prefix='helm-sqlite-readonly-'))
    mountpoint = private / 'source'
    mountpoint.mkdir(mode=0o700)
    mounted = False
    try:
        run_checked(['mount', '--bind', str(source), str(mountpoint)])
        mounted = True
        run_checked(['mount', '-o', 'remount,bind,ro', str(mountpoint)])
        snapshot(mountpoint, output)
    finally:
        if mounted:
            run_checked(['umount', str(mountpoint)])
        mountpoint.rmdir()
        private.rmdir()


def create(source, output, container_image=None):
    if not stat.S_ISDIR(source.lstat().st_mode):
        raise ValueError('SQLite source must be a real directory')
    source, output = source.resolve(strict=True), output.absolute()
    helper = Path(__file__).resolve()
    if container_image:
        # Explicit disposable-test alternative when unshare is unavailable.
        # No network, writable source, privileged mode, or container GC lease.
        command = ['docker', 'run', '--rm', '--network', 'none', '--read-only',
                   '--user', f'{os.geteuid()}:{os.getegid()}',
                   '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                   '--mount', f'type=bind,src={source},dst=/source,readonly',
                   '--mount', f'type=bind,src={output.parent},dst=/snapshot',
                   '--mount', f'type=bind,src={helper},dst=/snapshot_database.py,readonly',
                   '--entrypoint', '/usr/bin/python3', container_image,
                   '-B', '/snapshot_database.py', '/source', '/snapshot/' + output.name]
    else:
        command = ['unshare', '--mount', '--propagation', 'private', '--', sys.executable,
                   '-B', str(helper), '--mount-source', str(source), str(output)]
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as error:
        raise RuntimeError('read-only SQLite snapshot failed: ' + error.stderr.strip()) from None
    if result.stdout:
        raise RuntimeError('unexpected SQLite snapshot output')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mount-source', action='store_true')
    parser.add_argument('source', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    if args.output.exists():
        parser.error('snapshot output must not exist')
    if args.mount_source:
        mounted_snapshot(args.source, args.output)
    else:
        snapshot(args.source, args.output)


if __name__ == '__main__':
    main()
