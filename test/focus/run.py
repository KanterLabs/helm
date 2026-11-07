#!/usr/bin/env python3
"""Real isolated Helm HTTP/CLI acceptance; saves only synthetic response receipts."""
import argparse
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
from urllib import request, error
import uuid
from journeys import run_journeys

ROOT = Path(__file__).resolve().parents[2]
CLI = ROOT / 'skills/helm/scripts/helm.py'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, default=ROOT / '.artifacts/focus-helm')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.output is None:
        args.output = ROOT / '.artifacts' / ('focus-' + uuid.uuid4().hex)
    assert not args.output.exists(), 'Use a fresh evidence path'
    args.output.mkdir(parents=True)
    receipt = {'scenarios': [], 'status': 'failed'}
    with tempfile.TemporaryDirectory(prefix='helm-focus-') as raw:
        base = Path(raw)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        url = f'http://127.0.0.1:{port}'
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('HELM_', 'ROADMAP_', 'TC_ROADMAP_', 'CODEX_'))}
        env.update(HELM_ADDR=f'127.0.0.1:{port}', HELM_DB=str(base / 'test.db'),
                   HELM_AUTH_MODE='disabled', HELM_RELEASE_SHA='0' * 40,
                   HELM_PUBLIC_ORIGIN=url)
        server = subprocess.Popen([str(args.binary.resolve())], cwd=base, env=env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def api(method, path, body=None, version=None, expected=200):
            headers = {'Content-Type': 'application/json', 'Origin': url,
                       'Idempotency-Key': str(uuid.uuid4())}
            if version is not None:
                headers['If-Match'] = f'"v{version}"'
            if path.endswith('/tokens'):
                headers.pop('Idempotency-Key')
            req = request.Request(url + '/api/v1' + path, method=method, headers=headers,
                                  data=json.dumps(body).encode() if body is not None else None)
            try:
                response = request.urlopen(req, timeout=5)
            except error.HTTPError as exc:
                response = exc
            with response:
                data = response.read()
                assert response.status == expected, (path, response.status, data[:300])
            return json.loads(data) if data else None
        def cli(*argv, ok=True):
            result = subprocess.run([sys.executable, str(CLI), *argv], cwd=base,
                                    env=cli_env, text=True, capture_output=True, timeout=40)
            assert (result.returncode == 0) == ok, (argv, result.stdout[:500], result.stderr[:500])
            return json.loads(result.stdout or result.stderr)
        try:
            for _ in range(150):
                assert server.poll() is None, 'test server stopped'
                try:
                    request.urlopen(url + '/readyz', timeout=.2).close()
                    break
                except (OSError, error.URLError):
                    time.sleep(.1)
            else:
                raise AssertionError('test server readiness timed out')
            project = api('POST', '/projects', {'key': 'SYN', 'name': 'Synthetic onboarding'}, expected=201)
            foreign = api('POST', '/projects', {'key': 'OTHER', 'name': 'Foreign project'}, expected=201)
            agent = api('POST', '/agents', {'name': 'Synthetic Focus CLI', 'project_ids': [project['id']]}, expected=201)
            # The token remains in memory and a temporary private config only.
            token = api('POST', '/agents/' + agent['id'] + '/tokens',
                        {'name': 'synthetic', 'scopes': ['tasks:read', 'tasks:write', 'tasks:claim',
                         'projects:read', 'events:read'], 'project_ids': [project['id']]}, expected=201)
            config = base / 'credentials.json'
            config.write_text(json.dumps({'base_url': url, 'token': token['token']}))
            config.chmod(0o600)
            cli_env = dict(env, HELM_CONFIG=str(config), HELM_SESSION_DIR=str(base / 'sessions'))
            focus = api('POST', '/projects/SYN/releases', {'name': 'Onboarding launch'}, expected=201)
            tasks = [api('POST', '/projects/SYN/tasks', {'title': title, 'release_id': focus['id']}, expected=201)
                     for title in ('Create account', 'First login', 'Welcome checklist')]
            backlog = api('POST', '/projects/SYN/tasks', {'title': 'Deferred acceptance clarification'}, expected=201)
            # Clocked existing data is changed only in this owned synthetic DB.
            with sqlite3.connect(base / 'test.db') as db:
                for task in tasks + [backlog]:
                    db.execute("UPDATE tasks SET created_at='2026-01-01T00:00:00Z' WHERE id=?", (task['id'],))
                    db.execute("UPDATE events SET created_at='2026-01-01T00:00:00Z' WHERE task_id=?", (task['id'],))
                before = db.execute('SELECT COUNT(*) FROM events').fetchone()[0]
            review = cli('focus', 'review', '--project', 'SYN')
            assert review['project']['id'] == project['id']
            assert any(item['reason'] == 'inactive' for item in review['findings']), review
            with sqlite3.connect(base / 'test.db') as db:
                assert db.execute('SELECT COUNT(*) FROM events').fetchone()[0] == before
            receipt['scenarios'].append({'name': 'read_only_review', 'response': review})
            bounded = cli('focus', 'review', '--project', 'SYN', '--history-limit', '1')
            assert bounded['coverage']['partial'] and bounded.get('next_cursor'), bounded
            following = cli('focus', 'review', '--project', 'SYN', '--history-limit', '1', '--cursor', bounded['next_cursor'])
            assert following['coverage']['histories_read'] == 1
            tiny = cli('focus', 'review', '--project', 'SYN', '--request-limit', '3')
            assert tiny['coverage']['partial'] and not tiny['next_cursor'], tiny
            assert tiny['resume']['restart_required'], tiny
            denied = cli('focus', 'review', '--project', 'OTHER', ok=False)
            receipt['scenarios'].append({'name': 'bounded_continuation_and_scope',
                                        'partial': bounded['coverage'], 'denied': denied})
            # New events must distinguish actual scope changes from metadata;
            # this real HTTP failure is recorded before adding that behavior.
            updated = api('PATCH', '/tasks/' + backlog['id'], {'priority': 'high'}, version=backlog['version'])
            history = api('GET', '/tasks/' + backlog['id'] + '/timeline')
            patch = next(row['change']['payload'] for row in history['data']
                         if row['kind'] == 'task_change' and row['change']['event_type'] == 'task.updated')
            assert patch.get('changed_fields') == [], patch
            updated = api('PATCH', '/tasks/' + backlog['id'], {'description': 'Acceptance: a concrete next action'}, version=updated['version'])
            fresh = cli('focus', 'review', '--project', 'SYN')
            evidence = next(t['activity'] for t in fresh['tasks'] if t['id'] == backlog['id'])
            assert evidence['source'] == 'task.scope_or_state_changed', evidence
            receipt['scenarios'].append({'name': 'scope_vs_metadata_events', 'activity': evidence})
            receipt['scenarios'].extend(run_journeys(
                api, cli, base / 'test.db', project, focus, tasks, backlog))
            receipt['status'] = 'passed'
        finally:
            server.terminate()
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()
            (args.output / 'result.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({'status': receipt['status'], 'receipt': str(args.output / 'result.json')}))


if __name__ == '__main__':
    main()
