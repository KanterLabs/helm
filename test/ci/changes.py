"""Captured local Git input and strict JSON helpers for CI selection."""
import json
from pathlib import PurePosixPath
import re
import subprocess

SHA = re.compile(r'[0-9a-f]{40}(?:[0-9a-f]{24})?\Z')


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate JSON key')
        value[key] = item
    return value


def read_json(path):
    return json.loads(path.read_text(), object_pairs_hook=unique_object)


def git(repo, *arguments):
    result = subprocess.run(['git', '-C', str(repo), *arguments], capture_output=True,
                            timeout=30, check=False)
    if result.returncode:
        raise ValueError('required local Git object or history unavailable')
    return result.stdout


def commit(repo, value):
    if not isinstance(value, str) or not SHA.fullmatch(value) or set(value) == {'0'}:
        raise ValueError('invalid captured commit')
    actual = git(repo, 'rev-parse', '--verify', value + '^{commit}').decode().strip()
    if actual != value:
        raise ValueError('captured revision must identify a commit')
    return actual


def path_valid(value):
    return (isinstance(value, str) and value and not value.startswith('/') and
            '..' not in PurePosixPath(value).parts and
            not any(ord(char) < 32 or ord(char) == 127 for char in value))


def captured_diff(repo, base, head, pr=False, expected_count=None):
    result = {'complete': False, 'paths': [], 'reason': None, 'merge_base_sha': None}
    try:
        base, head = commit(repo, base), commit(repo, head)
        if pr:
            bases = git(repo, 'merge-base', '--all', base, head).decode().splitlines()
            if len(bases) != 1:
                raise ValueError('missing or ambiguous merge base')
            base = result['merge_base_sha'] = bases[0]
        raw = git(repo, 'diff', '--name-status', '-z', '--find-renames', base, head, '--')
        fields = raw.split(b'\0')
        if fields.pop() != b'':
            raise ValueError('truncated local diff')
        index = 0
        while index < len(fields):
            status = fields[index].decode('ascii')
            path = fields[index + 1].decode('utf-8', errors='strict')
            index += 2
            record = {'status': status, 'path': path}
            if status.startswith(('R', 'C')):
                record['old_path'] = path
                record['path'] = fields[index].decode('utf-8', errors='strict')
                index += 1
            if not path_valid(record['path']) or not path_valid(path):
                raise ValueError('ambiguous Git path')
            result['paths'].append(record)
        if not result['paths']:
            raise ValueError('empty diff cannot authorize reduced validation')
        if expected_count is not None and (type(expected_count) is not int or
                                           expected_count != len(result['paths'])):
            raise ValueError('captured file count does not match complete local diff')
        result['complete'] = True
    except (ValueError, UnicodeError, IndexError) as error:
        result['reason'] = str(error)
    return result


def full_specs(repo, sha):
    names = git(repo, 'ls-tree', '-rz', '--name-only', sha, '--', 'web/e2e').split(b'\0')
    return sorted(name.decode().removeprefix('web/') for name in names
                  if name.endswith(b'.spec.ts'))
