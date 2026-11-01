"""Verify an externally provisioned root-private native host context binding."""
import os
from pathlib import Path
import re
import stat
import sys

from .filesystem import BootstrapError, digest, open_directory, read_private_json

KEYS = {'format', 'boot_id', 'pid_namespace', 'mnt_namespace'}


def verify(base):
    # This module deliberately has no writer or auto-provisioning path.
    if sys.platform != 'linux' or os.geteuid() != 0:
        raise BootstrapError('native Linux root operator required')
    operator = Path(base) / 'operator'
    directory = open_directory(operator, private=True)
    try:
        metadata = os.fstat(directory)
        if metadata.st_gid != 0 or stat.S_IMODE(metadata.st_mode) != 0o700:
            raise BootstrapError('external native host directory must be root-private mode0700')
        bound = read_private_json(operator / 'host.json', maximum=4096)
    finally:
        os.close(directory)
    if not isinstance(bound, dict) or set(bound) != KEYS or type(bound['format']) is not int or bound['format'] != 1:
        raise BootstrapError('invalid external native host binding')
    with open('/proc/sys/kernel/random/boot_id', encoding='ascii') as stream:
        boot = stream.read(81).strip()
    if not re.fullmatch(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', boot):
        raise BootstrapError('native host boot identity unavailable')
    if bound['boot_id'] != boot:
        raise BootstrapError('external native host boot binding differs')
    for kind in ('pid', 'mnt'):
        own = os.readlink('/proc/self/ns/' + kind)
        init = os.readlink('/proc/1/ns/' + kind)
        if (not re.fullmatch(kind + r':\[[0-9]+\]', own) or own != init
                or bound[kind + '_namespace'] != own):
            raise BootstrapError('external native host namespace binding differs')
    return digest(bound)
