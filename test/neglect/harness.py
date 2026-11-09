"""Isolated Helm server, fixed fixture clock and receipt sanitizing for neglect cases.

Mirrors test/focus/run.py: a real binary on a private SQLite database in a
temporary directory under $TMPDIR, auth disabled for the human caller, and
synthetic agent tokens kept in memory only. Nothing here knows the future
activity schema; contract.py owns every assumption about it.
"""
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import signal
import socket
import sqlite3
import subprocess
import time
from urllib import request, error
import uuid

ROOT = Path(__file__).resolve().parents[2]
CLI = ROOT / 'skills/helm/scripts/helm.py'
SCOPES = ['tasks:read', 'tasks:write', 'tasks:claim', 'projects:read',
          'projects:write', 'events:read']


class Clock:
    """Fixture anchor T0 (whole second, UTC). All backdates are exact offsets.

    The server reads its own wall clock. Since TC-380 a binary built with
    `-tags helm_testclock` can fix its evaluation instant (case 14 only, see
    API.md). Receipts express fixture times as offsets from T0, which makes
    them identical between runs.
    """

    def __init__(self, anchor=None):
        self.t0 = anchor or datetime.now(timezone.utc).replace(microsecond=0)

    def ago(self, days=0, seconds=0):
        return iso(self.t0 - timedelta(days=days, seconds=seconds))

    def label(self, value):
        at = parse(value)
        if at is None:
            return value
        delta = (self.t0 - at).total_seconds()
        if delta <= 0:
            return 'live'
        if delta % 86400 == 0:
            return f'T0-{int(delta // 86400)}d'
        return f'T0-{delta:.3f}s'


def iso(at):
    return at.astimezone(timezone.utc).isoformat().replace('+00:00', 'Z')


def parse(value):
    try:
        at = datetime.fromisoformat(str(value).replace('Z', '+00:00'))
    except (TypeError, ValueError):
        return None
    return at.astimezone(timezone.utc) if at.tzinfo else None


_TIME = re.compile(r'^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$')
_ID = re.compile(r'^[0-9a-f]{32}$')
_SECRET_KEYS = {'token', 'authorization', 'password', 'secret', 'cookie', 'credentials'}


class Sanitizer:
    """Stable aliases for ids, T0 offsets for times, no secrets or free text."""

    def __init__(self, clock):
        self.clock = clock
        self.ids = {}

    def __call__(self, value, key=''):
        if isinstance(value, dict):
            return {k: ('[redacted]' if k.lower() in _SECRET_KEYS else self(v, k))
                    for k, v in value.items()}
        if isinstance(value, list):
            return [self(v, key) for v in value]
        if isinstance(value, str):
            if _TIME.match(value):
                return self.clock.label(value)
            if _ID.match(value):
                return self.ids.setdefault(value, f'id#{len(self.ids) + 1}')
            if key in ('body', 'description', 'summary') and value:
                return f'[text:{len(value)}]'
            if key == 'cursor' or key.endswith('_cursor'):
                return '[cursor]'
        return value


class ApiError(RuntimeError):
    """Unexpected HTTP status from a fixture step: a harness error, not a red case."""


class Server:
    """One real binary on one database; restartable with another binary."""

    def __init__(self, binary, workdir, name='server', db_path=None, extra_env=None):
        self.binary = Path(binary).resolve()
        self.extra_env = dict(extra_env or {})
        self.workdir = Path(workdir)
        self.db_path = Path(db_path) if db_path else self.workdir / f'{name}.db'
        self.log = self.workdir / f'{name}.log'
        self.process = None
        self.url = ''

    def env(self):
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('HELM_', 'ROADMAP_', 'TC_ROADMAP_', 'CODEX_'))}
        env.update(HELM_ADDR=self.url.removeprefix('http://'), HELM_DB=str(self.db_path),
                   HELM_AUTH_MODE='disabled', HELM_RELEASE_SHA='0' * 40,
                   HELM_PUBLIC_ORIGIN=self.url)
        env.update(self.extra_env)
        return env

    def start(self, binary=None, timeout=30):
        if binary is not None:
            self.binary = Path(binary).resolve()
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        self.url = f'http://127.0.0.1:{port}'
        started = time.monotonic()
        with open(self.log, 'ab') as log:
            self.process = subprocess.Popen([str(self.binary)], cwd=self.workdir, env=self.env(),
                                            stdout=log, stderr=subprocess.STDOUT)
        while time.monotonic() - started < timeout:
            if self.process.poll() is not None:
                raise ApiError(f'server exited during start ({self.process.returncode}); see log')
            try:
                request.urlopen(self.url + '/readyz', timeout=.3).close()
                return time.monotonic() - started
            except (OSError, error.URLError):
                time.sleep(.05)
        raise ApiError('server readiness timed out')

    def stop(self, kill=False):
        if not self.process or self.process.poll() is not None:
            return
        self.process.send_signal(signal.SIGKILL if kill else signal.SIGTERM)
        try:
            self.process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait()

    def call(self, method, path, body=None, version=None, token=None):
        """Return (status, parsed body); never raises for HTTP status codes."""
        headers = {'Content-Type': 'application/json', 'Origin': self.url,
                   'Idempotency-Key': str(uuid.uuid4())}
        if path.endswith(('/tokens', '/heartbeat')) or method == 'GET':
            headers.pop('Idempotency-Key')
        if version is not None:
            headers['If-Match'] = f'"v{version}"'
        if token:
            headers['Authorization'] = 'Bearer ' + token
        req = request.Request(self.url + '/api/v1' + path, method=method, headers=headers,
                              data=json.dumps(body).encode() if body is not None else None)
        try:
            response = request.urlopen(req, timeout=15)
        except error.HTTPError as exc:
            response = exc
        with response:
            raw = response.read()
            try:
                data = json.loads(raw) if raw else None
            except ValueError:
                data = {'unparsed_bytes': len(raw)}
            return response.status, data

    def api(self, method, path, body=None, version=None, expected=200, token=None):
        status, data = self.call(method, path, body, version, token)
        if status != expected:
            raise ApiError(f'{method} {path} -> {status}, expected {expected}: {str(data)[:300]}')
        return data

    @contextmanager
    def db(self):
        """Commit on success and always close, so no WAL is left behind."""
        conn = sqlite3.connect(self.db_path, timeout=15)
        try:
            conn.execute('PRAGMA busy_timeout=15000')
            with conn:
                yield conn
        finally:
            conn.close()


