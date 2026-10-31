"""Native root fixture launcher; only exclusively created fixture children are removed."""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import stat
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
PARENT = Path('/var/lib/helm-admission-tests')


def binding(base):
    operator = base / 'operator'
    operator.mkdir(mode=0o700)
    value = dict(format=1, boot_id=Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
                 pid_namespace=os.readlink('/proc/1/ns/pid'), mnt_namespace=os.readlink('/proc/1/ns/mnt'))
    path = operator / 'host.json'
    path.write_text(json.dumps(value, sort_keys=True, separators=(',', ':')) + '\n')
    path.chmod(0o600)


def launch(script, label, timeout=300, arguments=()):
    output = ROOT / '.artifacts/tc335/bootstrap' / (label + '-' + secrets.token_hex(6))
    output.mkdir(parents=True)
    result = subprocess.run(['sudo', '-n', '/usr/bin/python3', '-B', str(Path(__file__).resolve()),
                             '--script', str(script.resolve()), '--token', secrets.token_hex(12),
                             '--script-args', *arguments],
                            text=True, capture_output=True, timeout=timeout)
    if len(result.stdout.encode()) > 20000:
        raise RuntimeError('bounded native receipt exceeded')
    (output / 'receipt.jsonl').write_text(result.stdout)
    (output / 'result.json').write_text(json.dumps(dict(exit_code=result.returncode,
                                                      error=result.stderr[-2500:]), indent=2) + '\n')
    print(str(output.relative_to(ROOT)))
    if result.returncode:
        print(result.stderr[-2500:], file=sys.stderr)
    return result.returncode


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--script', type=Path, required=True)
    parser.add_argument('--token', required=True)
    parser.add_argument('--script-args', nargs=argparse.REMAINDER, default=[])
    args = parser.parse_args()
    assert os.geteuid() == 0 and len(args.token) == 24 and all(c in '0123456789abcdef' for c in args.token)
    assert args.script.parent == ROOT / 'test/deployment-policy'
    try:
        PARENT.mkdir(mode=0o700)
    except FileExistsError:
        pass
    s = PARENT.lstat()
    assert stat.S_ISDIR(s.st_mode) and s.st_uid == s.st_gid == 0 and stat.S_IMODE(s.st_mode) == 0o700
    fixture = PARENT / args.token
    fixture.mkdir(mode=0o700)
    pinned = fixture.stat()
    try:
        result = subprocess.run(['/usr/bin/python3', '-B', str(args.script), '--inside', '--output', str(fixture),
                                 *args.script_args],
                                check=False)
        return result.returncode
    finally:
        current = fixture.lstat()
        assert (current.st_dev, current.st_ino) == (pinned.st_dev, pinned.st_ino)
        shutil.rmtree(fixture)


if __name__ == '__main__':
    raise SystemExit(main())
