#!/usr/bin/env python3
"""Real isolated-server flows for the neglect-signal failure cases (plan cases 1-17,
CLI cases 18-20 from CLI.md).

  python3 test/neglect/run.py --binary .artifacts/neglect-helm              # strict
  python3 test/neglect/run.py --binary B --expect-red                        # red gate
  python3 test/neglect/run.py --binary B --case 1 --case 10                  # subset

Every case runs against a real binary on a private SQLite database and writes
a sanitized receipt (.artifacts/neglect-<run>/case-NN.json) plus result.json.
Outcomes: passed, feature_absent (the planned signal is missing: the expected
red state until TC-380/TC-381), failed (wrong behavior) and error (harness).

Strict mode exits 0 only when every selected case passed. --expect-red exits
0 only when no case failed or errored, every red case is feature_absent, the
guard cases passed, and every store_* check (projector guards since TC-379)
passed. Since TC-381 every case 1-20 is a guard, so both modes agree.
See FAILURES.md and CLI.md.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import traceback
import uuid

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from contract import FeatureAbsent  # noqa: E402
from harness import ROOT, Clock, Sanitizer, Server, World  # noqa: E402
import cases_boundary  # noqa: E402
import cases_cli  # noqa: E402
import cases_cli_fallback  # noqa: E402
import cases_projector  # noqa: E402
import cases_scale  # noqa: E402
import cases_signal  # noqa: E402
import cases_writers  # noqa: E402
import populate  # noqa: E402

CASES = {**cases_signal.CASES, **cases_boundary.CASES, **cases_writers.CASES, **cases_scale.CASES,
         **cases_projector.CASES, **cases_cli.CASES,
         **cases_cli_fallback.CASES}
# A landed case must keep passing: 15 since TC-378, 17 throughout, every API
# case (1-14, 16) since TC-380, the v1 fallback (20) from the start, and the
# CLI server path (18, 19; red before the CLI change) since TC-381 (CLI.md).
GUARDS = set(range(1, 21))
OWN_SERVER = {6, 8, 14, 15, 16, 17, 18, 19, 20}


class Context:
    def __init__(self, args, workdir, clock):
        self.args, self.workdir, self.clock = args, workdir, clock
        self.binary = args.binary.resolve()
        self.rollback_binary = (args.rollback_binary or args.binary).resolve()
        self.clock_binary = args.clock_binary.resolve()
        self.rollback_sha = hashlib.sha256(self.rollback_binary.read_bytes()).hexdigest()
        self.output = args.output
        self.server = self.world = None
        self._populated = None

    def shared(self):
        if self.server is None:
            self.server = Server(self.binary, self.workdir, name='shared')
            self.server.start()
            self.world = World(self.server, self.clock)
        return self

    def populated_db(self):
        if self._populated is None and self.args.populated_db:
            self._populated = self.args.populated_db.resolve()
        if self._populated is None:
            target = Path(self.workdir) / 'populated-source.db'
            populate.build(populate.parser().parse_args([
                '--binary', str(self.binary), '--tasks', str(self.args.scale_tasks),
                '--events', str(self.args.scale_events), '--output', str(target)]))
            self._populated = target
        return self._populated


def run_case(number, ctx, sanitize):
    fn = CASES[number]
    receipt = {'case': number, 'name': fn.__name__.split('_', 2)[2], 'outcome': 'error'}
    try:
        fn(ctx if number in OWN_SERVER else ctx.shared(), receipt)
        receipt['outcome'] = 'passed'
    except FeatureAbsent as exc:
        receipt.update(outcome='feature_absent', detail=str(exc))
    except AssertionError as exc:
        receipt.update(outcome='failed', detail=str(exc)[:800])
    except Exception as exc:  # harness or transport fault, never an expected red
        receipt.update(outcome='error', detail=repr(exc)[:800],
                       trace=traceback.format_exc().splitlines()[-6:])
    clean = sanitize(receipt)
    (ctx.output / f'case-{number:02d}.json').write_text(json.dumps(clean, indent=2) + '\n')
    return clean


def verdict(results, expect_red):
    outcomes = {r['case']: r['outcome'] for r in results}
    if not expect_red:
        return all(o == 'passed' for o in outcomes.values())
    # store_* checks observe the projector (TC-379, landed): guards inside
    # cases that stay FeatureAbsent until TC-380 exposes the API fields.
    if any(c['outcome'] != 'passed' for r in results for c in r.get('checks', [])
           if c['name'].startswith('store_')):
        return False
    for case, outcome in outcomes.items():
        if case in GUARDS and outcome != 'passed':
            return False
        if case not in GUARDS and outcome != 'feature_absent':
            return False
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--binary', type=Path, default=ROOT / '.artifacts/neglect-helm')
    parser.add_argument('--rollback-binary', type=Path,
                        help='retained pre-signal binary for cases 6, 17 and 20 (default: --binary)')
    parser.add_argument('--clock-binary', type=Path, default=ROOT / '.artifacts/neglect-helm-testclock',
                        help='case 14: binary built with -tags helm_testclock (make neglect-binary)')
    parser.add_argument('--output', type=Path)
    parser.add_argument('--case', type=int, action='append', choices=sorted(CASES))
    parser.add_argument('--expect-red', action='store_true')
    parser.add_argument('--scale-tasks', type=int, default=300)
    parser.add_argument('--scale-events', type=int, default=6000)
    parser.add_argument('--populated-db', type=Path,
                        help='cases 8/16: copy this populated database (make neglect-db) instead of building one')
    parser.add_argument('--seed-budget', type=float, default=60.0, help='case 16 seed budget, seconds')
    parser.add_argument('--write-budget-ms', type=float, default=1000.0, help='case 16: slowest write during seed')
    parser.add_argument('--rebuild-budget-ms', type=float, default=20.0, help='case 16: mean per-task rebuild')
    parser.add_argument('--lock-budget-ms', type=float, default=250.0, help='case 16: longest projector write tx')
    parser.add_argument('--kill-after-ms', type=int, default=150,
                        help='case 8: extra wait for the first committed seed batch before giving up')
    args = parser.parse_args()
    args.output = args.output or ROOT / '.artifacts' / ('neglect-' + uuid.uuid4().hex)
    assert not args.output.exists(), 'Use a fresh evidence path'
    args.output.mkdir(parents=True)
    clock = Clock()
    sanitize = Sanitizer(clock)
    selected = sorted(set(args.case or CASES))
    scratch = os.environ.get('TMPDIR') or '/var/tmp/agent-scratch-1000/tmp'
    with tempfile.TemporaryDirectory(prefix='helm-neglect-', dir=scratch) as workdir:
        ctx = Context(args, workdir, clock)
        try:
            results = [run_case(number, ctx, sanitize) for number in selected]
        finally:
            if ctx.server:
                ctx.server.stop()
    ok = verdict(results, args.expect_red)
    summary = {'status': 'passed' if ok else 'failed', 'mode': 'expect_red' if args.expect_red else 'strict',
               'fixture_clock': 'T0 (receipt times are offsets from T0)', 'guards': sorted(GUARDS),
               'source_revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
               'binary_sha256': hashlib.sha256(args.binary.read_bytes()).hexdigest(),
               'rollback_binary_sha256': ctx.rollback_sha,
               'clock_binary_sha256': (hashlib.sha256(ctx.clock_binary.read_bytes()).hexdigest()
                                       if ctx.clock_binary.exists() else None),
               'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
               'cases': [{k: r.get(k) for k in ('case', 'name', 'outcome', 'detail')} for r in results]}
    (args.output / 'result.json').write_text(json.dumps(summary, indent=2) + '\n')
    for row in summary['cases']:
        print(f"case {row['case']:>2} {row['outcome']:<15} {row['name']}: {str(row.get('detail') or '')[:110]}")
    print(json.dumps({'status': summary['status'], 'mode': summary['mode'],
                      'receipt': str(args.output / 'result.json')}))
    return 0 if ok else 1


if __name__ == '__main__':
    sys.exit(main())
