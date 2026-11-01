"""Failure-first parent recovery regressions; synthetic API ledger only."""
import copy
import json
from pathlib import Path
import sys
import tempfile

from fixture import CANDIDATE, TARGET, World, write_json


def activated(world):
    error, _ = world.run()
    assert error is None, error
    world.calls.clear()
    world.starts.clear()


def main(output):
    output.mkdir(parents=True, exist_ok=True)
    results = []
    scenarios = ['replay-floor-loss', 'unhealthy-floor-loss', 'startup-floor-drift',
                 'replay-floor-drift', 'same-recovery-artifact', 'same-image-different-source']
    for scenario in scenarios:
        with tempfile.TemporaryDirectory(prefix='helm-parent-policy-') as tmp:
            w = World(Path(tmp))
            w.short_wait = True
            try:
                if scenario.startswith('replay-'):
                    activated(w)
                if scenario.startswith('same-'):
                    item = w.bundle['artifacts']['recovery']
                    item['image'] = CANDIDATE['image']
                    if scenario == 'same-recovery-artifact':
                        item['sha'] = CANDIDATE['sha']
                    w.settings['HELM_RECOVERY_IMAGE'] = item['image']
                    w.settings['HELM_RECOVERY_SHA'] = item['sha']
                    for name, proof in item['proofs'].items():
                        path = w.root / proof['file']
                        value = json.loads(path.read_text())
                        value['subject'] = {k: item[k] for k in ['image', 'sha']}
                        proof['sha256'] = write_json(path, value)
                    w.seal()
                else:
                    original = w.request
                    observed = []
                    changed = False

                    def request(method, path, body=None, *, route=False):
                        nonlocal changed
                        # Change metadata only once verification has started.
                        # For unhealthy polling this is the first service GET
                        # after the candidate starts. Route reads remain bad.
                        if w.active == CANDIDATE and (
                                route or scenario == 'unhealthy-floor-loss' and path == '/services/' + TARGET['service_uuid']):
                            if not changed:
                                changed = True
                                if scenario.endswith('floor-loss'):
                                    w.envs.pop('HELM_AUTHORIZATION_FLOOR', None)
                                else:
                                    value = json.loads(w.envs['HELM_AUTHORIZATION_FLOOR'])
                                    value['activation']['bundle_sha256'] = 'd' * 64
                                    w.envs['HELM_AUTHORIZATION_FLOOR'] = json.dumps(value)
                            observed.append(path)
                        result = original(method, path, body, route=route)
                        if scenario == 'unhealthy-floor-loss' and method == 'GET' and path == '/services/' + TARGET['service_uuid']:
                            result['status'] = 'running:unhealthy'
                        return result

                    w.request = request
                error, evidence = w.run()
                assert error, 'unsafe transition accepted'
                if scenario.startswith('same-'):
                    assert not w.mutations(), 'invalid recovery pair mutated service'
                else:
                    assert w.active is None, 'invalid floor left a writer serving'
                    if scenario == 'unhealthy-floor-loss':
                        assert len(observed) <= 8, 'floor loss waited through readiness polling'
                record = {'scenario': scenario, 'result': 'passed', 'evidence': evidence,
                          'requests': w.calls, 'starts': w.starts}
            except Exception as exc:
                record = {'scenario': scenario, 'result': 'failed', 'error': str(exc)}
            raw = json.dumps(record, sort_keys=True, indent=2) + '\n'
            assert len(raw.encode()) <= 20_000 and 'fixture-secret' not in raw
            (output / (scenario + '.json')).write_text(raw)
            results.append({'scenario': scenario, 'result': record['result']})
            print(record['result'].upper(), scenario, record.get('error', ''))
    write_json(output / 'summary.json', results)
    return int(any(row['result'] == 'failed' for row in results))


if __name__ == '__main__':
    sys.exit(main(Path(sys.argv[1])))
