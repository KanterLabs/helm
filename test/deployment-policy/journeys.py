#!/usr/bin/env python3
"""Executable policy journeys. Each result binds actual synthetic API requests."""
import copy
import json
from pathlib import Path
import sys
import tempfile

from fixture import World, TARGET, OLD, CANDIDATE, RECOVERY, write_json


def rejection(world):
    error, evidence = world.run()
    assert error, 'unsafe deployment was accepted'
    assert not world.mutations(), 'preflight failure mutated service'
    return evidence


def accepted(world):
    was_stopped = world.status == 'exited'
    error, evidence = world.run()
    assert error is None, error
    assert world.active == CANDIDATE
    mutations = world.mutations()
    assert was_stopped or mutations[0]['path'].endswith('/stop?docker_cleanup=false')
    floor_patches = [i for i, c in enumerate(mutations) if c.get('key') == 'HELM_AUTHORIZATION_FLOOR']
    assert not floor_patches or floor_patches == [1]
    assert json.loads(world.envs['HELM_AUTHORIZATION_FLOOR'])['minimum'] == 41
    assert len(world.observations) >= 2
    return evidence


def recovery(world):
    world.bad_candidate = True
    error, evidence = world.run()
    assert error and 'synthetic readiness failure' in error
    assert world.active == RECOVERY
    assert world.starts == [CANDIDATE, RECOVERY]
    assert world.database == {'identity': TARGET['data_identity'], 'schema': 41,
                              'writes': ['retained', 'candidate-write']}
    assert evidence['status'] == 'failed-recovery-verified'
    return evidence


