"""`focus review` adoption of the server signal: TC-381 cases 18 and 19.

Failure modes are in CLI.md. Case 18 checks the server path and the new
statuses, case 19 partial coverage. Case 20 (older server, byte-for-byte
version 1) is in cases_cli_fallback.py and reuses these helpers. Every case
owns its server.
"""
import hashlib
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import uuid

from contract import Checks, cli_signal, projector_settled
from harness import CLI, Server, World



def run_cli(server, token, workdir, *argv, cli=CLI, ok=True):
    """Run one CLI against one server with a private 0600 config; return (json, raw)."""
    base = Path(workdir) / f'cli-{server.db_path.stem}'
    base.mkdir(exist_ok=True)
    config = base / 'credentials.json'
    config.write_text(json.dumps({'base_url': server.url, 'token': token}))
    config.chmod(0o600)
    sessions = base / ('sessions-' + hashlib.sha256(str(cli).encode()).hexdigest()[:8])
    env = dict(server.env(), HELM_CONFIG=str(config), HELM_SESSION_DIR=str(sessions))
    result = subprocess.run([sys.executable, str(cli), 'focus', 'review', *argv], env=env, cwd=base,
                            text=True, capture_output=True, timeout=90)
    if (result.returncode == 0) != ok:
        raise RuntimeError(f'focus review {argv} exited {result.returncode}: {result.stderr[:300]}')
    return json.loads(result.stdout or result.stderr), result


def pre_signal(world, task):
    """The retained binary must not have the signal, or cases 17/20 prove nothing."""
    assert 'activity' not in world.get(task), (
        'retained binary returns Task.activity; pass NEGLECT_ROLLBACK=<binary built from main 1c54493>')
    return 'no Task.activity'


def legacy(w, task, created_days, update_days):
    """Case 1's proxy for a pre-1c54493 edit: a task.updated without changed_fields."""
    w.patch(task, priority='high')
    with w.s.db() as db:
        db.execute("UPDATE events SET payload=json_remove(payload, '$.changed_fields') "
                   "WHERE task_id=? AND type='task.updated'", (task['id'],))
        db.execute('UPDATE tasks SET created_at=? WHERE id=?', (w.clock.ago(created_days), task['id']))
        db.execute("UPDATE events SET created_at=? WHERE task_id=? AND type='task.created'",
                   (w.clock.ago(created_days), task['id']))
        db.execute("UPDATE events SET created_at=? WHERE task_id=? AND type='task.updated'",
                   (w.clock.ago(update_days), task['id']))
    w.touch(task)
    return task


def insert_event(w, task, kind, payload, at):
    with w.s.db() as db:
        db.execute('INSERT INTO events(id,type,actor_id,project_id,task_id,payload,created_at) '
                   'VALUES (?,?,?,?,?,?,?)', (uuid.uuid4().hex, kind, 'actor-disabled-mode',
                                              w.project['id'], task['id'], payload, at))


def focus_fixture(w):
    """Focus G (old), K (depends on G), O (old, owned by the CLI agent)."""
    focus = w.s.api('POST', f'/projects/{w.key}/releases', {'name': 'Synthetic neglect Focus'}, expected=201)
    g = w.old_task('Synthetic old Focus member', 30, release_id=focus['id'])
    k = w.old_task('Synthetic dependent Focus member', 30, release_id=focus['id'])
    o = w.old_task('Synthetic owned Focus member', 30, release_id=focus['id'])
    w.depend(k, g)
    w.action(o, 'claim', token=w.token)
    return focus, {'G': g, 'K': k, 'O': o}


def events(server):
    with server.db() as db:
        return db.execute('SELECT COUNT(*) FROM events').fetchone()[0]


def by_key(review, cards):
    rows = {t['id']: t for t in review['tasks']}
    return {name: rows.get(card['id']) for name, card in cards.items()}


def reasons(review, card):
    return {f['reason'] for f in review['findings'] if f['task_id'] == card['id']}


