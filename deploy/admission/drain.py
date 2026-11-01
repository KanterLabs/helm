"""Bounded Linux access snapshot; never a fence or continuing exclusion proof.

The caller must establish the private UID barrier, closed guard authority, and
host execution externally. PID namespace equality cannot prove host execution.
Only metadata is read. Scanner-owned directory descriptors are closed before
scanning; only this process is excluded to avoid observing its own inspection.
"""
import os
from pathlib import Path
import re
import stat
import sys

from .mount_scope import bound_mount_count, native_legacy_identity, source_scope

MAX_ENTRIES = 32768
MAX_PIDS = 4096
MAX_FDS = 4096
MAX_MATCHES = 64
MAX_MAP_BYTES = 1024 * 1024
MAX_TOTAL_BYTES = 128 * 1024 * 1024
MAX_REASONS = 24
_NAMESPACE = re.compile(r'^(pid|mnt):\[[0-9]+\]$')


class Uncertain(Exception):
    pass


def _fail(reason):
    raise Uncertain(reason)


def _read(path, maximum, budget=None):
    remaining = MAX_TOTAL_BYTES - budget[0] if budget is not None else maximum + 1
    if remaining <= 0:
        _fail('aggregate-limit')
    with path.open('rb') as stream:
        content = stream.read(min(maximum + 1, remaining))
    if len(content) > maximum:
        _fail('maps-limit' if path.name == 'maps' else 'metadata-limit')
    if budget is not None:
        budget[0] += len(content)
        if budget[0] >= MAX_TOTAL_BYTES:
            _fail('aggregate-limit')
    try:
        return content.decode('utf-8', 'strict')
    except UnicodeError:
        _fail('proc-malformed')


def _entries(path, maximum, reason):
    names = []
    with os.scandir(path) as entries:
        for entry in entries:
            names.append(entry.name)
            if len(names) > maximum:
                _fail(reason)
    return names


def _metadata(value):
    return (value.st_dev, value.st_ino, value.st_mode, value.st_nlink,
            value.st_uid, value.st_gid, value.st_size, value.st_mtime_ns,
            value.st_ctime_ns)


def _inventory(root):
    objects, directories = {}, set()
    root_device = None
    pending = [(root, ())]
    while pending:
        path, key = pending.pop()
        before = path.lstat()
        if root_device is None:
            root_device = before.st_dev
        if before.st_dev != root_device:
            _fail('inventory-mount-boundary')
        mode = before.st_mode
        opaque_link = stat.S_ISLNK(mode)
        if not (stat.S_ISDIR(mode) or stat.S_ISREG(mode) or opaque_link):
            _fail('inventory-unsupported')
        if opaque_link and key in {('roadmap.db',), ('roadmap.db-wal',), ('roadmap.db-shm',)}:
            _fail('inventory-unsupported')
        if stat.S_ISREG(mode) and before.st_nlink != 1:
            _fail('inventory-unsupported')
        # O_PATH pins the symlink object itself; never open its target.
        flags = (os.O_PATH if opaque_link else os.O_RDONLY) | os.O_CLOEXEC | os.O_NOFOLLOW
        if stat.S_ISDIR(mode):
            flags |= os.O_DIRECTORY
        fd = os.open(path, flags)
        try:
            if _metadata(os.fstat(fd)) != _metadata(before):
                _fail('inventory-changed')
            attributes = (os.listxattr(path, follow_symlinks=False) if opaque_link
                          else os.listxattr(fd))
            if any(name in ('system.posix_acl_access', 'system.posix_acl_default')
                   for name in attributes):
                _fail('inventory-acl')
            identity = (before.st_dev, before.st_ino)
            objects[key] = _metadata(before)
            if len(objects) + len(pending) > MAX_ENTRIES:
                _fail('inventory-limit')
            if stat.S_ISDIR(mode):
                directories.add(identity)
                names = _entries(fd, MAX_ENTRIES, 'inventory-limit')
                for name in names:
                    pending.append((path / name, key + (name,)))
                if len(objects) + len(pending) > MAX_ENTRIES:
                    _fail('inventory-limit')
            if _metadata(os.fstat(fd)) != _metadata(before):
                _fail('inventory-changed')
        finally:
            os.close(fd)
        if _metadata(path.lstat()) != _metadata(before):
            _fail('inventory-changed')
    return objects, directories


def _unescape_mount(value):
    # Linux mountinfo encodes whitespace and backslashes as octal escapes.
    return re.sub(r'\\([0-7]{3})', lambda m: chr(int(m.group(1), 8)), value)


