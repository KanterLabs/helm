"""Projector seed lifecycle and scale on a populated database: plan cases 8 and 16.

Both run on private copies of a populated database from populate.py (or
--populated-db, normally `make neglect-db`). Checks named store_* observe the
projector (TC-379, landed) through its tables and the private /metrics path
and must pass; listing checks stay FeatureAbsent until TC-380. Budgets and
measured numbers: PROJECTOR.md.
"""
from contextlib import closing
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
from urllib import request as urlrequest

from contract import Checks, listing, neglected, projector_settled, table_rows
from harness import ROOT, Server


def _populated(ctx, name):
    target = Path(ctx.workdir) / f'{name}.db'
    # The backup API includes any WAL content a plain file copy would miss.
    with closing(sqlite3.connect(ctx.populated_db())) as source, closing(sqlite3.connect(target)) as copy:
        source.backup(copy)
    return Server(ctx.binary, ctx.workdir, name=name, db_path=target)


def _projects(server):
    with server.db() as db:
        return [row[0] for row in db.execute("SELECT key FROM projects WHERE key LIKE 'BEN%' ORDER BY key")]


def _metrics(server):
    """Projector metrics from the private /metrics path (loopback, no labels but pass)."""
    body = urlrequest.urlopen(server.url + '/metrics', timeout=10).read().decode()
    return {line.split(' ')[0]: float(line.split(' ')[1]) for line in body.splitlines()
            if line.startswith('helm_task_activity_signal_')}


def _projection(server):
    rows = table_rows(server, 'SELECT event_cursor, seeded_version FROM task_activity_signal_projection WHERE id=1')
    count = table_rows(server, 'SELECT COUNT(*) FROM task_activity_signal')[0][0]
    return (rows[0] if rows else None), count


def case_08_interrupted_seed(ctx, receipt):
    server = _populated(ctx, 'seed-kill')
    tasks = None
    ready = server.start()
    try:
        tasks = table_rows(server, 'SELECT COUNT(*) FROM tasks')[0][0]
        # Kill as soon as the seed has committed a batch but not finished, so
        # the kill provably lands mid-seed (not at an arbitrary delay).
        deadline = time.monotonic() + ctx.args.kill_after_ms / 1000 + 60
        while True:
            state, rows = _projection(server)
            if state and state[1] == 0 and 0 < rows < tasks:
                before_kill = _metrics(server)
                server.stop(kill=True)
                break
            if (state and state[1] != 0) or time.monotonic() > deadline:
                raise RuntimeError(f'kill missed the seed window ({state}, {rows}/{tasks}); raise --scale-tasks')
            time.sleep(.005)
    finally:
        server.stop(kill=True)
    at_kill, rows_at_kill = _projection(server)
    restarted = server.start()
    receipt['fixture'] = {'tasks': tasks, 'events': ctx.args.scale_events, 'ready_seconds': round(ready, 3),
                          'restart_ready_seconds': round(restarted, 3), 'kill': 'SIGKILL after first seed batch'}
    checks = Checks(receipt)
    try:
        def landed_mid_seed():
            assert at_kill and at_kill[1] == 0 and 0 < rows_at_kill < tasks, (at_kill, rows_at_kill, tasks)
            assert before_kill['helm_task_activity_signal_seed_complete'] == 0, before_kill
            return {'rows_at_kill': rows_at_kill, 'tasks': tasks, 'seeded_version_at_kill': at_kill[1]}

        def no_missing_rows():
            projector_settled(server)
            rows = table_rows(server, 'SELECT COUNT(*) FROM task_activity_signal WHERE classification_version=2')[0][0]
            assert rows == tasks, ('rows', rows, 'tasks', tasks)
            return rows

        def cursor_caught_up():
            settled = projector_settled(server)
            assert settled['processed'] >= at_kill[0], ('cursor regressed', at_kill[0], settled)
            return {'processed_equals_max': settled['processed'] == settled['high'], 'seeded_version': 2}

        def no_duplicate_work():
            projector_settled(server)
            after = _metrics(server)
            batch = after['helm_task_activity_signal_seed_batch_size']
            redone = after['helm_task_activity_signal_rebuilds_total{pass="seed"}']
            # The restart rebuilds exactly the tasks without a committed row;
            # the killed process lost at most its one uncommitted batch.
            tail = after['helm_task_activity_signal_rebuilds_total{pass="tail"}']
            assert redone <= tasks - rows_at_kill <= redone + tail, ('restart seed rebuilt', redone, 'tail', tail,
                                                                    'missing', tasks - rows_at_kill)
            assert before_kill['helm_task_activity_signal_rebuilds_total{pass="seed"}'] <= rows_at_kill
            return {'restart_seed_rebuilds': redone, 'missing_at_kill': tasks - rows_at_kill, 'batch': batch}
        checks.check('store_kill_landed_mid_seed', landed_mid_seed)
        checks.check('store_no_missing_rows_after_restart', no_missing_rows)
        checks.check('store_cursor_caught_up_without_regression', cursor_caught_up)
        checks.check('store_no_duplicate_work_beyond_one_batch', no_duplicate_work)
        checks.check('listings_reach_complete_coverage',
                     lambda: {key: neglected(server, key, 1)[1][-1]['coverage'] for key in _projects(server)})
    finally:
        server.stop()
    checks.finish()