def run_all(output):
    output.mkdir(parents=True, exist_ok=True)
    results = []

    def case(name, setup, check=rejection):
        with tempfile.TemporaryDirectory(prefix='helm-policy-') as temporary:
            world = World(Path(temporary))
            try:
                setup(world)
                evidence = check(world)
                result = {'scenario': name, 'result': 'passed', 'evidence': evidence,
                          'requests': world.calls, 'observations': world.observations,
                          'database': world.database, 'starts': world.starts}
            except Exception as exc:
                result = {'scenario': name, 'result': 'failed', 'error': str(exc),
                          'requests': world.calls, 'starts': world.starts}
            # Synthetic values only. One receipt per journey stays bounded.
            raw = json.dumps(result, sort_keys=True, indent=2) + '\n'
            assert 'fixture-secret' not in raw
            if len(raw.encode()) > 20_000:
                result.pop('observations', None)
                raw = json.dumps(result, sort_keys=True, indent=2) + '\n'
            assert len(raw.encode()) <= 20_000
            (output / (name + '.json')).write_text(raw)
            results.append({'scenario': name, 'result': result['result']})
            print(result['result'].upper(), name, result.get('error', ''))

    case('missing-proof', lambda w: w.settings.pop('HELM_ACCEPTANCE_BUNDLE'))
    case('wrong-root-checksum', lambda w: w.settings.update(HELM_ACCEPTANCE_SHA256='0' * 64))
    case('direct-legacy-target', lambda w: w.settings.update(HELM_IMAGE=OLD['image'], HELM_RELEASE_SHA=OLD['sha']))
    for role in ['candidate', 'recovery']:
        for key in ['image', 'sha']:
            def mismatch(w, role=role, key=key):
                w.bundle['artifacts'][role][key] = OLD[key]
                w.seal()
            case(role + '-' + key + '-mismatch', mismatch)
    for fault, mutate in [
        ('unknown-capability', lambda b: b['artifacts']['candidate']['capabilities'].append('unknown')),
        ('missing-capability', lambda b: b['artifacts']['recovery']['capabilities'].pop()),
        ('lane-mismatch', lambda b: b['target'].update(lane='production')),
        ('stale-proof', lambda b: b.update(issued_at=1, expires_at=2)),
        ('future-proof', lambda b: b.update(issued_at=9999999999, expires_at=99999999999)),
        ('missing-runtime-proof', lambda b: b['artifacts']['candidate']['proofs'].pop('zero-grant-human')),
        ('proof-path-traversal', lambda b: b['artifacts']['candidate']['proofs']['zero-grant-human'].update(file='../escape')),
    ]:
        def setup(w, mutate=mutate):
            mutate(w.bundle)
            w.seal()
        case(fault, setup)
    case('malformed-json', lambda w: (w.root / 'bundle.json').write_text('{'))
    case('proof-checksum-mismatch', lambda w: (w.root / 'candidate-zero-grant-human.json').write_text('{}'))
    case('missing-floor', lambda w: w.envs.pop('HELM_AUTHORIZATION_FLOOR'))
    case('unknown-higher-floor', lambda w: w.envs.update(HELM_AUTHORIZATION_FLOOR=json.dumps(
        {'format': 1, 'minimum': 42, 'target': TARGET})))
    case('valid-first-activation', lambda w: None, accepted)
    case('candidate-failure-preserves-data', lambda w: None, recovery)

    for key in ['HELM_AUTHORIZATION_FLOOR', 'HELM_IMAGE', 'HELM_RELEASE_SHA']:
        for lost in [False, True]:
            def setup(w, key=key, lost=lost):
                w.fail, w.lost_response = key, lost

            def check(w, key=key):
                error, evidence = w.run()
                assert error, 'failure masked'
                assert OLD not in w.starts
                if key == 'HELM_AUTHORIZATION_FLOOR':
                    assert not w.starts, 'uncertain floor started a process'
                else:
                    assert w.active == RECOVERY
                return evidence
            case(key.lower() + ('-lost' if lost else '-failure'), setup, check)

    def replay(w):
        accepted(w)
        floor = w.envs['HELM_AUTHORIZATION_FLOOR']
        w.calls.clear()
        error, evidence = w.run()
        assert error is None and evidence['status'] == 'already-current'
        assert not w.mutations()
        assert floor == w.envs['HELM_AUTHORIZATION_FLOOR']
        return evidence
    case('idempotent-replay', lambda w: None, replay)

    def interrupted(w):
        accepted(w)
        w.active = None
        w.status = 'exited'
        w.envs['HELM_IMAGE'] = OLD['image']  # interrupted/mixed coordinates
        w.calls.clear()
        return accepted(w)
    case('interrupted-activation', lambda w: None, interrupted)

    def failed_recovery(w):
        w.bad_candidate = w.bad_recovery = True
        error, evidence = w.run()
        assert error and evidence['status'] == 'failed-recovery-failed'
        assert OLD not in w.starts and w.active is None
        assert json.loads(w.envs['HELM_AUTHORIZATION_FLOOR'])['minimum'] == 41
        return evidence
    case('recovery-fails-closed', lambda w: None, failed_recovery)

    def barrier(w):
        error, evidence = w.run()
        assert error
        assert not w.starts
        assert not any(c.get('key') == 'HELM_AUTHORIZATION_FLOOR' for c in w.mutations())
        return evidence
    for name, fault in [
        ('stale-witness', lambda r: r.update(observed_at=1)),
        ('nonce-mismatch', lambda r: r.update(nonce='wrong')),
        ('witness-lane-mismatch', lambda r: r['target'].update(lane='production')),
        ('surviving-writer', lambda r: r['running_writers'].append('old')),
        ('restart-eligible', lambda r: r['restart_eligible'].append('old')),
        ('queued-operation', lambda r: r['queued_operations'].append('start')),
        ('unknown-witness', lambda r: r.update(result='unknown')),
        ('missing-fence', lambda r: r.update(fence='')),
    ]:
        case(name, lambda w, fault=fault: setattr(w, 'witness_fault', fault), barrier)
    def lost_fence(w):
        def fault(r):
            if len(w.observations) > 1:
                r['fence'] = 'changed'
        w.witness_fault = fault
        error, evidence = w.run()
        assert error and not w.starts
        return evidence
    case('lost-continuing-fence', lambda w: None, lost_fence)
    write_json(output / 'summary.json', results)
    return 1 if any(r['result'] != 'passed' for r in results) else 0


if __name__ == '__main__':
    sys.exit(run_all(Path(sys.argv[1])))
