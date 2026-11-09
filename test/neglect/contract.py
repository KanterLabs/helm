"""The only place that encodes the planned neglect-signal API (plan D2, D5, D6).

A missing field or ignored parameter raises FeatureAbsent, which the runner
records as the expected red state before TC-378 to TC-380 land. Any other
assertion is a behavior failure. If a later card legitimately chooses a
different spelling, change it here once and record the decision in FAILURES.md.
"""
import time
from urllib.parse import urlencode

STATUSES = {'known', 'bounded', 'unknown'}
TRANSPORT_STATUSES = STATUSES | {'pending'}
REASONS = {'legacy_update', 'unknown_event_type', 'actor_unavailable',
           'unparseable_time', 'classification_error'}
ACTIVITY_FIELDS = {'status', 'last_meaningful_at', 'source', 'not_after', 'reason',
                   'classification_version'}
VERSION = 2


class FeatureAbsent(AssertionError):
    """The server does not yet expose the planned neglect signal."""


def activity(task):
    """Validate and return Task.activity (plan D2)."""
    if 'activity' not in task:
        raise FeatureAbsent('Task.activity is absent from the task representation')
    value = task['activity']
    assert isinstance(value, dict), value
    missing = ACTIVITY_FIELDS - set(value)
    assert not missing, ('activity fields missing', sorted(missing), value)
    status = value['status']
    assert status in TRANSPORT_STATUSES, value
    if status == 'pending':
        return value
    assert value['classification_version'] == VERSION, value
    if status == 'known':
        assert value['last_meaningful_at'] and value['not_after'] == value['last_meaningful_at'], value
        assert value['source'], value
    elif status == 'bounded':
        assert value['last_meaningful_at'] is None and value['not_after'], value
        assert value['reason'] in REASONS, value
    else:
        assert value['not_after'] is None and value['reason'] in REASONS, value
    return value


def task_activity(world, task, settle=True):
    """Read one task; when settle, wait out transient pending (plan D5)."""
    deadline = time.monotonic() + 10
    while True:
        value = activity(world.get(task))
        if not settle or value['status'] != 'pending' or time.monotonic() > deadline:
            return value
        time.sleep(.1)


def listing(server, project_key, token=None, expected=200, **params):
    """GET a project task page with neglect inputs; return (status, body)."""
    query = urlencode({k: v for k, v in params.items() if v is not None})
    status, body = server.call('GET', f'/projects/{project_key}/tasks?{query}', token=token)
    uses_signal = any(k in params for k in ('inactive_days', 'activity_status')) or \
        params.get('sort') == 'not_after'
    if status == 400 and uses_signal and 'sort must be' in str(body):
        raise FeatureAbsent('sort=not_after is rejected: ' + str(body)[:160])
    if status != expected:
        raise AssertionError((params, status, str(body)[:300]))
    if status == 200 and uses_signal:
        missing = {'evaluated_at', 'activity_coverage'} - set(body)
        if missing:
            raise FeatureAbsent(f'collection ignored neglect inputs; missing {sorted(missing)}')
        coverage = body['activity_coverage']
        assert isinstance(coverage.get('complete'), bool), coverage
    return status, body


def neglected(server, project_key, days=14, token=None, settle=True, **params):
    """Every page of a neglect listing; settle waits for complete coverage.

    Returns (tasks, pages) where pages keep evaluated_at and coverage.
    """
    deadline = time.monotonic() + 20
    while True:
        tasks, pages, cursor = [], [], None
        while True:
            _, body = listing(server, project_key, token=token, inactive_days=days,
                              cursor=cursor, **params)
            pages.append({'evaluated_at': body['evaluated_at'],
                          'coverage': body['activity_coverage'], 'count': len(body['data'])})
            tasks.extend(body['data'])
            cursor = body.get('next_cursor')
            if not cursor:
                break
        complete = all(page['coverage']['complete'] for page in pages)
        if complete or not settle or time.monotonic() > deadline:
            return tasks, pages
        time.sleep(.2)


