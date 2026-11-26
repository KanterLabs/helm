"""The trusted worker's short-deadline client for Helm admission."""

from dataclasses import dataclass
from datetime import datetime
from email.utils import parsedate_to_datetime
from http.client import HTTPException
import json
from queue import Empty, SimpleQueue
import threading
import time
from urllib import error, parse, request
from uuid import uuid4

from helm_http import _NoRedirect


REQUEST_SECONDS = 2.0
MAX_RESPONSE_BYTES = 256 * 1024


class RunnerError(RuntimeError):
    """A bounded diagnostic which contains no credential or task text."""


@dataclass(frozen=True)
class Admission:
    task_id: str
    project_id: str
    fence: str
    version: int
    until: float


class Authority:
    def __init__(self, config, args):
        self.config = config
        self.args = args
        # Session/key input, stored receipts, and the ambient agent session are
        # intentionally never accepted. Each invocation can authorize one start.
        self.session = str(uuid4())
        self.path = '/projects/' + parse.quote(args.project, safe='') + '/agent-work/admit'

    def call(self, method, path, *, body=None, headers=None):
        req_headers = {
            'Accept': 'application/json',
            'Authorization': f'Bearer {self.config.token}',
            'Cache-Control': 'no-cache, no-store',
            'X-Helm-Agent-Session': self.session,
            'User-Agent': 'helm-worker-runner/1',
            **(headers or {}),
        }
        if self.config.cf_access_client_id:
            req_headers['CF-Access-Client-Id'] = self.config.cf_access_client_id
            req_headers['CF-Access-Client-Secret'] = self.config.cf_access_client_secret
        raw_body = None
        if body is not None:
            req_headers['Content-Type'] = 'application/json'
            raw_body = json.dumps(body, separators=(',', ':')).encode()
        req = request.Request(self.config.base_url + '/api/v1' + path, raw_body, req_headers, method=method)
        result = SimpleQueue()
        started = time.monotonic()

        def exchange():
            try:
                opener = request.build_opener(request.ProxyHandler({}), _NoRedirect)
                with opener.open(req, timeout=REQUEST_SECONDS) as response:
                    raw = response.read(MAX_RESPONSE_BYTES + 1)
                    if len(raw) > MAX_RESPONSE_BYTES:
                        raise RunnerError('Helm response exceeded the runner limit')
                    result.put((json.loads(raw), response.headers.get('Date', '')))
            except error.HTTPError as exc:
                exc.close()
                result.put(RunnerError(f'Helm returned HTTP {exc.code}'))
            except RunnerError as exc:
                result.put(exc)
            except (HTTPException, OSError, ValueError):
                result.put(RunnerError('Helm authority is unreachable or returned an invalid response'))

        # urllib's socket timeout is per read. The runner needs a hard request
        # budget even if a server or proxy sends a partial response very slowly.
        threading.Thread(target=exchange, daemon=True).start()
        try:
            value = result.get(timeout=REQUEST_SECONDS)
        except Empty:
            raise RunnerError('Helm authority exceeded the request deadline') from None
        if isinstance(value, Exception):
            raise value
        payload, server_date = value
        return payload, server_date, started

    def acquire(self):
        identity, _, _ = self.call('GET', '/auth/me')
        if not isinstance(identity, dict) or identity.get('kind') != 'agent' or identity.get('id') != self.args.actor:
            raise RunnerError('Helm identity did not match the configured worker actor')
        return self.admission(self.call('POST', self.path, headers={'Idempotency-Key': str(uuid4())}, body={
            'session_id': self.session,
            'task_key': self.args.task,
            'next_action': self.args.next_action,
            'finish_condition': self.args.finish_condition,
            'lease_seconds': max(30, self.args.run_seconds + 20),
        }))

    def current(self, previous):
        query = parse.urlencode({'session_id': self.session})
        current = self.admission(self.call('GET', self.path + '?' + query))
        if (current.task_id, current.project_id, current.fence) != (previous.task_id, previous.project_id, previous.fence):
            raise RunnerError('Helm changed the worker admission binding')
        return current

    def release(self, expected):
        current = self.current(expected)
        path = '/tasks/' + parse.quote(current.task_id, safe='') + '/release'
        self.call('POST', path, headers={
            'If-Match': f'"v{current.version}"', 'Idempotency-Key': str(uuid4()),
            'X-Helm-Agent-Fence': current.fence,
        })

    def admission(self, response):
        payload, server_date, started = response
        try:
            data = payload['data']
            task = data['task']
            task_id = task['id']
            project_id = task['project_id']
            version = task['version']
            fence = data['fence']
            if (data['session_id'] != self.session or task['claimed_by'] != self.args.actor
                    or task['key'].casefold() != self.args.task.casefold()
                    or not all(isinstance(value, str) and value for value in (task_id, project_id, fence))
                    or type(version) is not int or version < 1):
                raise ValueError
            expires = datetime.fromisoformat(task['claim_expires_at'].replace('Z', '+00:00'))
            server_clock = parsedate_to_datetime(server_date)
            # The HTTP date has one-second precision. Anchor to the sending
            # instant so network/transit time cannot lengthen the permit.
            until = started + (expires - server_clock).total_seconds() - 1.0
        except (AttributeError, KeyError, TypeError, ValueError, OverflowError):
            raise RunnerError('Helm returned an invalid worker admission binding') from None
        if until <= time.monotonic():
            raise RunnerError('Helm worker admission has expired')
        return Admission(task_id, project_id, fence, version, until)
