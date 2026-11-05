#!/usr/bin/env python3
"""Native new-context rebind over explicitly synthetic prior-boot history."""
import argparse
import copy
import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from unittest.mock import patch

from bootstrap_fixture import save, snapshot
from bootstrap_native import launch
from operator_native_e2e import BASE, TARGET, Journey, sha


def preflight(output):
    from admission.operator_plan import load
    path = output / 'host-rebind-preflight.json'
    save(path, dict(format=3, operation='native-admission-host-rebind', action='host-rebind',
        lane='beta', role='candidate', target=TARGET,
        artifacts={role: dict(image=BASE + letter * 64, sha=letter * 40,
                              binary_sha256=letter * 64)
                   for role, letter in [('candidate', 'b'), ('recovery', 'c')]},
        authorization_floor=dict(format=1, minimum=41, target=TARGET),
        bootstrap_plan=str(output / 'bootstrap.json'), acceptance_bundle=str(output / 'bundle.json'),
        acceptance_sha256='a' * 64, expected_marker_sha256='a' * 64,
        expected_generation=1, operation_nonce='a' * 64,
        expected_previous_host_context_sha256='b' * 64,
        expected_host_context_sha256='c' * 64, expected_history_sha256='d' * 64))
    assert load(path)['action'] == 'host-rebind'


class Rebind(Journey):
    def __init__(self, output, binary):
        from admission.filesystem import digest
        from admission import host_context
        # Only seed the immutable prior-boot fixture with a synthetic context.
        # Every rebind and post-rebind operation uses unpatched native verification.
        self.previous_context = digest(dict(format=1, boot_id='00000000-0000-0000-0000-000000000001',
            pid_namespace='pid:[4026531836]', mnt_namespace='mnt:[4026531841]'))
        with patch('admission.host_context.verify', return_value=self.previous_context):
            super().__init__(output, binary)
            self.transition(self.plan('close'))
            self.transition(self.plan())
        self.current_context = host_context.verify(self.base)
        assert self.current_context != self.previous_context
        self.original = {p.name: p.read_bytes() for p in self.security.iterdir() if p.is_file()}

    def rebind(self, **changes):
        names = sorted(self.security.glob('operator-intent-*.json'))
        tip = names[-1] if names else self.security / 'operator-origin.json'
        return self.plan('host-rebind', format=3, operation='native-admission-host-rebind',
            expected_previous_host_context_sha256=self.previous_context,
            expected_host_context_sha256=self.current_context,
            expected_history_sha256=sha(tip), **changes)

    def refuse_unchanged(self, label, plan):
        before = {p.name: p.read_bytes() for p in self.security.iterdir() if p.is_file()}
        self.refuse(label, plan)
        assert before == {p.name: p.read_bytes() for p in self.security.iterdir() if p.is_file()}

    def preservation(self):
        assert all((self.security / name).read_bytes() == body
                   for name, body in self.original.items() if name != 'marker.json')
        assert all(snapshot(self.lane / name) == before for name, before in self.before.items())
        assert (self.security / 'runtime.lock').stat().st_ino == self.lock_inode
        assert json.loads((self.security / 'operator-origin.json').read_bytes())['authorization_floor'] == self.floor

    def run(self):
        from admission.filesystem import BootstrapError
        from admission import operator_state
        self.refuse_unchanged('ordinary-close-changed-context', self.plan('close'))
        self.refuse_unchanged('ordinary-activate-changed-context', self.plan())
        for key in ('expected_previous_host_context_sha256', 'expected_host_context_sha256',
                    'expected_history_sha256'):
            value = json.loads(self.rebind().read_bytes())
            value[key] = 'f' * 64
            path = self.base / ('bad-' + key + '.json')
            save(path, value)
            self.refuse_unchanged(key, path)
        self.refuse_unchanged('rebind-stale-marker', self.rebind(expected_generation=999))
        self.refuse_unchanged('rebind-reused-nonce', self.rebind(operation_nonce='a' * 64))
        changed = copy.deepcopy(self.artifacts)
        changed['recovery']['binary_sha256'] = 'f' * 64
        self.refuse_unchanged('rebind-pair-change', self.rebind(artifacts=changed))
        self.refuse_unchanged('rebind-role-change', self.rebind(role='recovery'))
        floor = copy.deepcopy(self.floor)
        floor['activation']['bundle_sha256'] = 'f' * 64
        self.refuse_unchanged('rebind-floor-change', self.rebind(authorization_floor=floor))
        self.refuse_unchanged('rebind-wrong-acceptance', self.rebind(acceptance_sha256='f' * 64))
        body = self.bundle.read_bytes()
        expired = json.loads(body)
        expired['issued_at'], expired['expires_at'] = int(time.time()) - 90000, int(time.time()) - 1
        save(self.bundle, expired)
        self.refuse_unchanged('rebind-expired-proof', self.rebind())
        self.bundle.write_bytes(body)
        lock = os.open(self.security / 'runtime.lock', os.O_RDONLY)
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.refuse_unchanged('rebind-busy-lock', self.rebind())
        finally:
            os.close(lock)
        binding = self.base / 'operator/host.json'
        binding_body = binding.read_bytes()
        foreign = json.loads(binding_body)
        foreign['boot_id'] = '00000000-0000-0000-0000-000000000002'
        save(binding, foreign)
        self.refuse_unchanged('rebind-unverified-external-binding', self.rebind())
        binding.write_bytes(binding_body)
        for name in ('data', 'config'):
            held = self.lane / name / ('roadmap.db' if name == 'data' else 'settings')
            process = subprocess.Popen([sys.executable, '-c',
                'import sys,time; f=open(sys.argv[1],"rb"); print("ready",flush=True); time.sleep(60)', str(held)],
                stdout=subprocess.PIPE, text=True)
            try:
                assert process.stdout.readline().strip() == 'ready'
                self.refuse_unchanged('rebind-retained-' + name, self.rebind())
            finally:
                process.terminate()
                process.wait(timeout=10)
        committed = self.rebind()
        switch = operator_state.switch
        def lost_response(directory, marker):
            switch(directory, marker)
            raise OSError('synthetic lost response after rebind switch')
        try:
            with patch('admission.operator_state.switch', lost_response):
                self.transition(committed)
        except BootstrapError:
            pass
        else:
            raise AssertionError('missing post-commit interruption')
        assert self.transition(committed)['replay']
        with patch('rollback_policy.time.time', return_value=time.time() + 90000):
            assert self.transition(committed)['replay']
        self.runtime(False)
        self.preservation()
        self.events.append(dict(case='closed-rebind-lost-response', replay_read_only=True,
                                prior_boot_history='synthetic', destination_context='native-verified'))
        self.previous_context = self.current_context
        self.refuse_unchanged('same-context-rebind', self.rebind())
        self.transition(self.plan())
        self.runtime(True)
        self.refuse_unchanged('old-rebind-replay-after-activation', committed)
        latest = sorted(self.security.glob('operator-intent-*.json'))[-1]
        raw = latest.read_bytes()
        record = json.loads(raw)
        record['host_context_sha256'] = 'f' * 64
        save(latest, record)
        self.refuse('ordinary-history-context-jump', self.plan('close'))
        latest.write_bytes(raw)
        self.preservation()
        self.events.append(dict(case='preservation', origin_history_bytes=True, permanent_lock=True,
                                source_bytes_inodes_mtimes=True, original_floor=True))
        print(json.dumps(dict(scope='native-root synthetic prior-boot host rebind, NOT actual reboot or TC336 acceptance',
                              events=self.events), sort_keys=True))
        return 0


