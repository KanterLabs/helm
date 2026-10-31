"""Directory handover, native access exclusion, then regular-file metadata."""
import os
import stat

from .filesystem import BootstrapError, PRIVATE_ID, identity, no_acl
from . import drain

MECHANISM = 'directory-handover-and-native-host-drain-v1'


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


def checked_metadata(inventory, record):
    fd = inventory.fds[record['path']]
    s = os.fstat(fd)
    if (identity(s) != (record['device'], record['inode']) or s.st_mtime_ns != record['mtime_ns']
            or (s.st_uid, s.st_gid, stat.S_IMODE(s.st_mode)) != inventory.expected_metadata[record['path']]):
        raise BootstrapError('source metadata changed before handover; preserve for manual reconciliation')
    no_acl(fd)
    return fd, s


def transfer(inventory, record):
    fd, before = checked_metadata(inventory, record)
    mode = desired_mode(record)
    if (before.st_uid, before.st_gid, stat.S_IMODE(before.st_mode)) == (PRIVATE_ID, PRIVATE_ID, mode):
        return
    os.fchown(fd, PRIVATE_ID, PRIVATE_ID)
    after = os.fstat(fd)
    # Directory chown preserves all permission bits on the supported Linux
    # kernel. A legacy race survives ownership transfer and MUST precede chmod.
    if (identity(after) != identity(before) or after.st_mtime_ns != before.st_mtime_ns
            or stat.S_IMODE(after.st_mode) != stat.S_IMODE(before.st_mode)
            or (after.st_uid, after.st_gid) != (PRIVATE_ID, PRIVATE_ID)):
        raise BootstrapError('source mode changed during handover; preserve for manual reconciliation')
    no_acl(fd)
    os.fchmod(fd, mode)
    inventory.expected_metadata[record['path']] = (PRIVATE_ID, PRIVATE_ID, mode)
    checked_metadata(inventory, record)
    os.fsync(fd)


def close_directories(inventory, original):
    inventory.rehash()
    # Inventory traversal is root-to-leaf in each tree. Every directory in both
    # trees is handed over before the caller may inspect or mutate regular files.
    for record in original:
        if record['kind'] == 'directory':
            transfer(inventory, record)
    inventory.rehash()


def native_snapshots(lane_path):
    from .filesystem import digest
    evidence = {}
    for name in ('data', 'config'):
        for attempt in range(3):
            result = drain.observe(lane_path / name)
            if (result.get('status') == 'no_access_observed' and not result.get('reasons')
                    and not result.get('matching_processes')):
                evidence[name + '_snapshot_sha256'] = digest(result)
                break
            if (result.get('reasons') == ['process-set-changed']
                    and not result.get('matching_processes') and attempt < 2):
                continue
            raise BootstrapError('native host access observation is not clean; reconcile retained access or uncertainty')
    return evidence


def apply_regular(inventory, original):
    # The caller must have closed ALL directories and obtained fresh native
    # observations under prepared authority and the held permanent lane lock.
    # The installer must also exclude file-only aliases and unsupported actor
    # profiles; neither namespace binding nor a snapshot proves those premises.
    inventory.rehash()
    for record in original:
        if record['kind'] == 'file':
            transfer(inventory, record)
    inventory.rehash()
