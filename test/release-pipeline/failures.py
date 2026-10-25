"""Failure-path command scenarios from RELEASE_PIPELINE_FAILURES.md."""
import json
from pathlib import Path
import tempfile

from fixture import World


def exercise(source, evidence):
    results = []

    def scenario(name, check):
        with tempfile.TemporaryDirectory(prefix='helm-release-failure-') as directory:
            world = World(Path(directory), source)
            try:
                check(world)
                results.append({'scenario': name, 'result': 'passed'})
            finally:
                world.close()

    def call(world, *args, success=True, contains=None):
        result = world.command(*args)
        assert (result.returncode == 0) == success, result.stdout + result.stderr
        if contains:
            assert contains in result.stdout + result.stderr, result.stdout + result.stderr
        assert 'synthetic-secret' not in result.stdout + result.stderr
        return result

    def partial_branch(world):
        call(world, 'prepare', 'v0.2.2')
        expected = world.ref('canonical', 'release/v0.2.2')
        # Model interruption between canonical and mirror publication.
        world.git('--git-dir', str(world.root / 'mirror'), 'update-ref', '-d', 'refs/heads/release/v0.2.2')
        world.prs.clear()
        call(world, 'prepare', 'v0.2.2')
        assert world.ref('mirror', 'release/v0.2.2') == expected
        assert len(world.prs) == 1

    def branch_collision(world):
        world.git('push', 'origin', 'main:refs/heads/release/v0.2.2')
        call(world, 'prepare', 'v0.2.2', success=False, contains='no bounded release manifest')
        assert not world.ref('canonical', 'release/v0.2.2')
        assert not world.prs

    def beta_drift(world):
        world.git('--git-dir', str(world.root / 'canonical'), 'update-ref', 'refs/heads/beta',
                  world.ref('canonical', 'main'))
        call(world, 'prepare', 'v0.2.2', success=False, contains='differs between Gitea and GitHub')
        assert not world.prs

    def oversized_notes(world):
        notes = world.root / 'large.md'
        notes.write_text('x' * 20_001)
        call(world, 'prepare', 'v0.2.2', '--notes', str(notes), success=False, contains='12,000')
        assert not world.ref('canonical', 'release/v0.2.2')

    def untested_main(world):
        world.git('switch', 'main')
        (world.checkout / 'main-only.txt').write_text('untested application change\n')
        world.git('add', '.')
        world.git('commit', '-m', 'Main only change')
        for remote in ['gitea', 'origin']:
            world.git('push', remote, 'main')
        world.production_ready = world.git('rev-parse', 'HEAD')
        call(world, 'prepare', 'v0.2.2', success=False, contains='absent from tested beta')
        assert not world.ref('canonical', 'release/v0.2.2')
        assert not world.prs

    def schema_review(world):
        (world.checkout / 'internal/db/migrations/002_next.sql').write_text('-- additive migration\n')
        world.git('add', '.')
        world.git('commit', '-m', 'New migration')
        for remote in ['gitea', 'origin']:
            world.git('push', remote, 'beta')
        world.beta_ready = world.git('rev-parse', 'HEAD')
        call(world, 'prepare', 'v0.2.2', contains='Schema review required')
        assert '002_next.sql' in world.prs[0]['body']
        assert '- [ ] Populated-data upgrade' in world.prs[0]['body']
        assert world.ref('mirror', 'main') != world.beta_ready

    def tag_collision(world):
        call(world, 'prepare', 'v0.2.2')
        merged = world.merge_release('v0.2.2')
        world.production_ready = merged
        old_main = world.ref('canonical', 'main')
        world.git('tag', 'v0.2.2', old_main)
        world.git('push', 'origin', 'v0.2.2')
        call(world, 'finish', 'v0.2.2', success=False, contains='refusing overwrite')
        assert world.ref('canonical', 'main') == old_main
        assert not world.gitea_releases and not world.github_releases

    def partial_release(world):
        call(world, 'prepare', 'v0.2.2')
        world.production_ready = world.merge_release('v0.2.2')
        call(world, 'finish', 'v0.2.2')
        world.github_releases.clear()
        call(world, 'finish', 'v0.2.2')
        assert len(world.github_releases) == len(world.gitea_releases) == 1
        world.gitea_releases[0]['body'] = 'Different approved notes'
        call(world, 'finish', 'v0.2.2', success=False, contains='different notes/status')

    def partial_beta(world):
        (world.checkout / 'app.txt').write_text('another committed beta change\n')
        world.git('add', '.')
        world.git('commit', '-m', 'Next beta')
        head = world.git('rev-parse', 'HEAD')
        world.git('push', 'gitea', 'beta')
        call(world, 'beta')
        assert world.ref('mirror', 'beta') == world.ref('canonical', 'beta') == head

    def changed_notes(world):
        notes = world.root / 'reviewed.md'
        notes.write_text('Reviewed synthetic feature.\n')
        call(world, 'prepare', 'v0.2.2', '--notes', str(notes))
        original = world.ref('mirror', 'release/v0.2.2')
        notes.write_text('Changed notes requiring another review.\n')
        call(world, 'prepare', 'v0.2.2', '--notes', str(notes), success=False, contains='notes differ')
        assert world.ref('mirror', 'release/v0.2.2') == original

    def malformed_ci(world):
        original = world.gh
        world.gh = lambda args, files: ({'output': '[]'} if args[0] == 'api' else original(args, files))
        call(world, 'prepare', 'v0.2.2', success=False, contains='unexpected response shape')
        assert not world.ref('mirror', 'release/v0.2.2')

    for name, check in [('partial branch publication', partial_branch), ('branch collision', branch_collision),
                        ('beta remote drift', beta_drift), ('oversized notes', oversized_notes),
                        ('untested main changes', untested_main), ('schema review is explicit', schema_review),
                        ('tag collision', tag_collision), ('partial release publication', partial_release),
                        ('partial beta publication', partial_beta), ('changed notes', changed_notes),
                        ('malformed CI response', malformed_ci)]:
        scenario(name, check)
    (evidence / 'failure-paths.json').write_text(json.dumps(results, indent=2) + '\n')
    print(f'{len(results)} release failure/recovery scenarios passed.')