def _mounts(text, root=None, *, collector_view=True):
    for line in text.splitlines():
        fields = line.split()
        try:
            divider = fields.index('-')
            if divider < 6 or len(fields) < divider + 4:
                _fail('mountinfo-malformed')
            point = _unescape_mount(fields[4])
            if not point.startswith('/'):
                _fail('mountinfo-malformed')
            options = fields[5].split(',') + fields[divider + 3].split(',')
            if collector_view and fields[divider + 1] == 'proc' and any(
                    option.startswith('hidepid=') and option != 'hidepid=0'
                    or option == 'subset=pid' for option in options):
                _fail('restricted-proc-visibility')
            if root is not None and Path(point) != root and root in Path(point).parents:
                _fail('nested-mount')
        except (ValueError, IndexError):
            _fail('mountinfo-malformed')
    if not text.strip():
        _fail('mountinfo-malformed')


def _provenance(proc, root):
    info = {'host_execution': 'requires-external-verification'}
    for namespace in ('pid', 'mnt'):
        init = os.readlink(proc / '1/ns' / namespace)
        own = os.readlink(proc / 'self/ns' / namespace)
        if not _NAMESPACE.fullmatch(init) or not _NAMESPACE.fullmatch(own):
            _fail('provenance-malformed')
        info[namespace + '_namespace_matches_init'] = init == own
        if init != own:
            _fail('namespace-mismatch')
    own_status = _read(proc / 'self/status', 65536)
    namespace_pids = [line.split()[1:] for line in own_status.splitlines()
                      if line.startswith('NSpid:')]
    if len(namespace_pids) != 1 or not namespace_pids[0] or any(
            not value.isdecimal() for value in namespace_pids[0]):
        _fail('provenance-malformed')
    if len(namespace_pids[0]) != 1:
        _fail('nested-pid-namespace')
    _mounts(_read(proc / 'self/mountinfo', MAX_MAP_BYTES), root)
    return info


def _gone(path):
    try:
        path.lstat()
        return False
    except FileNotFoundError:
        return True


def _starttime(path, pid, budget):
    value = _read(path / 'stat', 65536, budget)
    # Comm may contain spaces and parentheses; its final ')' ends field two.
    end = value.rfind(')')
    fields = value[end + 1:].split()
    if end < 0 or value.split(' ', 1)[0] != str(pid) or len(fields) < 20:
        _fail('process-malformed')
    if not fields[19].isdecimal():
        _fail('process-malformed')
    return fields[19]


def _process(proc, pid, identities, directories, budget, match, scope, host_namespace):
    path = proc / str(pid)
    before = path.stat()
    starttime = _starttime(path, pid, budget)
    status = _read(path / 'status', 65536, budget)
    if [line.split()[1:] for line in status.splitlines() if line.startswith('Pid:')] != [[str(pid)]]:
        _fail('process-malformed')
    kernel_fields = [line.split()[1:] for line in status.splitlines() if line.startswith('Kthread:')]
    if kernel_fields not in ([], [['0']], [['1']]):
        _fail('process-malformed')
    kernel_thread = kernel_fields == [['1']]
    # Another task's private proc restrictions do not restrict our host view.
    # Its metadata still must be readable, bounded and syntactically complete.
    mountinfo = _read(path / 'mountinfo', MAX_MAP_BYTES, budget)
    _mounts(mountinfo, collector_view=False)
    try:
        count = bound_mount_count(mountinfo, scope)
        if count:
            match['namespace_mount_count'] = count
        namespace = os.readlink(path / 'ns/pid')
        if native_legacy_identity(status, namespace, host_namespace):
            match['native_legacy_identity'] = True
    except ValueError:
        _fail('process-scope-unavailable')
    # Read all names before filtering: limits cover hostile nonnumeric entries too.
    descriptors = _entries(path / 'fd', MAX_FDS, 'fd-limit')
    for descriptor in descriptors:
        if not descriptor.isdecimal():
            _fail('fd-malformed')
        fd_path = path / 'fd' / descriptor
        try:
            value = fd_path.stat()
        except FileNotFoundError:
            if _gone(fd_path):
                continue
            raise
        if (value.st_dev, value.st_ino) in identities:
            match['fd_count'] += 1
    maps = _read(path / 'maps', MAX_MAP_BYTES, budget)
    for line in maps.splitlines():
        fields = line.split(None, 5)
        try:
            if len(fields) < 5 or not re.fullmatch(r'[0-9a-fA-F]+-[0-9a-fA-F]+', fields[0]):
                _fail('maps-malformed')
            if not re.fullmatch(r'[r-][w-][x-][ps]', fields[1]):
                _fail('maps-malformed')
            int(fields[2], 16)
            major, minor = fields[3].split(':')
            identity = (os.makedev(int(major, 16), int(minor, 16)), int(fields[4]))
            if identity in identities:
                match['mapping_count'] += 1
        except (ValueError, OverflowError):
            _fail('maps-malformed')
    if kernel_thread:
        # Kernel tasks may retain a normal fs cwd or have no resolvable cwd.
        # Only explicit kernel identity plus empty FD/maps permits the latter.
        if descriptors or maps.strip():
            _fail('kernel-thread-conflict')
        try:
            cwd = (path / 'cwd').stat()
        except FileNotFoundError:
            # Linux procfs exposes a dangling cwd symlink for kernel tasks.
            pass
        else:
            match['cwd_match'] = (cwd.st_dev, cwd.st_ino) in directories
    else:
        cwd = (path / 'cwd').stat()
        match['cwd_match'] = (cwd.st_dev, cwd.st_ino) in directories
    after = path.stat()
    if starttime != _starttime(path, pid, budget):
        _fail('process-changed')
    if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino):
        _fail('process-changed')
    return match


