#!/usr/bin/env python3
"""Synthetic root-only CLOSED bootstrap journeys; no live paths or images changed."""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import sqlite3
import struct
import subprocess
import sys
import time
from unittest import mock

from bootstrap_fixture import fixture, save, snapshot

ROOT = Path(__file__).resolve().parents[2]
SETUP = 'golang@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51'
sys.path.insert(0, str(ROOT / 'deploy'))


class Journeys:
    def __init__(self, output):
        from admission.bootstrap import BootstrapError, prepare, preview
        self.error, self.prepare, self.preview = BootstrapError, prepare, preview
        self.output = output
        self.records = []

    def fixture(self, label):
        return fixture(self.output, label)

    def plan(self, base):
        value = self.preview('beta', 'synthetic-snapshot',
                             'ghcr.io/kanterlabs/helm@sha256:' + 'ab' * 32,
                             'cd' * 20, 'ef' * 32, '12' * 32, base=base)
        path = base / 'plan.json'
        save(path, value)
        return path

    def refused(self, label, fn, unchanged=None):
        before = snapshot(unchanged) if unchanged else None
        try:
            fn()
        except self.error as error:
            assert len(str(error)) < 300
            if unchanged:
                assert snapshot(unchanged) == before, label + ': preservation'
            self.records.append(dict(case=label, result='refused', reason=str(error)))
        else:
            raise AssertionError(label + ': unexpectedly accepted')

    def run(self):
        base, lane, _ = self.fixture('preserved')
        before = {name: snapshot(lane / name) for name in ('data', 'config')}
        path = self.plan(base)
        assert not (lane / 'security').exists(), 'preview must not create authority'
        receipt = self.prepare(path, base=base)
        assert receipt['phase'] == 'prepared' and receipt['closed'] is True
        security = lane / 'security'
        marker = json.loads((security / 'marker.json').read_text())
        assert len(marker) == 14 and marker['phase'] == 'prepared'
        assert marker['minimum_authorization'] == 41 and marker['generation'] == 1
        lock = (security / 'runtime.lock').stat()
        assert (lock.st_dev, lock.st_ino) == (marker['lock_device'], marker['lock_inode'])
        for name in ('data', 'config'):
            assert snapshot(lane / name) == before[name]
            for item in [lane / name, *(lane / name).rglob('*')]:
                s = item.stat()
                assert s.st_uid == s.st_gid == 70541
                assert s.st_mode & 0o077 == 0
        assert (lane / 'data/support').stat().st_mode & 0o777 == 0o700
        assert (lane / 'config/settings').stat().st_mode & 0o777 == 0o400
        authority_before = snapshot(security)
        assert self.prepare(path, base=base) == receipt
        assert snapshot(security) == authority_before
        self.records.append(dict(case='populated-preserved-repeat', result='closed', receipt=receipt))

        for name in ('runtime.lock', 'marker.json', 'journal.json', 'inventory-0000.json'):
            base, lane, _ = self.fixture('lost-' + name)
            path = self.plan(base)
            self.prepare(path, base=base)
            (lane / 'security' / name).unlink()
            self.refused('lost-' + name, lambda: self.prepare(path, base=base), lane / 'data')
            assert not (lane / 'security' / name).exists()

        base, lane, _ = self.fixture('empty-security')
        path = self.plan(base)
        (lane / 'security').mkdir(mode=0o700)
        self.refused('existing-empty-security', lambda: self.prepare(path, base=base), lane / 'data')

        for name in ('stale-content', 'stale-inode', 'foreign-owner', 'backup-mismatch', 'stale-backup'):
            base, lane, backup = self.fixture(name)
            path = self.plan(base)
            target = lane / 'data/sessions/synthetic-session'
            if name == 'stale-content':
                target.write_bytes(b'changed after preview')
            elif name == 'stale-inode':
                original = target.read_bytes()
                target.unlink()
                target.write_bytes(original)
                os.chown(target, 65532, 65532)
            elif name == 'foreign-owner':
                os.chown(target, 12345, 12345)
            elif name == 'backup-mismatch':
                (backup / 'data/sessions/synthetic-session').write_bytes(b'bad snapshot')
            else:
                os.utime(backup, (time.time() - 90000,) * 2)
            self.refused(name, lambda: self.prepare(path, base=base), lane / 'data')
            assert not (lane / 'security').exists()

        for name in ('sqlite-symlink', 'hardlink', 'acl', 'backup-symlink'):
            base, lane, backup = self.fixture(name)
            target = lane / 'data/sessions/synthetic-session'
            if name == 'sqlite-symlink':
                (lane / 'data/roadmap.db').unlink()
                (lane / 'data/roadmap.db').symlink_to('/SYNTHETIC/database')
                os.chown(lane / 'data/roadmap.db', 65532, 65532, follow_symlinks=False)
            elif name == 'hardlink':
                os.link(target, lane / 'data/alias')
            elif name == 'backup-symlink':
                (backup / 'data/link').symlink_to('roadmap.db')
            else:
                acl = struct.pack('<I', 2) + b''.join(struct.pack('<HHI', *v) for v in (
                    (1, 6, 0xffffffff), (2, 4, 1000), (4, 0, 0xffffffff),
                    (16, 4, 0xffffffff), (32, 0, 0xffffffff)))
                os.setxattr(target, 'system.posix_acl_access', acl)
            self.refused(name, lambda: self.plan(base))

        base, lane, _ = self.fixture('partial-chown')
        path = self.plan(base)
        original = os.fchown
        seen = [0]

        def interrupted(fd, uid, gid):
            if uid == 70541:
                seen[0] += 1
                if seen[0] == 3:
                    raise OSError('synthetic interruption')
            return original(fd, uid, gid)

        with mock.patch('os.fchown', interrupted):
            self.refused('partial-chown', lambda: self.prepare(path, base=base))
        assert json.loads((lane / 'security/marker.json').read_text())['phase'] == 'prepared'
        assert self.prepare(path, base=base)['closed']
        self.records.append(dict(case='same-plan-partial-resume', result='closed'))

        base, lane, _ = self.fixture('live-write')
        path = self.plan(base)
        wrote = [False]

        def writes(fd, uid, gid):
            result = original(fd, uid, gid)
            if uid == 70541 and not wrote[0]:
                wrote[0] = True
                (lane / 'data/sessions/synthetic-session').write_bytes(b'concurrent synthetic writer')
            return result

        with mock.patch('os.fchown', writes):
            self.refused('writer-during-metadata', lambda: self.prepare(path, base=base))
        self.refused('changed-content-resume', lambda: self.prepare(path, base=base))

        for name in ('active-marker', 'boolean-marker', 'different-plan', 'tampered-page',
                     'permissive-plan', 'prepared-directory'):
            base, lane, _ = self.fixture(name)
            path = self.plan(base)
            if name != 'permissive-plan':
                self.prepare(path, base=base)
            if name in ('active-marker', 'boolean-marker'):
                marker_path = lane / 'security/marker.json'
                value = json.loads(marker_path.read_text())
                value['phase' if name == 'active-marker' else 'format'] = 'active' if name == 'active-marker' else True
                marker_path.write_text(json.dumps(value))
            elif name == 'different-plan':
                value = json.loads(path.read_text())
                value['operation_nonce'] = '34' * 32
                save(path, value)
            elif name == 'tampered-page':
                with (lane / 'security/inventory-0000.json').open('a') as page:
                    page.write(' ')
            elif name == 'prepared-directory':
                (lane / 'security').chmod(0o700)
            else:
                path.chmod(0o644)
            self.refused(name, lambda: self.prepare(path, base=base), lane / 'data')

        base, lane, _ = self.fixture('competing-first')
        path = self.plan(base)
        script = ('import json,sys; from pathlib import Path; from admission.bootstrap import prepare; '
                  'print(json.dumps(prepare(Path(sys.argv[1]),base=Path(sys.argv[2]))))')
        env = {**os.environ, 'PYTHONPATH': str(ROOT / 'deploy')}
        commands = [subprocess.Popen([sys.executable, '-c', script, str(path), str(base)],
                                    env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(2)]
        results = [p.communicate(timeout=30) for p in commands]
        assert any(p.returncode == 0 for p in commands)
        assert self.prepare(path, base=base)['closed']
        self.records.append(dict(case='competing-first', result='closed',
                                 successful=sum(p.returncode == 0 for p in commands)))

        base, lane, _ = self.fixture('partial-finalization')
        path = self.plan(base)
        chmod = os.fchmod
        interrupted = [False]

        def finish_interrupted(fd, mode):
            if mode == 0o750 and not interrupted[0]:
                interrupted[0] = True
                raise OSError('synthetic final directory interruption')
            return chmod(fd, mode)

        with mock.patch('os.fchmod', finish_interrupted):
            self.refused('partial-security-finalization', lambda: self.prepare(path, base=base))
        assert (lane / 'security').stat().st_gid == 70541
        assert self.prepare(path, base=base)['closed']
        self.records.append(dict(case='same-plan-finalization-resume', result='closed'))

        for value in self.records:
            print(json.dumps(value, sort_keys=True))

    def additional(self):
        for name in ('invalid-sqlite', 'foreign-key'):
            base, lane, backup = self.fixture(name)
            db_path = lane / 'data/roadmap.db'
            if name == 'invalid-sqlite':
                db_path.write_bytes(b'not a database')
            else:
                with sqlite3.connect(db_path) as db:
                    db.execute('INSERT INTO c VALUES(2,999)')
            shutil.copyfile(db_path, backup / 'data/roadmap.db')
            self.refused(name, lambda: self.plan(base), lane / 'data')

        base, lane, backup = self.fixture('wal-preservation')
        db = sqlite3.connect(lane / 'data/roadmap.db')
        db.execute('PRAGMA journal_mode=WAL')
        db.execute('INSERT INTO p VALUES(7)')
        db.commit()
        for item in (lane / 'data').iterdir():
            if item.is_file():
                os.chown(item, 65532, 65532)
        shutil.rmtree(backup / 'data')
        shutil.copytree(lane / 'data', backup / 'data')
        for item in [backup / 'data', *(backup / 'data').rglob('*')]:
            os.chown(item, 0, 0)
            item.chmod(0o700 if item.is_dir() else 0o600)
        before = snapshot(lane / 'data')
        assert self.prepare(self.plan(base), base=base)['closed']
        assert snapshot(lane / 'data') == before
        db.close()
        self.records.append(dict(case='wal-preserved', result='closed'))

        base, lane, _ = self.fixture('bounded-inventory')
        for number in range(32769):
            item = lane / 'data' / str(number)
            item.touch()
            os.chown(item, 65532, 65532)
        self.refused('bounded-inventory', lambda: self.plan(base))

        for name in ('unknown-field', 'forged-identity', 'forged-count', 'oversized-plan',
                     'unsafe-plan-parent', 'lock-held'):
            base, lane, _ = self.fixture(name)
            path = self.plan(base)
            if name == 'unknown-field':
                value = json.loads(path.read_text())
                value['backup_verified'] = True
                save(path, value)
            elif name in ('forged-identity', 'forged-count'):
                value = json.loads(path.read_text())
                value['data_inode' if name == 'forged-identity' else 'entries'] += 1
                save(path, value)
            elif name == 'oversized-plan':
                path.write_bytes(b' ' * 20001)
            elif name == 'unsafe-plan-parent':
                base.chmod(0o750)
            else:
                import fcntl
                self.prepare(path, base=base)
                with (lane / 'security/runtime.lock').open('rb') as lock:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    self.refused(name, lambda: self.prepare(path, base=base), lane / 'data')
                continue
            self.refused(name, lambda: self.prepare(path, base=base), lane / 'data')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.inside:
        if os.geteuid() != 0:
            raise SystemExit('root fixture container required')
        args.output.mkdir(parents=True, exist_ok=True)
        os.chown(args.output, 0, 0)
        args.output.chmod(0o700)
        journey = Journeys(args.output)
        journey.additional()
        journey.run()
        return
    output = ROOT / '.artifacts/tc335/bootstrap' / ('run-' + secrets.token_hex(6))
    output.mkdir(parents=True)
    result = subprocess.run([
        'docker', 'run', '--rm', '--network', 'none', '--read-only', '--cap-drop', 'ALL',
        '--ulimit', 'nofile=131072:131072',
        '--cap-add', 'CHOWN', '--cap-add', 'FOWNER', '--cap-add', 'DAC_OVERRIDE',
        '--security-opt', 'no-new-privileges', '--user', '0:0',
        '--tmpfs', '/tmp:rw,nosuid,nodev,noexec',
        '--mount', f'type=bind,src={ROOT},dst=/source,readonly',
        '--mount', f'type=bind,src={output},dst=/fixtures',
        '--entrypoint', 'python3', SETUP,
        '/source/test/deployment-policy/bootstrap_journeys.py', '--inside', '--output', '/fixtures'],
        text=True, capture_output=True, timeout=180)
    # Retain sanitized receipts only; synthetic root fixtures are cleaned in a separate container.
    cleanup = subprocess.run([
        'docker', 'run', '--rm', '--network', 'none', '--read-only', '--cap-drop', 'ALL',
        '--cap-add', 'CHOWN', '--cap-add', 'FOWNER', '--cap-add', 'DAC_OVERRIDE',
        '--mount', f'type=bind,src={output},dst=/fixtures', '--entrypoint', '/bin/sh', SETUP,
        '-eu', '-c', 'find /fixtures -mindepth 1 -delete; chown 1000:1000 /fixtures; chmod 700 /fixtures'],
        text=True, capture_output=True, timeout=30)
    if cleanup.returncode:
        raise SystemExit('synthetic fixture cleanup failed')
    (output / 'receipt.jsonl').write_text(result.stdout)
    (output / 'result.json').write_text(json.dumps(dict(exit_code=result.returncode,
                                                      error=result.stderr[-2500:]), indent=2) + '\n')
    print(str(output.relative_to(ROOT)))
    if result.returncode:
        print(result.stderr[-2500:], file=sys.stderr)
        raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
