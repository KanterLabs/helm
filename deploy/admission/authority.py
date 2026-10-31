"""Durable root-owned CLOSED authority, permanent lock and paged inventory."""
import fcntl
import os
import stat

from .filesystem import (BootstrapError, FLAGS, PRIVATE_ID, bounded_read, canonical,
                         bounded_names, digest, identity, no_acl, strict_json)
from .inventory_pages import MAX_INDEXES, MAX_PAGES, build as inventory_pages, load as load_inventory

JOURNAL_KEYS = {'format', 'phase', 'plan_sha256', 'marker', 'indexes'}
METADATA_LIMIT = 20000


def marker_for(plan, lock):
    return dict(format=1, minimum_authorization=41, phase='prepared', lane=plan['lane'],
                generation=1, operation_nonce=plan['operation_nonce'], data_device=plan['data_device'],
                data_inode=plan['data_inode'], lock_device=lock.st_dev, lock_inode=lock.st_ino,
                binary_sha256=plan['binary_sha256'], role='candidate', image=plan['image'],
                release_sha=plan['release_sha'])


def write_new(directory, name, body, mode=0o600, gid=0):
    if len(body) > METADATA_LIMIT:
        raise BootstrapError('durable metadata bound exceeded')
    fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                 mode, dir_fd=directory)
    try:
        os.fchown(fd, 0, gid)
        os.fchmod(fd, mode)
        view = memoryview(body)
        while view:
            view = view[os.write(fd, view):]
        os.fsync(fd)
    finally:
        os.close(fd)
    os.fsync(directory)


def read_authority(directory, name, mode=0o600, gid=0, maximum=METADATA_LIMIT):
    fd = os.open(name, FLAGS, dir_fd=directory)
    try:
        s = os.fstat(fd)
        if (not stat.S_ISREG(s.st_mode) or s.st_nlink != 1 or s.st_uid != 0
                or s.st_gid != gid or stat.S_IMODE(s.st_mode) != mode):
            raise BootstrapError('authority metadata invalid; manual reconciliation required')
        no_acl(fd)
        body = bounded_read(fd, maximum)
        if identity(os.stat(name, dir_fd=directory, follow_symlinks=False)) != identity(s):
            raise BootstrapError('authority identity changed')
        return body
    finally:
        os.close(fd)


def lock_existing(directory):
    # O_CREAT is deliberately absent: a lost permanent lock is never repaired.
    fd = os.open('runtime.lock', FLAGS, dir_fd=directory)
    try:
        s = os.fstat(fd)
        if (not stat.S_ISREG(s.st_mode) or s.st_nlink != 1 or s.st_uid != 0
                or s.st_gid != PRIVATE_ID or stat.S_IMODE(s.st_mode) != 0o640 or s.st_size != 0):
            raise BootstrapError('permanent lock metadata invalid; manual reconciliation required')
        no_acl(fd)
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return fd
    except BaseException:
        os.close(fd)
        raise


def check_lock(directory, lock, marker):
    actual = os.stat('runtime.lock', dir_fd=directory, follow_symlinks=False)
    pinned = os.fstat(lock)
    if identity(actual) != identity(pinned) or identity(pinned) != (marker['lock_device'], marker['lock_inode']):
        raise BootstrapError('permanent lock identity differs; manual reconciliation required')


def load_state(directory, lock, plan):
    s = os.fstat(directory)
    metadata = s.st_uid, s.st_gid, stat.S_IMODE(s.st_mode)
    if metadata not in ((0, 0, 0o700), (0, PRIVATE_ID, 0o700), (0, PRIVATE_ID, 0o750)):
        raise BootstrapError('authority directory metadata invalid')
    no_acl(directory)
    journal = strict_json(read_authority(directory, 'journal.json'))
    if (not isinstance(journal, dict) or set(journal) != JOURNAL_KEYS
            or type(journal['format']) is not int or journal['format'] != 2
            or journal['phase'] not in ('preparing', 'prepared') or journal['plan_sha256'] != digest(plan)):
        raise BootstrapError('same original plan required; reconcile CLOSED authority manually')
    if journal['phase'] == 'prepared' and metadata != (0, PRIVATE_ID, 0o750):
        raise BootstrapError('prepared authority directory metadata differs')
    marker = strict_json(read_authority(directory, 'marker.json', mode=0o640, gid=PRIVATE_ID, maximum=4096))
    if (canonical(marker) != canonical(marker_for(plan, os.fstat(lock)))
            or canonical(marker) != canonical(journal['marker'])):
        raise BootstrapError('CLOSED marker identity differs; manual reconciliation required')
    check_lock(directory, lock, marker)
    records, files = load_inventory(directory, journal['indexes'], read_authority)
    expected = ['runtime.lock', 'marker.json', 'journal.json'] + files
    if bounded_names(directory, MAX_PAGES + MAX_INDEXES + 3) != sorted(expected):
        raise BootstrapError('unknown partial authority; manual reconciliation required')
    if len(records) != plan['entries'] or digest(records) != plan['original_inventory_sha256']:
        raise BootstrapError('durable original inventory differs')
    return journal, records


def create_state(parent, plan, source):
    pages, indexes, descriptors = inventory_pages(source.records)
    # Ensure the eventual journal fits before committing the first authority inode.
    template = dict(format=2, phase='preparing', plan_sha256=digest(plan),
                    marker=marker_for(plan, os.fstat(source.fds['data'])), indexes=descriptors)
    if len(canonical(template)) > METADATA_LIMIT:
        raise BootstrapError('durable journal bound exceeded')
    os.mkdir('security', mode=0o700, dir_fd=parent)
    os.fsync(parent)
    directory = os.open('security', FLAGS | os.O_DIRECTORY, dir_fd=parent)
    lock = None
    try:
        os.fchown(directory, 0, 0)
        os.fchmod(directory, 0o700)
        os.fsync(directory)
        lock = os.open('runtime.lock', os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                       0o640, dir_fd=directory)
        os.fchown(lock, 0, PRIVATE_ID)
        os.fchmod(lock, 0o640)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        os.fsync(lock)
        os.fsync(directory)
        marker = marker_for(plan, os.fstat(lock))
        write_new(directory, 'marker.json', canonical(marker), mode=0o640, gid=PRIVATE_ID)
        for name, body in pages + indexes:
            write_new(directory, name, body)
        journal = dict(format=2, phase='preparing', plan_sha256=digest(plan), marker=marker, indexes=descriptors)
        write_new(directory, 'journal.json', canonical(journal))
        os.fsync(parent)
        return directory, lock, journal
    except BaseException:
        if lock is not None:
            os.close(lock)
        os.close(directory)
        raise


def pinned_authority(parent, directory):
    if identity(os.stat('security', dir_fd=parent, follow_symlinks=False)) != identity(os.fstat(directory)):
        raise BootstrapError('authority directory identity changed')
