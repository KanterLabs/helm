#!/usr/bin/env python3
"""Repeatable, sanitized TC335 snapshot journeys; no application data is used."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from unittest.mock import patch

from drain_fixture import fixture, load

ROOT = Path(__file__).resolve().parents[2]
ARTIFACT = ROOT / '.artifacts/tc335/drain'


def run():
    mod = load()
    results = []
    def observe(data, proc):
        with patch.object(mod.os, 'geteuid', return_value=0):
            value = mod.observe(data, proc_root=proc)
        assert value['format'] == 1 and value['scope'] == 'protected-tree-access-snapshot'
        assert value['status'] in ('unknown', 'access_present', 'no_access_observed')
        serialized = json.dumps(value)
        assert str(data) not in serialized and 'synthetic database' not in serialized
        return value
    def journey(name, change, expected):
        with tempfile.TemporaryDirectory(prefix='tc335-drain-') as temp:
            data, proc = fixture(Path(temp))
            change(data, proc)
            value = observe(data, proc)
            assert value['status'] == 'unknown', value
            assert expected in value['reasons'], value
            results.append({'journey': name, 'result': value})
    journey('restricted visibility', lambda d, p: None, 'restricted-proc-visibility')
    with tempfile.TemporaryDirectory(prefix='tc335-nsfs-') as temp:
        data, proc = fixture(Path(temp))
        for name in ('self', '200'):
            with (proc / name / 'mountinfo').open('a') as stream:
                stream.write('999998 1 0:5 mnt:[123] /synthetic-namespace rw - nsfs nsfs rw\n')
        value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'] and value['scanned_process_count'] == 2, value
        results.append({'journey': 'unrelated nsfs mount root', 'result': value})
    journey('kernel metadata conflict', lambda d, p: (
        (p / '200/status').write_text('Pid:\t200\nKthread:\t1\nUid:\t0\t0\t0\t0\n'),
        (p / '200/fd/4').symlink_to(d / 'helm.db')), 'kernel-thread-conflict')
    journey('missing maps', lambda d, p: (p / '200/maps').unlink(), 'process-unreadable')
    journey('malformed maps', lambda d, p: (p / '200/maps').write_text('bad\n'), 'maps-malformed')
    journey('truncated maps', lambda d, p: (p / '200/maps').write_bytes(b'x' * (mod.MAX_MAP_BYTES + 1)), 'maps-limit')
    journey('namespace mismatch', lambda d, p: ((p / 'self/ns/pid').unlink(), (p / 'self/ns/pid').symlink_to('pid:[456]')), 'namespace-mismatch')
    journey('missing namespace', lambda d, p: (p / '1/ns/pid').unlink(), 'provenance-unreadable')
    journey('missing mount visibility', lambda d, p: (p / '200/mountinfo').unlink(), 'process-unreadable')
    journey('database symlink', lambda d, p: (d / 'roadmap.db').symlink_to(d / 'helm.db'), 'inventory-unsupported')
    journey('hardlink inventory', lambda d, p: os.link(d / 'helm.db', d / 'alias'), 'inventory-unsupported')
    journey('nested mount', lambda d, p: (p / 'self/mountinfo').write_text(f'1 0 0:1 / {d}/nested rw - tmpfs tmpfs rw\n'), 'nested-mount')
    journey('pid limit', lambda d, p: [ (p / str(n + 1000)).mkdir() for n in range(mod.MAX_PIDS + 1)], 'pid-limit')
    journey('fd limit', lambda d, p: [(p / '200/fd' / str(n)).symlink_to(d / 'helm.db') for n in range(mod.MAX_FDS + 1)], 'fd-limit')
    for kind in ('bound-namespace', 'native-legacy', 'unrelated-namespace'):
        with tempfile.TemporaryDirectory(prefix='tc335-actor-scope-') as temp:
            data, proc = fixture(Path(temp))
            (proc / '200/status').write_text('Pid:\t200\nNSpid:\t200\nUid:\t65532\t65532\t65532\t65532\n')
            if kind != 'native-legacy':
                (proc / '200/ns/pid').unlink()
                (proc / '200/ns/pid').symlink_to('pid:[456]')
            if kind == 'bound-namespace':
                device = data.stat().st_dev
                with (proc / '200/mountinfo').open('a') as stream:
                    stream.write(f'999999 1 {os.major(device)}:{os.minor(device)} {data} /different-container-path rw - ext4 synthetic rw\n')
            value = observe(data, proc)
            matches = [p for p in value['matching_processes'] if p['pid'] == 200]
            if kind == 'unrelated-namespace':
                assert not matches, value
            else:
                assert matches and matches[0]['fd_count'] == matches[0]['mapping_count'] == 0, value
                assert not matches[0]['cwd_match'], value
                assert matches[0].get('namespace_mount_count') or matches[0].get('native_legacy_identity'), value
            results.append({'journey': kind + ' without target handles', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-live-shape-') as temp:
        data, proc = fixture(Path(temp))
        outside = Path(temp) / 'synthetic-outside'
        outside.write_bytes(b'synthetic external target unchanged')
        (data / 'opaque-link').symlink_to(outside)
        (data / 'dangling-link').symlink_to('/synthetic-unresolved-target')
        before = outside.stat()
        for index in range(10300):
            (data / f'synthetic-{index}').touch()
        value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'], value
        assert value['scanned_process_count'] == 2, value
        assert outside.stat() == before and outside.read_bytes() == b'synthetic external target unchanged'
        results.append({'journey': 'large opaque-link inventory', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-drain-') as temp:
        data, proc = fixture(Path(temp))
        identity = (data / 'helm.db').stat()
        dev = f'{os.major(identity.st_dev):x}:{os.minor(identity.st_dev):x}'
        (proc / '200/fd/4').symlink_to(data / 'helm.db')
        (proc / '200/maps').write_text(f'1-2 rw-s 0 {dev} {identity.st_ino} /redacted (deleted)\n')
        (proc / '200/cwd').unlink()
        (proc / '200/cwd').symlink_to(data)
        value = observe(data, proc)
        assert value['matching_processes'] == [{'pid': 200, 'fd_count': 1, 'mapping_count': 1, 'cwd_match': True}], value
        results.append({'journey': 'descriptor mapping and cwd identity', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-kthread-') as temp:
        data, proc = fixture(Path(temp))
        (proc / '200/status').write_text('Pid:\t200\nKthread:\t1\nUid:\t0\t0\t0\t0\n')
        (proc / '200/cwd').unlink()
        value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'], value
        assert value['scanned_process_count'] == 2, value
        results.append({'journey': 'verified kernel thread', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-match-limit-') as temp:
        import shutil
        data, proc = fixture(Path(temp))
        (proc / '200/fd/4').symlink_to(data / 'helm.db')
        for pid in range(201, 265):
            p = proc / str(pid)
            shutil.copytree(proc / '200', p, symlinks=True)
            (p / 'status').write_text(f'Pid:\t{pid}\nNSpid:\t{pid}\nUid:\t1000\t1000\t1000\t1000\n')
            (p / 'stat').write_text(f'{pid} (fixture) S ' + '0 ' * 18 + '123 0\n')
        value = observe(data, proc)
        assert len(value['matching_processes']) == 64, value
        assert 'matching-process-limit' in value['reasons'] and value['status'] == 'unknown', value
        size = len(json.dumps(value).encode())
        assert size <= 20000
        results.append({'journey': 'bounded matching response', 'status': value['status'],
                        'matches': len(value['matching_processes']), 'bytes': size,
                        'reasons': value['reasons']})
    with tempfile.TemporaryDirectory(prefix='tc335-kthread-link-') as temp:
        data, proc = fixture(Path(temp))
        (proc / '200/status').write_text('Pid:\t200\nKthread:\t1\nUid:\t0\t0\t0\t0\n')
        (proc / '200/cwd').unlink()
        (proc / '200/cwd').symlink_to(Path(temp) / 'no-userspace-cwd')
        value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'], value
        assert value['scanned_process_count'] == 2, value
        results.append({'journey': 'kernel procfs dangling cwd link', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-kthread-cwd-') as temp:
        data, proc = fixture(Path(temp))
        (proc / '200/status').write_text('Pid:\t200\nKthread:\t1\nUid:\t0\t0\t0\t0\n')
        value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'], value
        assert value['scanned_process_count'] == 2, value
        results.append({'journey': 'kernel task with unrelated cwd', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-other-proc-') as temp:
        data, proc = fixture(Path(temp))
        (proc / '200/mountinfo').write_text('1 0 0:1 / / rw,hidepid=2 - proc proc rw,subset=pid\n')
        value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'], value
        assert value['scanned_process_count'] == 2, value
        results.append({'journey': 'other process restricted proc mount', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-own-proc-') as temp:
        data, proc = fixture(Path(temp))
        (proc / 'self/mountinfo').write_text('1 0 0:1 / / rw,hidepid=2 - proc proc rw\n')
        value = observe(data, proc)
        assert value['scanned_process_count'] == 0 and value['status'] == 'unknown', value
        results.append({'journey': 'collector restricted proc mount', 'result': value})
    # EACCES must dominate apparent absence; only verified ENOENT races skip.
    for failure in ('missing-maps', 'malformed-maps', 'missing-cwd', 'identity-drift'):
        with tempfile.TemporaryDirectory(prefix='tc335-provisional-') as temp:
            data, proc = fixture(Path(temp))
            identity = (data / 'helm.db').stat()
            dev = f'{os.major(identity.st_dev):x}:{os.minor(identity.st_dev):x}'
            if failure in ('missing-maps', 'malformed-maps'):
                (proc / '200/fd/4').symlink_to(data / 'helm.db')
                if failure == 'missing-maps':
                    (proc / '200/maps').unlink()
                else:
                    (proc / '200/maps').write_text('synthetic malformed\n')
            else:
                (proc / '200/maps').write_text(f'1-2 rw-s 0 {dev} {identity.st_ino} /redacted\n')
            if failure == 'missing-cwd':
                (proc / '200/cwd').unlink()
            original_starttime = mod._starttime
            calls = [0]
            def drift(path, pid, budget):
                value = original_starttime(path, pid, budget)
                if pid == 200:
                    calls[0] += 1
                    if calls[0] == 2:
                        return str(int(value) + 1)
                return value
            with patch.object(mod, '_starttime', drift if failure == 'identity-drift' else original_starttime):
                value = observe(data, proc)
            matches = [p for p in value['matching_processes'] if p['pid'] == 200]
            assert matches and matches[0].get('provisional') is True, value
            expected = 'fd_count' if 'maps' in failure else 'mapping_count'
            assert matches[0][expected] == 1 and matches[0].get('uncertainty'), value
            assert value['status'] == 'unknown', value
            results.append({'journey': 'positive evidence before ' + failure,
                            'status': value['status'], 'matching_processes': matches,
                            'reasons': value['reasons']})
    with tempfile.TemporaryDirectory(prefix='tc335-denied-') as temp:
        data, proc = fixture(Path(temp))
        original_open = Path.open
        def denied(path, *args, **kwargs):
            if path == proc / '200/maps':
                raise PermissionError('synthetic denied metadata')
            return original_open(path, *args, **kwargs)
        with patch.object(Path, 'open', denied):
            value = observe(data, proc)
        assert 'process-unreadable' in value['reasons'], value
        results.append({'journey': 'permission denied maps', 'result': value})
    with tempfile.TemporaryDirectory(prefix='tc335-exit-') as temp:
        data, proc = fixture(Path(temp))
        original_open = Path.open
        def exited(path, *args, **kwargs):
            if path == proc / '200/status':
                import shutil
                shutil.rmtree(proc / '200')
            return original_open(path, *args, **kwargs)
        with patch.object(Path, 'open', exited):
            value = observe(data, proc)
        assert value['reasons'] == ['restricted-proc-visibility'], value
        results.append({'journey': 'verified exited process', 'result': value})
    # A real process, a real FD, and a real mapping without a retained FD.
    # The proc fixture restricts visibility and therefore always remains unknown.
    with tempfile.TemporaryDirectory(prefix='tc335-real-') as temp:
        data, proc = fixture(Path(temp))
        script = ('import ctypes,os,sys; '
                  'fd=os.open(sys.argv[1],os.O_RDWR); '
                  'other=os.open(sys.argv[2],os.O_RDWR); '
                  'lib=ctypes.CDLL(None,use_errno=True); '
                  'lib.mmap.restype=ctypes.c_void_p; '
                  'lib.mmap.argtypes=[ctypes.c_void_p,ctypes.c_size_t,ctypes.c_int,'
                  'ctypes.c_int,ctypes.c_int,ctypes.c_long]; '
                  'mapped=lib.mmap(None,os.fstat(other).st_size,3,1,other,0); '
                  'assert mapped!=ctypes.c_void_p(-1).value; os.close(other); '
                  'print("ready",flush=True); sys.stdin.read(1)')
        child = subprocess.Popen([sys.executable, '-c', script, str(data / 'helm.db'), str(data / 'helm.db-wal')],
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            assert child.stdout.readline().strip() == 'ready'
            for entry in ('200',):
                import shutil
                shutil.rmtree(proc / entry)
            (proc / str(child.pid)).symlink_to(Path('/proc') / str(child.pid), target_is_directory=True)
            value = observe(data, proc)
            matching = [p for p in value['matching_processes'] if p['pid'] == child.pid]
            assert matching and matching[0]['fd_count'] == 1 and matching[0]['mapping_count'] == 1, value
            assert value['status'] == 'unknown', value
            results.append({'journey': 'real retained FD and closed-FD mapping', 'result': value})
        finally:
            child.communicate('x', timeout=10)
            assert child.returncode == 0
    ARTIFACT.mkdir(parents=True, exist_ok=True)
    (ARTIFACT / 'journeys.json').write_text(json.dumps({'format': 1, 'journeys': results}, indent=2) + '\n')
    print(f'{len(results)} drain journeys passed; sanitized artifact .artifacts/tc335/drain/journeys.json')


if __name__ == '__main__':
    run()