def case_18_cli_server_path(ctx, receipt):
    server = Server(ctx.binary, ctx.workdir, name='cli-signal')
    server.start()
    try:
        w = World(server, ctx.clock, key='CLI')
        cards = {'A': w.old_task('Synthetic inactive card', 30),
                 'B': legacy(w, w.task('Synthetic legacy old card'), 61, 60),
                 'C': legacy(w, w.task('Synthetic legacy recent card'), 61, 1),
                 'D': w.old_task('Synthetic unknown card', 30),
                 'E': w.task('Synthetic fresh card'),
                 'F': w.old_task('Synthetic twelve day card', 12)}
        insert_event(w, cards['D'], 'task.updated', '{"changed_fields": ["title"]}', 'not-a-timestamp')
        focus, members = focus_fixture(w)
        cards.update(members)
        projector_settled(server)
        before = events(server)
        cli = lambda *a, **k: run_cli(server, w.token, ctx.workdir, '--project', 'CLI', *a, **k)[0]  # noqa: E731
        review, ten, focused = cli(), cli('--inactive-days', '10'), cli('--focus', focus['id'])
        shallow, tiny = cli('--timeline-limit', '1'), cli('--request-limit', '3')
        chain, cursor = [], ''
        for _ in range(20):
            page = cli('--history-limit', '1', *(('--cursor', cursor) if cursor else ()))
            chain.append(page)
            cursor = page['next_cursor']
            if not cursor:
                break
        after = events(server)
    finally:
        server.stop()
    receipt['fixture'] = {'cards': {'A': 'T0-30d', 'B': 'legacy T0-60d', 'C': 'legacy T0-1d',
                                    'D': 'unparseable event', 'E': 'fresh', 'F': 'T0-12d',
                                    'G': 'Focus T0-30d', 'K': 'Focus, depends on G', 'O': 'Focus, owned'},
                          'review': review}
    checks = Checks(receipt)
    rows = by_key(review, cards)

    def server_path():
        for result in (review, ten, focused, shallow, *chain):
            cli_signal(result)
        return cli_signal(review)

    def no_timeline_reads():
        cli_signal(review)
        assert review['coverage']['histories_read'] == 0, review['coverage']
        assert all(t['timeline_next'] == '' for t in review['tasks']) and 'history_continuations' not in review
        hydrated = len(members)
        assert review['coverage']['requests'] <= 7 + hydrated, review['coverage']
        return {'requests': review['coverage']['requests'], 'tasks': len(review['tasks'])}

    def statuses():
        cli_signal(review)
        expected = {'A': ('known', 'inactive', 30), 'B': ('bounded', 'inactive', 60),
                    'C': ('bounded', 'undetermined', 1), 'D': ('unknown', 'unknown', None),
                    'G': ('known', 'inactive', 30), 'O': ('known', 'inactive', 30)}
        for name, (status, assessment, days) in expected.items():
            value = (rows[name] or {}).get('activity')
            assert value and value['status'] == status and value['assessment'] == assessment, (name, value)
            assert value['not_after'] == (ctx.clock.ago(days) if days else None), (name, value)
        assert rows['E'] is None and rows['F'] is None, 'active cards listed as neglected'
        return {name: rows[name]['activity']['assessment'] for name in expected}

    def findings():
        cli_signal(review)
        for name in 'ABGO':
            assert 'inactive' in reasons(review, cards[name]), name
        assert 'activity_undetermined' in reasons(review, cards['C']), reasons(review, cards['C'])
        assert 'inactive' not in reasons(review, cards['C'])
        assert 'activity_unknown' in reasons(review, cards['D'])
        assert 'dependency_blocked' in reasons(review, cards['K']) and 'owned' in reasons(review, cards['O'])
        return sorted(f['reason'] for f in review['findings'])

    def coverage():
        signal = cli_signal(review)
        assert signal['activity_coverage']['complete'] is True, signal
        assert review['coverage']['histories_unavailable'] == 1 and review['coverage']['partial'], review['coverage']
        return {'signal': signal, 'coverage': review['coverage']}

    def threshold_mapped():
        signal = cli_signal(ten)
        row = by_key(ten, cards)['F']
        assert signal['inactive_days'] == 10 and row and row['activity']['assessment'] == 'inactive', row
        return signal['inactive_days']

    def budgets():
        signal = cli_signal(review)
        assert signal['unused_limits'] == ['timeline_limit'], signal
        same = lambda r: {t['id']: t['activity'] for t in r['tasks']}  # noqa: E731
        assert same(shallow) == same(review), 'timeline-limit changed the server-path result'
        assert tiny['resume']['restart_required'] and tiny['coverage']['partial'], tiny
        seen = [t['id'] for page in chain for t in page['tasks']]
        assert all(len(page['tasks']) <= 1 for page in chain), 'history-limit no longer bounds the slice'
        assert len(seen) == len(set(seen)) and set(seen) == set(same(review)), (len(seen), len(review['tasks']))
        return {'chain_pages': len(chain)}

    def focus_mode():
        cli_signal(focused)
        assert {t['id'] for t in focused['tasks']} == {c['id'] for c in members.values()}, focused['tasks']
        assert 'inactive' in reasons(focused, members['G'])
        return len(focused['tasks'])
    checks.check('server_path_reported', server_path)
    checks.check('no_timeline_reads', no_timeline_reads)
    checks.check('statuses_and_bounds', statuses)
    checks.check('findings_per_status_and_focus_evidence', findings)
    checks.check('server_coverage_reported', coverage)
    checks.check('inactive_days_mapped', threshold_mapped)
    checks.check('budget_flags', budgets)
    checks.check('focus_mode_server_path', focus_mode)

    def read_only():
        assert after == before, (before, after)
        return {'events_before': before, 'events_after': after}
    checks.check('read_only', read_only)
    checks.finish()


