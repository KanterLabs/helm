"""`focus review` against an older server: TC-381 case 20 (CLI.md failure 1, 2).

The version 1 CLI exactly as shipped at main 1c54493 and the candidate CLI run
against the retained pre-signal binary (--rollback-binary) on one database;
stdout, stderr and exit codes must match byte for byte, wall-clock
`evaluated_at` aside.
"""
import json
from pathlib import Path
import re
import subprocess

from cases_cli import focus_fixture, legacy, pre_signal, run_cli
from contract import Checks
from harness import CLI, ROOT, Server, World

V1_SHA = '1c54493'
_EVALUATED = re.compile(r'("evaluated_at":\s*)"[^"]*"')  # wall-clock: the only allowed difference


def v1_cli(workdir):
    """The version 1 CLI exactly as shipped at main 1c54493."""
    target = Path(workdir) / f'v1-{V1_SHA}'
    if not target.exists():
        target.mkdir()
        archive = subprocess.run(['git', 'archive', V1_SHA, 'skills/helm/scripts'], cwd=ROOT,
                                 capture_output=True, check=True).stdout
        subprocess.run(['tar', '-x', '-C', str(target)], input=archive, check=True)
    return target / 'skills/helm/scripts/helm.py'


def _difference(old, new, path='$'):
    """The first differing JSON path (never values: receipts stay sanitized)."""
    if isinstance(old, (list, tuple)) and isinstance(new, (list, tuple)) and len(old) == len(new):
        return next((_difference(a, b, f'{path}[{i}]') for i, (a, b) in enumerate(zip(old, new)) if a != b), None)
    if isinstance(old, str) and isinstance(new, str) and old.startswith('{') and new.startswith('{'):
        return _difference(json.loads(old), json.loads(new), path)
    if isinstance(old, dict) and isinstance(new, dict):
        if list(old) != list(new):
            return f'{path} keys {sorted(set(old) ^ set(new)) or "order"}'
        return next(_difference(old[k], new[k], f'{path}.{k}') for k in old if old[k] != new[k])
    return path


def case_20_cli_older_server_unchanged(ctx, receipt):
    server = Server(ctx.rollback_binary, ctx.workdir, name='cli-older')
    server.start()
    try:
        w = World(server, ctx.clock, key='OLD')
        cards = {'A': w.old_task('Synthetic inactive card', 30),
                 'B': legacy(w, w.task('Synthetic legacy card'), 61, 60),
                 'E': w.task('Synthetic fresh card'), 'F': w.old_task('Synthetic twelve day card', 12)}
        focus, members = focus_fixture(w)
        cards.update(members)
        old_cli = v1_cli(ctx.workdir)
        compared = {}
        for name, argv in (('default', ()), ('ten_days', ('--inactive-days', '10')),
                           ('history_limit', ('--history-limit', '1')), ('request_limit', ('--request-limit', '3')),
                           ('focus', ('--focus', focus['id'])), ('timeline_limit', ('--timeline-limit', '1')),
                           ('beyond_server_max', ('--inactive-days', '5000'))):
            outputs = {}
            for label, cli in (('v1', old_cli), ('candidate', CLI)):
                parsed, raw = run_cli(server, w.token, ctx.workdir, '--project', 'OLD', *argv, cli=cli)
                runs = [raw]
                if parsed.get('next_cursor') and name == 'history_limit':
                    runs.append(run_cli(server, w.token, ctx.workdir, '--project', 'OLD', *argv,
                                        '--cursor', parsed['next_cursor'], cli=cli)[1])
                outputs[label] = [(r.returncode, _EVALUATED.sub(r'\1"*"', r.stdout), r.stderr)
                                  for r in runs]
            compared[name] = outputs
        probe = pre_signal(w, cards['A'])
    finally:
        server.stop()
    receipt['fixture'] = {'server': 'retained binary', 'binary_is_candidate': ctx.rollback_binary == ctx.binary,
                          'v1_cli': f'git archive {V1_SHA} skills/helm/scripts', 'modes': sorted(compared)}
    checks = Checks(receipt)
    checks.check('retained_binary_is_pre_signal', lambda: probe)

    def unchanged(name):
        old, new = compared[name]['v1'], compared[name]['candidate']
        assert old == new, f'{name}: candidate CLI output differs from v1 at {_difference(old, new)}'
        assert '"activity_signal"' not in ''.join(o[1] for o in new)
        return {'bytes': len(new[0][1]), 'invocations': len(new)}
    for name in compared:
        checks.check(f'byte_identical_{name}', lambda name=name: unchanged(name))
    checks.finish()


CASES = {20: case_20_cli_older_server_unchanged}
