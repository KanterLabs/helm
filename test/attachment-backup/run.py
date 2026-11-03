#!/usr/bin/env python3
"""Disposable SQLite/filesystem/process journey; never targets a running lane."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time

REPO = Path(__file__).resolve().parents[2]
BACKUP = REPO / 'deploy/coolify/backup.py'
RESTORE = REPO / 'deploy/coolify/restore_attachments.py'
IDS = [f'{index:032x}' for index in range(1, 6)]
KEEPERS = []
IMAGE = os.environ.get('HELM_BACKUP_TEST_IMAGE', 'fe3da92a5cb2')


def invoke(args, success=True):
    result = subprocess.run([sys.executable, *map(str, args)], capture_output=True, text=True)
    assert (result.returncode == 0) == success, result.stderr
    return result


def fixture(base, attachments=True):
    lane = base / 'beta'
    (lane / 'data').mkdir(parents=True)
    (lane / 'config').mkdir()
    (lane / 'config/synthetic.json').write_text('{"fixture": true}\n')
    (lane / 'data/public-endpoints').mkdir()
    (lane / 'data/public-endpoints/synthetic').write_text('fixture only\n')
    db = sqlite3.connect(lane / 'data/roadmap.db')
    db.execute('PRAGMA journal_mode=WAL')
    db.executescript('CREATE TABLE projects(id TEXT PRIMARY KEY);'
                     'CREATE TABLE tasks(id TEXT PRIMARY KEY);'
                     'CREATE TABLE actors(id TEXT PRIMARY KEY);'
                     'CREATE TABLE comments(id TEXT PRIMARY KEY);'
                     "INSERT INTO projects VALUES('p');INSERT INTO tasks VALUES('t');"
                     "INSERT INTO actors VALUES('a');")
    if attachments:
        db.executescript((REPO / 'internal/db/migrations/042_task_attachments.sql').read_text())
        root = lane / 'data/attachments'
        (root / 'objects').mkdir(parents=True)
        (root / '.gc.lock').touch(mode=0o600)
        for index, status in enumerate(('published', 'staged', 'deleted', 'staged')):
            ident = IDS[index]
            blob = b'synthetic screenshot ' + ident.encode()
            db.execute('INSERT INTO task_attachments '
                       '(id,task_id,project_id,actor_id,filename,content_type,size_bytes,sha256,'
                       'width,height,status,created_at,updated_at,ready) '
                       'VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                       (ident, 't', 'p', 'a', 'fixture.png', 'image/png', len(blob),
                        hashlib.sha256(blob).hexdigest(), 1, 1, status, 'now', 'now', int(index < 3)))
            if index < 3:
                folder = root / 'objects' / ident
                folder.mkdir()
                (folder / 'original').write_bytes(blob)
                if index != 1:
                    (folder / 'thumbnail.png').write_bytes(b'synthetic thumbnail')
    db.commit()
    db.close()
    keeper = sqlite3.connect(lane / 'data/roadmap.db')
    keeper.execute('SELECT * FROM tasks').fetchall()
    KEEPERS.append(keeper)
    return lane


def backup(base, stamp, success=True):
    return invoke([BACKUP, '--base', base, '--lane', 'beta', '--stamp', stamp,
                   '--snapshot-container-image', IMAGE], success)


def wait_path(path, process):
    deadline = time.monotonic() + 15
    while not path.exists():
        assert process.poll() is None, process.communicate()
        assert time.monotonic() < deadline, f'timeout: {path.name}'
        time.sleep(.02)


def race_journey(base):
    lane = fixture(base)
    root = lane / 'data/attachments'
    inode = (root / '.gc.lock').stat().st_ino
    checkpoint, proceed, acquired = [base / name for name in ('checkpoint', 'proceed', 'acquired')]
    # Pause the real backup process after its SQLite snapshot, inside blob copy.
    script = base / 'backup-process.py'
    script.write_text('''import sys,time
from pathlib import Path
sys.path.insert(0,sys.argv[1])
import backup, attachment_backup
original=attachment_backup.copy_blob
def pause(*args,**kwargs):
    Path(sys.argv[3]).touch()
    while not Path(sys.argv[4]).exists(): time.sleep(.01)
    return original(*args,**kwargs)
attachment_backup.copy_blob=pause
backup.main(['--base',sys.argv[2],'--lane','beta','--stamp','race',
             '--snapshot-container-image',sys.argv[5]])
''')
    writer = subprocess.Popen([sys.executable, str(script), str(BACKUP.parent), str(base),
                               str(checkpoint), str(proceed), IMAGE], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    wait_path(checkpoint, writer)
    gc_script = base / 'gc-process.py'
    gc_script.write_text('''import fcntl,sys,sqlite3,shutil
from pathlib import Path
root=Path(sys.argv[1])
with (root/'.gc.lock').open('rb') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 Path(sys.argv[2]).touch()
 db=sqlite3.connect(root.parent/'roadmap.db')
 db.execute('DELETE FROM task_attachments WHERE id=?',(sys.argv[3],))
 db.commit();db.close()
 shutil.rmtree(root/'objects'/sys.argv[3])
''')
    gc = subprocess.Popen([sys.executable, str(gc_script), str(root), str(acquired), IDS[2]])
    try:
        time.sleep(.15)
        assert not acquired.exists(), 'GC entered while snapshot originals remained pinned'
        # Simulate a concurrent post-snapshot reservation; it need not be copied.
        with sqlite3.connect(lane / 'data/roadmap.db') as db:
            db.execute('UPDATE task_attachments SET caption=? WHERE id=?', ('later', IDS[0]))
        proceed.touch()
        out, err = writer.communicate(timeout=20)
        assert writer.returncode == 0, err.decode()
        assert gc.wait(timeout=10) == 0
    finally:
        for process in (writer, gc):
            if process.poll() is None:
                process.kill()
                process.wait()
    assert (root / '.gc.lock').stat().st_ino == inode
    target = base / 'restored'
    snapshot = lane / 'backups/race'
    invoke([RESTORE, snapshot, target])
    with sqlite3.connect(target / 'data/roadmap.db') as db:
        rows = db.execute('SELECT id,caption FROM task_attachments WHERE ready=1 ORDER BY id').fetchall()
    assert len(rows) == 3 and all(caption == '' for _, caption in rows)
    assert (target / 'data/attachments/objects' / IDS[2] / 'original').is_file()
    assert (target / 'config/synthetic.json').is_file()
    assert (target / 'data/public-endpoints/synthetic').is_file()
    assert not (target.stat().st_mode & 0o077)
    manifest = json.loads((snapshot / 'attachments.json').read_text())
    assert len(manifest['objects']) == 3
    assert any(item['thumbnail']['regenerable'] for item in manifest['objects'])
    invoke([RESTORE, snapshot, target], False)
    (snapshot / 'attachments/objects' / IDS[0] / 'original').write_bytes(b'corrupt')
    invoke([RESTORE, snapshot, base / 'corrupt-restore'], False)
    assert not (base / 'corrupt-restore').exists()
    return hashlib.sha256((snapshot / 'verified.json').read_bytes()).hexdigest()


def readonly_journey(base):
    lane = fixture(base)
    data = lane / 'data'
    def inventory():
        return {path.name: (path.stat().st_ino, path.stat().st_uid, path.stat().st_mode,
                            hashlib.sha256(path.read_bytes()).hexdigest())
                for path in data.iterdir() if path.name.startswith('roadmap.db')}
    before = inventory()
    assert 'roadmap.db-wal' in before and 'roadmap.db-shm' in before
    backup(base, 'readonly')
    assert inventory() == before, 'read-only snapshot changed source SQLite state'
    KEEPERS[-1].close()
    assert not (data / 'roadmap.db-wal').exists()
    assert not (data / 'roadmap.db-shm').exists()
    before = inventory()
    backup(base, 'missing-sidecars', False)
    assert inventory() == before, 'failed read-only snapshot created SQLite sidecars'
    assert not (lane / 'backups/missing-sidecars/verified.json').exists()
    return 'passed'


def failure_journeys(base):
    outcomes = []
    for name in ('missing', 'corrupt', 'symlink', 'missing-lock', 'invalid-id'):
        lane = fixture(base / name)
        root = lane / 'data/attachments'
        original = root / 'objects' / IDS[0] / 'original'
        if name == 'missing': original.unlink()
        if name == 'corrupt': original.write_bytes(b'bad')
        if name == 'symlink':
            original.unlink()
            original.symlink_to(root / 'objects' / IDS[1] / 'original')
        if name == 'missing-lock': (root / '.gc.lock').unlink()
        if name == 'invalid-id':
            with sqlite3.connect(lane / 'data/roadmap.db') as db:
                db.execute('UPDATE task_attachments SET id=? WHERE id=?', ('../escape', IDS[0]))
        backup(base / name, name, False)
        assert not (lane / f'backups/{name}/verified.json').exists()
        if name == 'missing-lock': assert not (root / '.gc.lock').exists()
        outcomes.append(name)
    legacy = base / 'legacy'
    lane = fixture(legacy, False)
    root = lane / 'data/attachments'
    orphan = root / 'objects' / IDS[4]
    orphan.mkdir(parents=True)
    (root / '.gc.lock').touch(mode=0o600)
    (orphan / 'original').write_bytes(b'legacy orphan retained without guessed linkage')
    backup(legacy, 'legacy')
    invoke([RESTORE, lane / 'backups/legacy', legacy / 'restored'])
    assert (legacy / 'restored/data/attachments/objects' / IDS[4] / 'original').is_file()
    manifest = json.loads((lane / 'backups/legacy/attachments.json').read_text())
    assert manifest['objects'][0]['reference'] == 'orphan'
    return outcomes


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--receipt', required=True, type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='helm-attachment-backup-') as folder:
        base = Path(folder)
        race_hash = race_journey(base / 'race')
        failures = failure_journeys(base / 'failures')
        readonly = readonly_journey(base / 'readonly')
        for keeper in KEEPERS:
            keeper.close()
    receipt = {'result': 'passed', 'gc_race_and_restore': 'passed',
               'legacy_restore': 'passed', 'readonly_source_mount': readonly, 'rejected': failures,
               'race_backup_receipt_sha256': race_hash}
    receipt['evidence_sha256'] = hashlib.sha256(json.dumps(receipt, sort_keys=True).encode()).hexdigest()
    args.receipt.parent.mkdir(parents=True, exist_ok=True)
    args.receipt.write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps(receipt, sort_keys=True))


if __name__ == '__main__':
    main()
