#!/usr/bin/env python3
"""Bind a gate to its checkout and privately route results to the strict verifier."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from changes import git, read_json, unique_object
from plan import validate_plan

HERE = Path(__file__).resolve().parent


class GateJobError(Exception):
    """Only static messages safe for workflow logs and published receipts."""


def bind_checkout(root, plan):
    sha = git(root, 'rev-parse', 'HEAD').decode('ascii').strip()
    tree = git(root, 'rev-parse', 'HEAD^{tree}').decode('ascii').strip()
    if sha != plan['source']['sha'] or tree != plan['source']['tree']:
        raise GateJobError('actual checkout does not match the planned source')
    if git(root, 'diff', '--name-only', 'HEAD', '--'):
        raise GateJobError('tracked checkout is dirty')
    if 'GITHUB_SHA' in os.environ and os.environ['GITHUB_SHA'] != sha:
        raise GateJobError('workflow source does not match actual checkout')
    return sha, tree


def private_file(path, value):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w') as stream:
        stream.write(value)


def child(arguments, message):
    # Child diagnostics can contain raw needs values or API error text. Keep
    # those private; the final failure receipt contains only a static reason.
    environment = {key: value for key, value in os.environ.items() if key != 'NEEDS_JSON'}
    result = subprocess.run([sys.executable, *map(str, arguments)], capture_output=True,
                            env=environment, timeout=180, check=False)
    if result.returncode:
        raise GateJobError(message)


def gate(args):
    receipt = {'schema_version': 1, 'format': 1, 'passed': False, 'accepted': False,
               'errors': [], 'failed_stage': 'inputs'}
    try:
        plan = read_json(args.plan)
        validate_plan(plan)
        receipt.update(effective_scope=plan['effective_scope'], scope=plan['effective_scope'],
                       source_sha=plan['source']['sha'], source_tree=plan['source']['tree'])
        raw = os.environ.get('NEEDS_JSON')
        if raw is None or len(raw) > 2_000_000:
            raise GateJobError('job results are missing or exceed the input limit')
        try:
            needs = json.loads(raw, object_pairs_hook=unique_object)
        except (ValueError, TypeError):
            raise GateJobError('job results are malformed') from None
        if not isinstance(needs, dict):
            raise GateJobError('job results must be an object')
        receipt['failed_stage'] = 'checkout'
        sha, _tree = bind_checkout(args.root, plan)
        with tempfile.TemporaryDirectory(prefix='helm-ci-gate-') as temporary:
            directory = Path(temporary)
            results, proof, verified = (directory / name for name in
                                        ('needs.json', 'fresh.json', 'verified.json'))
            private_file(results, json.dumps(needs, separators=(',', ':')))
            private_file(verified, '')
            command = [HERE / 'verify-plan.py', '--plan', args.plan, '--results', results,
                       '--browser-evidence', args.browser_evidence, '--output', verified]
            if plan['effective_scope'] == 'promotion':
                receipt['failed_stage'] = 'promotion'
                if (os.environ.get('GITHUB_EVENT_NAME') != 'push' or
                        os.environ.get('GITHUB_REF') != 'refs/heads/main' or
                        os.environ.get('GITHUB_SHA') != sha):
                    raise GateJobError('promotion requires an exact main push workflow source')
                private_file(proof, '')
                child([HERE / 'promotion-candidate.py', '--root', args.root, '--output', proof,
                       '--mode', 'enforced', '--run-id', plan['promotion']['producer_run_id']],
                      'fresh promotion candidate resolution failed')
                bind_checkout(args.root, plan)
                command += ['--promotion-proof', proof]
            receipt['failed_stage'] = 'verification'
            child(command, 'selected jobs or execution evidence did not satisfy the plan')
            accepted = read_json(verified)
            if accepted.get('passed') is not True or accepted.get('accepted') is not True:
                raise GateJobError('strict verifier did not accept the declared plan')
            bind_checkout(args.root, plan)
            receipt = accepted
    except GateJobError as error:
        receipt['errors'] = [str(error)]
    except (OSError, ValueError, TypeError, KeyError, AttributeError, subprocess.SubprocessError):
        # Never forward exception text: Git, JSON and subprocess failures may
        # contain untrusted input, private paths or credentials.
        receipt['errors'] = ['gate inputs or execution could not be verified']
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(receipt, indent=2, sort_keys=True) + '\n')
    print('CI gate accepted.' if receipt['passed'] else 'CI gate rejected.',
          file=sys.stdout if receipt['passed'] else sys.stderr)
    return 0 if receipt['passed'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=HERE.parents[1])
    parser.add_argument('--plan', type=Path, required=True)
    parser.add_argument('--browser-evidence', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    return gate(parser.parse_args())


if __name__ == '__main__':
    sys.exit(main())
