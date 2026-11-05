"""Immutable attachment snapshots pinned against the application's GC lease."""
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat

ID = re.compile(r'[0-9a-f]{32}\Z')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def regular(path):
    value = path.lstat()
    if not stat.S_ISREG(value.st_mode):
        raise ValueError(f'expected regular file: {path.name}')
    return value


def directory(path):
    if not stat.S_ISDIR(path.lstat().st_mode):
        raise ValueError(f'expected directory: {path.name}')


@contextmanager
def gc_pin(root):
    """Never create the runtime-owned root/lock, nor acquire runtime.lock."""
    if not root.exists():
        yield False
        return
    directory(root)
    lock_path = root / '.gc.lock'
    before = regular(lock_path)
    descriptor = os.open(lock_path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        opened = os.fstat(descriptor)
        if (before.st_dev, before.st_ino) != (opened.st_dev, opened.st_ino):
            raise ValueError('GC lease inode changed')
        fcntl.flock(descriptor, fcntl.LOCK_SH)
        current = lock_path.lstat()
        if (current.st_dev, current.st_ino) != (opened.st_dev, opened.st_ino):
            raise ValueError('GC lease replaced while acquiring pin')
        yield True
    finally:
        os.close(descriptor)


def ready_rows(db):
    present = db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='task_attachments'").fetchone()
    if not present:
        return {}
    result = {}
    for ident, size, sha in db.execute('SELECT id,size_bytes,sha256 FROM task_attachments WHERE ready=1'):
        if not ID.fullmatch(ident):
            raise ValueError('invalid ready attachment ID')
        if not isinstance(sha, str) or not re.fullmatch(r'[0-9a-f]{64}', sha):
            raise ValueError('invalid ready attachment checksum')
        result[ident] = (size, sha)
    return result


def copy_blob(source, target, expected=None):
    before = regular(source)
    descriptor = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        with os.fdopen(descriptor, 'rb', closefd=False) as incoming, target.open('xb') as outgoing:
            shutil.copyfileobj(incoming, outgoing, length=1024 * 1024)
            outgoing.flush()
            os.fsync(outgoing.fileno())
        after = os.fstat(descriptor)
        if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino):
            raise ValueError('attachment inode changed')
        size, sha = target.stat().st_size, digest(target)
        if expected is not None and (size, sha) != expected:
            raise ValueError('attachment original size/checksum mismatch')
        return {'size_bytes': size, 'sha256': sha}
    finally:
        os.close(descriptor)


def capture(db, root, destination):
    expected = ready_rows(db)
    objects = root / 'objects'
    identifiers = set(expected)
    if objects.exists():
        directory(objects)
        for child in objects.iterdir():
            if not ID.fullmatch(child.name):
                raise ValueError('invalid attachment object directory')
            directory(child)
            # Crash/unready objects have no snapshot linkage. Retain available
            # originals explicitly as orphans, including table-less rollbacks.
            if (child / 'original').exists():
                identifiers.add(child.name)
    manifest = {'format': 1, 'objects': []}
    for ident in sorted(identifiers):
        source = objects / ident
        directory(source)
        target = destination / 'attachments/objects' / ident
        target.mkdir(parents=True, mode=0o700)
        original = copy_blob(source / 'original', target / 'original', expected.get(ident))
        thumbnail = {'regenerable': True}
        if (source / 'thumbnail.png').exists():
            thumbnail.update(copy_blob(source / 'thumbnail.png', target / 'thumbnail.png'))
        manifest['objects'].append({'id': ident, 'reference': 'ready' if ident in expected else 'orphan',
                                    **original, 'thumbnail': thumbnail})
    path = destination / 'attachments.json'
    path.write_text(json.dumps(manifest, indent=2) + '\n')
    verify(db, destination)
    return manifest


def verify(db, destination):
    manifest = json.loads((destination / 'attachments.json').read_text())
    if manifest.get('format') != 1 or not isinstance(manifest.get('objects'), list):
        raise ValueError('unsupported attachment manifest')
    expected = ready_rows(db)
    seen = set()
    for item in manifest['objects']:
        ident = item['id']
        if not ID.fullmatch(ident) or ident in seen:
            raise ValueError('invalid or duplicate attachment manifest ID')
        seen.add(ident)
        if item.get('reference') not in ('ready', 'orphan'):
            raise ValueError('invalid attachment manifest reference')
        metadata = (item['size_bytes'], item['sha256'])
        if item['reference'] == 'ready' and expected.get(ident) != metadata:
            raise ValueError('attachment manifest differs from snapshot')
        if ident in expected and item['reference'] != 'ready':
            raise ValueError('ready attachment misclassified as orphan')
        folder = destination / 'attachments/objects' / ident
        directory(folder)
        original = folder / 'original'
        if (regular(original).st_size, digest(original)) != metadata:
            raise ValueError('backup original size/checksum mismatch')
        thumb = item['thumbnail']
        if thumb.get('regenerable') is not True:
            raise ValueError('thumbnail regeneration policy missing')
        if 'sha256' in thumb:
            thumbnail = folder / 'thumbnail.png'
            if (regular(thumbnail).st_size, digest(thumbnail)) != (thumb['size_bytes'], thumb['sha256']):
                raise ValueError('backup thumbnail size/checksum mismatch')
    if not set(expected).issubset(seen):
        raise ValueError('snapshot originals missing from manifest')
    return manifest
