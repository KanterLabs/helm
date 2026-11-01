"""Read-only, locally checksum-pinned host observer interface; no remote shell.

Parent supplies the actual least-privilege transport/helper. Its continuing
exclusion fence must cover surviving containers, restart policies and queued
operations, and outlive this process. Coolify cached status is never proof.
"""
import copy
import hashlib
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time

from rollback_policy import HASH_RE, PolicyError, keys, strict_json


class StopWitness:
    def __init__(self, path, checksum, target):
        self.path, self.checksum, self.target = Path(path), checksum, target
        self.fence = None
        self.containers = None
        self.pin()

    def pin(self):
        if (not self.path.is_absolute() or self.path.is_symlink() or not self.path.is_file()
                or self.path.stat().st_size > 1_048_576 or not os.access(self.path, os.X_OK)
                or not HASH_RE.fullmatch(self.checksum)
                or hashlib.sha256(self.path.read_bytes()).hexdigest() != self.checksum):
            raise PolicyError('stop observer missing, invalid or checksum mismatch')

    def transport(self, request):
        self.pin()
        # No API tokens/ambient credentials enter the observer. Its operator
        # transport is separately supplied and reviewed by the parent.
        import json
        with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
            try:
                result = subprocess.run([str(self.path)], input=json.dumps(request).encode(),
                    stdout=stdout, stderr=stderr, timeout=20,
                    env={'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8'}, cwd='/')
            except (OSError, subprocess.TimeoutExpired):
                raise PolicyError('stop observer could not finish') from None
            if result.returncode or stdout.tell() > 20_000 or stderr.tell() > 20_000:
                raise PolicyError('stop observer failed or exceeded output bounds')
            stdout.seek(0)
            return strict_json(stdout.read(20_001))

    def observe(self, previous, transport):
        self.pin()
        started = int(time.time())
        request = {'nonce': secrets.token_hex(32), 'target': self.target,
                   'expected': {'container_name': 'helm-' + self.target['service_uuid'],
                                'configured': previous, 'container_ids': self.containers or []},
                   'fence': self.fence or ''}
        value = transport(copy.deepcopy(request))
        keys(value, {'format', 'nonce', 'target', 'expected', 'observed_at', 'expires_at',
                     'result', 'running_writers', 'restart_eligible', 'queued_operations',
                     'fence', 'container_ids'}, 'stop witness')
        now = int(time.time())
        if (type(value['format']) is not int or value['format'] != 1
                or any(value[k] != request[k] for k in ['nonce', 'target', 'expected'])
                or type(value['observed_at']) is not int or type(value['expires_at']) is not int
                or not started <= value['observed_at'] <= now
                or not now < value['expires_at'] <= value['observed_at'] + 60
                or value['result'] != 'stopped-and-fenced'
                or any(value[k] != [] for k in ['running_writers', 'restart_eligible', 'queued_operations'])):
            raise PolicyError('stop witness is stale, mismatched or exclusion is uncertain')
        if (not isinstance(value['fence'], str) or not 1 <= len(value['fence']) <= 128
                or (self.fence is not None and value['fence'] != self.fence)
                or not isinstance(value['container_ids'], list) or not 1 <= len(value['container_ids']) <= 16
                or any(not isinstance(c, str) or not 1 <= len(c) <= 128 for c in value['container_ids'])
                or (self.containers is not None and value['container_ids'] != self.containers)):
            raise PolicyError('stop witness continuing fence/container identity missing or changed')
        self.fence = value['fence']
        self.containers = list(value['container_ids'])
        # Receipt is bounded/allowlisted; no host paths, raw inspect or stderr.
        return {'nonce': value['nonce'], 'observed_at': value['observed_at'],
                'expires_at': value['expires_at'], 'fence': value['fence'],
                'container_ids': value['container_ids'], 'result': value['result']}
