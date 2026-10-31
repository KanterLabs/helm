#!/usr/bin/python3 -I
"""Pinned read-only SSH transport; secrets/config live in its private directory.

Copy this reviewed executable into a mode0700 job-private directory containing
mode0600 client.json, id_ed25519 and known_hosts. No ambient credentials or remote
commands are accepted. Host evidence remains subject to StopWitness validation.
"""
import ipaddress
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

DIRECTORY = Path(__file__).resolve().parent
SSH = '/usr/bin/ssh'


def private_file(name):
    path = DIRECTORY / name
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
            or info.st_mode & 0o077 or info.st_size > 20000):
        raise ValueError('untrusted observer input')
    return path


def main():
    try:
        info = DIRECTORY.lstat()
        if (len(sys.argv) != 1 or not stat.S_ISDIR(info.st_mode)
                or info.st_uid != os.getuid() or info.st_mode & 0o077):
            raise ValueError('private observer directory required')
        config = json.loads(private_file('client.json').read_text())
        if (not isinstance(config, dict) or set(config) != {'host', 'port', 'user'}
                or config['user'] != 'helm-observer' or type(config['port']) is not int
                or config['port'] != 22 or not isinstance(config['host'], str)):
            raise ValueError('observer endpoint mismatch')
        host = ipaddress.ip_address(config['host'])
        if not any(host in ipaddress.ip_network(n) for n in ['100.64.0.0/10', 'fd7a:115c:a1e0::/48']):
            raise ValueError('observer must use an allocated private Tailnet address')
        key, known = private_file('id_ed25519'), private_file('known_hosts')
        request = sys.stdin.buffer.read(20001)
        if len(request) > 20000:
            raise ValueError('observer request exceeds limit')
        command = [SSH, '-F', '/dev/null', '-T', '-p', '22', '-i', str(key),
                   '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'IdentityAgent=none',
                   '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(known),
                   '-o', 'GlobalKnownHostsFile=/dev/null', '-o', 'ClearAllForwardings=yes',
                   '-o', 'ForwardAgent=no', '-o', 'PermitLocalCommand=no',
                   '-o', 'ConnectTimeout=5', '-o', 'ConnectionAttempts=1',
                   '-o', 'ServerAliveInterval=3', '-o', 'ServerAliveCountMax=2',
                   'helm-observer@' + str(host)]
        result = subprocess.run(command, input=request, capture_output=True, timeout=17,
                                cwd='/', env={'PATH': '/usr/bin:/bin', 'LANG': 'C'})
        if result.returncode or len(result.stdout) > 20000:
            raise ValueError('observer connection unavailable')
        # Validate bounded JSON before forwarding only the host's response.
        json.loads(result.stdout)
        sys.stdout.write(result.stdout.decode())
        return 0
    except (OSError, ValueError, TypeError, RecursionError, subprocess.TimeoutExpired):
        print('SSH observer unavailable', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
