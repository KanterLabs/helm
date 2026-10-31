"""One-time root operator preparation of permanent CLOSED admission authority.

Candidate coordinates are labels only. No command in this module activates an
application, accepts an image/binary pair, drains writers or restores data.
"""
import functools
import os
from pathlib import Path
import secrets
import stat

from . import backup
from .filesystem import (BootstrapError, FLAGS, Inventory, MAX_ENTRIES, ORIGINAL_ID, PRIVATE_ID,
                         apply_private, canonical, desired_mode, digest,
                         bounded_names, lower_hex, matches_original, open_directory, read_private_json)
from .authority import (METADATA_LIMIT, check_lock, create_state, inventory_pages,
                        load_state, lock_existing, pinned_authority, write_new)

BASE = Path('/srv/helm')
PLAN_KEYS = {'format', 'operation', 'lane', 'backup', 'image', 'release_sha',
             'binary_sha256', 'operation_nonce', 'data_device', 'data_inode',
             'config_device', 'config_inode', 'original_inventory_sha256',
             'backup_content_sha256', 'entries', 'total_bytes'}


def guarded(function):
    @functools.wraps(function)
    def call(*args, **kwargs):
        try:
            if os.geteuid() != 0:
                raise BootstrapError('root operator required')
            return function(*args, **kwargs)
        except BootstrapError:
            raise
        except (OSError, ValueError, TypeError, KeyError, UnicodeError, RecursionError):
            raise BootstrapError('filesystem or metadata refused; preserve CLOSED state for operator review') from None
    return call


def coordinates(lane, image, release_sha, binary_sha256, nonce):
    prefix = 'ghcr.io/kanterlabs/helm@sha256:'
    if lane not in ('beta', 'production'):
        raise BootstrapError('exact beta or production lane required')
    if not isinstance(image, str) or not image.startswith(prefix) or not lower_hex(image[len(prefix):], 64):
        raise BootstrapError('immutable candidate image coordinate required')
    if not lower_hex(release_sha, 40) or not lower_hex(binary_sha256, 64) or not lower_hex(nonce, 64):
        raise BootstrapError('lowercase release, binary and operation hashes required')


def validate_plan(plan):
    if not isinstance(plan, dict) or set(plan) != PLAN_KEYS or len(canonical(plan)) > METADATA_LIMIT:
        raise BootstrapError('unsupported or oversized saved plan')
    if type(plan['format']) is not int or plan['format'] != 1 or plan['operation'] != 'closed-bootstrap':
        raise BootstrapError('unsupported saved plan operation')
    coordinates(plan['lane'], plan['image'], plan['release_sha'], plan['binary_sha256'], plan['operation_nonce'])
    backup.validate_name(plan['backup'])
    for key in ('data_device', 'data_inode', 'config_device', 'config_inode', 'entries'):
        if type(plan[key]) is not int or plan[key] <= 0:
            raise BootstrapError('invalid saved plan identity')
    if plan['entries'] > MAX_ENTRIES or type(plan['total_bytes']) is not int or not 0 <= plan['total_bytes'] <= 2 * 1024 ** 3:
        raise BootstrapError('invalid saved plan bounds')
    if not all(lower_hex(plan[key], 64) for key in ('original_inventory_sha256', 'backup_content_sha256')):
        raise BootstrapError('invalid saved plan digest')


def validate_source_binding(plan, records):
    roots = {r['path']: r for r in records if r['path'] in ('data', 'config')}
    for name in ('data', 'config'):
        if (plan[name + '_device'], plan[name + '_inode']) != (roots[name]['device'], roots[name]['inode']):
            raise BootstrapError('saved plan source directory identity differs')
    if plan['entries'] != len(records) or plan['total_bytes'] != sum(r['size'] for r in records):
        raise BootstrapError('saved plan source inventory bounds differ')


