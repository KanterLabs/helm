#!/usr/bin/env python3
"""Real Helm startup on synthetic Docker mounts; never artifact acceptance."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import time


ROOT = Path(__file__).resolve().parents[2]
BASE = 'ghcr.io/kanterlabs/helm@sha256:5484aee2b817d5cd54987a7c24b1ed9b54176430302d6f08eacf8abb59d29cdd'
SETUP = 'golang@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51'
PRIVATE = '/var/lib/roadmap/data'
AUTHORITY = '/etc/helm-admission'


def run(args, timeout=30):
    return subprocess.run(args, text=True, capture_output=True, timeout=timeout)


def checked(args, timeout=30):
    result = run(args, timeout)
    if result.returncode:
        raise RuntimeError(f'command failed: {args[:2]}: {result.stderr[:500]}')
    return result.stdout


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Journey:
    def __init__(self, binary):
        self.binary = binary.resolve(strict=True)
        self.binary_hash = digest(self.binary)
        self.prefix = 'helm-admission-e2e-' + secrets.token_hex(5)
        self.out = ROOT / '.artifacts/tc335/runtime-journey' / self.prefix
        self.out.mkdir(parents=True, exist_ok=False)
        self.names = []
        self.receipts = []
        self.failures = []
        self.fixtures = []

    def setup(self, root, uid):
        # Only this test's new, explicit directory is exposed to setup root.
        script = ('chown -R "$1:$1" /fixture/data; chmod 700 /fixture/data; '
                  'chmod 600 /fixture/data/roadmap.db; '
                  'if [ -d /fixture/authority ]; then '
                  'chown 0:70541 /fixture/authority /fixture/authority/*; '
                  'chmod 750 /fixture/authority; chmod 640 /fixture/authority/*; fi')
        checked(['docker', 'run', '--rm', '--network', 'none', '--read-only',
                 '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--cap-add', 'FOWNER',
                 '--cap-add', 'DAC_OVERRIDE', '--security-opt', 'no-new-privileges',
                 '--user', '0:0', '--mount', f'type=bind,src={root},dst=/fixture',
                 '--entrypoint', '/bin/sh', SETUP, '-eu', '-c', script, 'setup', str(uid)])

    def fixture(self, label, uid=70541, marker=True, patch=None):
        root = self.out / label
        data = root / 'data'
        data.mkdir(parents=True)
        database = data / 'roadmap.db'
        with sqlite3.connect(database) as conn:
            conn.executescript('PRAGMA foreign_keys=ON; '
                               'CREATE TABLE preservation_parent(id TEXT PRIMARY KEY); '
                               'CREATE TABLE preservation_child(id TEXT PRIMARY KEY, parent TEXT '
                               'REFERENCES preservation_parent(id)); '
                               "INSERT INTO preservation_parent VALUES('synthetic-parent'); "
                               "INSERT INTO preservation_child VALUES('synthetic-child','synthetic-parent');")
        self.fixtures.append(root)
        if marker:
            authority = root / 'authority'
            authority.mkdir()
            lock = authority / 'runtime.lock'
            lock.touch()
            ds, ls = data.stat(), lock.stat()
            value = dict(format=1, minimum_authorization=41, phase='active', lane='beta',
                         generation=1, operation_nonce='ab' * 32,
                         data_device=ds.st_dev, data_inode=ds.st_ino,
                         lock_device=ls.st_dev, lock_inode=ls.st_ino,
                         binary_sha256=self.binary_hash, role='candidate', image=BASE,
                         release_sha='189e5aecc1e0f9a6c9d039dd1be738ef935409a5')
            value.update(patch or {})
            (authority / 'marker.json').write_text(json.dumps(value))
        before = digest(database)
        self.setup(root, uid)
        return root, before

    def start(self, label, root, uid=70541, public=False, authority_ro=True, args=()):
        name = self.prefix + '-' + label
        self.names.append(name)
        target = '/data' if public else PRIVATE
        command = ['docker', 'run', '-d', '--name', name, '--network', 'none',
                   '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                   '--user', f'{uid}:{uid}', '--workdir', '/', '--tmpfs', '/tmp:rw,noexec,nosuid,nodev',
                   '--mount', f'type=bind,src={self.binary},dst=/usr/local/bin/helm,readonly',
                   '--mount', f'type=bind,src={root / "data"},dst={target}',
                   '-e', f'HELM_DB={target}/roadmap.db', '-e', 'HELM_AUTH_MODE=disabled',
                   '-e', 'HELM_ADDR=127.0.0.1:8080', '-e', 'HELM_PUBLIC_ORIGIN=http://localhost:8080',
                   '-e', 'HELM_SECURE_COOKIES=false', '-e', 'HELM_DEMO_SEED=false']
        if (root / 'authority').exists():
            suffix = ',readonly' if authority_ro else ''
            command += ['--mount', f'type=bind,src={root / "authority"},dst={AUTHORITY}{suffix}']
        checked(command + ['--entrypoint', '/usr/local/bin/helm', BASE, *args])
        return name

    def state(self, name):
        return json.loads(checked(['docker', 'inspect', '--format', '{{json .State}}', name]))

    def health(self, name):
        return run(['docker', 'exec', name, '/usr/local/bin/helm', 'healthcheck']).returncode == 0

    def wait_health(self, name):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if not self.state(name)['Running']:
                return False
            if self.health(name):
                return True
            time.sleep(0.15)
        return False

    def reject(self, label, uid=70541, marker=True, patch=None, authority_ro=True, args=()):
        root, before = self.fixture(label, uid, marker, patch)
        name = self.start(label, root, uid, authority_ro=authority_ro, args=args)
        deadline = time.monotonic() + 4
        while self.state(name)['Running'] and time.monotonic() < deadline:
            time.sleep(0.05)
        state = self.state(name)
        logs = checked(['docker', 'logs', '--tail', '10', name])[:2000]
        # Docker logs may use stderr; collect both bounded streams.
        result = run(['docker', 'logs', '--tail', '10', name])
        logs = (result.stdout + result.stderr)[:2000]
        checked(['docker', 'stop', '-t', '2', name])
        self.restore_owner(root)
        after = digest(root / 'data/roadmap.db')
        passed = not state['Running'] and state['ExitCode'] != 0 and 'admission' in logs and before == after
        self.receipts.append(dict(case=label, passed=passed, exit=state['ExitCode'],
                                  was_running=state['Running'], database_unchanged=before == after,
                                  bounded_logs=logs))
        if not passed:
            self.failures.append(label)

    def restore_owner(self, root):
        checked(['docker', 'run', '--rm', '--network', 'none', '--read-only',
                 '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--cap-add', 'FOWNER',
                 '--cap-add', 'DAC_OVERRIDE', '--security-opt', 'no-new-privileges',
                 '--user', '0:0', '--mount', f'type=bind,src={root},dst=/fixture',
                 '--entrypoint', '/bin/sh', SETUP, '-eu', '-c',
                 'chown -R "$1:$2" /fixture', 'restore', str(os.getuid()), str(os.getgid())])

    def preserved(self, root):
        self.restore_owner(root)
        with sqlite3.connect(root / 'data/roadmap.db') as conn:
            return (conn.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
                    and conn.execute('PRAGMA foreign_key_check').fetchall() == []
                    and conn.execute('SELECT * FROM preservation_child').fetchall()
                    == [('synthetic-child', 'synthetic-parent')])

    def serving(self, public=False):
        label = 'public' if public else 'admitted'
        root, _ = self.fixture(label, 65532 if public else 70541, marker=not public)
        name = self.start(label, root, 65532 if public else 70541, public=public)
        healthy = self.wait_health(name)
        duplicate_denied = True
        reacquired = True
        if healthy and not public:
            duplicate = self.start('duplicate', root)
            deadline = time.monotonic() + 4
            while self.state(duplicate)['Running'] and time.monotonic() < deadline:
                time.sleep(0.05)
            state = self.state(duplicate)
            result = run(['docker', 'logs', '--tail', '10', duplicate])
            duplicate_denied = (not state['Running'] and state['ExitCode'] != 0
                                and 'admission' in result.stdout + result.stderr
                                and self.health(name))
            checked(['docker', 'stop', '-t', '2', duplicate])
            checked(['docker', 'stop', '-t', '10', name])
            next_name = self.start('reacquire', root)
            reacquired = self.wait_health(next_name)
            checked(['docker', 'stop', '-t', '10', next_name])
        else:
            checked(['docker', 'stop', '-t', '10', name])
        preserved = self.preserved(root)
        passed = healthy and duplicate_denied and reacquired and preserved
        self.receipts.append(dict(case=label, passed=passed, ready=healthy,
                                  duplicate_rejected=duplicate_denied, lease_reacquired=reacquired,
                                  populated_data_preserved=preserved))
        if not passed:
            self.failures.append(label)

    def execute(self):
        try:
            self.reject('old-profile-no-authority', uid=65532, marker=False)
            self.reject('missing-authority', marker=False)
            self.reject('closed-authority', patch={'phase': 'prepared'})
            self.reject('foreign-executable', patch={'binary_sha256': '00' * 32})
            self.reject('writable-authority', authority_ro=False)
            self.reject('old-uid', uid=65532)
            for command in ['schema-preflight', 'migration-apply', 'audit-access']:
                self.reject('cli-' + command, uid=65532, marker=False,
                            args=(command, PRIVATE + '/roadmap.db'))
            self.serving()
            self.serving(public=True)
        except Exception as error:
            self.failures.append(type(error).__name__)
            self.receipts.append(dict(harness_error=str(error)[:1000]))
        finally:
            for name in self.names:
                run(['docker', 'rm', '-f', name])
            for root in self.fixtures:
                self.restore_owner(root)
            remaining = checked(['docker', 'ps', '-a', '--filter', f'name={self.prefix}',
                                 '--format', '{{.Names}}']).splitlines()
            if remaining:
                self.failures.append('cleanup')
            receipt = dict(scope='DEV hybrid actual-binary startup, NOT immutable artifact acceptance',
                           base_image=BASE, setup_image=SETUP, binary_sha256=self.binary_hash,
                           cases=self.receipts, failures=self.failures, remaining_containers=remaining)
            path = self.out / 'receipt.json'
            content = json.dumps(receipt, indent=2) + '\n'
            if len(content.encode()) > 20000:
                raise RuntimeError('receipt exceeds maintained artifact limit')
            path.write_text(content)
            print(json.dumps(dict(receipt=str(path), failures=self.failures)))
        return 1 if self.failures else 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    raise SystemExit(Journey(parser.parse_args().binary).execute())
