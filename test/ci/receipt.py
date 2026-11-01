#!/usr/bin/env python3
"""Create source-bound validation receipts; receipts alone never authorize promotion."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

REPOSITORY = 'KanterLabs/helm'
WORKFLOW = '.github/workflows/ci.yml'
SHA = re.compile(r'[0-9a-f]{40}\Z')
DIGEST = re.compile(r'[0-9a-f]{64}\Z')
IMAGE = re.compile(r'ghcr\.io/kanterlabs/helm@sha256:[0-9a-f]{64}\Z')
JOBS = ('checks', 'browser', 'race', 'container')
LIMIT = 2_000_000


class ReceiptError(Exception):
    """A bounded, credential-free reason that trusted proof is unavailable."""


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()


def sha256(value):
    return hashlib.sha256(value).hexdigest()


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ReceiptError('duplicate JSON fields')
            result[key] = value
        return result
    try:
        if len(raw) > LIMIT:
            raise ReceiptError('JSON proof is oversized')
        return json.loads(raw, object_pairs_hook=pairs)
    except (ValueError, UnicodeError, TypeError):
        raise ReceiptError('invalid JSON proof') from None


def read_json(path):
    try:
        with Path(path).open('rb') as stream:
            return strict_json(stream.read(LIMIT + 1))
    except OSError:
        raise ReceiptError('JSON proof is unavailable') from None


def command(args, root=None):
    try:
        result = subprocess.run(args, cwd=root, capture_output=True, timeout=60)
    except (OSError, subprocess.TimeoutExpired):
        raise ReceiptError('required local command unavailable') from None
    if result.returncode or len(result.stdout) > LIMIT:
        # Never include command stderr: authentication tools may print credentials.
        raise ReceiptError('required local command failed or exceeded output limit')
    return result.stdout


def git(root, *args):
    return command(['git', '-C', str(root), *args]).decode().strip()


def tracked(root, sha):
    return git(root, 'ls-tree', '-r', '--name-only', sha).splitlines()


def snapshot(root):
    sha = git(root, 'rev-parse', '--verify', 'HEAD')
    tree = git(root, 'rev-parse', '--verify', 'HEAD^{tree}')
    if not SHA.fullmatch(sha) or not SHA.fullmatch(tree):
        raise ReceiptError('actual checkout has invalid source identity')
    if git(root, 'diff', '--name-only', 'HEAD', '--'):
        raise ReceiptError('tracked checkout is dirty')
    return {'sha': sha, 'tree': tree}


def inventory(root, sha):
    specs = ['e2e/' + name.removeprefix('web/e2e/') for name in tracked(root, sha)
             if name.startswith('web/e2e/') and name.endswith('.spec.ts')]
    if not specs:
        raise ReceiptError('full browser inventory is missing')
    return sorted(specs + ['github'])


def source_inputs(root, sha):
    paths = tracked(root, sha)
    dependencies = ['go.mod', 'go.sum', 'web/package.json', 'web/package-lock.json']
    if any(path not in paths for path in dependencies):
        raise ReceiptError('dependency inputs are missing')
    policies = [path for path in paths if path.startswith(('.github/', 'test/', 'deploy/'))
                or path in ['Dockerfile', 'Makefile', '.dockerignore', 'VERSION']]
    def hashes(selected):
        return {path: sha256(command(['git', '-C', str(root), 'show', sha + ':' + path]))
                for path in sorted(selected)}
    return {'dependencies': hashes(dependencies), 'policies': hashes(policies)}


def build_inputs(settings_path):
    settings = read_json(settings_path)
    allowed = {'goos', 'goarch', 'cgo_enabled', 'go_flags', 'ldflags', 'node_options',
               'docker_platform', 'build_args', 'frontend_command', 'go_command'}
    if not isinstance(settings, dict) or not settings or set(settings) - allowed:
        raise ReceiptError('build settings need explicit known nonsecret fields')
    for key, value in settings.items():
        if key == 'build_args':
            if (not isinstance(value, dict) or set(value) - {'CODEX_VERSION', 'TARGETARCH',
                    'TARGETOS', 'BUILDPLATFORM', 'TARGETPLATFORM'}
                    or any(not isinstance(v, str) or len(v) > 300 for v in value.values())):
                raise ReceiptError('unrecognized or invalid nonsecret build arguments')
        elif not isinstance(value, str) or len(value) > 1000:
            raise ReceiptError('invalid build setting')
    versions = {}
    for tool, args in {'go': ['version'], 'node': ['--version'], 'npm': ['--version'],
                       'docker': ['--version']}.items():
        raw = command([tool, *args]).decode().strip()
        if not raw or len(raw) > 300:
            raise ReceiptError('invalid toolchain version response')
        versions[tool] = raw
    return {'settings': settings, 'toolchain': versions}


def verify_validation(value, source, expected):
    if (not isinstance(value, dict) or value.get('format') != 1
            or value.get('accepted') is not True or value.get('passed') is not True
            or value.get('scope') != 'full' or value.get('effective_scope') != 'full'):
        raise ReceiptError('accepted full validation is required')
    if (value.get('source', {}).get('sha') != source['sha']
            or value.get('source', {}).get('tree') != source['tree']
            or value.get('source_sha') != source['sha']
            or value.get('source_tree') != source['tree']):
        raise ReceiptError('validation does not bind actual checkout')
    for field in ['selected_suites', 'executed_suites']:
        if value.get(field) != expected:
            raise ReceiptError('full validation inventory is incomplete or changed')
    if any(value.get('results', {}).get(name) != 'success' for name in JOBS):
        raise ReceiptError('full validation jobs did not all succeed')
    for field in ['policy_hash', 'plan_sha256', 'evidence_sha256']:
        if not DIGEST.fullmatch(str(value.get(field, ''))):
            raise ReceiptError('full validation evidence hashes are missing')
    if sorted(value.get('browser', {}).get('actual_inventory', [])) != expected:
        raise ReceiptError('actual browser inventory is missing')


def file_digest(path):
    target = Path(path)
    if not target.is_file() or target.is_symlink():
        raise ReceiptError('build output is missing or symbolic')
    digest = hashlib.sha256()
    with target.open('rb') as stream:
        for block in iter(lambda: stream.read(1_048_576), b''):
            digest.update(block)
    return digest.hexdigest()


def frontend_digest(directory):
    root = Path(directory)
    if not root.is_dir() or root.is_symlink():
        raise ReceiptError('frontend output is missing')
    files = {}
    for path in sorted(root.rglob('*')):
        if path.is_symlink():
            raise ReceiptError('frontend output contains a symbolic link')
        if path.is_file():
            files[path.relative_to(root).as_posix()] = file_digest(path)
    if not files:
        raise ReceiptError('frontend output is empty')
    return sha256(canonical(files))


def producer(source):
    env = os.environ
    repo = env.get('GITHUB_REPOSITORY')
    branch = env.get('GITHUB_REF', '').removeprefix('refs/heads/')
    if (repo != REPOSITORY or branch not in ['beta', 'main']
            or env.get('GITHUB_EVENT_NAME') != 'push'
            or env.get('GITHUB_REF') != 'refs/heads/' + branch
            or env.get('GITHUB_SHA') != source['sha']
            or env.get('GITHUB_WORKFLOW_SHA') != source['sha']
            or env.get('GITHUB_WORKFLOW_REF') != repo + '/' + WORKFLOW + '@refs/heads/' + branch):
        raise ReceiptError('producer must be exact trusted branch push checkout/workflow')
    try:
        run_id, attempt = int(env['GITHUB_RUN_ID']), int(env['GITHUB_RUN_ATTEMPT'])
    except (KeyError, ValueError):
        raise ReceiptError('producer run identity is missing') from None
    if min(run_id, attempt) <= 0:
        raise ReceiptError('producer run identity is invalid')
    return {'repository': repo, 'workflow': WORKFLOW, 'workflow_sha': source['sha'],
            'workflow_ref': env['GITHUB_WORKFLOW_REF'], 'run_id': run_id,
            'run_attempt': attempt, 'event': 'push', 'branch': branch}


def create(args):
    source = snapshot(args.root)
    validation = read_json(args.validation)
    verify_validation(validation, source, inventory(args.root, source['sha']))
    if not IMAGE.fullmatch(args.image):
        raise ReceiptError('an immutable trusted registry image digest is required')
    value = {'format': 1, 'kind': 'helm-trusted-validation', 'source': source,
             'producer': producer(source), 'inputs': source_inputs(args.root, source['sha']),
             'build': build_inputs(args.build_settings), 'validation': validation,
             'artifacts': {'image': args.image, 'binary_sha256': file_digest(args.binary),
                           'frontend_sha256': frontend_digest(args.frontend)}}
    if snapshot(args.root) != source:
        raise ReceiptError('checkout changed during receipt creation')
    output = canonical(value) + b'\n'
    if len(output) > LIMIT:
        raise ReceiptError('receipt is oversized')
    Path(args.output).parent.mkdir(parents=True, exist_ok=True)
    Path(args.output).write_bytes(output)
    print(json.dumps({'created': True, 'source_sha': source['sha'],
                      'receipt_sha256': sha256(output)}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    create_parser = sub.add_parser('create')
    for name in ['root', 'validation', 'image', 'binary', 'frontend', 'build-settings', 'output']:
        create_parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    try:
        create(args)
    except (ReceiptError, OSError, ValueError, KeyError, TypeError, AttributeError):
        # Emit only known receipt errors; supplied documents/paths may contain secrets.
        error = sys.exc_info()[1]
        print('Receipt refused: ' + (str(error) if isinstance(error, ReceiptError)
                                     else 'invalid or unavailable receipt input'), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
