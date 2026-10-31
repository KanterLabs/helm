#!/usr/bin/env python3
"""Real command/Git journeys for prepare/finish authorization provenance."""
import json
from pathlib import Path
import sys
import tempfile

from fixture import World
from policy_fixture import authorization

SOURCE = Path(__file__).resolve().parents[2]


def main(output):
    output.mkdir(parents=True, exist_ok=True)
    results = []
    for scenario in ['retained-provenance', 'missing-beta-policy', 'missing-production-policy',
                     'changed-beta-acceptance-replay']:
        with tempfile.TemporaryDirectory(prefix='release-policy-') as temporary:
            world = World(Path(temporary), SOURCE)
            original = world.gh
            changed = False

            def gh(args, files):
                result = original(args, files)
                if args[:2] == ['run', 'download']:
                    artifact = result['artifact']
                    lane = 'beta' if args[2] == '1' else 'production'
                    artifact['service_uuid'] = 'fixture-' + lane
                    artifact['authorization'] = authorization(lane, artifact['revision'], artifact['image'])
                    if changed and lane == 'beta':
                        artifact['authorization']['bundle_sha256'] = '9' * 64
                    if scenario == 'missing-' + lane + '-policy':
                        artifact.pop('authorization')
                return result
            world.gh = gh
            try:
                prepared = world.command('prepare', 'v0.2.2')
                if scenario == 'missing-beta-policy':
                    assert prepared.returncode != 0
                    assert not world.prs
                else:
                    assert prepared.returncode == 0, prepared.stderr
                    head = world.ref('mirror', 'release/v0.2.2')
                    manifest = json.loads(world.git('show', head + ':.helm-release.json'))
                    assert manifest['beta_authorization']['bundle_sha256'] == 'd' * 64
                    if scenario == 'changed-beta-acceptance-replay':
                        changed = True
                        replayed = world.command('prepare', 'v0.2.2')
                        assert replayed.returncode != 0
                        assert world.ref('mirror', 'release/v0.2.2') == head
                    else:
                        world.production_ready = world.merge_release('v0.2.2')
                        finished = world.command('finish', 'v0.2.2')
                        if scenario == 'missing-production-policy':
                            assert finished.returncode != 0
                            assert not world.github_releases and not world.gitea_releases
                        else:
                            assert finished.returncode == 0, finished.stderr
                            assert 'Authorization acceptance' in world.github_releases[0]['body']
                            assert 'Recovery image' in world.github_releases[0]['body']
                result = {'scenario': scenario, 'result': 'passed', 'calls': world.calls}
            except Exception as exc:
                result = {'scenario': scenario, 'result': 'failed', 'error': str(exc)}
            finally:
                world.close()
            results.append(result)
            print(result['result'].upper(), scenario, result.get('error', ''))
    (output / 'release-commands.json').write_text(json.dumps(results, indent=2) + '\n')
    return int(any(r['result'] == 'failed' for r in results))


if __name__ == '__main__':
    sys.exit(main(Path(sys.argv[1])))
