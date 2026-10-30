#!/usr/bin/env python3
"""Release policy gates, written before release implementation changes."""
import copy
import json
from pathlib import Path
import sys
from unittest.mock import patch

from fixture import CANDIDATE, RECOVERY, TARGET
from release_support import Runtime, ReleaseError


def provenance(lane='beta'):
    return {'format': 1, 'minimum': 41, 'lane': lane, 'target': {**TARGET, 'lane': lane},
            'bundle_sha256': 'd' * 64, 'candidate': CANDIDATE, 'recovery': RECOVERY,
            'stop_observer_sha256': 'e' * 64, 'floor_sha256': 'f' * 64}


def main(output):
    output.mkdir(parents=True, exist_ok=True)
    base = {'revision': CANDIDATE['sha'], 'health_revision': CANDIDATE['sha'],
            'ready_revision': CANDIDATE['sha'], 'ready': True, 'status': 'deployed',
            'image': CANDIDATE['image'], 'service_uuid': TARGET['service_uuid'],
            'authorization': provenance()}
    cases = [('accepted', lambda e: None, True),
             ('missing-policy', lambda e: e.pop('authorization'), False),
             ('wrong-lane', lambda e: e['authorization'].update(lane='production'), False),
             ('wrong-digest', lambda e: e['authorization']['candidate'].update(image=RECOVERY['image']), False),
             ('same-recovery-image', lambda e: e['authorization']['recovery'].update(image=CANDIDATE['image']), False),
             ('same-recovery-source', lambda e: e['authorization']['recovery'].update(sha=CANDIDATE['sha']), False),
             ('lower-floor', lambda e: e['authorization'].update(minimum=0), False),
             ('invalid-bundle-hash', lambda e: e['authorization'].update(bundle_sha256='invalid'), False)]
    results = []
    for name, mutate, expected in cases:
        evidence = copy.deepcopy(base)
        mutate(evidence)
        runtime = Runtime.__new__(Runtime)
        runtime.api = lambda path: ({'workflow_runs': [{'head_sha': CANDIDATE['sha'],
            'head_branch': 'beta', 'event': 'push', 'status': 'completed',
            'conclusion': 'success', 'id': 1, 'html_url': 'https://example.test/run'}]}
            if '/workflows/' in path else {'jobs': [{'name': n, 'conclusion': 'success'}
            for n in ['checks', 'browser', 'race', 'container', 'beta_deploy']]})
        runtime.ready = lambda *args: None

        def gh(*args):
            directory = Path(args[args.index('--dir') + 1])
            (directory / 'deployment.json').write_text(json.dumps(evidence))
        runtime.gh = gh
        try:
            result = runtime.deployed('beta', CANDIDATE['sha'])
            actual = True
            retained = result.get('authorization') == evidence.get('authorization')
        except ReleaseError:
            actual = False
            retained = False
        passed = actual == expected and (not expected or retained)
        results.append({'scenario': name, 'result': 'passed' if passed else 'failed',
                        'accepted': actual})
        print(results[-1]['result'].upper(), name)
    (output / 'release-summary.json').write_text(json.dumps(results, indent=2) + '\n')
    return int(any(r['result'] == 'failed' for r in results))


if __name__ == '__main__':
    sys.exit(main(Path(sys.argv[1])))
