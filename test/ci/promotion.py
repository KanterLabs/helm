#!/usr/bin/env python3
"""Select a validated digest through trusted API provenance; never deploy anything."""
import argparse
import json
import sys

from promotion_api import download_receipt, run_provenance
from receipt import (DIGEST, IMAGE, REPOSITORY, WORKFLOW, ReceiptError, build_inputs,
                     git, inventory, snapshot, source_inputs, verify_validation)

FRESH_GATES = ['image-security', 'target-configuration', 'readiness-live-smoke',
               'migration-backup-compatibility', 'admission', 'release-authorization']


def validate_receipt(value, run, root, build):
    """Pure local comparison after the caller has established real API binding.

    This function is deliberately not an accepting CLI for supplied provenance JSON.
    Only resolve() supplies the real GitHub run and downloaded immutable artifact.
    """
    source = value.get('source', {})
    if (value.get('format') != 1 or value.get('kind') != 'helm-trusted-validation'
            or source.get('sha') != run['head_sha']):
        raise ReceiptError('receipt source does not match trusted producer')
    producer = value.get('producer', {})
    expected = {'repository': REPOSITORY, 'workflow': WORKFLOW,
                'workflow_sha': run['head_sha'], 'run_id': run['id'],
                'run_attempt': run['run_attempt'], 'event': 'push', 'branch': 'beta',
                'workflow_ref': REPOSITORY + '/' + WORKFLOW + '@refs/heads/beta'}
    if producer != expected:
        raise ReceiptError('receipt producer does not match trusted workflow/run/attempt')
    tree = git(root, 'rev-parse', '--verify', run['head_sha'] + '^{tree}')
    if source.get('tree') != tree:
        raise ReceiptError('receipt source tree does not match actual source object')
    verify_validation(value.get('validation'), source, inventory(root, source['sha']))
    if value.get('inputs') != source_inputs(root, source['sha']):
        raise ReceiptError('receipt dependency/workflow/test-policy hashes do not match source')
    if value.get('build') != build:
        raise ReceiptError('toolchain or build settings changed; full validation required')
    artifacts = value.get('artifacts', {})
    if (not IMAGE.fullmatch(str(artifacts.get('image', '')))
            or any(not DIGEST.fullmatch(str(artifacts.get(name, '')))
                   for name in ['binary_sha256', 'frontend_sha256'])):
        raise ReceiptError('receipt lacks immutable image/binary/frontend identity')
    candidate = snapshot(root)
    if candidate['tree'] != tree:
        raise ReceiptError('candidate tree changed, including version/build/runtime/policy inputs; '
                           'full validation required')
    if source_inputs(root, candidate['sha']) != value['inputs']:
        raise ReceiptError('candidate dependency/workflow/test-policy inputs changed')
    return candidate


def resolve(args):
    result = {'accepted': False, 'full_required': True, 'reason': 'trusted proof unavailable',
              'image': None, 'artifact_source_sha': None, 'promotion_sha': None,
              'receipt_sha256': None, 'inputs_match': False, 'runtime_identity_match': False,
              'fresh_gates_required': FRESH_GATES}
    try:
        result['promotion_sha'] = snapshot(args.root)['sha']
        run = run_provenance(args.run_id)
        value, digest, artifact_id = download_receipt(run)
        candidate = validate_receipt(value, run, args.root, build_inputs(args.build_settings))
        if run_provenance(args.run_id) != run:
            raise ReceiptError('producer run changed during resolution; full validation required')
        same_source = candidate['sha'] == run['head_sha']
        result.update(accepted=same_source, full_required=not same_source,
                      inputs_match=True, runtime_identity_match=same_source,
                      reason=('trusted beta full validation matches exact source and inputs' if same_source else
                              'exact inputs match but runtime readiness/admission require the source SHA; '
                              'full validation required until runtime identity migration is proven'),
                      image=value['artifacts']['image'] if same_source else None, artifact_source_sha=run['head_sha'],
                      promotion_sha=candidate['sha'], receipt_sha256=digest,
                      artifact_id=artifact_id, producer_run_id=run['id'],
                      producer_run_attempt=run['run_attempt'])
    except (ReceiptError, OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        result['reason'] = str(error) if isinstance(error, ReceiptError) else 'malformed or unavailable trusted proof'
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    resolver = commands.add_parser('resolve')
    resolver.add_argument('--root', required=True)
    resolver.add_argument('--run-id', required=True, type=int)
    resolver.add_argument('--build-settings', required=True)
    args = parser.parse_args()
    if args.run_id <= 0:
        parser.error('run-id must be positive')
    print(json.dumps(resolve(args), sort_keys=True))
    # A fallback is a routing decision, not approval. Caller must inspect accepted.
    return 0


if __name__ == '__main__':
    sys.exit(main())
