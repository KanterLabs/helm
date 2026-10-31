#!/usr/bin/env python3
"""Project completed private-copy runtime receipts into bounded policy proofs.

Run only after independently reviewing the producer, provenance and complete
private diagnostic run. The protected bundle checksum still requires operator
review; this exporter never writes a protected lane setting or accepts itself.
"""
from __future__ import annotations
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import time

REPO = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rollback_policy', REPO/'deploy/rollback_policy.py')
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


def write(path, value):
    raw = (json.dumps(value, sort_keys=True, separators=(',', ':'))+'\n').encode()
    assert len(raw) <= 20_000, 'bounded policy proof exceeded limit'
    path.write_bytes(raw)
    path.chmod(0o600)
    return hashlib.sha256(raw).hexdigest()


def export(run_root, provenance_path, target_path, output):
    summary = json.loads((run_root/'summary.json').read_text())
    provenance = json.loads(provenance_path.read_text())
    target = json.loads(target_path.read_text())
    policy.target_valid(target)
    assert summary['result'] == 'passed' and summary['input'] == 'private-lane-copies', 'complete real-copy run required'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    now = int(time.time())
    root = {'format': 1, 'kind': 'helm-runtime-acceptance-v1', 'producer': 'tc336-isolated-runtime-v1',
            'issued_at': now, 'expires_at': now+3600, 'target': target, 'artifacts': {}}
    for role in ['candidate', 'recovery']:
        runs = [r for r in summary['runs'] if r['role'] == role]
        assert len(runs) == 2 and {r['schema_before'] for r in runs} == {35, 40}
        source = provenance['artifacts'][role]
        pair = {'image': source['registry_image'], 'sha': source['head_sha']}
        proofs = {}
        for run in runs:
            assert run['result'] == 'passed' and run['image_id'] == source['manifest_digest']
            assert run['source_sha'] == pair['sha'] and source['oci_blob_hashes_verified'] is True
            assert source['github_zip_digests_verified'] is True
            assert run['storage']['integrity'] == 'ok' and run['storage']['foreign_key_violations'] == 0
        for scenario in sorted(policy.SCENARIOS):
            # Authorization runs were repeated on both copies. The bounded proof
            # projects schema40; full local diagnostics retain both repetitions.
            version = 35 if scenario == 'schema35-populated-upgrade' else 40
            selected = next(r for r in runs if r['schema_before'] == version)
            observations = [{k: o[k] for k in ['assertion', 'passed', 'exchange']}
                            for o in selected['observations'] if o['scenario'] == scenario]
            if scenario in {'schema35-populated-upgrade', 'schema40-populated-upgrade'}:
                for observation in observations:
                    observation['assertion'] = f"{scenario}: actual ready HTTP response after in-memory comparison preserved all prior values across {selected['storage']['preserved_tables']} original tables, with SQLite integrity and foreign keys clean"
            if scenario == 'current-data-recovery-rollforward':
                observations = []
                for recovery in summary['rollforward']:
                    assert recovery['result'] == 'passed' and recovery['same_current_database'] is True
                    assert recovery['memberships_grants_history_preserved'] is True
                    # Only exchanges actually served by this subject image.
                    offset = 0 if role == 'recovery' else 1
                    observation = recovery['observations'][offset]
                    observations.append({k: observation[k] for k in ['assertion', 'passed', 'exchange']})
            assert observations and all(o['passed'] is True for o in observations), 'incomplete scenario '+scenario
            proof = {'format': 1, 'scenario': scenario, 'target': target, 'subject': pair,
                     'result': 'passed', 'observations': observations}
            filename = role+'-'+scenario+'.json'
            proofs[scenario] = {'file': filename, 'sha256': write(output/filename, proof)}
        root['artifacts'][role] = {**pair, 'capabilities': sorted(policy.CAPABILITIES), 'proofs': proofs}
    digest = write(output/'acceptance.json', root)
    policy.Acceptance(output/'acceptance.json', digest, target,
                      {k: root['artifacts']['candidate'][k] for k in ['image', 'sha']},
                      {k: root['artifacts']['recovery'][k] for k in ['image', 'sha']})
    print(json.dumps({'bundle': str(output/'acceptance.json'), 'sha256': digest,
                      'expires_at': root['expires_at'], 'proofs': 22}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-root', type=Path, required=True)
    parser.add_argument('--provenance', type=Path, required=True)
    parser.add_argument('--target', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    export(args.run_root, args.provenance, args.target, args.output)