@guarded
def preview(lane, backup_basename, image, release_sha, binary_sha256, nonce=None, *, base=BASE):
    """Read source/backup only; SQLite checks use a disposable private copy.

    Only the private library accepts a fixture base. The production CLI has no
    environment, path or ownership overrides. Save stdout as root mode 0600 in
    a root-owned private directory before invoking prepare.
    """
    nonce = secrets.token_hex(32) if nonce is None else nonce
    coordinates(lane, image, release_sha, binary_sha256, nonce)
    backup.validate_name(backup_basename)
    lane_path = Path(base) / lane
    parent = open_directory(lane_path)
    try:
        if 'security' in bounded_names(parent):
            raise BootstrapError('authority already exists; use the original saved plan or reconcile manually')
        with Inventory(parent) as source:
            backup_digest = backup.verify(lane_path, backup_basename, source)
            roots = {r['path']: r for r in source.records if r['path'] in ('data', 'config')}
            plan = dict(format=1, operation='closed-bootstrap', lane=lane, backup=backup_basename,
                        image=image, release_sha=release_sha, binary_sha256=binary_sha256,
                        operation_nonce=nonce, data_device=roots['data']['device'],
                        data_inode=roots['data']['inode'], config_device=roots['config']['device'],
                        config_inode=roots['config']['inode'], entries=len(source.records),
                        total_bytes=sum(r['size'] for r in source.records),
                        original_inventory_sha256=digest(source.records),
                        backup_content_sha256=backup_digest)
            validate_plan(plan)
            # Refuse excessive generated operational metadata before any mkdir.
            inventory_pages(source.records)
            return plan
    finally:
        os.close(parent)


def receipt(plan, journal):
    return dict(format=1, lane=plan['lane'], phase='prepared', closed=True,
                plan_sha256=digest(plan), entries=plan['entries'],
                original_inventory_sha256=plan['original_inventory_sha256'],
                backup_content_sha256=plan['backup_content_sha256'])


@guarded
def prepare(plan_path, *, base=BASE):
    """Independently verify a saved plan and leave only CLOSED authority."""
    plan = read_private_json(Path(plan_path))
    validate_plan(plan)
    lane_path = Path(base) / plan['lane']
    parent = open_directory(lane_path)
    directory = lock = None
    try:
        with Inventory(parent, owners=(ORIGINAL_ID, PRIVATE_ID)) as source:
            validate_source_binding(plan, source.records)
            if backup.verify(lane_path, plan['backup'], source) != plan['backup_content_sha256']:
                raise BootstrapError('saved backup content differs')
            if 'security' in bounded_names(parent):
                directory = os.open('security', FLAGS | os.O_DIRECTORY, dir_fd=parent)
                lock = lock_existing(directory)
                journal, original = load_state(directory, lock, plan)
                matches_original(source.records, original, prepared=journal['phase'] == 'prepared')
                pinned_authority(parent, directory)
                if journal['phase'] == 'prepared':
                    source.rehash()
                    check_lock(directory, lock, journal['marker'])
                    return receipt(plan, journal)
            else:
                if digest(source.records) != plan['original_inventory_sha256']:
                    raise BootstrapError('original source identity, content or metadata differs from saved plan')
                original = source.records
                directory, lock, journal = create_state(parent, plan, source)
            # CLOSED marker + journal + paged originals are already durable.
            pinned_authority(parent, directory)
            check_lock(directory, lock, journal['marker'])
            apply_private(source, original)
            for record in original:
                if record['kind'] == 'symlink':
                    continue  # Immutable opaque link metadata was rechecked, never changed.
                s = os.fstat(source.fds[record['path']])
                if (s.st_uid, s.st_gid, stat.S_IMODE(s.st_mode)) != (PRIVATE_ID, PRIVATE_ID, desired_mode(record)):
                    raise BootstrapError('private metadata verification failed; authority remains CLOSED')
            pinned_authority(parent, directory)
            check_lock(directory, lock, journal['marker'])
            os.fchown(directory, 0, PRIVATE_ID)
            os.fchmod(directory, 0o750)
            os.fsync(directory)
            journal['phase'] = 'prepared'
            write_new(directory, 'journal.next', canonical(journal))
            os.rename('journal.next', 'journal.json', src_dir_fd=directory, dst_dir_fd=directory)
            os.fsync(directory)
            os.fsync(parent)
            return receipt(plan, journal)
    finally:
        if lock is not None:
            os.close(lock)
        if directory is not None:
            os.close(directory)
        os.close(parent)