class PendingRebind(Rebind):
    def run(self):
        from admission.filesystem import BootstrapError
        pending = self.rebind()
        def interrupted(directory, marker):
            raise OSError('synthetic interruption before rebind marker temp')
        try:
            with patch('admission.operator_state.switch', interrupted):
                self.transition(pending)
        except BootstrapError:
            pass
        else:
            raise AssertionError('missing pre-switch interruption')
        self.refuse_unchanged('pending-rebind-replay', pending)
        self.refuse_unchanged('pending-rebind-activation', self.plan())
        self.transition(self.plan('close'))
        self.runtime(False)
        self.transition(self.plan())
        self.runtime(True)
        self.preservation()
        print(json.dumps(dict(scope='native-root synthetic pending rebind cancellation', events=self.events), sort_keys=True))
        return 0


class PendingUpgradeRebind(Rebind):
    def run(self):
        from admission.filesystem import BootstrapError
        original_pair, original_bundle = copy.deepcopy(self.artifacts), self.bundle
        with patch('admission.host_context.verify', return_value=self.previous_context):
            self.transition(self.plan('close'))
            self.artifacts = dict(candidate=dict(image=BASE + 'e' * 64, sha='e' * 40,
                                                 binary_sha256='e' * 64),
                                  recovery=copy.deepcopy(original_pair['candidate']))
            self.bundle = self.base / 'upgrade-bundle.json'
            self.acceptance()
            pending = self.plan('upgrade', format=2, operation='native-admission-upgrade')
            def interrupted(directory, marker):
                raise OSError('synthetic prior-boot pending upgrade')
            try:
                with patch('admission.operator_state.switch', interrupted):
                    self.transition(pending)
            except BootstrapError:
                pass
            else:
                raise AssertionError('missing pending upgrade')
        self.original = {p.name: p.read_bytes() for p in self.security.iterdir() if p.is_file()}
        self.refuse_unchanged('rebind-cannot-adopt-pending-upgrade-pair', self.rebind())
        self.artifacts, self.bundle = original_pair, original_bundle
        self.acceptance()
        self.transition(self.rebind())
        self.runtime(False)
        self.transition(self.plan())
        self.runtime(True)
        self.preservation()
        self.events.append(dict(case='rebind-cancels-pending-upgrade', current_marker_pair_preserved=True))
        print(json.dumps(dict(scope='native-root synthetic prior-boot pending upgrade', events=self.events), sort_keys=True))
        return 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.inside:
        from admission import drain
        observe = drain.observe
        def diagnosed_observe(root):
            result = observe(root)
            if result['status'] != 'no_access_observed':
                print(json.dumps(dict(scope='synthetic fixture observation diagnostic',
                    reasons=result['reasons'], matching_processes=result['matching_processes'])), flush=True)
            return result
        drain.observe = diagnosed_observe
        preflight(args.output)
        Rebind(args.output, args.binary.resolve(strict=True)).run()
        # The fixture helper has a fixed child name, so the second journey owns
        # an independent private parent inside this exclusively created fixture.
        pending_output = args.output / 'pending'
        pending_output.mkdir(mode=0o700)
        PendingRebind(pending_output, args.binary.resolve(strict=True)).run()
        upgrade_output = args.output / 'pending-upgrade'
        upgrade_output.mkdir(mode=0o700)
        raise SystemExit(PendingUpgradeRebind(upgrade_output, args.binary.resolve(strict=True)).run())
    raise SystemExit(launch(Path(__file__), 'operator-host-rebind-native', timeout=600,
                            arguments=('--binary', str(args.binary.resolve(strict=True)))))
