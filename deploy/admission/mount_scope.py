"""Filesystem coordinates and potential actors; never a writer-fence assertion."""
import os
from pathlib import Path
import re
import stat


def unescape(value):
    return re.sub(r'\\([0-7]{3})', lambda match: chr(int(match.group(1), 8)), value)


def rows(text, device):
    result = []
    for line in text.splitlines():
        fields = line.split()
        try:
            divider = fields.index('-')
            if divider < 6 or len(fields) < divider + 4:
                raise ValueError
            major, minor = fields[2].split(':')
            if not major.isdecimal() or not minor.isdecimal():
                raise ValueError
            actual = os.makedev(int(major), int(minor))
            if actual != device:
                # nsfs roots are namespace handles, not filesystem paths.
                # They cannot expose an inode on the protected filesystem.
                continue
            root, point = Path(unescape(fields[3])), Path(unescape(fields[4]))
            if not root.is_absolute() or not point.is_absolute() or '..' in root.parts + point.parts:
                raise ValueError
            result.append((int(fields[0]), actual, root, point))
        except (ValueError, IndexError, OverflowError):
            raise ValueError('mount scope unavailable') from None
    return result


def source_scope(root, expected, mountinfo):
    """Use the actual open directory's mount ID, including same-device binds."""
    fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        actual = os.fstat(fd)
        if (not stat.S_ISDIR(actual.st_mode)
                or (actual.st_dev, actual.st_ino) != (expected.st_dev, expected.st_ino)):
            raise ValueError('mount scope unavailable')
        with open('/proc/self/fdinfo/' + str(fd), encoding='ascii') as stream:
            metadata = stream.read(65537)
        if len(metadata) > 65536:
            raise ValueError('mount scope unavailable')
        ids = [line.split()[1:] for line in metadata.splitlines() if line.startswith('mnt_id:')]
        if len(ids) != 1 or len(ids[0]) != 1 or not ids[0][0].isdecimal():
            raise ValueError('mount scope unavailable')
        mounted = [row for row in rows(mountinfo, actual.st_dev) if row[0] == int(ids[0][0])]
        if len(mounted) != 1 or mounted[0][1] != actual.st_dev:
            raise ValueError('mount scope unavailable')
        _, device, filesystem_root, point = mounted[0]
        relative = Path(root).relative_to(point)
        return device, filesystem_root / relative
    finally:
        os.close(fd)


def bound_mount_count(mountinfo, scope):
    device, protected = scope
    # A whole ancestor filesystem still requires searching the private source
    # directories. A bind of the target or a descendant exposes that subtree
    # directly and identifies actors with potentially in-flight operations.
    return sum(1 for _, actual, root, _ in rows(mountinfo, device)
               if actual == device and root.is_relative_to(protected))


def native_legacy_identity(status, process_namespace, host_namespace):
    lines = [line.split()[1:] for line in status.splitlines() if line.startswith('Uid:')]
    if (len(lines) != 1 or len(lines[0]) != 4
            or any(not item.isdecimal() for item in lines[0])):
        raise ValueError('process credentials unavailable')
    if not {65532, 70541}.intersection(map(int, lines[0])):
        return False
    if not re.fullmatch(r'pid:\[[0-9]+\]', process_namespace):
        raise ValueError('process namespace unavailable')
    return process_namespace == host_namespace
