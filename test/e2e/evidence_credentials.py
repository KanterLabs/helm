"""Quarantine credential-bearing evidence after reporters and copies finish.

Only one-way fingerprints live in private fixture state. Neither raw values
nor their fingerprints are copied into retained evidence or diagnostic output.
Failure inventory: MEMBERSHIP_SUITE_FAILURES.md.
"""
import hashlib
import json
from pathlib import Path
import re
import sys

TOKEN = re.compile(rb'[A-Za-z0-9_+/=.\-]{8,}')


def fingerprints(state):
    records = set()
    for path in state.rglob('membership-credential-fingerprints.json'):
        if path.is_symlink() or path.stat().st_size > 1_000_000:
            raise ValueError('invalid fingerprint metadata')
        entries = json.loads(path.read_text())
        if not isinstance(entries, list):
            raise ValueError('invalid fingerprint metadata')
        for entry in entries:
            width, digest = entry['length'], entry['sha256']
            if type(width) is not int or not 8 <= width <= 512 or not re.fullmatch('[a-f0-9]{64}', digest):
                raise ValueError('invalid fingerprint metadata')
            records.add((width, digest))
    return records


def contains_credential(path, records):
    widths = {width for width, _ in records}
    with path.open('rb') as stream:
        carry = b''
        while chunk := stream.read(1_048_576):
            chunk = carry + chunk
            for match in TOKEN.finditer(chunk):
                token = match.group()
                for width in widths:
                    for offset in range(len(token) - width + 1):
                        if (width, hashlib.sha256(token[offset:offset + width]).hexdigest()) in records:
                            return True
            carry = chunk[-512:]
    return False


def scan_evidence(artifacts, state):
    records = fingerprints(state)
    if not records:
        return {'status': 'not-required', 'checked_files': 0, 'removed_files': 0}
    checked = removed = 0
    for path in artifacts.rglob('*'):
        if path.is_symlink():
            path.unlink()
            removed += 1
        elif path.is_file():
            checked += 1
            if contains_credential(path, records):
                path.unlink()
                removed += 1
    return {'status': 'failed' if removed else 'passed', 'checked_files': checked, 'removed_files': removed}


def main():
    artifacts, state = map(Path, sys.argv[1:])
    try:
        receipt = scan_evidence(artifacts, state)
    except (OSError, ValueError, TypeError, KeyError):
        # Unknown metadata cannot certify any retained output as safe. Remove
        # only generated evidence; private fixture state and live data stay put.
        removed = 0
        for path in artifacts.rglob('*'):
            if path.is_file() or path.is_symlink():
                path.unlink()
                removed += 1
        receipt = {'status': 'failed', 'reason': 'unverified-fingerprints', 'removed_files': removed}
    (artifacts / 'credential-evidence.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print('Credential evidence gate: ' + receipt['status'])
    return int(receipt['status'] == 'failed')


if __name__ == '__main__':
    sys.exit(main())
