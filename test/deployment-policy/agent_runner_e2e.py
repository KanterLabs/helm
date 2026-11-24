#!/usr/bin/env python3
"""Exercise Helm admission and the real isolated Docker worker with synthetic tasks."""

import argparse
import hashlib
import http.cookiejar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tempfile
import threading
import time
from urllib import error, request


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / 'skills/helm/scripts/helm_runner.py'
DOCKER = ['docker', '--host', 'unix:///var/run/docker.sock']
PROJECT = 'RUNE2E'
CANARY = r'''
import hashlib, os, pathlib, subprocess, sys, time
assert os.getuid() == 65534
assert 'HELM_TOKEN' not in os.environ and 'HELM_CF_ACCESS_CLIENT_SECRET' not in os.environ
assert not pathlib.Path('/var/run/docker.sock').exists()
print('worker-task=' + os.environ['HELM_AGENT_TASK_KEY'], flush=True)
print('worker-session=' + os.environ['HELM_AGENT_SESSION_ID'], flush=True)
p = pathlib.Path('/tmp/runner-fixture')
p.write_bytes(b'helm-runner-e2e')
print('canary=' + hashlib.sha256(p.read_bytes()).hexdigest(), flush=True)
if sys.argv[1] == 'hold':
    subprocess.Popen([sys.executable, '-u', '-c',
        "import time\nwhile True:\n print('descendant-tick', flush=True)\n time.sleep(.12)"], start_new_session=True)
    time.sleep(60)
'''


def eventually(predicate, timeout=10):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if result := predicate():
            return result
        time.sleep(.04)
    raise AssertionError('fixture did not reach the expected state within its deadline')


def docker(*args):
    return subprocess.run([*DOCKER, *args], capture_output=True, text=True, timeout=8)


