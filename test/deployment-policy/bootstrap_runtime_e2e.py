#!/usr/bin/env python3
"""DEV closed-bootstrap integration; no activation or live deployment."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import stat
import sys
import time
from unittest.mock import patch

from bootstrap_fixture import fixture, save, snapshot
from bootstrap_native import launch


ROOT = Path(__file__).resolve().parents[2]
TOOLS = 'golang@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51'
sys.path.insert(0, str(ROOT / 'deploy'))
from admission import bootstrap
from admission.drain import observe
from admission.filesystem import BootstrapError

BASE = 'ghcr.io/kanterlabs/helm@sha256:5484aee2b817d5cd54987a7c24b1ed9b54176430302d6f08eacf8abb59d29cdd'


class Journey:
    def __init__(self, binary, output):
        self.binary = binary.resolve(strict=True)
        self.prefix = 'helm-bootstrap-e2e-' + secrets.token_hex(5)
        self.out = output
        self.fixture = self.out / 'fixture'
        self.fixture.mkdir(parents=True, exist_ok=False)
        self.names = []
        self.events = []

    def call(self, args, stdin=None, timeout=60, required=True):
        result = subprocess.run(args, input=stdin, text=True, capture_output=True, timeout=timeout)
        if required and result.returncode:
            raise RuntimeError(f'{args[0]} failed: {(result.stderr + result.stdout)[:500]}')
        return result

    def initialize(self):
        self.base, self.lane, _ = fixture(self.fixture, 'runtime')
        self.before = snapshot(self.lane / 'data')
        digest = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        self.plan = self.base / 'plan.json'
        save(self.plan, bootstrap.preview('beta', 'synthetic-snapshot', BASE,
                                         '690620c9b9b4a037cf5aa2447cf29d8441a090c9',
                                         digest, 'cd' * 32, base=self.base))

    def holder(self, mode):
        name = self.prefix + '-' + mode
        self.names.append(name)
        code = ("import os,mmap,time; p='/var/lib/roadmap/data/roadmap.db'; "
                "fd=os.open(p,os.O_RDWR); ")
        if mode == 'namespace':
            code = 'import time; '
        elif mode == 'map':
            # Python before 3.13 keeps its own duplicated fd in mmap objects.
            # Use libc directly so this process really has no matching fd.
            code += ('import ctypes; lib=ctypes.CDLL(None,use_errno=True); '
                     'lib.mmap.restype=ctypes.c_void_p; '
                     'lib.mmap.argtypes=[ctypes.c_void_p,ctypes.c_size_t,ctypes.c_int,'
                     'ctypes.c_int,ctypes.c_int,ctypes.c_long]; '
                     'size=os.fstat(fd).st_size; mapping=lib.mmap(None,size,3,1,fd,0); '
                     'assert mapping != ctypes.c_void_p(-1).value; os.close(fd); ')
        code += "print('synthetic-holder-ready',flush=True); time.sleep(180)"
        self.call(['docker', 'run', '-d', '--name', name, '--network', 'none',
                   '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                   '--user', '65532:65532', '--workdir', '/',
                   '--mount', f'type=bind,src={self.lane / "data"},dst=/var/lib/roadmap/data',
                   '--entrypoint', 'python3', TOOLS, '-B', '-u', '-c', code])
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            result = self.call(['docker', 'logs', '--tail', '2', name])
            if 'synthetic-holder-ready' in result.stdout:
                return name
            time.sleep(0.05)
        raise RuntimeError('synthetic holder never became ready')

    def prepare(self, refusal=False):
        regular_handover = [0]
        original = os.fchown
        def count_regular(fd, uid, gid):
            if uid == 70541 and stat.S_ISREG(os.fstat(fd).st_mode):
                regular_handover[0] += 1
            return original(fd, uid, gid)
        try:
            with patch('os.fchown', count_regular):
                value = bootstrap.prepare(self.plan, base=self.base)
        except BootstrapError as error:
            if not refusal:
                raise
            assert regular_handover[0] == 0
            assert (self.lane / 'data/roadmap.db').stat().st_uid == 65532
            assert (self.lane / 'data').stat().st_uid == 70541
            return dict(refused=True, reason=str(error), regular_handover=0)
        assert not refusal, 'retained actor did not block preparation'
        return value

    def observe(self):
        return observe(self.lane / 'data')

    def reject_runtime(self):
        name = self.prefix + '-closed-runtime'
        self.names.append(name)
        result = self.call(['docker', 'run', '--name', name, '--network', 'none',
                            '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                            '--user', '70541:70541', '--workdir', '/',
                            '--mount', f'type=bind,src={self.binary},dst=/usr/local/bin/helm,readonly',
                            '--mount', f'type=bind,src={self.lane / "data"},dst=/var/lib/roadmap/data',
                            '--mount', f'type=bind,src={self.lane / "security"},dst=/etc/helm-admission,readonly',
                            '--entrypoint', '/usr/local/bin/helm', BASE], required=False, timeout=15)
        return dict(rejected=result.returncode != 0 and 'admission' in result.stderr + result.stdout,
                    exit=result.returncode)

    def verify(self):
        lock = (self.lane / 'security/runtime.lock').stat().st_ino
        assert bootstrap.prepare(self.plan, base=self.base)['closed']
        assert (self.lane / 'security/runtime.lock').stat().st_ino == lock
        assert snapshot(self.lane / 'data') == self.before
        marker = self.lane / 'security/marker.json'
        assert json.loads(marker.read_text())['phase'] == 'prepared'
        assert json.loads((self.lane / 'security/journal.json').read_text())['format'] == 3
        marker.unlink()
        try:
            bootstrap.prepare(self.plan, base=self.base)
        except BootstrapError:
            pass
        else:
            raise AssertionError('lost marker was accepted')
        assert not marker.exists()
        return dict(bytes_inodes_mtimes_preserved=True, lock_stable=True,
                    journal_format=3, marker_phase='prepared', lost_marker_refused=True)

    def execute(self):
        failures = []
        try:
            self.initialize()
            namespace = self.holder('namespace')
            self.events.append(dict(stage='namespace-only-blocks', receipt=self.prepare(refusal=True)))
            namespace_scan = self.observe()
            matches = namespace_scan['matching_processes']
            assert matches and any(p.get('namespace_mount_count') for p in matches)
            assert all(p['fd_count'] == p['mapping_count'] == 0 and not p['cwd_match'] for p in matches)
            self.events.append(dict(stage='namespace-without-target-handles', observation=namespace_scan))
            self.call(['docker', 'stop', '-t', '2', namespace])
            # Use a separate fresh fixture for held-access setup. The first
            # fixture stays CLOSED; no ownership or authority is reset.
            self.base, self.lane, _ = fixture(self.fixture, 'retained-handles')
            self.before = snapshot(self.lane / 'data')
            self.plan = self.base / 'plan.json'
            digest = hashlib.sha256(self.binary.read_bytes()).hexdigest()
            save(self.plan, bootstrap.preview('beta', 'synthetic-snapshot', BASE,
                                             '690620c9b9b4a037cf5aa2447cf29d8441a090c9',
                                             digest, 'ef' * 32, base=self.base))
            fd, mapping = self.holder('fd'), self.holder('map')
            self.events.append(dict(stage='held-access-blocks', receipt=self.prepare(refusal=True)))
            both = self.observe()
            matches = both['matching_processes']
            assert both['status'] != 'no_access_observed'
            assert any(p['fd_count'] for p in matches) and any(p['mapping_count'] for p in matches)
            self.events.append(dict(stage='both-held', observation=both))
            self.call(['docker', 'stop', '-t', '2', fd])
            only_map = self.observe()
            matches = only_map['matching_processes']
            assert matches and all(p['fd_count'] == 0 for p in matches) and any(p['mapping_count'] for p in matches)
            self.events.append(dict(stage='mapping-only', observation=only_map))
            self.events.append(dict(stage='mapping-blocks', receipt=self.prepare(refusal=True)))
            self.call(['docker', 'stop', '-t', '2', mapping])
            clean = self.observe()
            assert clean['status'] == 'no_access_observed', clean
            self.events.append(dict(stage='holders-stopped', observation=clean))
            self.events.append(dict(stage='prepared-after-drain', receipt=self.prepare()))
            closed = self.reject_runtime()
            assert closed['rejected'], closed
            self.events.append(dict(stage='closed-runtime', **closed))
            self.events.append(dict(stage='replay-and-lost-marker', result=self.verify()))
        except Exception as error:
            failures.append(type(error).__name__)
            self.events.append(dict(harness_error=str(error)[:1000]))
        finally:
            for name in list(self.names):
                self.call(['docker', 'rm', '-f', name], required=False)
            remaining = self.call(['docker', 'ps', '-aq', '--filter', f'name={self.prefix}']).stdout.split()
            if remaining:
                failures.append('remaining-containers')
            receipt = dict(scope='DEV closed bootstrap and host-access snapshot, NOT continuing fence or activation',
                           events=self.events, failures=failures, remaining_containers=remaining)
            text = json.dumps(receipt, indent=2) + '\n'
            if len(text.encode()) > 20000:
                raise RuntimeError('receipt exceeds bound')
            print(text)
        return 1 if failures else 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.inside:
        raise SystemExit(Journey(args.binary, args.output).execute())
    raise SystemExit(launch(Path(__file__), 'runtime-native', timeout=600,
                            arguments=('--binary', str(args.binary.resolve(strict=True)))))
