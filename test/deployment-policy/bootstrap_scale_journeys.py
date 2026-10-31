#!/usr/bin/env python3
"""Actual-sized synthetic CLOSED bootstrap with opaque links and paged authority."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import resource
import secrets
import shutil
import subprocess
import sys
from unittest import mock

from bootstrap_fixture import fixture, save, snapshot

ROOT = Path(__file__).resolve().parents[2]
SETUP = 'golang@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51'
sys.path.insert(0, str(ROOT / 'deploy'))


def inside(output):
    from admission.bootstrap import BootstrapError, prepare, preview
    from admission import backup as backup_module
    output.chmod(0o700)
    os.chown(output, 0, 0)
    # A late legitimate metadata change must not be overwritten from stale
    # inventory merely because the file bytes and inode are unchanged.
    drift_base, drift_lane, _ = fixture(output, 'metadata-drift')
    drift_plan = preview('beta', 'synthetic-snapshot', 'ghcr.io/kanterlabs/helm@sha256:' + 'ab' * 32,
                         'cd' * 20, 'ef' * 32, '12' * 32, base=drift_base)
    drift_path = drift_base / 'plan.json'
    save(drift_path, drift_plan)
    target = drift_lane / 'data/sessions/synthetic-session'
    original_verify = backup_module.verify
    def change_after_verification(*args, **kwargs):
        result = original_verify(*args, **kwargs)
        target.chmod(0o700)
        return result
    with mock.patch.object(backup_module, 'verify', change_after_verification):
        try:
            prepare(drift_path, base=drift_base)
        except BootstrapError:
            pass
        else:
            raise AssertionError('late source metadata change was silently overwritten')
    assert target.stat().st_uid == 65532 and target.stat().st_mode & 0o777 == 0o700
    assert target.read_bytes() == b'synthetic durable session\n'
    assert json.loads((drift_lane / 'security/marker.json').read_text())['phase'] == 'prepared'
    base, lane, backup = fixture(output, 'actual-shape')
    external = output / 'SYNTHETIC-external'
    external.write_bytes(b'SYNTHETIC protected external bytes\n')
    external.chmod(0o640)
    for number in range(10320):
        item = lane / 'data/sessions' / ('SYNTHETIC-' + str(number).zfill(5))
        item.write_bytes(b'SYNTHETIC durable bytes\n')
        os.chown(item, 65532, 65532)
    for number, target in enumerate((str(external), '/SYNTHETIC/unresolved',
                                     '../SYNTHETIC/dangling', '/SYNTHETIC/second-external')):
        link = lane / 'data/sessions' / ('SYNTHETIC-link-' + str(number))
        link.symlink_to(target)
        os.chown(link, 65532, 65532, follow_symlinks=False)
    shutil.rmtree(backup / 'data')
    shutil.copytree(lane / 'data', backup / 'data', symlinks=True)
    for item in [backup / 'data', *(backup / 'data').rglob('*')]:
        os.chown(item, 0, 0, follow_symlinks=False)
        if not item.is_symlink():
            item.chmod(0o700 if item.is_dir() else 0o600)
    source_before = snapshot(lane / 'data')
    external_before = snapshot(external.parent)['SYNTHETIC-external']
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    resource.setrlimit(resource.RLIMIT_NOFILE, (1024, hard))
    value = preview('beta', 'synthetic-snapshot', 'ghcr.io/kanterlabs/helm@sha256:' + 'ab' * 32,
                    'cd' * 20, 'ef' * 32, '12' * 32, base=base)
    raised, unchanged_hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    assert raised > 2 * 10229 and unchanged_hard == hard
    script = ('import resource; from pathlib import Path; from bootstrap_fixture import fixture; '
              'from admission.bootstrap import preview; resource.setrlimit(resource.RLIMIT_NOFILE,(1024,1024)); '
              'base,_,_=fixture(Path("/fixtures"),"small-hard-limit"); '
              'p=preview("beta","synthetic-snapshot","ghcr.io/kanterlabs/helm@sha256:"+"ab"*32,'
              '"cd"*20,"ef"*32,"12"*32,base=base); assert p["entries"]==7')
    env = {**os.environ, 'PYTHONPATH': str(ROOT / 'deploy') + ':' + str(ROOT / 'test/deployment-policy')}
    small = subprocess.run([sys.executable, '-c', script], env=env, text=True, capture_output=True, timeout=20)
    assert small.returncode == 0, 'small fixture must fit existing low hard limit'
    path = base / 'plan.json'
    save(path, value)
    original = os.fchown
    count = [0]

    def interrupt(fd, uid, gid):
        if uid == 70541:
            count[0] += 1
            if count[0] == 5000:
                raise OSError('SYNTHETIC metadata interruption')
        return original(fd, uid, gid)

    with mock.patch('os.fchown', interrupt):
        try:
            prepare(path, base=base)
        except BootstrapError:
            pass
        else:
            raise AssertionError('partial metadata must interrupt')
    security = lane / 'security'
    assert json.loads((security / 'journal.json').read_text())['phase'] == 'preparing'
    receipt = prepare(path, base=base)
    assert receipt['closed'] and receipt['entries'] > 10229
    assert snapshot(lane / 'data') == source_before
    assert snapshot(external.parent)['SYNTHETIC-external'] == external_before
    for item in [lane / 'data', *(lane / 'data').rglob('*')]:
        s = item.lstat()
        if item.is_symlink():
            assert s.st_uid == s.st_gid == 65532
        else:
            assert s.st_uid == s.st_gid == 70541 and not s.st_mode & 0o077
    journal = json.loads((security / 'journal.json').read_text())
    assert journal['format'] == 2 and 2 <= len(journal['indexes']) <= 32
    pages = []
    for descriptor in journal['indexes']:
        index = security / descriptor['name']
        assert index.stat().st_size <= 16000
        pages.extend(json.loads(index.read_text()))
    assert len(pages) > 64 and len(pages) <= 2048
    assert max(item.stat().st_size for item in security.iterdir()) <= 20000
    before = snapshot(security)
    assert prepare(path, base=base) == receipt
    assert snapshot(security) == before
    refused = []

    def refusal(label):
        try:
            prepare(path, base=base)
        except BootstrapError:
            refused.append(label)
        else:
            raise AssertionError(label + ': unexpectedly accepted')

    index = security / journal['indexes'][0]['name']
    index_body = index.read_bytes()
    index.write_bytes(index_body + b' ')
    refusal('corrupt-index')
    index.write_bytes(index_body)
    page = security / pages[-1]['name']
    page_body = page.read_bytes()
    page.unlink()
    refusal('missing-page')
    page.write_bytes(page_body)
    page.chmod(0o600)
    unknown = security / 'SYNTHETIC-unknown'
    unknown.write_bytes(b'SYNTHETIC')
    unknown.chmod(0o600)
    refusal('unknown-authority')
    unknown.unlink()
    link = lane / 'data/sessions/SYNTHETIC-link-0'
    os.chown(link, 12345, 12345, follow_symlinks=False)
    refusal('link-owner-drift')
    os.chown(link, 65532, 65532, follow_symlinks=False)
    assert prepare(path, base=base) == receipt
    assert snapshot(lane / 'data') == source_before
    link.unlink()
    link.symlink_to('/SYNTHETIC/changed-target')
    os.chown(link, 65532, 65532, follow_symlinks=False)
    refusal('link-target-and-inode-drift')
    assert snapshot(external.parent)['SYNTHETIC-external'] == external_before
    print(json.dumps(dict(result='closed', entries=receipt['entries'], links=4,
                          record_pages=len(pages), indexes=len(journal['indexes']),
                          preservation_sha256=hashlib.sha256(json.dumps(source_before, sort_keys=True).encode()).hexdigest(),
                          refused=refused), sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', action='store_true')
    args = parser.parse_args()
    if args.inside:
        inside(Path('/fixtures'))
        return
    output = ROOT / '.artifacts/tc335/bootstrap' / ('scale-' + secrets.token_hex(6))
    output.mkdir(parents=True)
    result = subprocess.run([
        'docker', 'run', '--rm', '--network', 'none', '--read-only', '--cap-drop', 'ALL',
        '--ulimit', 'nofile=131072:131072', '--cap-add', 'CHOWN', '--cap-add', 'FOWNER',
        '--cap-add', 'DAC_OVERRIDE', '--security-opt', 'no-new-privileges', '--user', '0:0',
        '--tmpfs', '/tmp:rw,nosuid,nodev,noexec',
        '--mount', f'type=bind,src={ROOT},dst=/source,readonly',
        '--mount', f'type=bind,src={output},dst=/fixtures', '--entrypoint', 'python3', SETUP,
        '/source/test/deployment-policy/bootstrap_scale_journeys.py', '--inside'],
        text=True, capture_output=True, timeout=240)
    cleanup = subprocess.run([
        'docker', 'run', '--rm', '--network', 'none', '--read-only', '--cap-drop', 'ALL',
        '--cap-add', 'CHOWN', '--cap-add', 'FOWNER', '--cap-add', 'DAC_OVERRIDE',
        '--mount', f'type=bind,src={output},dst=/fixtures', '--entrypoint', '/bin/sh', SETUP,
        '-eu', '-c', 'find /fixtures -mindepth 1 -delete; chown 1000:1000 /fixtures; chmod 700 /fixtures'],
        text=True, capture_output=True, timeout=60)
    if cleanup.returncode:
        raise SystemExit('SYNTHETIC fixture cleanup failed')
    (output / 'receipt.jsonl').write_text(result.stdout)
    (output / 'result.json').write_text(json.dumps(dict(exit_code=result.returncode,
                                                      error=result.stderr[-2500:]), indent=2) + '\n')
    print(str(output.relative_to(ROOT)))
    if result.returncode:
        print(result.stderr[-2500:], file=sys.stderr)
        raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
