"""Bounded ownership-aware cleanup for nested runtime suite supervisors."""
import ctypes
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


class OwnedProcesses:
    def __init__(self, grace):
        self.grace = grace
        self.children = []
        self.groups = set()
        # Linux reparents orphaned grandchildren to this coordinator. This is
        # essential when run-browsers creates separate worker sessions and dies.
        self.subreaper = sys.platform.startswith("linux")
        if self.subreaper:
            libc = ctypes.CDLL(None, use_errno=True)
            if libc.prctl(36, 1, 0, 0, 0) != 0:  # PR_SET_CHILD_SUBREAPER
                raise OSError(ctypes.get_errno(), "cannot supervise orphaned fixtures")

    def start(self, command, env, log):
        process = subprocess.Popen(command, env=env, stdout=log,
                                   stderr=subprocess.STDOUT, start_new_session=True)
        self.children.append(process)
        self.groups.add(process.pid)
        return process

    def discover(self):
        if not self.subreaper:
            return
        rows = {}
        for path in Path("/proc").glob("[0-9]*/stat"):
            try:
                # Process names may contain spaces and parentheses.
                values = path.read_text().rsplit(")", 1)[1].split()
                rows[int(path.parent.name)] = (int(values[1]), int(values[2]))
            except (OSError, ValueError, IndexError):
                continue
        owned = {os.getpid()}
        while True:
            descendants = {pid for pid, (parent, _) in rows.items() if parent in owned}
            if descendants <= owned:
                break
            owned.update(descendants)
        own_group = os.getpgrp()
        # Use the current descendant snapshot as authority. Remembering a dead
        # group forever would risk signaling an unrelated process after PID reuse.
        self.groups = {group for pid, (_, group) in rows.items()
                       if pid in owned and pid != os.getpid() and group != own_group}

    def signal(self, signum, groups=None):
        for group in self.groups if groups is None else groups:
            try:
                os.killpg(group, signum)
            except ProcessLookupError:
                pass

    def reap(self):
        direct = {child.pid for child in self.children}
        for child in self.children:
            child.poll()
        if self.subreaper:
            # Poll Popen children only through Popen. waitpid(-1) can race an
            # exiting direct child and replace its real failure code with zero.
            for path in Path("/proc").glob("[0-9]*/stat"):
                pid = int(path.parent.name)
                if pid in direct:
                    continue
                try:
                    fields = path.read_text().rsplit(")", 1)[1].split()
                    if int(fields[1]) == os.getpid():
                        os.waitpid(pid, os.WNOHANG)
                except (OSError, ValueError, IndexError):
                    continue

    def live_groups(self):
        live = set()
        for group in self.groups:
            try:
                os.killpg(group, 0)
                live.add(group)
            except ProcessLookupError:
                pass
        return live

    def cleanup(self):
        self.discover()
        self.signal(signal.SIGTERM)
        deadline = time.monotonic() + self.grace
        while self.live_groups() and time.monotonic() < deadline:
            self.discover()
            self.reap()
            time.sleep(.025)
        self.discover()
        self.signal(signal.SIGKILL)
        for child in self.children:
            child.wait(timeout=5)
        deadline = time.monotonic() + 5
        while self.live_groups() and time.monotonic() < deadline:
            self.discover()
            self.signal(signal.SIGKILL)
            self.reap()
            time.sleep(.025)
        self.reap()
        if self.live_groups():
            raise RuntimeError("owned fixture process group survived cleanup")
        self.groups.clear()
        self.children.clear()


def supervise(command, grace):
    """Keep a suite slot occupied until all its nested runtime groups stop."""
    owner = OwnedProcesses(grace)
    interrupted = 0

    def interrupt(signum, _frame):
        nonlocal interrupted
        interrupted = signum

    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGINT, interrupt)
    process = None
    result = 1
    try:
        process = owner.start(command, dict(os.environ), None)
        while process.poll() is None and not interrupted:
            owner.discover()
            time.sleep(.025)
        result = process.returncode if process.returncode is not None else 128 + interrupted
    finally:
        owner.cleanup()
    return 128 + interrupted if interrupted else result


if __name__ == "__main__":
    # Internal subprocess boundary: no shell parsing or argument reinterpretation.
    sys.exit(supervise(sys.argv[2:], float(sys.argv[1])))