def table_rows(server, sql, args=()):
    """Read the derived tables directly; their absence is the feature's absence."""
    import sqlite3
    with server.db() as db:
        try:
            return db.execute(sql, args).fetchall()
        except sqlite3.OperationalError as exc:
            if 'no such table' in str(exc):
                raise FeatureAbsent(f'derived table missing: {exc}')
            raise


STORED_FIELDS = ('status', 'last_meaningful_at', 'source', 'not_after', 'reason', 'classification_version')


def stored_activity(server, task_id, timeout=15):
    """The derived row for one task, once the projector (TC-379) has processed
    every event committed before this call; validated like Task.activity.

    This observes the projector directly through task_activity_signal, so
    projector behaviour is checked before TC-380 exposes the field. Checks that
    use it are named store_* and are guards: since TC-379 landed, a missing
    projection row is a failure, never FeatureAbsent.
    """
    target = table_rows(server, 'SELECT COALESCE(MAX(cursor), 0) FROM events')[0][0]
    deadline = time.monotonic() + timeout
    while True:
        state = table_rows(server, 'SELECT event_cursor FROM task_activity_signal_projection WHERE id=1')
        if state and state[0][0] >= target:
            rows = table_rows(server, f'SELECT {", ".join(STORED_FIELDS)} FROM task_activity_signal '
                                      'WHERE task_id=?', (task_id,))
            if rows:
                return activity({'activity': dict(zip(STORED_FIELDS, rows[0]))})
        assert time.monotonic() < deadline, ('projector did not reach cursor', target, state)
        time.sleep(.05)


def projector_settled(server, timeout=60):
    """Wait until the seed is complete and the processed cursor equals MAX(cursor)."""
    deadline = time.monotonic() + timeout
    while True:
        state = table_rows(server, 'SELECT event_cursor, seeded_version FROM task_activity_signal_projection WHERE id=1')
        high = table_rows(server, 'SELECT COALESCE(MAX(cursor), 0) FROM events')[0][0]
        if state and state[0][0] >= high and state[0][1] == VERSION:
            return {'processed': state[0][0], 'high': high, 'seeded_version': state[0][1]}
        assert time.monotonic() < deadline, ('projector did not settle', state, high)
        time.sleep(.05)


def ids(tasks):
    return [task['id'] for task in tasks]


ASSESSMENTS = {'inactive', 'active', 'undetermined', 'unknown', 'pending'}


def cli_signal(review):
    """The server-path indicator of `focus review` output (TC-381, CLI.md).

    Its absence is the version 1 indicator, so on a server with the signal a
    missing key means the CLI has not adopted it: FeatureAbsent.
    """
    if 'activity_signal' not in review:
        raise FeatureAbsent('focus review output has no activity_signal (server path not adopted)')
    value = review['activity_signal']
    assert value.get('path') == 'server' and value.get('classification_version') == VERSION, value
    assert value.get('evaluated_at') and isinstance(value.get('activity_coverage'), dict), value
    for task in review['tasks']:
        assert task['activity'].get('assessment') in ASSESSMENTS, task['activity']
        assert 'not_after' in task['activity'], task['activity']
    return value


class Checks:
    """Run every observation, then fail with the most important outcome.

    A behavior failure outranks FeatureAbsent so a partly implemented server
    cannot hide a wrong answer behind a missing field elsewhere.
    """

    def __init__(self, receipt):
        self.receipt = receipt
        self.receipt.setdefault('checks', [])
        self.failures = []

    def check(self, name, fn):
        try:
            observed = fn()
        except FeatureAbsent as exc:
            self.receipt['checks'].append({'name': name, 'outcome': 'feature_absent', 'detail': str(exc)})
            self.failures.append(exc)
            return None
        except AssertionError as exc:
            self.receipt['checks'].append({'name': name, 'outcome': 'failed', 'detail': str(exc)[:600]})
            self.failures.append(exc)
            return None
        self.receipt['checks'].append({'name': name, 'outcome': 'passed', 'observed': observed})
        return observed

    def finish(self):
        wrong = [f for f in self.failures if not isinstance(f, FeatureAbsent)]
        if wrong:
            raise wrong[0]
        if self.failures:
            raise self.failures[0]
