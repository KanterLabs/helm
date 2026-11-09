"""Concurrency, parity and older-server flows: plan cases 9, 15 and 17.

Case 17 runs the CLI against the retained (pre-signal) binary. Cases 8 and 16
(projector seed and scale) live in cases_projector.py.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import time

from contract import Checks, FeatureAbsent, activity, ids, neglected, task_activity
from harness import CLI, ROOT, Server, World, parse
import corpus_run

sys.path.insert(0, str(CLI.parent))
import helm_focus_activity as v1  # noqa: E402

GO_PARITY_TEST = ROOT / 'internal/store/task_activity_signal_parity_test.go'
GO_RACE_TEST = ('internal/store/task_activity_signal_projector_tail_test.go',
                'TestTaskActivitySignalTailProcessesEventsCommittedDuringRebuild')


def case_09_concurrent_writes(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    cards = [w.old_task(f'Synthetic concurrent card {n}', 30) for n in range(6)]
    acknowledged, violations, errors, restarts, stop = {}, [], [], [], threading.Event()

    def writer(task):
        try:
            for _ in range(3):
                w.comment(task, 'Synthetic concurrent comment')
                acknowledged.setdefault(task['id'], time.monotonic())
        except Exception as exc:  # recorded and re-raised as a harness error below
            errors.append(repr(exc))

    def reader():
        while not stop.is_set():
            started = time.monotonic()
            try:
                status, body = w.s.call('GET', f'/projects/{w.key}/tasks?inactive_days=14&limit=200')
                if status == 409 and body['error']['code'] == 'task_collection_changed':
                    restarts.append(1)  # documented concurrent-read restart; retry
                    continue
                if status != 200:
                    raise RuntimeError(f'reader listing returned {status}')
                if 'activity_coverage' not in body:
                    raise FeatureAbsent('collection ignored neglect inputs; missing activity_coverage')
            except FeatureAbsent as exc:
                violations.append(('feature_absent', str(exc)))
                return
            except Exception as exc:  # surfaced as a harness error after join
                errors.append(repr(exc))
                return
            for task in body['data']:
                acked = acknowledged.get(task['id'])
                if acked is not None and acked < started and activity(task)['status'] != 'pending':
                    violations.append(('acknowledged_comment_listed_inactive', task['id']))
    threads = [threading.Thread(target=writer, args=(c,)) for c in cards] + [threading.Thread(target=reader)]
    for thread in threads:
        thread.start()
    for thread in threads[:-1]:
        thread.join()
    stop.set()
    threads[-1].join()
    if errors:
        raise RuntimeError(f'concurrent writer failed: {errors[0]}')
    receipt['fixture'] = {'cards': len(cards), 'comments_each': 3, 'history_at': 'T0-30d',
                          'collection_changed_restarts': len(restarts),
                          'deterministic_window': ':'.join(GO_RACE_TEST)}
    checks = Checks(receipt)

    def go_race_window_test_present():
        path, name = GO_RACE_TEST
        assert ('func ' + name + '(') in (ROOT / path).read_text(), 'Go race-window test missing'
        return 'present; enforced by go test ./internal/store'
    checks.check('store_go_race_window_test_present', go_race_window_test_present)

    def reader_saw_no_stale_inactive():
        absent = [v for v in violations if v[0] == 'feature_absent']
        if absent:
            raise FeatureAbsent(absent[0][1])
        assert not violations, violations[:5]
        return True

    def all_current_after_quiescence():
        tasks, _ = neglected(w.s, w.key, 14)
        stale = set(ids(tasks)) & {c['id'] for c in cards}
        assert not stale, sorted(stale)
        for card in cards:
            assert parse(task_activity(w, card)['not_after']) >= clock.t0
        return True
    checks.check('no_acknowledged_write_listed_inactive', reader_saw_no_stale_inactive)
    checks.check('every_card_current_after_quiescence', all_current_after_quiescence)
    checks.finish()


def case_15_parity(ctx, receipt):
    corpus_receipt = Path(ctx.output) / 'corpus-v1.json'
    with open(os.devnull, 'w') as sink:
        stdout, sys.stdout = sys.stdout, sink
        try:
            status = corpus_run.main(['--receipt', str(corpus_receipt)])
        finally:
            sys.stdout = stdout
    summary = json.loads(corpus_receipt.read_text())
    receipt['fixture'] = {'fixtures': summary['fixtures'], 'mapping': summary['mapping'],
                          'go_parity_test': str(GO_PARITY_TEST.relative_to(ROOT))}
    checks = Checks(receipt)

    def python_half():
        assert status == 0, summary['violations']
        return summary['status']

    def go_half():
        if not GO_PARITY_TEST.exists():
            raise FeatureAbsent('Go v2 classifier parity test is absent (TC-378)')
        return 'present; enforced by go test ./internal/store'
    checks.check('python_v1_reference_and_contract', python_half)
    checks.check('go_v2_parity_test_present', go_half)
    checks.finish()


def case_17_older_server(ctx, receipt):
    server = Server(ctx.rollback_binary, ctx.workdir, name='older')
    server.start()
    try:
        w = World(server, ctx.clock, key='OLD')
        task = w.task('Synthetic older-server card')
        comment = w.comment(task, 'Synthetic human evidence')
        w.backdate(task, ctx.clock.ago(30))
        with server.db() as db:
            db.execute('UPDATE comments SET created_at=?, updated_at=? WHERE id=?',
                       (ctx.clock.ago(20), ctx.clock.ago(20), comment['id']))
        config = Path(ctx.workdir) / 'older-credentials.json'
        config.write_text(json.dumps({'base_url': server.url, 'token': w.token}))
        config.chmod(0o600)
        env = dict(server.env(), HELM_CONFIG=str(config), HELM_SESSION_DIR=str(Path(ctx.workdir) / 'sessions'))
        result = subprocess.run([sys.executable, str(CLI), 'focus', 'review', '--project', 'OLD'],
                                env=env, cwd=ctx.workdir, text=True, capture_output=True, timeout=60)
        if result.returncode != 0:
            raise RuntimeError(f'focus review failed: {result.stderr[:300]}')
        review = json.loads(result.stdout)
        timeline = w.timeline(task)
        retained = 'activity' not in w.get(task)
    finally:
        server.stop()
    receipt['fixture'] = {'server': 'retained binary', 'binary_is_candidate': ctx.rollback_binary == ctx.binary,
                          'history': 'created T0-30d, human comment T0-20d'}
    checks = Checks(receipt)
    row = next((t for t in review['tasks'] if t['id'] == task['id']), None)

    def v1_output_unchanged():
        expected = v1.activity_evidence(dict(task, created_at=ctx.clock.ago(30)), timeline, complete=True)
        assert expected['status'] == 'known' and expected['at'] == ctx.clock.ago(20), expected
        assert row is not None, 'card missing from the review'
        assert row['activity'] == expected, (row['activity'], expected)
        assert set(row['activity']) == {'status', 'at', 'source', 'classification_version'}, row
        assert row['activity']['classification_version'] == 1
        return row['activity']

    def inactive_finding():
        assert any(f['task_id'] == task['id'] and f['reason'] == 'inactive' for f in review['findings'])
        return True
    def retained_is_pre_signal():
        assert retained, ('retained binary returns Task.activity; pass NEGLECT_ROLLBACK='
                          '<binary built from main 1c54493>')
        return 'no Task.activity'
    checks.check('retained_binary_is_pre_signal', retained_is_pre_signal)
    checks.check('cli_v1_activity_unchanged', v1_output_unchanged)
    checks.check('cli_v1_inactive_finding', inactive_finding)
    checks.finish()


CASES = {9: case_09_concurrent_writes, 15: case_15_parity, 17: case_17_older_server}
