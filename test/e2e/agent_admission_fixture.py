"""Own a loopback Helm server and synthetic SQLite database for admission checks."""
from contextlib import closing, contextmanager
from dataclasses import dataclass
import json
import os
import socket
import sqlite3
import subprocess
import time
from urllib import error, request
import uuid


SCOPES = ['tasks:read', 'tasks:write', 'tasks:claim', 'projects:read']
PROJECT_ROWS = {
    'projects': 'id = ?',
    'columns': 'project_id = ?',
    'tasks': 'project_id = ?',
    'project_counters': 'project_id = ?',
    'events': 'project_id = ?',
    'comments': 'task_id IN (SELECT id FROM tasks WHERE project_id = ?)',
    'task_checklist_items': 'task_id IN (SELECT id FROM tasks WHERE project_id = ?)',
    'task_agent_work': 'task_id IN (SELECT id FROM tasks WHERE project_id = ?)',
    'task_agent_work_history': 'task_id IN (SELECT id FROM tasks WHERE project_id = ?)',
    'task_agent_admissions': 'task_id IN (SELECT id FROM tasks WHERE project_id = ?)',
}


def operation():
    return str(uuid.uuid4())


@dataclass(frozen=True)
class Response:
    status: int
    body: dict

    @property
    def code(self):
        return self.body.get('error', {}).get('code')

    @property
    def details(self):
        return self.body.get('error', {}).get('details') or {}

    def expect(self, status, code=None):
        assert self.status == status, f'expected HTTP {status}, got HTTP {self.status} ({self.code})'
        if code is not None:
            assert self.code == code, f'expected {code}, got {self.code}'
        return self.body


class Fixture:
    def __init__(self, root):
        self.root = root
        self.db_path = root / 'helm.db'
        self.url = None

    @contextmanager
    def server(self, binary):
        assert self.url is None, 'only one server may own a fixture database'
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            address = f'127.0.0.1:{sock.getsockname()[1]}'
        self.url = 'http://' + address
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('HELM_', 'ROADMAP_', 'TC_ROADMAP_', 'CODEX_'))}
        env.update(HELM_ADDR=address, HELM_DB=str(self.db_path), HELM_AUTH_MODE='disabled',
                   HELM_PUBLIC_ORIGIN=self.url, GOMAXPROCS='2')
        server = subprocess.Popen([str(binary)], cwd=self.root, env=env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            self.wait_until_ready(server)
            yield self
        finally:
            server.terminate()
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)
            self.url = None

    def wait_until_ready(self, server):
        deadline = time.monotonic() + 15
        opener = request.build_opener(request.ProxyHandler({}))
        while time.monotonic() < deadline:
            assert server.poll() is None, 'fixture server stopped before readiness'
            try:
                with opener.open(self.url + '/readyz', timeout=.2) as response:
                    if response.status == 200:
                        return
            except error.URLError:
                time.sleep(.1)
        raise AssertionError('fixture readiness timed out')

    def call(self, method, path, body=None, *, token=None, version=None, session=None, fence=None, key=None):
        headers = {'Content-Type': 'application/json', 'Origin': self.url}
        if method != 'GET' and not path.endswith(('/tokens', '/heartbeat')):
            headers['Idempotency-Key'] = key or operation()
        if token is not None:
            headers['Authorization'] = 'Bearer ' + token
        if version is not None:
            headers['If-Match'] = f'"v{version}"'
        if session is not None:
            headers['X-Helm-Agent-Session'] = session
        if fence is not None:
            headers['X-Helm-Agent-Fence'] = fence
        req = request.Request(self.url + '/api/v1' + path, method=method, headers=headers,
                              data=json.dumps(body).encode() if body is not None else None)
        opener = request.build_opener(request.ProxyHandler({}))
        try:
            response = opener.open(req, timeout=10)
        except error.HTTPError as exc:
            response = exc
        with response:
            data = response.read()
            return Response(response.status, json.loads(data) if data else {})

    def project(self, key):
        return self.call('POST', '/projects', {'name': f'Synthetic {key}', 'key': key}).expect(201)

    def have_capacity(self, project, count):
        detail = self.call('GET', '/projects/' + project['key']).expect(200)
        listing = self.call('GET', '/projects').expect(200)['data']
        matches = [entry for entry in listing if entry['id'] == project['id']]
        assert len(matches) == 1, 'project missing from the collection'
        for payload in (detail, matches[0]):
            assert payload.get('active_task_limit') == 5, 'project capacity limit differs from five'
            assert payload.get('active_task_count') == count, (
                f"expected Active count {count}, got {payload.get('active_task_count')}"
            )

    def column(self, project, state):
        columns = self.call('GET', f"/projects/{project['key']}/columns").expect(200)['data']
        return next(column['id'] for column in columns if column['semantic_state'] == state)

    def task(self, project, title, **fields):
        return self.call('POST', f"/projects/{project['key']}/tasks", {'title': title, **fields}).expect(201)

    def task_action(self, task, action, body, **options):
        return self.call('POST', f"/tasks/{task['id']}/{action}", body, version=task['version'], **options)

    def agent(self, name, projects):
        body = {'name': name, 'project_ids': [project['id'] for project in projects]}
        return self.call('POST', '/agents', body).expect(201)

    def token(self, agent, projects, *, scopes=SCOPES):
        body = {'name': 'Synthetic acceptance', 'scopes': scopes,
                'project_ids': [project['id'] for project in projects]}
        return self.call('POST', f"/agents/{agent['id']}/tokens", body).expect(201)['token']

    def admit(self, project, token, body, *, key=None):
        return self.call('POST', f"/projects/{project['key']}/agent-work/admit", body, token=token, key=key)

    def resume_session(self, project, token, session):
        path = f"/projects/{project['key']}/agent-work/admit?session_id={session}"
        return self.call('GET', path, token=token)

    def snapshot(self, project):
        with closing(sqlite3.connect(f'{self.db_path.as_uri()}?mode=ro', uri=True)) as db:
            db.execute('BEGIN')
            db.row_factory = sqlite3.Row
            tables = {name for name, in db.execute("SELECT name FROM sqlite_schema WHERE type='table'")}
            result = {}
            for table, where in PROJECT_ROWS.items():
                if table == 'task_agent_admissions' and table not in tables:
                    # Pre-admission binaries have no admission table.
                    result[table] = []
                    continue
                rows = db.execute(f'SELECT * FROM {table} WHERE {where} ORDER BY rowid', (project['id'],))
                result[table] = [dict(row) for row in rows]
            return result

    def expire(self, task):
        # The only directly edited data is the timestamp in this synthetic DB.
        with closing(sqlite3.connect(self.db_path)) as db, db:
            result = db.execute("UPDATE tasks SET claim_expires_at=datetime('now','-1 minute') WHERE id=?",
                                (task['id'],))
            assert result.rowcount == 1, 'could not expire the synthetic lease'
