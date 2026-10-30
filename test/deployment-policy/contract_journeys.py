#!/usr/bin/env python3
"""Additional controller journeys for receipt bytes and the real helper pipe.

These contracts are run red before strengthening their implementation. This
uses no server or remote transport; the pinned executable is a temporary fake.
"""
import hashlib
import json
from pathlib import Path
import sys
import tempfile

from fixture import World, TARGET, CANDIDATE, RECOVERY, write_json


def main(output):
    output.mkdir(parents=True, exist_ok=True)
    results = []
    cases = ['proof-response-mismatch', 'proof-no-exchange', 'wrong-container-identity',
             'changed-container-identity', 'missing-observer', 'unpinned-observer',
             'duplicate-json', 'symlink-proof', 'stopped-service-replay', 'real-helper-pipe',
             'floor-disappears-after-start']
    for scenario in cases:
        with tempfile.TemporaryDirectory(prefix='helm-contract-') as temporary:
            root = Path(temporary)
            w = World(root)
            expected = scenario in ['stopped-service-replay', 'real-helper-pipe']
            if scenario.startswith('proof-'):
                path = root / 'candidate-zero-grant-human.json'
                value = json.loads(path.read_text())
                if scenario == 'proof-response-mismatch':
                    value['observations'][0]['exchange'] = {'method': 'GET', 'path': '/api/v1/projects',
                        'status': 200, 'expected_status': 403, 'response': {'authorized': True},
                        'expected_response': {'authorized': False}}
                else:
                    value['observations'][0].pop('exchange', None)
                w.bundle['artifacts']['candidate']['proofs']['zero-grant-human']['sha256'] = write_json(path, value)
                w.seal()
            elif scenario == 'wrong-container-identity':
                w.witness_fault = lambda r: r['expected'].update(container_name='unrelated')
            elif scenario == 'changed-container-identity':
                def fault(r):
                    if len(w.observations) > 1:
                        r['container_ids'] = ['different-container']
                w.witness_fault = fault
            elif scenario == 'missing-observer':
                w.settings.pop('HELM_STOP_OBSERVER')
            elif scenario == 'unpinned-observer':
                w.helper.write_text('#!/bin/sh\nexit 0\n')
            elif scenario == 'duplicate-json':
                path = root / 'bundle.json'
                path.write_text(path.read_text().replace('"format": 1', '"format": 1, "format": 1', 1))
                w.settings['HELM_ACCEPTANCE_SHA256'] = hashlib.sha256(path.read_bytes()).hexdigest()
            elif scenario == 'symlink-proof':
                path = root / 'candidate-zero-grant-human.json'
                held = root / 'held.json'
                path.rename(held)
                path.symlink_to(held)
            elif scenario == 'stopped-service-replay':
                w.active, w.status = None, 'exited'
                original = w.request
                def request(method, path, body=None, *, route=False):
                    if method == 'POST' and '/stop?' in path:
                        raise RuntimeError('official API returns 400 when already stopped')
                    return original(method, path, body, route=route)
                w.request = request
            elif scenario == 'floor-disappears-after-start':
                w.short_wait = True
                original = w.request
                def request(method, path, body=None, *, route=False):
                    if route:
                        w.envs.pop('HELM_AUTHORIZATION_FLOOR', None)
                    return original(method, path, body, route=route)
                w.request = request
            elif scenario == 'real-helper-pipe':
                w.real_observer = True
                capture = root / 'observer-requests.jsonl'
                w.helper.write_text(f'''#!{sys.executable}
import json,os,sys,time
r=json.load(sys.stdin)
assert 'COOLIFY_API_TOKEN' not in os.environ
with open({str(capture)!r},'a') as f: f.write(json.dumps(r)+'\\n')
now=int(time.time())
r.update(format=1,observed_at=now,expires_at=now+60,result='stopped-and-fenced',
 running_writers=[],restart_eligible=[],queued_operations=[],fence='fixture-fence',
 container_ids=['fixture-container'])
print(json.dumps(r))
''')
                w.settings['HELM_STOP_OBSERVER_SHA256'] = hashlib.sha256(w.helper.read_bytes()).hexdigest()
            try:
                error, evidence = w.run()
                assert (error is None) == expected, error or 'unsafe contract accepted'
                if not expected:
                    if scenario == 'floor-disappears-after-start':
                        assert w.active is None, 'uncertain policy left a writer serving'
                        assert w.starts == [CANDIDATE]
                    else:
                        assert not w.starts
                else:
                    assert w.active == CANDIDATE
                if scenario == 'real-helper-pipe':
                    requests = [json.loads(line) for line in capture.read_text().splitlines()]
                    assert len(requests) >= 2
                    assert len({r['nonce'] for r in requests}) == len(requests)
                record = {'scenario': scenario, 'result': 'passed', 'evidence': evidence,
                          'requests': w.calls, 'starts': w.starts}
            except Exception as exc:
                record = {'scenario': scenario, 'result': 'failed', 'error': str(exc)}
            raw = json.dumps(record, sort_keys=True, indent=2) + '\n'
            assert len(raw.encode()) < 20_000 and 'fixture-secret' not in raw
            (output / ('contract-' + scenario + '.json')).write_text(raw)
            results.append({'scenario': scenario, 'result': record['result']})
            print(record['result'].upper(), scenario, record.get('error', ''))
    write_json(output / 'contract-summary.json', results)
    return int(any(r['result'] == 'failed' for r in results))


if __name__ == '__main__':
    sys.exit(main(Path(sys.argv[1])))
