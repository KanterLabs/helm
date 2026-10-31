"""Synthetic proc and filesystem scope fixtures; never a host fence."""
import os
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]


def load():
    sys.path.insert(0, str(ROOT / 'deploy'))
    from admission import drain
    return drain


def fixture(base):
    data, proc = base / 'data', base / 'proc'
    data.mkdir()
    (data / 'helm.db').write_bytes(b'synthetic database placeholder')
    (data / 'helm.db-wal').write_bytes(b'synthetic wal placeholder')
    fd = os.open(data, os.O_RDONLY | os.O_DIRECTORY)
    try:
        info = Path(f'/proc/self/fdinfo/{fd}').read_text()
        mount_id = next(line.split()[1] for line in info.splitlines() if line.startswith('mnt_id:'))
    finally:
        os.close(fd)
    device = data.stat().st_dev
    filesystem = f'{mount_id} 0 {os.major(device)}:{os.minor(device)} / / rw - ext4 synthetic rw\n'
    for name in ('1', 'self', '200'):
        p = proc / name
        (p / 'ns').mkdir(parents=True)
        for namespace in ('pid', 'mnt'):
            (p / 'ns' / namespace).symlink_to(f'{namespace}:[123]')
        uid = 1000 if name == '200' else 0
        (p / 'status').write_text(f'Pid:\t{200 if name == "200" else 1}\nNSpid:\t1\nUid:\t{uid}\t{uid}\t{uid}\t{uid}\n')
        (p / 'stat').write_text(f'{200 if name == "200" else 1} (fixture) S ' + '0 ' * 18 + '123 0\n')
        (p / 'mountinfo').write_text(filesystem)
    (proc / '200/fd').mkdir()
    (proc / '200/maps').write_text('')
    (proc / '200/cwd').symlink_to(base)
    (proc / '1/fd').mkdir()
    (proc / '1/maps').write_text('')
    (proc / '1/cwd').symlink_to(base)
    return data, proc
