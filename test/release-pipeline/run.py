#!/usr/bin/env python3
"""Exercise release commands against real temporary Git repos and HTTP fixtures."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from fixture import World
from failures import exercise

SOURCE = Path(__file__).resolve().parents[2]


def main():
    evidence = Path(sys.argv[1] if len(sys.argv) > 1 else SOURCE / '.artifacts/release-pipeline')
    evidence.mkdir(parents=True, exist_ok=False)
    outcomes = []
    with tempfile.TemporaryDirectory(prefix='helm-release-test-') as directory:
        world = World(Path(directory), SOURCE)
        try:
            def command(*args, ok=True):
                result = world.command(*args)
                outcomes.append({'args': args, 'code': result.returncode, 'output': result.stdout})
                assert (result.returncode == 0) == ok, result.stdout + result.stderr
                assert 'synthetic-secret' not in result.stdout + result.stderr
                return result

            initial_main = world.ref('mirror', 'main')
            initial_head = world.git('rev-parse', 'HEAD')
            (world.checkout / 'local-draft.txt').write_text('Keep this local draft.\n')
            command('prepare', 'v0.2.2', '--dry-run')
            assert not world.branches('mirror').get('release/v0.2.2')
            assert not world.prs

            command('prepare', 'v0.2.1', ok=False)
            world.ci_status = 'in_progress'
            command('prepare', 'v0.2.2', ok=False)
            assert not world.prs
            world.ci_status = 'completed'
            world.beta_ready = initial_main
            command('prepare', 'v0.2.2', ok=False)
            world.beta_ready = world.ref('mirror', 'beta')
            command('prepare', 'v0.2.2')
            prepared = world.ref('mirror', 'release/v0.2.2')
            assert prepared == world.ref('canonical', 'release/v0.2.2')
            assert world.ref('mirror', 'main') == initial_main
            assert world.ref('canonical', 'main') == initial_main
            assert world.git('rev-parse', 'HEAD') == initial_head
            assert (world.checkout / 'local-draft.txt').read_text() == 'Keep this local draft.\n'
            assert len(world.prs) == 1
            assert not world.ref('mirror', 'v0.2.2', tag=True)
            manifest = json.loads(world.git('show', prepared + ':.helm-release.json'))
            assert manifest['version'] == 'v0.2.2'
            assert manifest['beta_sha'] == world.beta_ready
            assert world.git('show', prepared + ':VERSION') == '0.2.2'
            assert 'Fresh verified production backup' in world.prs[0]['body']
            command('prepare', 'v0.2.2')
            assert world.ref('mirror', 'release/v0.2.2') == prepared
            assert len(world.prs) == 1
            command('finish', 'v0.2.2', ok=False)

            merged = world.merge_release('v0.2.2')
            command('finish', 'v0.2.2', ok=False)
            assert not world.ref('mirror', 'v0.2.2', tag=True)
            world.production_ready = merged
            command('finish', 'v0.2.2')
            assert world.ref('canonical', 'main') == merged
            assert world.ref('mirror', 'v0.2.2', tag=True) == merged
            assert world.ref('canonical', 'v0.2.2', tag=True) == merged
            assert len(world.github_releases) == len(world.gitea_releases) == 1
            command('finish', 'v0.2.2')
            assert len(world.github_releases) == len(world.gitea_releases) == 1
            command('beta', ok=False)  # Never publish a dirty checkout.
            assert world.ref('mirror', 'beta') == world.beta_ready
            report = {'result': 'passed', 'commands': outcomes, 'manifest': manifest,
                      'main': merged, 'release': world.ref('mirror', 'v0.2.2', tag=True),
                      'caller_head_unchanged': world.git('rev-parse', 'HEAD') == initial_head,
                      'caller_draft_preserved': (world.checkout / 'local-draft.txt').exists(),
                      'pull_requests': len(world.prs), 'calls': world.calls}
            output = json.dumps(report, indent=2) + '\n'
            assert len(output.encode()) <= 20_000
            (evidence / 'result.json').write_text(output)
            print('Release command flow passed; evidence:', evidence / 'result.json')
        finally:
            world.close()
    exercise(SOURCE, evidence)


if __name__ == '__main__':
    main()
