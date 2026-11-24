#!/usr/bin/env python3
"""Run one Docker workload under a checked, task-bound Helm admission."""

import argparse
import re
import signal
import sys
import threading
import time

from helm_config import HelmError, load_config
from helm_runner_authority import Authority, RunnerError
from helm_runner_container import Worker


CHECK_SECONDS = .75


def argument_parser():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--project', required=True)
    parser.add_argument('--task', required=True, help='canonical key of an existing task')
    parser.add_argument('--actor', required=True, help='expected Helm agent actor ID')
    parser.add_argument('--next-action', required=True)
    parser.add_argument('--finish-condition', required=True)
    parser.add_argument('--run-seconds', required=True, type=int, metavar='1–300')
    parser.add_argument('--image', required=True, help='trusted local immutable image ID (sha256:...)')
    parser.add_argument('workload', nargs=argparse.REMAINDER, help='-- COMMAND ARG ...')
    return parser


def execute(args, authority, interrupt):
    approved = None
    worker = Worker(args, authority.session)
    result = 1
    try:
        if interrupt.is_set():
            raise RunnerError('worker was cancelled before admission')
        approved = authority.acquire()
        if interrupt.is_set():
            raise RunnerError('worker was cancelled before launch')
        worker.prepare()
        active = authority.current(approved)
        expires = time.monotonic() + args.run_seconds
        if interrupt.is_set():
            raise RunnerError('worker was cancelled before launch')
        worker.start()
        while True:
            remaining = min(expires, active.until) - time.monotonic()
            if remaining <= 0 or interrupt.is_set():
                raise RunnerError('worker deadline reached or execution was cancelled')
            worker.permit(min(expires, active.until))
            exit_code = worker.result(min(remaining, CHECK_SECONDS))
            if exit_code is not None:
                # Do not report success if authority disappeared just before
                # exit, before the next periodic check could observe the loss.
                if exit_code == 0:
                    authority.current(approved)
                result = exit_code if exit_code >= 0 else 128 - exit_code
                break
            active = authority.current(approved)
    except RunnerError as exc:
        print(f'Helm worker stopped: {exc}', file=sys.stderr)
    finally:
        try:
            worker.stop()
        except RunnerError as exc:
            # Keep the claim reserved until expiry if Docker cannot confirm
            # stop. The independent in-container permit still expires.
            print(f'Helm worker cleanup: {exc}', file=sys.stderr)
            result = 1
        else:
            if approved is not None:
                try:
                    authority.release(approved)
                except RunnerError as exc:
                    print(f'Helm worker could not confirm claim release: {exc}', file=sys.stderr)
                    if result == 0:
                        result = 1
    return result


def main():
    parser = argument_parser()
    args = parser.parse_args()
    if not 1 <= args.run_seconds <= 300:
        parser.error('--run-seconds must be between 1 and 300')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', args.image):
        parser.error('--image must be a full local sha256 image ID')
    if len(args.workload) < 2 or args.workload[0] != '--':
        parser.error('provide a workload after --')
    args.workload = args.workload[1:]
    try:
        config = load_config()
        if config.url_source == 'default':
            raise RunnerError('configure the trusted Helm origin explicitly')
        interrupt = threading.Event()
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(signum, lambda *_: interrupt.set())
        return execute(args, Authority(config, args), interrupt)
    except (HelmError, RunnerError):
        print('Helm worker refused: validate the configured Helm origin and credentials', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