def case_16_scale(ctx, receipt):
    args = ctx.args
    server = _populated(ctx, 'scale')
    budgets = {'ready_seconds': 10, 'write_during_seed_ms': args.write_budget_ms, 'seed_seconds': args.seed_budget,
               'rebuild_mean_ms': args.rebuild_budget_ms, 'writer_lock_max_ms': args.lock_budget_ms,
               'listing_ms': 500}
    started = time.monotonic()
    ready = server.start()
    try:
        at_ready, rows_at_ready = _projection(server)
        keys = _projects(server)
        with server.db() as db:
            tasks = db.execute('SELECT COUNT(*) FROM tasks').fetchone()[0]
            sample = [r[0] for r in db.execute('SELECT id FROM tasks WHERE deleted_at IS NULL ORDER BY id LIMIT 40')]
        writes = []
        while not (_projection(server)[0] or (0, 0))[1] and time.monotonic() - started < budgets['seed_seconds'] + 60:
            began = time.monotonic()
            server.api('POST', f'/tasks/{sample[len(writes) % len(sample)]}/comments',
                       {'body': 'Synthetic write during seed'}, expected=201)
            writes.append((time.monotonic() - began) * 1000)
            time.sleep(.25)
        seed_seconds = time.monotonic() - started
        projector_settled(server, timeout=budgets['seed_seconds'] + 60)
        seeded = _metrics(server)
        idle_writes = []
        for task_id in sample[:20]:  # steady state: one tail rebuild per touched card
            began = time.monotonic()
            server.api('POST', f'/tasks/{task_id}/comments', {'body': 'Synthetic steady write'}, expected=201)
            idle_writes.append((time.monotonic() - began) * 1000)
            projector_settled(server)
        steady = _metrics(server)
    except Exception:
        server.stop()
        raise
    seed_count = seeded['helm_task_activity_signal_rebuild_duration_seconds_count{pass="seed"}']
    seed_ms = 1000 * seeded['helm_task_activity_signal_rebuild_duration_seconds_sum{pass="seed"}'] / max(seed_count, 1)
    tail_count = (steady['helm_task_activity_signal_rebuild_duration_seconds_count{pass="tail"}'] -
                  seeded['helm_task_activity_signal_rebuild_duration_seconds_count{pass="tail"}'])
    tail_ms = 1000 * (steady['helm_task_activity_signal_rebuild_duration_seconds_sum{pass="tail"}'] -
                      seeded['helm_task_activity_signal_rebuild_duration_seconds_sum{pass="tail"}']) / max(tail_count, 1)
    lock_ms = 1000 * steady['helm_task_activity_signal_write_duration_max_seconds']
    writes_sorted = sorted(writes) or [0]
    receipt['fixture'] = {
        'tasks': tasks, 'events': args.scale_events, 'projects': len(keys), 'budgets': budgets,
        'measured': {'ready_seconds': round(ready, 3), 'seed_seconds': round(seed_seconds, 2),
                     'seed_rebuilds': seed_count, 'seed_rebuild_mean_ms': round(seed_ms, 3),
                     'tail_rebuilds': tail_count, 'tail_rebuild_mean_ms': round(tail_ms, 3),
                     'writer_lock_max_ms': round(lock_ms, 2),
                     'projector_writes': steady['helm_task_activity_signal_write_duration_seconds_count'],
                     'writes_during_seed': len(writes), 'write_max_ms': round(writes_sorted[-1], 1),
                     'write_p95_ms': round(writes_sorted[int(.95 * (len(writes_sorted) - 1))], 1),
                     'write_after_seed_max_ms': round(max(idle_writes), 1),
                     'write_after_seed_median_ms': round(sorted(idle_writes)[len(idle_writes) // 2], 1)},
        'plan_check': 'EXPLAIN QUERY PLAN of the served listing query (Go: TestTaskActivityListingPlanOnPopulatedDatabase)'}
    checks = Checks(receipt)
    try:
        def readiness_did_not_wait():
            assert at_ready is None or at_ready[1] == 0, ('seed already complete at readiness', at_ready)
            assert rows_at_ready < tasks, (rows_at_ready, tasks)
            return _within(ready, budgets['ready_seconds'])

        def writers_not_blocked():
            assert writes, 'no write landed during the seed'
            return _within(writes_sorted[-1], budgets['write_during_seed_ms'])
        checks.check('store_readiness_does_not_wait_for_seed', readiness_did_not_wait)
        checks.check('store_writer_not_blocked_beyond_bound', writers_not_blocked)
        checks.check('store_seed_completes_within_budget', lambda: _within(seed_seconds, budgets['seed_seconds']))
        checks.check('store_seed_rebuild_within_budget', lambda: _within(seed_ms, budgets['rebuild_mean_ms']))
        checks.check('store_tail_rebuild_within_budget', lambda: _within(tail_ms, budgets['rebuild_mean_ms']))
        checks.check('store_writer_lock_within_budget', lambda: _within(lock_ms, budgets['writer_lock_max_ms']))

        def filtered_listing_latency():
            began = time.monotonic()
            _, body = listing(server, keys[0], inactive_days=14, state='backlog', release='unassigned', limit=200)
            assert body['activity_coverage']['complete'], body['activity_coverage']
            return _within((time.monotonic() - began) * 1000, budgets['listing_ms'])
        checks.check('filtered_listing_within_budget', filtered_listing_latency)
    finally:
        server.stop()
    checks.check('filtered_listing_plan_is_project_scoped', lambda: _listing_plan(ctx, server))
    checks.finish()


def _listing_plan(ctx, server):
    """EXPLAIN QUERY PLAN of the exact listing SQL the store builds, on this
    case's populated copy after the server stopped (TC-380, API.md mode 14)."""
    plan_receipt = Path(ctx.workdir) / 'listing-plan.json'
    env = dict(os.environ, HELM_NEGLECT_PLAN_DB=str(server.db_path), HELM_NEGLECT_PLAN_RECEIPT=str(plan_receipt))
    result = subprocess.run([os.environ.get('GO', 'go'), 'test', '-count=1', '-run',
                             '^TestTaskActivityListingPlanOnPopulatedDatabase$', './internal/store'],
                            cwd=ROOT, env=env, capture_output=True, text=True, timeout=900)
    plan = json.loads(plan_receipt.read_text()) if plan_receipt.exists() else None
    assert result.returncode == 0 and plan, ('plan check failed', result.stdout[-800:], plan)
    return plan


def _within(value, budget):
    assert value <= budget, (value, 'budget', budget)
    return round(value, 3)


CASES = {8: case_08_interrupted_seed, 16: case_16_scale}