class Journey:
    def __init__(self, origin, image, directory):
        self.origin = origin
        self.image = image
        self.directory = directory
        self.owner = request.build_opener(request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        owner = {'email': 'runner@example.test', 'password': 'synthetic-runner-owner-password'}
        self.call('POST', '/auth/setup', {**owner, 'name': 'Runner E2E Owner'}, 201)
        self.call('POST', '/auth/login', owner)
        project = self.call('POST', '/projects', {'key': PROJECT, 'name': 'Runner E2E'}, 201)
        self.project = project['id']
        self.actor = self.call('POST', '/agents', {'name': 'Synthetic runner', 'project_ids': [self.project]}, 201)['id']
        token = self.call('POST', f'/agents/{self.actor}/tokens', {
            'name': 'Synthetic runner token', 'project_ids': [self.project],
            'scopes': ['projects:read', 'tasks:read', 'tasks:write', 'tasks:claim'],
        }, 201)['token']
        self.env = {
            'PATH': os.environ['PATH'], 'HELM_CONFIG': str(directory / 'no-existing-credentials.json'),
            'HELM_URL': origin, 'HELM_TOKEN': token,
            'HELM_CF_ACCESS_CLIENT_ID': 'synthetic-edge-id', 'HELM_CF_ACCESS_CLIENT_SECRET': 'synthetic-edge-secret',
            'HELM_AGENT_SESSION_ID': 'ambient-must-not-be-reused',
        }
        self.processes = []
        self.containers = []
        self.receipts = []

    def call(self, method, path, data=None, status=200, *, version=None, bearer=False, session=None, fence=None):
        headers = {'Origin': self.origin, 'Content-Type': 'application/json'}
        if version:
            headers['If-Match'] = f'"v{version}"'
        if bearer:
            headers['Authorization'] = 'Bearer ' + self.env['HELM_TOKEN']
        if session:
            headers['X-Helm-Agent-Session'] = session
            headers['X-Helm-Agent-Fence'] = fence
        payload = json.dumps(data).encode() if data is not None else None
        req = request.Request(self.origin + '/api/v1' + path, payload, headers, method=method)
        try:
            response = (request.build_opener() if bearer else self.owner).open(req, timeout=3)
        except error.HTTPError as exc:
            assert exc.code == status, f'{method} {path} returned {exc.code}, expected {status}'
            return {}
        with response:
            assert response.status == status, f'{method} {path} returned {response.status}, expected {status}'
            raw = response.read()
            return json.loads(raw) if raw else {}

    def task(self, name):
        return self.call('POST', f'/projects/{self.project}/tasks', {'title': name}, 201)

    def launch(self, label, task, *, hold=False, duration=12, actor=None):
        trace = self.directory / (label + '.log')
        with trace.open('wb') as output:
            child = subprocess.Popen([
                sys.executable, str(RUNNER), '--project', PROJECT, '--actor', actor or self.actor,
                '--task', task['key'], '--next-action', 'Verify the synthetic file digest',
                '--finish-condition', 'Print the synthetic digest', '--run-seconds', str(duration),
                '--image', self.image, '--', '/usr/local/bin/python3', '-u', '-c', CANARY,
                'hold' if hold else 'exit',
            ], env=self.env, stdout=output, stderr=subprocess.STDOUT)
        self.processes.append(child)
        return child, trace

    def live_session(self, trace):
        eventually(lambda: 'descendant-tick' in trace.read_text())
        match = re.search(r'worker-session=([0-9a-f-]{36})', trace.read_text())
        assert match, 'workload did not receive the per-run session'
        name = 'helm-agent-runner-' + match[1].replace('-', '')
        self.containers.append(name)
        assert docker('inspect', '--format', '{{.State.Running}}', name).stdout.strip() == 'true'
        return match[1], name

    def revoke(self, task, session):
        admission = self.call('GET', f'/projects/{self.project}/agent-work/admit?session_id={session}', bearer=True)['data']
        self.call('POST', '/tasks/' + task['id'] + '/release', version=admission['task']['version'],
                  bearer=True, session=session, fence=admission['fence'])

    def admitted(self):
        task = self.task('Execute digest verification')
        child, trace = self.launch('admitted', task)
        assert child.wait(15) == 0, trace.read_text()
        value = trace.read_text()
        digest = hashlib.sha256(b'helm-runner-e2e').hexdigest()
        assert f'worker-task={task["key"]}' in value and f'canary={digest}' in value, value
        assert 'ambient-must-not-be-reused' not in value
        released = self.call('GET', '/tasks/' + task['id'])
        assert 'claimed_by' not in released, 'runner did not release its finished claim'
        self.receipts.append({'journey': 'admitted', 'completed': True, 'output': value.splitlines()})

    def denied_actor(self):
        task = self.task('Reject a mismatched token actor')
        child, trace = self.launch('denied-actor', task, actor='wrong-synthetic-actor')
        assert child.wait(8) != 0, trace.read_text()
        assert 'canary=' not in trace.read_text(), trace.read_text()
        assert 'claimed_by' not in self.call('GET', '/tasks/' + task['id'])
        self.receipts.append({'journey': 'actor-mismatch', 'canary_absent': True, 'unclaimed': True})

    def denied_current(self):
        task = self.task('Deny start when a successful POST has no live confirmation')
        post_receipts = []
        origin = self.origin

        class DenyLiveConfirmation(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def forward(self):
                headers = {name: self.headers[name] for name in self.headers if name.lower() != 'host'}
                raw = self.rfile.read(int(self.headers.get('Content-Length', '0'))) or None
                req = request.Request(origin + self.path, raw, headers, method=self.command)
                try:
                    response = request.urlopen(req, timeout=2)
                except error.HTTPError as exc:
                    response = exc
                with response:
                    payload = response.read()
                    if self.command == 'POST' and response.status == 200:
                        post_receipts.append(json.loads(payload)['data'])
                    self.send_response_only(response.status)
                    self.send_header('Date', response.headers['Date'])
                    self.end_headers()
                    self.wfile.write(payload)

            def do_GET(self):
                if '/agent-work/admit?' in self.path:
                    self.send_response(404)
                    self.end_headers()
                    return
                self.forward()

            do_POST = forward

        with ThreadingHTTPServer(('127.0.0.1', 0), DenyLiveConfirmation) as proxy:
            thread = threading.Thread(target=proxy.serve_forever, daemon=True)
            thread.start()
            self.env['HELM_URL'] = f'http://127.0.0.1:{proxy.server_port}'
            try:
                child, trace = self.launch('denied-current', task)
                assert child.wait(10) != 0, trace.read_text()
                assert len(post_receipts) == 1, 'Helm did not issue the tested POST receipt'
                assert 'canary=' not in trace.read_text(), trace.read_text()
                session = post_receipts[0]['session_id']
                self.containers.append('helm-agent-runner-' + session.replace('-', ''))
                self.revoke(task, session)
            finally:
                self.env['HELM_URL'] = self.origin
                proxy.shutdown()
                thread.join()
        self.receipts.append({'journey': 'post-without-live-confirmation', 'post_accepted': True, 'canary_absent': True})

    def revoked(self):
        task = self.task('Stop running work when its authority is revoked')
        child, trace = self.launch('revoked', task, hold=True)
        session, name = self.live_session(trace)
        self.env['HELM_AGENT_SESSION_ID'] = session
        duplicate, duplicate_trace = self.launch('duplicate', task)
        assert duplicate.wait(8) != 0, duplicate_trace.read_text()
        assert 'canary=' not in duplicate_trace.read_text(), duplicate_trace.read_text()
        self.env['HELM_AGENT_SESSION_ID'] = 'ambient-must-not-be-reused'
        self.revoke(task, session)
        start = time.monotonic()
        assert child.wait(8) != 0, trace.read_text()
        assert time.monotonic() - start < 6, 'runner was too slow to stop on authority loss'
        assert docker('inspect', name).returncode != 0, 'revoked workload container survived'
        value = trace.read_text()
        time.sleep(.25)
        assert trace.read_text() == value, 'a descendant continued after revocation'
        self.receipts.append({'journey': 'revoked', 'descendant_ran': True, 'container_removed': True,
                              'duplicate_canary_absent': True, 'launcher_exit': child.returncode})

    def host_lost(self):
        task = self.task('Watchdog stops an orphaned worker')
        child, trace = self.launch('orphaned', task, hold=True)
        session, name = self.live_session(trace)
        child.kill()
        child.wait(5)
        eventually(lambda: docker('inspect', '--format', '{{.State.Running}}', name).stdout.strip() == 'false', 7)
        self.revoke(task, session)
        self.receipts.append({'journey': 'launcher-killed', 'container_stopped': True})

    def expires(self):
        task = self.task('Enforce a fixed runtime deadline')
        child, trace = self.launch('expired', task, hold=True, duration=2)
        _, name = self.live_session(trace)
        assert child.wait(8) != 0, trace.read_text()
        assert docker('inspect', name).returncode != 0, 'expired workload container survived'
        self.receipts.append({'journey': 'execution-deadline', 'container_removed': True})
        return task

    def disconnected(self, task, server):
        child, trace = self.launch('authority-disconnected', task, hold=True)
        _, name = self.live_session(trace)
        server.terminate()
        server.wait(8)
        assert child.wait(8) != 0, trace.read_text()
        assert docker('inspect', name).returncode != 0, 'disconnected workload container survived'
        self.receipts.append({'journey': 'authority-disconnected', 'container_removed': True})

    def close(self):
        for child in self.processes:
            if child.poll() is None:
                child.terminate()
                child.wait(8)
        for name in self.containers:
            docker('rm', '--force', '--volumes', name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--image', default='python:3.11-bookworm', help='locally installed Python fixture image')
    parser.add_argument('--artifact', required=True, type=Path)
    args = parser.parse_args()
    inspected = docker('image', 'inspect', '--format', '{{.Id}}', args.image)
    assert inspected.returncode == 0, 'fixture Python image must already be present locally'
    with tempfile.TemporaryDirectory(prefix='helm-agent-runner-e2e-') as temporary:
        directory = Path(temporary)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        env = {
            'PATH': os.environ['PATH'], 'HELM_ADDR': f'127.0.0.1:{port}',
            'HELM_DB': str(directory / 'helm.db'), 'HELM_AUTH_MODE': 'local',
            'HELM_PUBLIC_ORIGIN': origin, 'HELM_SECURE_COOKIES': 'false', 'HELM_DEMO_SEED': 'false',
        }
        with (directory / 'server.log').open('wb') as log:
            server = subprocess.Popen([str(args.binary.resolve(strict=True))], env=env, stdout=log, stderr=log)
            try:
                def ready():
                    try:
                        with request.urlopen(origin + '/readyz', timeout=.5) as response:
                            return response.status == 200
                    except OSError:
                        return False

                eventually(ready, 15)
                journey = Journey(origin, inspected.stdout.strip(), directory)
                try:
                    journey.admitted()
                    journey.denied_actor()
                    journey.denied_current()
                    journey.revoked()
                    journey.host_lost()
                    released_task = journey.expires()
                    journey.disconnected(released_task, server)
                    args.artifact.parent.mkdir(parents=True, exist_ok=True)
                    args.artifact.write_text(json.dumps(journey.receipts, indent=2) + '\n')
                finally:
                    journey.close()
            finally:
                server.terminate()
                server.wait(8)


if __name__ == '__main__':
    main()
