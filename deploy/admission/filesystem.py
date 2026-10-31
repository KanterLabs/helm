"""Descriptor-pinned, bounded filesystem observations for CLOSED bootstrap."""
import hashlib
import errno
import json
import os
from pathlib import Path
import re
import resource
import stat
import time

MAX_ENTRIES = 32768
MAX_BYTES = 2 * 1024 ** 3
MAX_SECONDS = 120
PRIVATE_ID = 70541
ORIGINAL_ID = 65532
FLAGS = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK


class BootstrapError(Exception):
    """A fixed, sanitized operator refusal; never embed operational filenames."""


def canonical(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True) + '\n').encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def lower_hex(value, length):
    return isinstance(value, str) and re.fullmatch('[0-9a-f]{' + str(length) + '}', value) is not None


def identity(s):
    return s.st_dev, s.st_ino


def no_acl(fd):
    if any(name in ('system.posix_acl_access', 'system.posix_acl_default')
           for name in os.listxattr(fd)):
        raise BootstrapError('unsupported ACL; remove only after operator review')


def link_hash(fd):
    target = os.fsencode(os.readlink('', dir_fd=fd))
    if len(target) > 4096:
        raise BootstrapError('opaque symlink target exceeds bound')
    return len(target), hashlib.sha256(target).hexdigest()


def no_link_acl(parent, name, fd):
    # O_PATH descriptors cannot be passed to flistxattr. Anchor the parent with
    # its descriptor and prohibit following the final component instead.
    expected = identity(os.fstat(fd))
    if identity(os.stat(name, dir_fd=parent, follow_symlinks=False)) != expected:
        raise BootstrapError('opaque symlink identity changed')
    path = '/proc/self/fd/' + str(parent) + '/' + name
    if any(value in ('system.posix_acl_access', 'system.posix_acl_default')
           for value in os.listxattr(path, follow_symlinks=False)):
        raise BootstrapError('unsupported opaque symlink ACL')
    if identity(os.stat(name, dir_fd=parent, follow_symlinks=False)) != expected:
        raise BootstrapError('opaque symlink identity changed')


def bounded_names(fd, limit=MAX_ENTRIES):
    names = []
    with os.scandir(fd) as entries:
        for entry in entries:
            if len(names) >= limit:
                raise BootstrapError('bounded directory enumeration limit exceeded')
            names.append(entry.name)
    return sorted(names)


def raise_descriptor_budget():
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    desired = 2 * MAX_ENTRIES + 256
    target = desired if hard == resource.RLIM_INFINITY else min(desired, hard)
    if soft != resource.RLIM_INFINITY and soft < target:
        try:
            resource.setrlimit(resource.RLIMIT_NOFILE, (target, hard))
        except (OSError, ValueError):
            pass  # Small trees may fit; refuse only actual capacity exhaustion.


def mount_id(fd):
    # Device alone misses bind mounts on the same filesystem.
    with open('/proc/self/fdinfo/' + str(fd), encoding='ascii') as info:
        for line in info:
            if line.startswith('mnt_id:'):
                return int(line.split()[1])
    raise BootstrapError('mount identity unavailable')


