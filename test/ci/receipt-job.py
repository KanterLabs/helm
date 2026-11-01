#!/usr/bin/env python3
"""Emit a full receipt or a separately typed promotion acceptance after the gate."""
import argparse
import json
from pathlib import Path
from types import SimpleNamespace
import sys

from plan import promotion_identity
from receipt import (DIGEST, IMAGE, LIMIT, ReceiptError, canonical, create, producer,
                     read_json, sha256, snapshot, strict_json)


def promotion_acceptance(args):
    source = snapshot(args.root)
    identity = producer(source)
    if identity['branch'] != 'main':
        raise ReceiptError('promotion acceptance requires a trusted main push producer')
    gate = read_json(args.validation)
    if (not isinstance(gate, dict) or gate.get('schema_version') != 1 or gate.get('format') != 1
            or gate.get('accepted') is not True or gate.get('passed') is not True
            or gate.get('scope') != 'promotion' or gate.get('effective_scope') != 'promotion'):
        raise ReceiptError('accepted promotion gate is required')
    gate_source = gate.get('source', {})
    if (gate_source.get('sha') != source['sha'] or gate_source.get('tree') != source['tree']
            or gate.get('source_sha') != source['sha'] or gate.get('source_tree') != source['tree']):
        raise ReceiptError('promotion gate does not bind actual checkout')
    if (gate.get('selected_suites') != [] or gate.get('executed_suites') != []
            or gate.get('browser') != {} or gate.get('errors', []) != []):
        raise ReceiptError('promotion must not claim a current full browser suite')
    jobs = {'checks': 'success', 'container': 'success'}
    if gate.get('jobs') != jobs or gate.get('results') != jobs:
        raise ReceiptError('current promotion checks and container jobs must succeed')
    for name in ['policy_hash', 'plan_sha256']:
        if not isinstance(gate.get(name), str) or not DIGEST.fullmatch(gate[name]):
            raise ReceiptError('promotion gate policy or plan identity is invalid')
    if not IMAGE.fullmatch(args.image):
        raise ReceiptError('promotion requires an immutable trusted image')
    reused = gate.get('reused_full_validation')
    # This is structural checking of the same-run gate artifact. Independent API
    # resolution belongs to the gate job; this wrapper never substitutes for it.
    proof = promotion_identity(reused, source['sha'])
    digest = sha256(canonical(proof))
    if reused.get('proof_sha256') != digest or proof['image'] != args.image:
        raise ReceiptError('promotion proof digest or image does not match accepted gate')
    if snapshot(args.root) != source:
        raise ReceiptError('checkout changed during promotion acceptance')
    raw = Path(args.validation).read_bytes()
    if len(raw) > LIMIT or strict_json(raw) != gate:
        raise ReceiptError('promotion gate changed during acceptance')
    current = {'accepted': True, 'passed': True, 'scope': 'promotion', 'effective_scope': 'promotion',
               'selected_suites': [], 'executed_suites': [], 'browser': {},
               'jobs': jobs, 'results': jobs, 'source': source,
               'source_sha': source['sha'], 'source_tree': source['tree'],
               'policy_hash': gate['policy_hash'], 'plan_sha256': gate['plan_sha256']}
    value = {'format': 1, 'schema_version': 1, 'kind': 'helm-promotion-acceptance',
             'source': source, 'producer': identity, 'artifacts': {'image': args.image},
             'validation': current, 'gate_sha256': sha256(raw),
             'reused_full_validation': {**proof, 'proof_sha256': digest}}
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    document = canonical(value) + b'\n'
    output.write_bytes(document)
    print(json.dumps({'created': True, 'kind': value['kind'], 'source_sha': source['sha'],
                      'receipt_sha256': sha256(document)}))


def run(args):
    if args.scope == 'promotion':
        promotion_acceptance(args)
    else:
        # Preserve every strict full-validation requirement, including full
        # executed inventory and actual binary/frontend hashes, in one creator.
        root = Path(args.root)
        create(SimpleNamespace(root=args.root, validation=args.validation, image=args.image,
                               binary=str(root / 'dist/helm'), frontend=str(root / 'web/dist'),
                               build_settings=args.build_settings or str(root / 'test/ci/build-settings.json'),
                               output=args.output))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['root', 'validation', 'image', 'scope', 'output']:
        options = {'choices': ['full', 'promotion']} if name == 'scope' else {}
        parser.add_argument('--' + name, required=True, **options)
    parser.add_argument('--build-settings')
    args = parser.parse_args()
    try:
        run(args)
    except (ReceiptError, OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        # No supplied document, path or third-party exception text reaches logs.
        reason = str(error) if isinstance(error, ReceiptError) else 'invalid or unavailable receipt input'
        print('Receipt job refused: ' + reason, file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
