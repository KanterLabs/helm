#!/usr/bin/env python3
"""Isolated HTTP acceptance for agent admission and the Active capacity cap."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import traceback
import uuid

from agent_admission_fixture import Fixture
from agent_admission_journeys import (
    cross_project_claim,
    final_slot_and_retry,
    recover_stale_at_capacity,
    upgrade_over_capacity,
)


ROOT = Path(__file__).resolve().parents[2]
SCENARIOS = (final_slot_and_retry, cross_project_claim, recover_stale_at_capacity, upgrade_over_capacity)


def digest(path):
    with path.open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True, help='candidate Helm server')
    parser.add_argument('--baseline-binary', type=Path, required=True, help='pre-change Helm server')
    parser.add_argument('--output', type=Path, help='fresh evidence directory under .artifacts by default')
    args = parser.parse_args()
    if not __debug__:
        parser.error('acceptance assertions require Python without optimization')
    candidate, baseline = args.binary.resolve(), args.baseline_binary.resolve()
    output = args.output or ROOT / '.artifacts' / f'agent-admission-{uuid.uuid4().hex}'
    output.mkdir(parents=True)
    receipt_path = output / 'result.json'
    receipt = {
        'status': 'failed',
        'source_revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'candidate_binary_sha256': digest(candidate),
        'baseline_binary_sha256': digest(baseline),
        'sources_sha256': {name: digest(Path(__file__).with_name(name)) for name in
                           ('agent-admission.py', 'agent_admission_fixture.py', 'agent_admission_journeys.py')},
        'scenarios': [],
    }
    for scenario in SCENARIOS:
        print(f'Running {scenario.__name__}', flush=True)
        result = {'name': scenario.__name__, 'status': 'failed'}
        with tempfile.TemporaryDirectory(prefix='helm-agent-admission-') as folder:
            try:
                result.update(scenario(Fixture(Path(folder)), candidate, baseline))
                result['status'] = 'passed'
            except AssertionError as exc:
                result['check'] = str(exc) or 'assertion failed'
            except Exception as exc:
                result['exception_type'] = type(exc).__name__
                frame = traceback.extract_tb(exc.__traceback__)[-1]
                result['location'] = f'{Path(frame.filename).name}:{frame.lineno}'
            finally:
                receipt['scenarios'].append(result)
                receipt_path.write_text(json.dumps(receipt, indent=2) + '\n')
    if all(result['status'] == 'passed' for result in receipt['scenarios']):
        receipt['status'] = 'passed'
        receipt_path.write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({'status': receipt['status'], 'receipt': str(receipt_path)}))
    return 0 if receipt['status'] == 'passed' else 1


if __name__ == '__main__':
    raise SystemExit(main())