def observe(data_root: Path, *, proc_root=Path('/proc')) -> dict:
    """Observe metadata without asserting a fence or verified host provenance.

    A nondefault proc root is supported for restricted fixtures and can never
    return a clean result. Errors dominate detected access: matching records
    remain useful evidence when the overall result is unknown.
    """
    result = {'format': 1, 'scope': 'protected-tree-access-snapshot',
              'status': 'unknown', 'target': None, 'scanned_process_count': 0,
              'matching_processes': [], 'reasons': [],
              'provenance': {'host_execution': 'requires-external-verification'}}
    def reason(code):
        if code not in result['reasons'] and len(result['reasons']) < MAX_REASONS:
            result['reasons'].append(code)
    def has_access(match):
        return (match['fd_count'] or match['mapping_count'] or match['cwd_match']
                or match.get('namespace_mount_count') or match.get('native_legacy_identity'))
    def retain(match, uncertainty=None):
        if not has_access(match):
            return True
        if len(result['matching_processes']) >= MAX_MATCHES:
            reason('matching-process-limit')
            return False
        record = dict(match)
        if uncertainty is not None:
            # Evidence survives later errors, without asserting current PID
            # identity or completeness of this process inspection.
            record.update(provisional=True, uncertainty=uncertainty)
        result['matching_processes'].append(record)
        return True
    if sys.platform != 'linux' or os.geteuid() != 0:
        reason('linux-root-required')
        return result
    proc, root = Path(proc_root), Path(data_root)
    if proc != Path('/proc'):
        reason('restricted-proc-visibility')
    try:
        first = root.lstat()
        if not stat.S_ISDIR(first.st_mode):
            _fail('inventory-unsupported')
        result['target'] = {'device': first.st_dev, 'inode': first.st_ino}
        try:
            result['provenance'] = _provenance(proc, root.resolve(strict=True))
        except OSError:
            _fail('provenance-unreadable')
        initial, directories = _inventory(root)
        scope = source_scope(root.resolve(strict=True), first,
                             _read(proc / 'self/mountinfo', MAX_MAP_BYTES))
        host_namespace = os.readlink(proc / '1/ns/pid')
        identities = {(value[0], value[1]) for value in initial.values()}
        names = _entries(proc, MAX_PIDS + 256, 'pid-limit')
        pids = [int(name) for name in names if name.isdecimal() and int(name) > 0]
        if len(pids) > MAX_PIDS:
            _fail('pid-limit')
        budget = [0]
        for pid in sorted(pids):
            if pid == os.getpid():
                continue
            match = {'pid': pid, 'fd_count': 0, 'mapping_count': 0, 'cwd_match': False}
            try:
                _process(proc, pid, identities, directories, budget, match, scope, host_namespace)
                result['scanned_process_count'] += 1
                if not retain(match):
                    break
            except OSError:
                if has_access(match):
                    reason('process-unreadable')
                    if not retain(match, 'process-unreadable'):
                        break
                else:
                    try:
                        if not _gone(proc / str(pid)):
                            reason('process-unreadable')
                    except OSError:
                        reason('process-unreadable')
            except Uncertain as error:
                reason(str(error))
                if not retain(match, str(error)):
                    break
                if str(error) == 'aggregate-limit':
                    break
        final, _ = _inventory(root)
        if initial != final or _metadata(root.lstat()) != _metadata(first):
            reason('inventory-changed')
        # PID discovery is repeated to catch obvious creation/reuse during scan.
        after_names = _entries(proc, MAX_PIDS + 256, 'pid-limit')
        after_pids = {int(name) for name in after_names if name.isdecimal() and int(name) > 0}
        if after_pids - set(pids):
            reason('process-set-changed')
    except Uncertain as error:
        reason(str(error))
    except (OSError, ValueError):
        reason('inventory-unreadable')
    if not result['reasons']:
        result['status'] = 'access_present' if result['matching_processes'] else 'no_access_observed'
    return result
