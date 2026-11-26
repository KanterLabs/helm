"""Local Docker isolation and the independent workload liveness guard."""

import os
import subprocess
import time

from helm_runner_authority import RunnerError


PERMIT_SECONDS = 4.0
DOCKER = ['docker', '--host', 'unix:///var/run/docker.sock']
DOCKER_ENV = {'PATH': os.defpath, 'LANG': 'C'}

# Run this code as PID 1 of a private PID namespace. The child runs without
# privilege and does not inherit the permit pipe. When PID 1 exits Linux
# terminates every process in this namespace, including detached descendants.
GUARD = '''
import math, os, select, subprocess, sys, time

def guard(max_duration, permit_duration, command):
    if os.getpid() != 1 or os.getuid() != 0:
        return 125
    until = time.monotonic() + permit_duration
    hard_deadline = math.inf
    pending = b''
    child = None
    while True:
        remaining = min(hard_deadline, until) - time.monotonic()
        if remaining <= 0:
            return 124
        if child is not None and child.poll() is not None:
            status = child.returncode
            return status if status >= 0 else 128 - status
        readable, _, _ = select.select([0], [], [], min(remaining, 0.1))
        if not readable:
            continue
        chunk = os.read(0, 128)
        if not chunk:
            return 124
        pending += chunk
        while b'\\n' in pending:
            frame, pending = pending.split(b'\\n', 1)
            deadline = float(frame)
            now = time.monotonic()
            if not math.isfinite(deadline) or now >= min(deadline, until, hard_deadline):
                return 124
            until = min(deadline, now + permit_duration)
            if child is None:
                hard_deadline = now + max_duration
                child = subprocess.Popen(command, stdin=subprocess.DEVNULL, user=65534, group=65534,
                                         extra_groups=(), umask=0o077)
        if len(pending) > 64:
            return 125

try:
    sys.exit(guard(int(sys.argv[1]), float(sys.argv[2]), sys.argv[3:]))
except (OSError, ValueError):
    print('isolated worker could not start', file=sys.stderr)
    sys.exit(125)
'''


class Worker:
    def __init__(self, args, session):
        self.args = args
        self.session = session
        self.name = 'helm-agent-runner-' + session.replace('-', '')
        self.attempted = False
        self.process = None

    def prepare(self):
        self.attempted = True
        result = self.docker(
            'create', '--name', self.name, '--pull', 'never', '--platform', 'linux',
            '--interactive', '--init=false', '--restart', 'no', '--no-healthcheck',
            '--network', 'none', '--ipc', 'private', '--cgroupns', 'private', '--read-only',
            '--tmpfs', '/tmp:rw,nosuid,nodev,noexec,mode=1777,size=64m', '--pids-limit', '128',
            '--memory', '512m', '--cpus', '1', '--log-driver', 'none', '--user', '0:0',
            '--cap-drop', 'ALL', '--cap-add', 'SETUID', '--cap-add', 'SETGID',
            '--security-opt', 'no-new-privileges:true',
            '--env', 'HELM_AGENT_SESSION_ID=' + self.session,
            '--env', 'HELM_AGENT_TASK_KEY=' + self.args.task,
            '--env', 'HELM_AGENT_ACTOR_ID=' + self.args.actor,
            '--env', 'HELM_AGENT_PROJECT=' + self.args.project,
            '--entrypoint', '/usr/local/bin/python3', self.args.image,
            '-I', '-S', '-u', '-c', GUARD, str(self.args.run_seconds), str(PERMIT_SECONDS), *self.args.workload,
        )
        if result.returncode:
            raise RunnerError('Docker refused to prepare the isolated worker')

    def start(self):
        try:
            self.process = subprocess.Popen(
                [*DOCKER, 'start', '--attach', '--interactive', self.name], env=DOCKER_ENV,
                stdin=subprocess.PIPE, bufsize=0, start_new_session=True,
            )
            os.set_blocking(self.process.stdin.fileno(), False)
        except OSError:
            raise RunnerError('Docker refused to start the isolated worker') from None

    def permit(self, expires):
        deadline = min(expires, time.monotonic() + PERMIT_SECONDS)
        if deadline <= time.monotonic():
            raise RunnerError('worker deadline reached')
        try:
            packet = f'{deadline:.6f}\n'.encode()
            if self.process.stdin.write(packet) != len(packet):
                raise RunnerError('the worker authorization channel could not accept a permit')
        except OSError:
            # The child may have exited while Helm was responding to a check.
            try:
                self.process.wait(timeout=.2)
            except subprocess.TimeoutExpired:
                raise RunnerError('the worker lost its authorization channel') from None

    def result(self, timeout):
        try:
            return self.process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            return None

    def stop(self):
        if self.process is not None:
            self.process.stdin.close()
        try:
            if self.attempted:
                removed = self.docker('rm', '--force', '--volumes', self.name)
                if removed.returncode and 'No such container:' not in removed.stderr:
                    raise RunnerError('Docker did not confirm worker removal')
        finally:
            if self.process is not None and self.result(1.0) is None:
                self.process.kill()
                self.process.wait()

    @staticmethod
    def docker(*args):
        try:
            return subprocess.run([*DOCKER, *args], env=DOCKER_ENV, stdin=subprocess.DEVNULL,
                                  capture_output=True, text=True, timeout=5, check=False)
        except (OSError, subprocess.TimeoutExpired):
            raise RunnerError('Docker is unavailable or exceeded the worker operation deadline') from None
