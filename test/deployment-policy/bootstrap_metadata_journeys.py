#!/usr/bin/env python3
"""Native DEV legacy-credential handover/drain journeys; no live preparation."""
import argparse
import ctypes
import errno
import json
import os
from pathlib import Path
import select
import signal
import stat
import sys
from unittest.mock import patch

from bootstrap_fixture import fixture, save, snapshot
from bootstrap_native import launch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'deploy'))


class LegacyFD:
    """Exactly one owned child retains a file, mapping, or directory handle."""
    def __init__(self, path, directory=False, mapping=False):
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY if directory else os.O_RDWR)
        commands, writer = os.pipe()
        reader, responses = os.pipe()
        self.pid = os.fork()
        if self.pid == 0:
            try:
                os.close(writer)
                os.close(reader)
                libc = ctypes.CDLL(None, use_errno=True)
                assert libc.prctl(38, 1, 0, 0, 0) == 0  # PR_SET_NO_NEW_PRIVS
                address = None
                if mapping:
                    libc.mmap.restype = ctypes.c_void_p
                    address = libc.mmap(None, 4096, 3, 1, fd, 0)
                    assert address not in (None, ctypes.c_void_p(-1).value)
                    os.close(fd)
                os.setgroups([])
                os.setgid(65532)
                os.setuid(65532)
                class Header(ctypes.Structure):
                    _fields_ = [('version', ctypes.c_uint32), ('pid', ctypes.c_int)]
                class Caps(ctypes.Structure):
                    _fields_ = [('effective', ctypes.c_uint32), ('permitted', ctypes.c_uint32),
                                ('inheritable', ctypes.c_uint32)]
                header, caps = Header(0x20080522, 0), (Caps * 2)()
                assert libc.capset(ctypes.byref(header), ctypes.byref(caps)) == 0
                fields = {}
                for line in Path('/proc/self/status').read_text().splitlines():
                    key, _, value = line.partition(':')
                    if key in ('CapEff', 'CapPrm', 'CapAmb', 'CapInh', 'NoNewPrivs'):
                        fields[key] = value.strip()
                assert os.getuid() == os.getgid() == 65532 and os.getgroups() == []
                assert all(int(fields[key], 16) == 0 for key in ('CapEff', 'CapPrm', 'CapAmb', 'CapInh'))
                assert fields['NoNewPrivs'] == '1'
                os.write(responses, (json.dumps(dict(uid=65532, gid=65532, status=fields)) + '\n').encode())
                while True:
                    command = os.read(commands, 1)
                    if command in (b'', b'q'):
                        break
                    try:
                        if command == b'm':
                            os.fchmod(fd, 0o711)
                        elif command == b's':
                            os.fchmod(fd, 0o6771)
                        elif command == b'w':
                            if mapping:
                                ctypes.memset(address, ord('X'), 1)
                            else:
                                os.pwrite(fd, b'X', 0)
                                os.fsync(fd)
                        elif command == b'n':
                            new = os.open('sessions/synthetic-session', os.O_RDONLY, dir_fd=fd)
                            os.close(new)
                        else:
                            raise AssertionError('unknown synthetic command')
                        result = {'ok': True}
                    except OSError as error:
                        result = {'ok': False, 'errno': error.errno}
                    os.write(responses, (json.dumps(result) + '\n').encode())
                os._exit(0)
            except BaseException:
                os._exit(3)
        os.close(fd)
        os.close(commands)
        os.close(responses)
        self.writer, self.reader = writer, reader
        self.identity = self.receive()

    def receive(self):
        body = bytearray()
        while not body.endswith(b'\n'):
            if not select.select([self.reader], [], [], 10)[0]:
                raise RuntimeError('owned legacy helper timed out')
            value = os.read(self.reader, 1)
            if not value:
                raise RuntimeError('owned legacy helper exited')
            body += value
            assert len(body) <= 1000
        return json.loads(body)

    def change(self, command=b'm'):
        os.write(self.writer, command)
        return self.receive()

    def close(self):
        try:
            os.write(self.writer, b'q')
        except BrokenPipeError:
            pass
        finally:
            os.close(self.writer)
            os.close(self.reader)
            try:
                os.kill(self.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            os.waitpid(self.pid, 0)


def inside(output):
    from admission import bootstrap
    from admission.filesystem import BootstrapError
    cases = []
    def plan(base):
        path = base / 'plan.json'
        save(path, bootstrap.preview('beta', 'synthetic-snapshot',
                                     'ghcr.io/kanterlabs/helm@sha256:' + 'ab' * 32,
                                     'cd' * 20, 'ef' * 32, '12' * 32, base=base))
        return path
    def refuse(call):
        try:
            call()
        except BootstrapError as error:
            return str(error)
        raise AssertionError('expected CLOSED refusal')

    for name in ('missing-binding', 'foreign-binding', 'host-directory-mode'):
        base, lane, _ = fixture(output, name)
        path = plan(base)
        host = base / 'operator/host.json'
        if name == 'missing-binding':
            host.unlink()
        elif name == 'host-directory-mode':
            host.parent.chmod(0o500)
        else:
            value = json.loads(host.read_text())
            value['boot_id'] = '00000000-0000-4000-8000-000000000000'
            save(host, value)
        before = snapshot(lane / 'data')
        refuse(lambda: bootstrap.prepare(path, base=base))
        assert not (lane / 'security').exists() and snapshot(lane / 'data') == before
        assert (lane / 'data').stat().st_uid == 65532
        cases.append(dict(case=name, passed=True))

    for name in ('legacy-file', 'legacy-mapping', 'legacy-directory', 'legacy-config'):
        base, lane, _ = fixture(output, name)
        path = plan(base)
        before = {key: snapshot(lane / key) for key in ('data', 'config')}
        target = lane / ('config/settings' if name == 'legacy-config' else 'data/sessions/synthetic-session')
        actor = LegacyFD(lane / 'data' if name == 'legacy-directory' else target,
                         directory=name == 'legacy-directory', mapping=name == 'legacy-mapping')
        if name == 'legacy-directory':
            assert actor.change(b'n') == {'ok': True}
        original = os.fchown
        regular_handover = [0]
        def count_regular(fd, uid, gid):
            if uid == 70541 and stat.S_ISREG(os.fstat(fd).st_mode):
                regular_handover[0] += 1
            return original(fd, uid, gid)
        try:
            with patch('os.fchown', count_regular):
                reason = refuse(lambda: bootstrap.prepare(path, base=base))
            assert regular_handover[0] == 0, 'held access reached regular-file handover'
            assert target.stat().st_uid == 65532
            for key in ('data', 'config'):
                assert snapshot(lane / key) == before[key]
                assert (lane / key).stat().st_uid == 70541
            if name == 'legacy-directory':
                assert actor.change(b'n') == {'ok': False, 'errno': errno.EACCES}
                assert actor.change() == {'ok': False, 'errno': errno.EPERM}
            cases.append(dict(case=name, passed=True, regular_handover=0, reason=reason, actor=actor.identity))
        finally:
            actor.close()
        assert bootstrap.prepare(path, base=base)['closed']
        for key in ('data', 'config'):
            assert snapshot(lane / key) == before[key]
        journal = json.loads((lane / 'security/journal.json').read_text())
        assert journal['format'] == 3 and journal['metadata_exclusion']['mechanism']
        cases.append(dict(case=name + '-closed-actor-resume', passed=True))

    for label, command, mode in (('ordinary', b'm', 0o711), ('special', b's', 0o6771)):
        base, lane, _ = fixture(output, 'directory-' + label + '-race')
        path = plan(base)
        root = lane / 'data'
        before = snapshot(root)
        actor = LegacyFD(root, directory=True)
        original = os.fchown
        fired = [False]
        def directory_race(fd, uid, gid):
            if uid == 70541 and os.fstat(fd).st_ino == root.stat().st_ino and not fired[0]:
                fired[0] = True
                assert actor.change(command) == {'ok': True}
            return original(fd, uid, gid)
        try:
            with patch('os.fchown', directory_race):
                refuse(lambda: bootstrap.prepare(path, base=base))
            assert fired[0] and root.stat().st_mode & 0o7777 == mode
            assert root.stat().st_uid == 70541 and (root / 'roadmap.db').stat().st_uid == 65532
            assert snapshot(root) == before
            assert actor.change() == {'ok': False, 'errno': errno.EPERM}
            cases.append(dict(case='directory-' + label + '-race-preserved', passed=True, mode=oct(mode), actor=actor.identity))
        finally:
            actor.close()
        refuse(lambda: bootstrap.prepare(path, base=base))
        assert root.stat().st_mode & 0o7777 == mode

    probe = output / 'SYNTHETIC-kernel-probe'
    probe.write_bytes(b'synthetic probe')
    os.chown(probe, 65532, 65532)
    probe.chmod(0o600)
    actor = LegacyFD(probe)
    try:
        os.chown(probe, 70541, 70541)
        assert actor.change() == {'ok': False, 'errno': errno.EPERM}
        assert actor.change(b'w') == {'ok': True}
        cases.append(dict(case='file-handover-still-allows-held-write', passed=True))
    finally:
        actor.close()
    print(json.dumps(dict(scope='native DEV two-phase metadata exclusion', cases=cases), sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.inside:
        inside(args.output)
        return 0
    return launch(Path(__file__), 'metadata-native', timeout=600)


if __name__ == '__main__':
    raise SystemExit(main())
