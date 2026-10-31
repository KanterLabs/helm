#!/usr/bin/env python3
"""DEV closed-bootstrap integration; no activation or live deployment."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import time


ROOT = Path(__file__).resolve().parents[2]
TOOLS = 'golang@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51'
BASE = 'ghcr.io/kanterlabs/helm@sha256:5484aee2b817d5cd54987a7c24b1ed9b54176430302d6f08eacf8abb59d29cdd'


class Journey:
    def __init__(self, binary):
        self.binary = binary.resolve(strict=True)
        self.prefix = 'helm-bootstrap-e2e-' + secrets.token_hex(5)
        self.out = ROOT / '.artifacts/tc335/bootstrap-runtime' / self.prefix
        self.fixture = self.out / 'fixture'
        self.fixture.mkdir(parents=True, exist_ok=False)
        self.names = []
        self.events = []

    def call(self, args, stdin=None, timeout=60, required=True):
        result = subprocess.run(args, input=stdin, text=True, capture_output=True, timeout=timeout)
        if required and result.returncode:
            raise RuntimeError(f'{args[0]} failed: {(result.stderr + result.stdout)[:500]}')
        return result

    def root(self, code):
        name = self.prefix + '-root-' + secrets.token_hex(3)
        self.names.append(name)
        args = ['docker', 'run', '--rm', '-i', '--name', name, '--network', 'none',
                '--read-only', '--cap-drop', 'ALL', '--cap-add', 'CHOWN',
                '--cap-add', 'FOWNER', '--cap-add', 'DAC_OVERRIDE',
                '--security-opt', 'no-new-privileges', '--user', '0:0',
                '--tmpfs', '/tmp:rw,nosuid,nodev',
                '--mount', f'type=bind,src={self.fixture},dst=/srv/helm',
                '--mount', f'type=bind,src={ROOT / "deploy"},dst=/src/deploy,readonly']
        result = self.call(args + ['--entrypoint', 'python3', TOOLS, '-B', '-'], code)
        if len(result.stdout.encode()) > 20000:
            raise RuntimeError('root metadata output exceeded bound')
        return json.loads(result.stdout)

    def initialize(self):
        return self.root('''import os,json,sqlite3,shutil
from pathlib import Path
r=Path('/srv/helm'); os.chown(r,0,0); os.chmod(r,0o755)
lane=r/'beta'; lane.mkdir(mode=0o755)
data=lane/'data'; data.mkdir(mode=0o700)
config=lane/'config'; config.mkdir(mode=0o700)
with sqlite3.connect(data/'roadmap.db') as c:
 c.executescript("PRAGMA foreign_keys=ON; CREATE TABLE p(id TEXT PRIMARY KEY); CREATE TABLE c(id TEXT PRIMARY KEY,p TEXT REFERENCES p(id)); INSERT INTO p VALUES('synthetic-parent'); INSERT INTO c VALUES('synthetic-child','synthetic-parent');")
(config/'runtime.env').write_text('SYNTHETIC_ONLY=1\\n')
(config/'origin.crt').write_text('synthetic certificate fixture\\n')
for tree in [data,config]:
 os.chown(tree,65532,65532)
 for p in tree.iterdir(): os.chown(p,65532,65532); os.chmod(p,0o600)
os.chmod(config/'origin.crt',0o400)
backups=lane/'backups'; backups.mkdir(mode=0o700)
backup=backups/'synthetic-integration'; backup.mkdir(mode=0o700)
for name in ['data','config']: shutil.copytree(lane/name,backup/name)
import hashlib
p=data/'roadmap.db'; s=p.stat()
print(json.dumps({'database_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'database_inode':s.st_ino,'database_device':s.st_dev}))
''')

    def holder(self, mode):
        name = self.prefix + '-' + mode
        self.names.append(name)
        code = ("import os,mmap,time; p='/var/lib/roadmap/data/roadmap.db'; "
                "fd=os.open(p,os.O_RDWR); ")
        if mode == 'map':
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
                   '--mount', f'type=bind,src={self.fixture / "beta/data"},dst=/var/lib/roadmap/data',
                   '--entrypoint', 'python3', TOOLS, '-B', '-u', '-c', code])
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            result = self.call(['docker', 'logs', '--tail', '2', name])
            if 'synthetic-holder-ready' in result.stdout:
                return name
            time.sleep(0.05)
        raise RuntimeError('synthetic holder never became ready')

    def prepare(self):
        digest = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        return self.root('''import sys,json,os
from pathlib import Path
sys.path.insert(0,'/src/deploy')
from admission.bootstrap import preview,prepare
p=preview('beta','synthetic-integration',IMAGE,SHA,BINARY,NONCE)
d=Path('/srv/helm/beta/operator'); d.mkdir(mode=0o700)
f=d/'prepare-plan.json'
f.write_text(json.dumps(p)); f.chmod(0o600)
print(json.dumps(prepare(f)))
'''.replace('IMAGE', repr(BASE)).replace('BINARY', repr(digest))
                         .replace('NONCE', repr('cd' * 32))
                         .replace('SHA', repr('690620c9b9b4a037cf5aa2447cf29d8441a090c9')))

    def observe(self):
        # Existing DEV operator privilege, actual host PID AND mount namespaces.
        # A Docker root process has a different mount namespace from host init.
        code = '''import sys,json
from pathlib import Path
sys.path.insert(0,DEPLOY)
from admission.drain import observe
print(json.dumps(observe(Path(DATA))))
'''.replace('DEPLOY', repr(str(ROOT / 'deploy'))).replace('DATA', repr(str(self.fixture / 'beta/data')))
        result = self.call(['sudo', '-n', '/usr/bin/python3', '-B', '-'], code, timeout=120)
        if len(result.stdout.encode()) > 20000:
            raise RuntimeError('host snapshot output exceeded bound')
        return json.loads(result.stdout)

    def reject_runtime(self):
        name = self.prefix + '-closed-runtime'
        self.names.append(name)
        result = self.call(['docker', 'run', '--name', name, '--network', 'none',
                            '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                            '--user', '70541:70541', '--workdir', '/',
                            '--mount', f'type=bind,src={self.binary},dst=/usr/local/bin/helm,readonly',
                            '--mount', f'type=bind,src={self.fixture / "beta/data"},dst=/var/lib/roadmap/data',
                            '--mount', f'type=bind,src={self.fixture / "beta/security"},dst=/etc/helm-admission,readonly',
                            '--entrypoint', '/usr/local/bin/helm', BASE], required=False, timeout=15)
        return dict(rejected=result.returncode != 0 and 'admission' in result.stderr + result.stdout,
                    exit=result.returncode)

    def verify(self):
        return self.root('''import sys,json,hashlib
from pathlib import Path
sys.path.insert(0,'/src/deploy')
from admission.bootstrap import prepare
r=Path('/srv/helm/beta'); p=r/'data/roadmap.db'; s=p.stat()
lock=(r/'security/runtime.lock').stat().st_ino
replay=prepare(r/'operator/prepare-plan.json')
assert (r/'security/runtime.lock').stat().st_ino==lock
marker=json.loads((r/'security/marker.json').read_text())
(r/'security/marker.json').unlink()
refused=False
try: prepare(r/'operator/prepare-plan.json')
except Exception: refused=True
print(json.dumps({'database_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'database_inode':s.st_ino,'database_device':s.st_dev,'marker_phase':marker['phase'],'lock_stable':True,'lost_marker_refused':refused,'lost_marker_recreated':(r/'security/marker.json').exists()}))
''')

    def execute(self):
        failures = []
        try:
            before = self.initialize()
            fd, mapping = self.holder('fd'), self.holder('map')
            self.events.append(dict(stage='prepared', receipt=self.prepare()))
            both = self.observe()
            self.events.append(dict(stage='both-held', observation=both))
            matches = both['matching_processes']
            if (both['status'] == 'no_access_observed' or not any(p['fd_count'] for p in matches)
                    or not any(p['mapping_count'] for p in matches)):
                failures.append('held-access-not-observed')
            self.call(['docker', 'stop', '-t', '2', fd])
            only_map = self.observe()
            self.events.append(dict(stage='mapping-only', observation=only_map))
            if only_map['status'] == 'no_access_observed':
                failures.append('mapping-access-not-observed')
            matches = only_map['matching_processes']
            if not matches or any(p['fd_count'] != 0 for p in matches) or not any(p['mapping_count'] for p in matches):
                failures.append('mapping-still-had-descriptor-or-was-missed')
            closed = self.reject_runtime()
            self.events.append(dict(stage='closed-runtime', **closed))
            if not closed['rejected']:
                failures.append('prepared-runtime-started')
            self.call(['docker', 'stop', '-t', '2', mapping])
            clean = self.observe()
            self.events.append(dict(stage='holders-stopped', observation=clean))
            if clean['status'] != 'no_access_observed':
                failures.append('complete-clean-snapshot-unavailable')
            after = self.verify()
            self.events.append(dict(stage='replay-and-lost-marker', result=after))
            if any(before[k] != after[k] for k in before):
                failures.append('data-bytes-or-identity-changed')
            if (after['marker_phase'] != 'prepared' or not after['lost_marker_refused']
                    or after['lost_marker_recreated']):
                failures.append('closed-authority-not-preserved')
        except Exception as error:
            failures.append(type(error).__name__)
            self.events.append(dict(harness_error=str(error)[:1000]))
        finally:
            for name in list(self.names):
                self.call(['docker', 'rm', '-f', name], required=False)
            # Only newly created synthetic fixtures; no retained image removal.
            try:
                self.root('''import os,json
from pathlib import Path
root=Path('/srv/helm')
for base,dirs,files in os.walk(root):
 for name in files+dirs: os.chown(Path(base)/name,UID,GID)
os.chown(root,UID,GID)
print(json.dumps({'fixture_ownership_restored':True}))
'''.replace('UID', str(os.getuid())).replace('GID', str(os.getgid())))
            except Exception:
                failures.append('fixture-cleanup')
            remaining = self.call(['docker', 'ps', '-aq', '--filter', f'name={self.prefix}']).stdout.split()
            if remaining:
                failures.append('remaining-containers')
            receipt = dict(scope='DEV closed bootstrap and host-access snapshot, NOT continuing fence or activation',
                           events=self.events, failures=failures, remaining_containers=remaining)
            text = json.dumps(receipt, indent=2) + '\n'
            if len(text.encode()) > 20000:
                raise RuntimeError('receipt exceeds bound')
            path = self.out / 'receipt.json'
            path.write_text(text)
            print(json.dumps({'receipt': str(path), 'failures': failures}))
        return 1 if failures else 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    raise SystemExit(Journey(parser.parse_args().binary).execute())