class World:
    """Synthetic project, columns, agent token and the mutation vocabulary."""

    def __init__(self, server, clock, key='SYN'):
        self.s, self.clock, self.key = server, clock, key
        self.project = server.api('POST', '/projects', {'key': key, 'name': f'Synthetic neglect {key}'},
                                  expected=201)
        columns = server.api('GET', f'/projects/{key}/columns')['data']
        self.columns = {c['semantic_state']: c['id'] for c in reversed(columns)}
        agent = server.api('POST', '/agents', {'name': f'Synthetic {key} agent',
                                               'project_ids': [self.project['id']]}, expected=201)
        self.agent_id = agent['id']
        self.token = server.api('POST', f'/agents/{agent["id"]}/tokens',
                                {'name': 'synthetic', 'scopes': SCOPES,
                                 'project_ids': [self.project['id']]}, expected=201)['token']

    def task(self, title, **fields):
        return self.s.api('POST', f'/projects/{self.key}/tasks', dict(title=title, **fields), expected=201)

    def get(self, task, token=None):
        return self.s.api('GET', '/tasks/' + task['id'], token=token)

    def version(self, task):
        return self.get(task)['version']

    def patch(self, task, **fields):
        return self.s.api('PATCH', '/tasks/' + task['id'], fields, version=self.version(task))

    def comment(self, task, body='Synthetic note', token=None):
        return self.s.api('POST', f'/tasks/{task["id"]}/comments', {'body': body}, expected=201, token=token)

    def checklist_add(self, task, text):
        return self.s.api('POST', f'/tasks/{task["id"]}/checklist', {'text': text},
                          version=self.version(task))['checklist']

    def checklist_set(self, task, item_id, completed):
        return self.s.api('PATCH', f'/tasks/{task["id"]}/checklist/{item_id}',
                          {'completed': completed}, version=self.version(task))

    def checklist_reorder(self, task, item_ids):
        return self.s.api('POST', f'/tasks/{task["id"]}/checklist/reorder',
                          {'item_ids': item_ids}, version=self.version(task))

    def move(self, task, state):
        current = self.get(task)
        return self.s.api('POST', f'/tasks/{task["id"]}/move',
                          {'column_id': self.columns[state], 'expected_source_column_id': current['column_id'],
                           'source': 'synthetic-neglect'}, version=current['version'])

    def depend(self, task, prerequisite):
        return self.s.api('POST', f'/tasks/{task["id"]}/dependencies',
                          {'prerequisite': prerequisite['id']}, version=self.version(task))

    def link_child(self, parent, child):
        return self.s.api('POST', f'/tasks/{parent["id"]}/children', {'child': child['id']},
                          version=self.version(child))

    def action(self, task, name, token=None):
        version = self.get(task, token=token)['version']
        return self.s.api('POST', f'/tasks/{task["id"]}/{name}', {}, version=version, token=token)

    def progress(self, task, operation, phase, completed, total=3, next_action='Verify synthetic result'):
        return self.s.api('POST', f'/tasks/{task["id"]}/progress',
                          {'operation_id': operation, 'state': 'working', 'phase': phase,
                           'summary': 'Synthetic progress', 'next_action': next_action,
                           'checkpoint_completed': completed, 'checkpoint_total': total},
                          version=self.get(task, token=self.token)['version'], token=self.token)

    def heartbeat(self, task, operation):
        return self.s.api('POST', f'/tasks/{task["id"]}/heartbeat', {'operation_id': operation},
                          token=self.token)

    def timeline(self, task):
        return self.s.api('GET', f'/tasks/{task["id"]}/timeline?limit=100')['data']

    def backdate(self, task, at):
        """Move every history row of one owned synthetic task to one instant."""
        with self.s.db() as db:
            db.execute('UPDATE tasks SET created_at=?, updated_at=? WHERE id=?', (at, at, task['id']))
            db.execute('UPDATE events SET created_at=? WHERE task_id=?', (at, task['id']))
            db.execute('UPDATE comments SET created_at=?, updated_at=? WHERE task_id=?', (at, at, task['id']))
            db.execute('UPDATE task_agent_work_history SET created_at=?, started_at=? WHERE task_id=?',
                       (at, at, task['id']))
            db.execute('UPDATE task_agent_work SET updated_at=?, started_at=? WHERE task_id=?',
                       (at, at, task['id']))

    def touch(self, task):
        """Append excluded events (claim, release) at new cursors.

        A direct timestamp edit does not advance the event cursor, so a
        projector that already processed the task would never see it. Claim and
        release are excluded by classification versions 1 and 2, so the touch
        changes which tasks the tail pass revisits but never their evidence.
        """
        self.action(task, 'claim')
        self.action(task, 'release')

    def old_task(self, title, days, **fields):
        task = self.task(title, **fields)
        self.backdate(task, self.clock.ago(days))
        self.touch(task)
        return self.get(task)
