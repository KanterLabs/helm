#!/usr/bin/env python3
"""Prepare a main release from deployed beta; publish only after production verifies."""
from __future__ import annotations

import argparse
import fcntl
import os
from pathlib import Path
import subprocess
import sys

from release_finish import finish
from release_prepare import prepare
from release_support import ReleaseError, Runtime


def publish_beta(runtime):
    if runtime.git('status', '--porcelain'):
        raise ReleaseError('Commit or move local changes before publishing beta; nothing was staged')
    head = runtime.git('rev-parse', 'HEAD')
    runtime.fetch()
    tips = {remote: runtime.ref(remote, 'beta') for remote in ['gitea', 'origin']}
    if any(not value or not runtime.ancestor(value, head) for value in tips.values()):
        raise ReleaseError('Local HEAD must contain both current beta tips; merge first, never force-push')
    # Resume a partial canonical-first push only when canonical already equals HEAD.
    if tips['gitea'] != tips['origin'] and tips['gitea'] != head:
        raise ReleaseError('Beta remotes differ outside a matching partial publication')
    for remote in ['gitea', 'origin']:
        if tips[remote] != head:
            runtime.git('push', remote, head + ':refs/heads/beta')
    print('Published beta candidate:', head)
    print('Wait for beta CI/deployment, then run make prepare-release VERSION=vX.Y.Z.')


def status(runtime):
    for branch, lane in [('beta', 'beta'), ('main', 'production')]:
        sha = runtime.pair(branch)
        print(branch + ': ' + sha)
        try:
            runtime.deployed(lane, sha)
            print('  CI passed; exact revision is deployed and ready.')
        except ReleaseError as error:
            print('  Not ready: ' + str(error))
    print('\n1. make publish-beta                 # publish committed local work to both remotes')
    print('2. make prepare-release VERSION=vX.Y.Z # prepare version/notes and a PR into main')
    print('3. Review backup/compatibility evidence and merge the PR after checks pass.')
    print('4. make finish-release VERSION=vX.Y.Z  # verify production and publish the version')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    commands.add_parser('status', help='show remote, CI and deployed revisions')
    commands.add_parser('beta', help='publish clean committed local HEAD to beta on both hosts')
    preparation = commands.add_parser('prepare', help='prepare a versioned release PR into main')
    preparation.add_argument('version')
    preparation.add_argument('--dry-run', action='store_true', help='verify inputs and show the plan only')
    preparation.add_argument('--notes', help='reviewed Markdown notes; defaults to beta unreleased notes')
    publication = commands.add_parser('finish', help='publish the version after its main deployment succeeds')
    publication.add_argument('version')
    args = parser.parse_args()
    if not os.environ.get('GITEA_TOKEN'):
        helper = Path.home() / '.codex/hooks/infisical_access.py'
        if not helper.is_file():
            parser.error('Install the Infisical access helper or inject GITEA_TOKEN into this process')
        # The helper injects only the named secret. Never use a human CLI session.
        return subprocess.call([sys.executable, str(helper), '--project', 'homelab',
            '--environment', 'lab', '--path', '/gitea', 'run', '--inject', 'gitea_token=GITEA_TOKEN',
            '--', sys.executable, str(Path(__file__).resolve()), *sys.argv[1:]])
    try:
        runtime = Runtime()
        # Serialize local operations across linked worktrees; remote protections
        # and fast-forward pushes still guard other machines/operators.
        with (runtime.common / 'helm-release.lock').open('a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise ReleaseError('Another release command is running in this repository') from None
            if args.command == 'status':
                status(runtime)
            elif args.command == 'beta':
                publish_beta(runtime)
            elif args.command == 'prepare':
                prepare(runtime, args.version, dry_run=args.dry_run, notes_file=args.notes)
            else:
                finish(runtime, args.version)
        return 0
    except (ReleaseError, ValueError, KeyError, OSError) as error:
        # No subprocess stderr or remote response body is emitted here.
        print('Release stopped: ' + str(error), file=sys.stderr)
        return 1
    except (TypeError, AttributeError, IndexError):
        print('Release stopped: a service returned an unexpected response shape.', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
