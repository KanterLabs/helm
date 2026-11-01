#!/usr/bin/env python3
"""Actual native-root operator transitions on synthetic data and proof exchanges."""
import argparse
import copy
import fcntl
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
from unittest.mock import patch

from bootstrap_fixture import fixture, save, snapshot
from bootstrap_native import launch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'deploy'))
from admission import bootstrap
from admission.filesystem import BootstrapError
from rollback_policy import CAPABILITIES, SCENARIOS

BASE = 'ghcr.io/kanterlabs/helm@sha256:'
TARGET = dict(lane='beta', service_uuid='synthetic-service', service_name='synthetic-beta',
              project_uuid='synthetic-project', environment_uuid='synthetic-environment',
              server_uuid='synthetic-server', site_url='https://synthetic.invalid',
              api_url='https://synthetic.invalid/api/v1', data_identity='synthetic-volume/roadmap.db')
RUNTIME_BASE = BASE + '5484aee2b817d5cd54987a7c24b1ed9b54176430302d6f08eacf8abb59d29cdd'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Journey:
    def __init__(self, output, binary):
        self.output, self.binary, self.events = output, binary, []
        self.base, self.lane, _ = fixture(output, 'operator')
        self.security = self.lane / 'security'
        self.artifacts = {role: dict(image=BASE + letter * 64, sha=letter * 40,
                                    binary_sha256=sha(binary) if role == 'candidate' else 'd' * 64)
                          for role, letter in [('candidate', 'b'), ('recovery', 'c')]}
        self.bootstrap_path = self.base / 'bootstrap.json'
        candidate = self.artifacts['candidate']
        save(self.bootstrap_path, bootstrap.preview('beta', 'synthetic-snapshot', candidate['image'],
             candidate['sha'], candidate['binary_sha256'], 'a' * 64, base=self.base))
        bootstrap.prepare(self.bootstrap_path, base=self.base)
        self.before = {name: snapshot(self.lane / name) for name in ('data', 'config')}
        self.lock_inode = (self.security / 'runtime.lock').stat().st_ino
        self.bundle = self.base / 'bundle.json'
        self.acceptance()
        pair = lambda role: {key: self.artifacts[role][key] for key in ('image', 'sha')}
        self.floor = dict(format=1, minimum=41, target=TARGET,
                          activation=dict(bundle_sha256=sha(self.bundle),
                                          candidate=pair('candidate'), recovery=pair('recovery')))

    def acceptance(self):
        items = {}
        for role, artifact in self.artifacts.items():
            subject = {key: artifact[key] for key in ('image', 'sha')}
            proofs = {}
            for scenario in SCENARIOS:
                path = self.base / (role + '-' + scenario + '.json')
                exchange = dict(method='GET', path='/synthetic', status=403, expected_status=403,
                                response={'error': 'forbidden'}, expected_response={'error': 'forbidden'})
                save(path, dict(format=1, scenario=scenario, target=TARGET, subject=subject,
                                result='passed', observations=[dict(assertion='SYNTHETIC ONLY',
                                                                   passed=True, exchange=exchange)]))
                proofs[scenario] = dict(file=path.name, sha256=sha(path))
            items[role] = dict(**subject, capabilities=sorted(CAPABILITIES), proofs=proofs)
        save(self.bundle, dict(format=1, kind='helm-runtime-acceptance-v1',
             producer='tc336-isolated-runtime-v1', issued_at=int(time.time()) - 1,
             expires_at=int(time.time()) + 3600, target=TARGET, artifacts=items))

    def plan(self, action='activate', role='candidate', **changes):
        marker = json.loads((self.security / 'marker.json').read_bytes())
        value = dict(format=1, operation='native-admission-transition', action=action,
                     lane='beta', role=role, target=TARGET, artifacts=copy.deepcopy(self.artifacts),
                     authorization_floor=copy.deepcopy(self.floor),
                     bootstrap_plan=str(self.bootstrap_path), acceptance_bundle=str(self.bundle),
                     acceptance_sha256=sha(self.bundle), expected_marker_sha256=sha(self.security / 'marker.json'),
                     expected_generation=marker['generation'], operation_nonce=secrets.token_hex(32))
        value.update(changes)
        path = self.base / ('plan-' + secrets.token_hex(6) + '.json')
        save(path, value)
        return path

    def transition(self, plan):
        from admission.operator import transition
        return transition(plan, base=self.base)

    def refuse(self, label, plan):
        before = (self.security / 'marker.json').read_bytes()
        try:
            self.transition(plan)
        except BootstrapError:
            assert (self.security / 'marker.json').read_bytes() == before
            self.events.append(dict(case=label, refused=True, marker_unchanged=True))
        else:
            raise AssertionError(label + ' was accepted')

    def runtime(self, admitted):
        name = 'helm-operator-test-' + secrets.token_hex(6)
        result = subprocess.run(['docker', 'run', '--rm', '--name', name, '--network', 'none',
             '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
             '--user', '70541:70541', '--workdir', '/',
             '--mount', f'type=bind,src={self.binary},dst=/usr/local/bin/helm,readonly',
             '--mount', f'type=bind,src={self.lane / "data"},dst=/var/lib/roadmap/data',
             '--mount', f'type=bind,src={self.security},dst=/etc/helm-admission,readonly',
             '--entrypoint', '/usr/local/bin/helm', RUNTIME_BASE, 'synthetic-noop'],
             text=True, capture_output=True, timeout=30)
        combined = result.stdout + result.stderr
        assert result.returncode != 0
        assert ('invalid_command' in combined) if admitted else ('admission_denied' in combined), combined
        self.events.append(dict(case='actual-runtime', admitted=admitted,
                                captured_code='invalid_command' if admitted else 'admission_denied'))

    def run(self):
        self.runtime(False)
        self.refuse('bad-marker-hash', self.plan(expected_marker_sha256='e' * 64))
        self.refuse('bad-generation', self.plan(expected_generation=999))
        self.refuse('extra-fields', self.plan(untrusted=True))
        self.refuse('wrong-acceptance', self.plan(acceptance_sha256='e' * 64))
        proof = next(self.base.glob('candidate-*.json'))
        proof.chmod(0o644)
        self.refuse('public-proof', self.plan())
        proof.chmod(0o600)
        raw = proof.read_bytes()
        proof.write_bytes(raw.replace(b'SYNTHETIC ONLY', b'UNACCEPTED'))
        self.refuse('altered-proof', self.plan())
        proof.write_bytes(raw)
        bundle = self.bundle.read_bytes()
        stale = json.loads(bundle)
        stale['issued_at'], stale['expires_at'] = int(time.time()) - 90000, int(time.time()) - 1
        save(self.bundle, stale)
        self.refuse('expired-acceptance', self.plan())
        self.bundle.write_bytes(bundle)
        bad = copy.deepcopy(self.artifacts)
        bad['recovery']['image'] = bad['candidate']['image']
        self.refuse('non-distinct-pair', self.plan(artifacts=bad))
        floor = copy.deepcopy(self.floor)
        floor['minimum'] = 0
        self.refuse('decreased-floor', self.plan(authorization_floor=floor))
        path = self.plan()
        path.chmod(0o644)
        self.refuse('public-plan', path)
        lock = os.open(self.security / 'runtime.lock', os.O_RDONLY)
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.refuse('busy-permanent-lock', self.plan())
        finally:
            os.close(lock)
        binding = self.base / 'operator/host.json'
        original = binding.read_bytes()
        value = json.loads(original)
        value['boot_id'] = '00000000-0000-0000-0000-000000000000'
        save(binding, value)
        self.refuse('foreign-host', self.plan())
        binding.write_bytes(original)
        for name in ('data', 'config'):
            held = self.lane / name / ('roadmap.db' if name == 'data' else 'settings')
            process = subprocess.Popen([sys.executable, '-c',
                'import os,sys,time; f=open(sys.argv[1],"rb"); print("ready",flush=True); time.sleep(60)', str(held)],
                stdout=subprocess.PIPE, text=True)
            try:
                assert process.stdout.readline().strip() == 'ready'
                self.refuse(name + '-held-descriptor', self.plan())
            finally:
                process.terminate()
                process.wait(timeout=10)
        # First close may persist the accepted local floor41 before the parent
        # advances and reads back the independently controlled external floor.
        initial_floor = dict(format=1, minimum=0, target=TARGET)
        closed = self.transition(self.plan('close', authorization_floor=initial_floor))
        assert closed['authorization_floor'] == self.floor
        self.events.append(dict(case='initial-close-before-external-floor', floor=41))
        origin_path = self.security / 'operator-origin.json'
        origin_body = origin_path.read_bytes()
        for malformed in ({}, None, []):
            save(origin_path, malformed)
            self.refuse('malformed-existing-origin-' + type(malformed).__name__, self.plan())
        origin_path.write_bytes(origin_body)
        activation = self.plan()
        receipt = self.transition(activation)
        assert receipt['phase'] == 'active' and not receipt['replay']
        first_files = {p.name: sha(p) for p in self.security.iterdir() if p.is_file()}
        assert self.transition(activation)['replay']
        # Completed replay neither opens fresh admission nor rereads expired
        # acceptance; the immutable exact prior plan identifies the commit.
        with patch('rollback_policy.time.time', return_value=time.time() + 90000):
            assert self.transition(activation)['replay']
        assert first_files == {p.name: sha(p) for p in self.security.iterdir() if p.is_file()}
        self.runtime(True)
        self.refuse('activate-from-active', self.plan())
        self.transition(self.plan('close', 'recovery'))
        self.runtime(False)
        self.refuse('stale-original-replay', activation)
        recovery = self.plan(role='recovery')
        self.transition(recovery)
        self.runtime(False)  # Different pinned binary cannot use actual candidate bytes.
        self.transition(self.plan('close', 'candidate'))
        pending = self.plan()
        original_rename = os.rename
        def interrupted(source, destination, **kwargs):
            if source == 'operator-marker.next':
                raise OSError('synthetic interrupted marker switch')
            return original_rename(source, destination, **kwargs)
        with patch('os.rename', interrupted):
            self.refuse('interrupted-before-marker', pending)
        self.refuse('pending-activation-replay', pending)
        # A temp file is uncertain metadata and intentionally needs trusted root
        # reconciliation, never automatic cleanup. Tests remove only their own
        # known complete synthetic temp inode before asking for a fresh close.
        temporary = self.security / 'operator-marker.next'
        if temporary.exists():
            temporary.unlink()
        self.transition(self.plan('close', 'recovery'))
        self.refuse('canceled-pending-replay', pending)
        self.transition(self.plan(role='recovery'))
        self.transition(self.plan('close', 'candidate'))
        committed = self.plan()
        from admission import operator_state
        original_switch = operator_state.switch
        def lost_response(directory, marker):
            original_switch(directory, marker)
            raise OSError('synthetic lost response after durable marker switch')
        try:
            with patch('admission.operator_state.switch', lost_response):
                self.transition(committed)
        except BootstrapError:
            pass
        else:
            raise AssertionError('synthetic post-commit interruption missing')
        assert self.transition(committed)['replay']
        self.events.append(dict(case='post-switch-interruption', replay_read_only=True))
        self.runtime(True)
        assert all(snapshot(self.lane / name) == before for name, before in self.before.items())
        assert (self.security / 'runtime.lock').stat().st_ino == self.lock_inode
        origin = json.loads((self.security / 'operator-origin.json').read_bytes())
        assert origin['authorization_floor'] == self.floor
        self.events.append(dict(case='preservation', bytes_inodes_mtimes=True, permanent_lock=True,
                                original_activation=True, synthetic_only=True))
        print(json.dumps(dict(scope='native-root synthetic operator E2E, NOT TC336 acceptance',
                              events=self.events), sort_keys=True))
        return 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.inside:
        raise SystemExit(Journey(args.output, args.binary.resolve(strict=True)).run())
    raise SystemExit(launch(Path(__file__), 'operator-native', timeout=600,
                            arguments=('--binary', str(args.binary.resolve(strict=True)))))