def checked_directory(fd, private=False):
    s = os.fstat(fd)
    if not stat.S_ISDIR(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022:
        raise BootstrapError('untrusted root ancestor')
    if private and s.st_mode & 0o077:
        raise BootstrapError('trusted private directory required')
    no_acl(fd)
    return s


def open_directory(path, private=False):
    """Walk every absolute ancestor without following links; caller owns fd."""
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts:
        raise BootstrapError('absolute trusted path required')
    fd = os.open('/', FLAGS | os.O_DIRECTORY)
    try:
        checked_directory(fd)
        for name in path.parts[1:]:
            child = os.open(name, FLAGS | os.O_DIRECTORY, dir_fd=fd)
            os.close(fd)
            fd = child
            checked_directory(fd)
        checked_directory(fd, private=private)
        return fd
    except BaseException:
        os.close(fd)
        raise


def read_private_json(path, maximum=20000):
    path = Path(path)
    parent = open_directory(path.parent, private=True)
    try:
        # Ancestors are root-owned and never writable by an untrusted identity;
        # the directory containing this operational plan is additionally private.
        fd = os.open(path.name, FLAGS, dir_fd=parent)
        try:
            s = os.fstat(fd)
            if (not stat.S_ISREG(s.st_mode) or s.st_nlink != 1 or s.st_uid != 0
                    or s.st_gid != 0 or stat.S_IMODE(s.st_mode) != 0o600):
                raise BootstrapError('root-private saved plan required')
            no_acl(fd)
            body = bounded_read(fd, maximum)
            value = strict_json(body)
            if identity(os.stat(path.name, dir_fd=parent, follow_symlinks=False)) != identity(s):
                raise BootstrapError('saved plan identity changed')
            return value
        finally:
            os.close(fd)
    finally:
        os.close(parent)


def bounded_read(fd, maximum):
    chunks, count = [], 0
    while True:
        data = os.read(fd, min(65536, maximum + 1 - count))
        if not data:
            return b''.join(chunks)
        chunks.append(data)
        count += len(data)
        if count > maximum:
            raise BootstrapError('bounded metadata limit exceeded')


def strict_json(body):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise BootstrapError('duplicate metadata field')
            result[key] = value
        return result
    return json.loads(body, object_pairs_hook=pairs)


class Inventory:
    """Keep every observed inode open until the caller finishes its operation."""
    def __init__(self, parent, names=('data', 'config'), owners=(ORIGINAL_ID,), backup=False):
        self.parent = parent
        self.names = names
        self.owners = owners
        self.backup = backup
        self.records = []
        self.fds = {}
        self.children = {}
        self._metadata_transition = False
        self.started = time.monotonic()
        self.bytes = 0
        try:
            raise_descriptor_budget()
            parent_stat, parent_mount = os.fstat(parent), mount_id(parent)
            for name in names:
                self._walk(parent, name, name, parent_stat.st_dev, parent_mount, 0)
            self.recheck()
        except OSError as error:
            self.close()
            if error.errno in (errno.EMFILE, errno.ENFILE):
                raise BootstrapError('descriptor capacity exhausted; supply sufficient operator process descriptor capacity') from None
            raise
        except BaseException:
            self.close()
            raise

    def close(self):
        for fd in self.fds.values():
            os.close(fd)
        self.fds.clear()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()

    def deadline(self):
        if time.monotonic() - self.started > MAX_SECONDS:
            raise BootstrapError('bounded inventory deadline exceeded')

    def _hash(self, fd, size):
        self.bytes += size
        if self.bytes > MAX_BYTES:
            raise BootstrapError('bounded source capacity exceeded')
        os.lseek(fd, 0, os.SEEK_SET)
        result, count = hashlib.sha256(), 0
        while True:
            self.deadline()
            data = os.read(fd, 1024 * 1024)
            if not data:
                break
            count += len(data)
            if count > size:
                raise BootstrapError('source changed during observation')
            result.update(data)
        if count != size:
            raise BootstrapError('source changed during observation')
        return result.hexdigest()

    def _walk(self, parent, name, path, device, mount, depth):
        self.deadline()
        if len(self.records) >= MAX_ENTRIES or depth > 64 or len(os.fsencode(path)) > 4096:
            raise BootstrapError('bounded inventory limit exceeded')
        before = os.stat(name, dir_fd=parent, follow_symlinks=False)
        kind = ('directory' if stat.S_ISDIR(before.st_mode) else
                'symlink' if stat.S_ISLNK(before.st_mode) else 'file')
        if not any((stat.S_ISDIR(before.st_mode), stat.S_ISREG(before.st_mode), stat.S_ISLNK(before.st_mode))):
            raise BootstrapError('unsupported source type')
        if (depth == 0 and kind != 'directory') or (path in (
                'data/roadmap.db', 'data/roadmap.db-wal', 'data/roadmap.db-shm') and kind != 'file'):
            raise BootstrapError('source roots and SQLite paths require direct directory or regular-file identity')
        flags = ((os.O_PATH | os.O_NOFOLLOW | os.O_CLOEXEC) if kind == 'symlink'
                 else FLAGS | (os.O_DIRECTORY if kind == 'directory' else 0))
        fd = os.open(name, flags, dir_fd=parent)
        self.fds[path] = fd
        s = os.fstat(fd)
        if identity(s) != identity(before):
            raise BootstrapError('source identity changed')
        current_mount = mount_id(fd)
        if device is not None and (s.st_dev != device or current_mount != mount):
            raise BootstrapError('unsupported source mount boundary')
        if s.st_uid not in self.owners or s.st_gid not in self.owners:
            raise BootstrapError('foreign source ownership')
        if s.st_mode & 0o7000:
            raise BootstrapError('unsupported special source permissions')
        if kind == 'file' and s.st_nlink != 1:
            raise BootstrapError('unsupported source hardlink')
        if self.backup and kind != 'symlink' and s.st_mode & 0o077:
            raise BootstrapError('backup must be root-owned and private')
        no_link_acl(parent, name, fd) if kind == 'symlink' else no_acl(fd)
        record = dict(path=path, kind=kind, device=s.st_dev, inode=s.st_ino,
                      uid=s.st_uid, gid=s.st_gid, mode=stat.S_IMODE(s.st_mode),
                      mtime_ns=s.st_mtime_ns, size=s.st_size if kind == 'file' else 0)
        self.records.append(record)
        if kind == 'file':
            record['sha256'] = self._hash(fd, s.st_size)
        elif kind == 'symlink':
            record['size'], record['sha256'] = link_hash(fd)
        else:
            children = bounded_names(fd)
            self.children[path] = children
            for child in children:
                self._walk(fd, child, path + '/' + child, s.st_dev, current_mount, depth + 1)
            if bounded_names(fd) != children:
                raise BootstrapError('source tree changed during observation')
        after = os.fstat(fd)
        if (identity(after) != identity(s) or after.st_mtime_ns != s.st_mtime_ns
                or (kind == 'file' and after.st_size != s.st_size)):
            raise BootstrapError('source changed during observation')

    def recheck(self):
        for record in self.records:
            path = record['path']
            parent, _, name = path.rpartition('/')
            parent_fd = self.fds[parent] if parent else self.parent
            s = os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
            fd = self.fds[path]
            if (identity(s) != (record['device'], record['inode'])
                    or identity(os.fstat(fd)) != identity(s)
                    or s.st_mtime_ns != record['mtime_ns']):
                raise BootstrapError('source identity or mtime changed')
            allowed = {(record['uid'], record['gid'], record['mode'])}
            if self._metadata_transition and record['kind'] != 'symlink':
                allowed.update(((PRIVATE_ID, PRIVATE_ID, record['mode']),
                                (PRIVATE_ID, PRIVATE_ID, desired_mode(record))))
            if (s.st_uid, s.st_gid, stat.S_IMODE(s.st_mode)) not in allowed:
                raise BootstrapError('source ownership or permissions changed')
            no_link_acl(parent_fd, name, fd) if record['kind'] == 'symlink' else no_acl(fd)
            if record['kind'] == 'file':
                if s.st_nlink != 1 or s.st_size != record['size']:
                    raise BootstrapError('source file changed')
            elif record['kind'] == 'symlink':
                if ((s.st_uid, s.st_gid, stat.S_IMODE(s.st_mode)) !=
                        (record['uid'], record['gid'], record['mode'])
                        or link_hash(fd) != (record['size'], record['sha256'])):
                    raise BootstrapError('opaque symlink metadata or target changed')
            else:
                if bounded_names(fd) != self.children[path]:
                    raise BootstrapError('source tree changed')

    def rehash(self):
        self.bytes = 0
        self.recheck()
        for record in self.records:
            if record['kind'] == 'file':
                if self._hash(self.fds[record['path']], record['size']) != record['sha256']:
                    raise BootstrapError('source content changed; authority remains CLOSED')
        self.recheck()


def content(records):
    return [{key: record[key] for key in ('path', 'kind', 'size')}
            | ({'sha256': record['sha256']} if record['kind'] in ('file', 'symlink') else {})
            for record in records]


def desired_mode(record):
    if record['kind'] == 'directory':
        return 0o700
    if record['path'] == 'data/roadmap.db':
        return 0o600
    return record['mode'] & 0o700


def matches_original(current, original, prepared=False):
    if len(current) != len(original):
        raise BootstrapError('saved source inventory differs')
    for actual, old in zip(current, original):
        if old['kind'] == 'symlink':
            if actual != old:
                raise BootstrapError('saved opaque symlink metadata differs')
            continue
        if {k: v for k, v in actual.items() if k not in ('uid', 'gid', 'mode')} != {
                k: v for k, v in old.items() if k not in ('uid', 'gid', 'mode')}:
            raise BootstrapError('saved source identity or content differs')
        before = old['uid'], old['gid'], old['mode']
        after = PRIVATE_ID, PRIVATE_ID, desired_mode(old)
        found = actual['uid'], actual['gid'], actual['mode']
        # chown and chmod are separate syscalls: interrupted chown may retain
        # original mode, but no arbitrary owner/mode combinations are accepted.
        partial = PRIVATE_ID, PRIVATE_ID, old['mode']
        if found not in ((after,) if prepared else (before, partial, after)):
            raise BootstrapError('saved source metadata differs')


def apply_private(inventory, original):
    inventory.rehash()
    inventory._metadata_transition = True
    for record in original:
        if record['kind'] == 'symlink':
            parent = record['path'].rpartition('/')[0]
            os.fsync(inventory.fds[parent])
            continue
        fd = inventory.fds[record['path']]
        s = os.fstat(fd)
        if identity(s) != (record['device'], record['inode']):
            raise BootstrapError('source identity changed before ownership')
        os.fchown(fd, PRIVATE_ID, PRIVATE_ID)
        os.fchmod(fd, desired_mode(record))
        os.fsync(fd)
    inventory.rehash()
