#!/usr/bin/env python3
"""Native synthetic accepted-pair upgrade, including actual binary guard binding."""
import argparse
import copy
import json
import os
import sys
from pathlib import Path
import shutil
import time
from unittest.mock import patch

from bootstrap_fixture import save, snapshot
from bootstrap_native import launch
from operator_native_e2e import BASE, TARGET, Journey, sha


def plan_preflight(output):
    """Exercise the real private input reader before costly native observations."""
    from admission.operator_plan import load
    path = output / 'upgrade-preflight.json'
    save(path, dict(format=2, operation='native-admission-upgrade', action='upgrade',
        lane='beta', role='candidate', target=TARGET,
        artifacts={role: dict(image=BASE + letter * 64, sha=letter * 40,
                              binary_sha256=letter * 64)
                   for role, letter in [('candidate', 'e'), ('recovery', 'b')]},
        authorization_floor=dict(format=1, minimum=41, target=TARGET),
        bootstrap_plan=str(output / 'bootstrap.json'), acceptance_bundle=str(output / 'bundle.json'),
        acceptance_sha256='a' * 64, expected_marker_sha256='a' * 64,
        expected_generation=1, operation_nonce='a' * 64))
    assert load(path)['action'] == 'upgrade'


class Upgrade(Journey):
    def acceptance(self):
        super().acceptance()
        bundle = json.loads(self.bundle.read_bytes())
        for artifact in bundle['artifacts'].values():
            for proof in artifact['proofs'].values():
                source = self.base / proof['file']
                name = self.bundle.stem + '-' + source.name
                source.rename(self.base / name)
                proof['file'] = name
        save(self.bundle, bundle)

    def upgrade(self, **changes):
        return self.plan('upgrade', format=2, operation='native-admission-upgrade', **changes)

    def run(self):
        self.transition(self.plan('close'))
        self.transition(self.plan())
        original_pair, original_bundle = copy.deepcopy(self.artifacts), self.bundle
        original_floor = copy.deepcopy(self.floor)
        origin = (self.security / 'operator-origin.json').read_bytes()
        bootstrap = {p.name: p.read_bytes() for p in self.security.iterdir()
                     if p.name == 'journal.json' or p.name.startswith('inventory-')}
        new_binary = self.base / 'new-helm'
        shutil.copyfile(self.binary, new_binary)
        # An ELF permits trailing bytes; its unchanged guard now sees distinct
        # actual executable bytes. No build or application/server invocation.
        with new_binary.open('ab') as stream:
            stream.write(b'\nsynthetic-upgrade-binding\n')
        new_binary.chmod(0o755)
        self.artifacts = dict(candidate=dict(image=BASE + 'e' * 64, sha='e' * 40,
                                             binary_sha256=sha(new_binary)),
                              recovery=copy.deepcopy(original_pair['candidate']))
        self.bundle = self.base / 'new-bundle.json'
        self.acceptance()
        self.refuse('upgrade-from-active', self.upgrade())
        new_pair, new_bundle = copy.deepcopy(self.artifacts), self.bundle
        self.artifacts, self.bundle = original_pair, original_bundle
        self.transition(self.plan('close'))
        old_activation = self.plan()
        self.artifacts, self.bundle = new_pair, new_bundle
        self.refuse('upgrade-old-format', self.plan('upgrade'))
        self.refuse('ordinary-pair-change', self.plan('close'))
        self.refuse('upgrade-stale-cas', self.upgrade(expected_generation=999))
        self.refuse('upgrade-wrong-checksum', self.upgrade(acceptance_sha256='f' * 64))
        changed = copy.deepcopy(new_pair)
        changed['recovery']['binary_sha256'] = 'f' * 64
        self.refuse('upgrade-wrong-recovery-binary', self.upgrade(artifacts=changed))
        changed = copy.deepcopy(new_pair)
        changed['recovery'] = original_pair['recovery']
        self.refuse('upgrade-wrong-recovery-image-source', self.upgrade(artifacts=changed))
        floor = copy.deepcopy(self.floor)
        floor['activation']['bundle_sha256'] = sha(self.bundle)
        self.refuse('upgrade-rewrites-original-floor', self.upgrade(authorization_floor=floor))
        self.refuse('upgrade-reused-nonce', self.upgrade(operation_nonce='a' * 64))
        proof = next(self.base.glob('new-bundle-recovery-*.json'))
        body = proof.read_bytes()
        proof.unlink()
        self.refuse('upgrade-missing-proof', self.upgrade())
        save(proof, json.loads(body))
        proof.write_bytes(body.replace(b'SYNTHETIC ONLY', b'TAMPERED'))
        self.refuse('upgrade-tampered-proof', self.upgrade())
        proof.write_bytes(body)
        bundle_body = self.bundle.read_bytes()
        expired = json.loads(bundle_body)
        expired['issued_at'], expired['expires_at'] = int(time.time()) - 90000, int(time.time()) - 1
        save(self.bundle, expired)
        self.refuse('upgrade-expired-proof', self.upgrade())
        self.bundle.write_bytes(bundle_body)
        import fcntl
        lock = os.open(self.security / 'runtime.lock', os.O_RDONLY)
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.refuse('upgrade-busy-lock', self.upgrade())
        finally:
            os.close(lock)
        import subprocess
        process = subprocess.Popen([sys.executable, '-c',
            'import sys,time; f=open(sys.argv[1],"rb"); print("ready",flush=True); time.sleep(60)',
            str(self.lane / 'config/settings')], stdout=subprocess.PIPE, text=True)
        try:
            assert process.stdout.readline().strip() == 'ready'
            self.refuse('upgrade-retained-config-descriptor', self.upgrade())
        finally:
            process.terminate()
            process.wait(timeout=10)
        pending = self.upgrade()
        rename = os.rename
        def interrupted(source, destination, **kwargs):
            if source == 'operator-marker.next':
                raise OSError('synthetic interrupted upgrade')
            return rename(source, destination, **kwargs)
        with patch('os.rename', interrupted):
            self.refuse('upgrade-interrupted-before-marker', pending)
        self.refuse('upgrade-pending-replay', pending)
        (self.security / 'operator-marker.next').unlink()
        self.refuse('upgrade-pending-replay-after-temp-review', pending)
        self.refuse('upgrade-pending-new-pair-close', self.plan('close'))
        self.artifacts, self.bundle = original_pair, original_bundle
        # Renew prior-pair acceptance before explicitly canceling its pending
        # upgrade. This is not an activation replay or new-pair close.
        self.acceptance()
        self.transition(self.plan('close'))
        self.artifacts, self.bundle = new_pair, new_bundle
        self.acceptance()
        committed = self.upgrade()
        from admission import operator_state
        switch = operator_state.switch
        def lost_response(directory, marker):
            switch(directory, marker)
            raise OSError('synthetic lost upgrade response')
        from admission.filesystem import BootstrapError
        try:
            with patch('admission.operator_state.switch', lost_response):
                self.transition(committed)
        except BootstrapError:
            pass
        else:
            raise AssertionError('lost response missing')
        assert self.transition(committed)['replay']
        with patch('rollback_policy.time.time', return_value=time.time() + 90000):
            assert self.transition(committed)['replay']
        self.events.append(dict(case='upgrade-lost-response', replay_read_only=True))
        self.refuse('old-pair-replay-after-upgrade', old_activation)
        upgrade_record = sorted(self.security.glob('operator-intent-*.json'))[-1]
        raw = upgrade_record.read_bytes()
        record = json.loads(raw)
        record['artifacts']['candidate']['binary_sha256'] = 'f' * 64
        save(upgrade_record, record)
        self.refuse('tampered-upgrade-history', self.plan())
        upgrade_record.write_bytes(raw)
        held_record = self.base / 'held-upgrade.json'
        upgrade_record.rename(held_record)
        self.refuse('missing-upgrade-history', self.plan())
        held_record.rename(upgrade_record)
        self.runtime(False)
        self.transition(self.plan())
        self.runtime(False)
        old_binary, self.binary = self.binary, new_binary
        self.runtime(True)
        self.transition(self.plan('close', 'recovery'))
        self.transition(self.plan(role='recovery'))
        self.runtime(False)
        self.binary = old_binary
        self.runtime(True)
        assert all(snapshot(self.lane / name) == before for name, before in self.before.items())
        assert (self.security / 'runtime.lock').stat().st_ino == self.lock_inode
        assert (self.security / 'operator-origin.json').read_bytes() == origin
        assert self.floor == original_floor
        assert bootstrap == {p.name: p.read_bytes() for p in self.security.iterdir() if p.name in bootstrap}
        self.events.append(dict(case='upgrade-preservation', data_config=True, lock=True,
                                original_floor_origin_bootstrap=True, synthetic_only=True))
        print(json.dumps(dict(scope='native synthetic upgrade E2E, NOT TC336 acceptance',
                              events=self.events), sort_keys=True))
        return 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.inside:
        plan_preflight(args.output)
        from admission import drain
        observe = drain.observe
        def diagnosed(path):
            result = observe(path)
            if result.get('status') != 'no_access_observed':
                print(json.dumps(dict(status=result.get('status'), reasons=result.get('reasons'),
                                      matches=len(result.get('matching_processes', [])))), file=sys.stderr)
            return result
        with patch('admission.drain.observe', diagnosed):
            raise SystemExit(Upgrade(args.output, args.binary.resolve(strict=True)).run())
    raise SystemExit(launch(Path(__file__), 'operator-upgrade', timeout=600,
                            arguments=('--binary', str(args.binary.resolve(strict=True)))))
