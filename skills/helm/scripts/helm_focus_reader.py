"""GET-only review budgets and scope-bound continuation envelopes."""
import base64
import json
import signal
import time
from contextlib import contextmanager
from helm_config import HelmError


class ReviewLimit(HelmError):
    pass


class ReviewReader:
    def __init__(self, client, request_limit, seconds):
        self.client = client
        self.config = getattr(client, 'config', None)
        self.limit = request_limit
        self.seconds = seconds
        self.started = time.monotonic()
        self.requests = 0

    def call(self, method, path, **kwargs):
        if method != 'GET' or kwargs:
            raise HelmError('Focus review permits GET requests only')
        if self.requests >= self.limit or time.monotonic() - self.started >= self.seconds:
            raise ReviewLimit('review request/time budget exhausted')
        self.requests += 1
        return self.client.call(method, path)

    @contextmanager
    def deadline(self):
        # The CLI runs on the main thread. Bound in-flight HTTP retries too,
        # rather than only checking time between requests. Restore any caller
        # timer, which matters when this module is imported by another tool.
        if not hasattr(signal, 'setitimer'):
            raise HelmError('Focus review requires a platform with bounded process timers')
        old_handler = signal.getsignal(signal.SIGALRM)
        old_timer = signal.getitimer(signal.ITIMER_REAL)
        def expired(*_args):
            raise ReviewLimit('review time budget exhausted')
        signal.signal(signal.SIGALRM, expired)
        signal.setitimer(signal.ITIMER_REAL, self.seconds)
        try:
            yield self
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            signal.signal(signal.SIGALRM, old_handler)
            if old_timer[0]:
                signal.setitimer(signal.ITIMER_REAL,
                                max(.001, old_timer[0] - (time.monotonic() - self.started)), old_timer[1])


def read_cursor(raw, project, focus):
    if not raw:
        return {}
    try:
        if len(raw) > 16000:
            raise ValueError()
        value = json.loads(base64.urlsafe_b64decode(raw.encode()))
        if (not isinstance(value, dict) or value.get('v') != 1
                or value.get('project') != project or value.get('focus', '') != focus
                or not isinstance(value.get('offset', 0), int) or value.get('offset', 0) < 0
                or not isinstance(value.get('queues', {}), dict)):
            raise ValueError()
        for field in ('focus_after', 'backlog_after', 'fingerprint'):
            if not isinstance(value.get(field, ''), str):
                raise ValueError()
        if not all(isinstance(k, str) and isinstance(v, str) for k, v in value.get('queues', {}).items()):
            raise ValueError()
        return value
    except (ValueError, TypeError, UnicodeError):
        raise HelmError('invalid Focus review cursor or project/Focus mismatch') from None


def write_cursor(project, focus, **state):
    result = base64.urlsafe_b64encode(json.dumps(
        {'v': 1, 'project': project, 'focus': focus, **state}, separators=(',', ':')).encode()).decode()
    if len(result) > 16000:
        raise HelmError('Focus continuation exceeded its safe size limit; narrow the review')
    return result
