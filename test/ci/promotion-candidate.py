#!/usr/bin/env python3
"""Discover or freshly recheck a beta candidate only for an enforced main push."""
import argparse
import json
import os
from pathlib import Path
import re
from types import SimpleNamespace
import sys

from promotion import FRESH_GATES, resolve
from promotion_api import api
from receipt import DIGEST, IMAGE, REPOSITORY, ReceiptError, snapshot


class CandidateError(Exception):
    """Static, nonsecret discovery failure reason."""


def fallback(reason='not eligible', sha=None):
    return {'schema_version': 1, 'accepted': False, 'full_required': True, 'reason': reason,
            'image': None, 'artifact_source_sha': None, 'promotion_sha': sha,
            'receipt_sha256': None, 'inputs_match': False, 'runtime_identity_match': False,
            'fresh_gates_required': list(FRESH_GATES)}


def positive_id(value):
    # Treat IDs as data; reject booleans, endpoint syntax and unbounded integers.
    if type(value) is int:
        return value if 0 < value < 2 ** 63 else None
    if isinstance(value, str) and re.fullmatch(r'[1-9][0-9]{0,18}', value):
        parsed = int(value)
        return parsed if parsed < 2 ** 63 else None
    return None


def discover(sha):
    endpoint = ('repos/' + REPOSITORY + '/actions/workflows/ci.yml/runs?branch=beta'
                '&event=push&status=success&head_sha=' + sha + '&per_page=10')
    listing = api(endpoint)
    if not isinstance(listing, dict):
        raise CandidateError('candidate discovery response is malformed; full validation required')
    values = listing.get('workflow_runs')
    count = listing.get('total_count')
    if (not isinstance(values, list) or len(values) > 10 or type(count) is not int
            or count < len(values) or count < 0):
        raise CandidateError('candidate discovery response is malformed; full validation required')
    candidates = []
    for run in values:
        if (not isinstance(run, dict) or type(run.get('id')) is not int
                or positive_id(run.get('id')) is None):
            raise CandidateError('candidate discovery response is malformed; full validation required')
        if (run.get('head_sha') == sha and run.get('head_branch') == 'beta'
                and run.get('event') == 'push' and run.get('status') == 'completed'
                and run.get('conclusion') == 'success'):
            candidates.append(positive_id(run['id']))
    if not candidates:
        raise CandidateError('no successful beta candidate for actual source; full validation required')
    # GitHub run IDs increase as runs are created. Rechecks still bind the current
    # attempt through the run/jobs/artifact APIs; never try a second candidate.
    return max(candidates)


def normalized(value, source, run_id):
    result = fallback('trusted proof unavailable or incompatible; full validation required', source['sha'])
    if not isinstance(value, dict):
        return result
    accepted = (value.get('accepted') is True and value.get('full_required') is False
                and value.get('inputs_match') is True and value.get('runtime_identity_match') is True
                and value.get('promotion_sha') == source['sha']
                and value.get('artifact_source_sha') == source['sha']
                and value.get('producer_run_id') == run_id
                and type(value.get('producer_run_id')) is int
                and positive_id(value.get('producer_run_attempt')) is not None
                and type(value.get('producer_run_attempt')) is int
                and positive_id(value.get('artifact_id')) is not None
                and type(value.get('artifact_id')) is int
                and IMAGE.fullmatch(str(value.get('image', ''))) is not None
                and DIGEST.fullmatch(str(value.get('receipt_sha256', ''))) is not None)
    if not accepted:
        return result
    result.update(accepted=True, full_required=False, inputs_match=True, runtime_identity_match=True,
                  reason='trusted beta full validation matches exact main source and inputs',
                  image=value['image'], artifact_source_sha=source['sha'],
                  receipt_sha256=value['receipt_sha256'], producer_run_id=run_id,
                  producer_run_attempt=value['producer_run_attempt'], artifact_id=value['artifact_id'])
    return result


def candidate(args):
    if (args.mode != 'enforced' or os.environ.get('GITHUB_EVENT_NAME') != 'push'
            or os.environ.get('GITHUB_REF') != 'refs/heads/main'):
        return fallback()
    source = None
    try:
        source = snapshot(args.root)
        if args.run_id is not None:
            run_id = positive_id(args.run_id)
            if run_id is None:
                return fallback('invalid candidate run ID; full validation required', source['sha'])
        else:
            run_id = discover(source['sha'])
        settings = args.build_settings or str(Path(args.root) / 'test/ci/build-settings.json')
        value = resolve(SimpleNamespace(root=args.root, run_id=run_id, build_settings=settings))
        if snapshot(args.root) != source:
            return fallback('actual checkout changed during candidate resolution; full validation required')
        return normalized(value, source, run_id)
    except CandidateError as error:
        return fallback(str(error), source['sha'] if source else None)
    except (ReceiptError, OSError, ValueError, KeyError, TypeError, AttributeError):
        # API/command errors may contain credentials or hostile content. Do not
        # forward exception messages into workflow logs or output files.
        return fallback('candidate proof unavailable; full validation required',
                        source['sha'] if source else None)


def write_outputs(args, value):
    path = Path(args.output)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, sort_keys=True, indent=2) + '\n')
    if args.github_output:
        image = value['image'] if value['accepted'] else ''
        # Both values are normalized above; no caller strings enter this format.
        with Path(args.github_output).open('a') as stream:
            stream.write('accepted=' + str(value['accepted']).lower() + '\nimage=' + image + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--run-id')
    parser.add_argument('--github-output')
    parser.add_argument('--mode', choices=['shadow', 'enforced'], default='shadow')
    parser.add_argument('--build-settings')
    args = parser.parse_args()
    try:
        value = candidate(args)
        write_outputs(args, value)
    except OSError:
        print('Could not write normalized candidate outputs.', file=sys.stderr)
        return 1
    print(json.dumps({'accepted': value['accepted'], 'full_required': value['full_required'],
                      'reason': value['reason']}, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