def _hold_projector(w, task, attempts=8):
    """Commit an excluded event, then hold the write lock before the projector
    sees it (its 2 s ticker is the only wake for another process's writes)."""
    for _ in range(attempts):
        insert_event(w, task, 'task.claimed', '{}', w.clock.ago(30))
        conn = sqlite3.connect(w.s.db_path, timeout=15, isolation_level=None)
        conn.execute('BEGIN IMMEDIATE')
        processed = conn.execute('SELECT event_cursor FROM task_activity_signal_projection WHERE id=1').fetchone()[0]
        if processed < conn.execute('SELECT MAX(cursor) FROM events').fetchone()[0]:
            return conn
        conn.execute('ROLLBACK')
        conn.close()
    raise RuntimeError('projector processed every inserted event before the lock was taken')


def case_19_cli_partial_coverage(ctx, receipt):
    server = Server(ctx.binary, ctx.workdir, name='cli-pending')
    server.start()
    try:
        w = World(server, ctx.clock, key='PND')
        held, steady = w.old_task('Synthetic pending card', 30), w.old_task('Synthetic settled card', 30)
        projector_settled(server)
        run_cli(server, w.token, ctx.workdir, '--project', 'PND')  # refreshes token metadata first
        lock = _hold_projector(w, held)
        try:
            partial, _ = run_cli(server, w.token, ctx.workdir, '--project', 'PND')
        finally:
            lock.execute('ROLLBACK')
            lock.close()
        projector_settled(server)
        settled, _ = run_cli(server, w.token, ctx.workdir, '--project', 'PND')
    finally:
        server.stop()
    receipt['fixture'] = {'cards': {'held': 'T0-30d + unprocessed excluded event', 'steady': 'T0-30d'},
                          'partial': partial}
    checks = Checks(receipt)
    row = by_key(partial, {'held': held})['held']

    def incomplete():
        signal = cli_signal(partial)
        assert signal['activity_coverage']['complete'] is False, signal
        return signal['activity_coverage']

    def partial_not_nothing_neglected():
        cli_signal(partial)
        assert partial['coverage']['partial'] is True, partial['coverage']
        assert any(w_['reason'] == 'activity_coverage_incomplete' for w_ in partial['warnings']), partial['warnings']
        return partial['warnings']

    def pending_not_concluded():
        cli_signal(partial)
        if row is None:
            return 'omitted by the server while pending'
        assert row['activity']['assessment'] == 'pending', row
        assert reasons(partial, held) & {'activity_pending'} and 'inactive' not in reasons(partial, held)
        return row['activity']

    def settled_rerun():
        signal = cli_signal(settled)
        assert signal['activity_coverage']['complete'] is True and not settled['coverage']['partial'], settled
        assert by_key(settled, {'h': held})['h']['activity']['assessment'] == 'inactive'
        return signal['activity_coverage']
    checks.check('server_path_reported', lambda: cli_signal(partial))
    checks.check('incomplete_coverage_reported', incomplete)
    checks.check('partial_never_nothing_neglected', partial_not_nothing_neglected)
    checks.check('pending_card_not_concluded', pending_not_concluded)
    checks.check('settled_rerun_complete', settled_rerun)
    checks.finish()


CASES = {18: case_18_cli_server_path, 19: case_19_cli_partial_coverage}
