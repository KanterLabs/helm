"""Failure-first subprocess journeys for the source-bound promotion scope."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import unittest

import test_plan_journeys as fixtures

ARTIFACTS, HERE = fixtures.ARTIFACTS, fixtures.HERE


class PromotionGateJourneys(unittest.TestCase):
    setUp = fixtures.PlanJourneys.setUp
    git = fixtures.PlanJourneys.git
    write = fixtures.PlanJourneys.write
    commit = fixtures.PlanJourneys.commit
    changed = fixtures.PlanJourneys.changed
    pr = fixtures.PlanJourneys.pr

    def prepare(self):
        self.changed('internal/runtime.go')
        sha = self.git('rev-parse', 'HEAD')
        self.proof = {'accepted': True, 'full_required': False, 'runtime_identity_match': True,
                      'promotion_sha': sha, 'artifact_source_sha': sha,
                      'image': 'ghcr.io/kanterlabs/helm@sha256:' + 'a' * 64,
                      'receipt_sha256': 'b' * 64, 'producer_run_id': 101,
                      'producer_run_attempt': 2, 'artifact_id': 303}
        self.event = {'ref': 'refs/heads/main', 'before': self.base, 'after': sha}

    def plan(self, proof=None, event=None, name='push', mode='enforced', raw=None):
        self.sequence += 1
        event_path = self.root / 'event.json'
        event_path.write_text(json.dumps(event or self.event))
        proof_path = self.root / 'promotion.json'
        proof_path.write_text(raw if raw is not None else json.dumps(proof if proof is not None else self.proof))
        output = self.root / 'promotion-plan.json'
        command = [sys.executable, str(HERE / 'plan.py'), '--event', str(event_path),
                   '--event-name', name, '--repository', str(self.repo), '--output', str(output),
                   '--mode', mode, '--promotion', str(proof_path)]
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        plan = json.loads(output.read_text())
        (ARTIFACTS / f'{self._testMethodName}-{self.sequence}.plan.json').write_text(
            json.dumps(plan, indent=2) + '\n')
        return plan

    def verify(self, plan, proof=None, results=None, okay=True, missing=False):
        self.sequence += 1
        path = self.root / 'planned.json'
        path.write_text(json.dumps(plan))
        proof_path = self.root / 'fresh-promotion.json'
        proof_path.write_text(json.dumps(proof if proof is not None else self.proof))
        jobs = self.root / 'jobs.json'
        jobs.write_text(json.dumps(results if results is not None else {
            'checks': {'result': 'success'}, 'container': {'result': 'success'}}))
        output = self.root / 'promotion-gate.json'
        command = [sys.executable, str(HERE / 'verify-plan.py'), '--plan', str(path),
                   '--results', str(jobs), '--output', str(output)]
        if not missing:
            command += ['--promotion-proof', str(proof_path)]
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, okay, result.stderr)
        receipt = json.loads(output.read_text())
        self.assertEqual(receipt['passed'], okay)
        (ARTIFACTS / f'{self._testMethodName}-{self.sequence}.receipt.json').write_text(
            json.dumps(receipt, indent=2) + '\n')
        return receipt

    def test_valid_main_push_selects_promotion_and_gate_proves_reuse(self):
        self.prepare()
        extra = {**self.proof, 'unbounded_untrusted_field': 'discard this'}
        plan = self.plan(extra)
        self.assertEqual(plan['proposed_scope'], 'promotion')
        self.assertEqual(plan['effective_scope'], 'promotion')
        self.assertEqual(plan['selected_suites'], [])
        self.assertEqual(plan['expected_jobs'], ['checks', 'container'])
        self.assertEqual(plan['promotion'], self.proof)
        self.assertTrue(plan['publish'])
        self.assertTrue(plan['deploy'])
        receipt = self.verify(plan)
        self.assertEqual(receipt['effective_scope'], 'promotion')
        self.assertEqual(receipt['scope'], 'promotion')
        self.assertEqual(receipt['executed_suites'], [])
        self.assertEqual(receipt['browser'], {})
        self.assertEqual(receipt['reused_full_validation']['receipt_sha256'], self.proof['receipt_sha256'])
        canonical = json.dumps(self.proof, sort_keys=True, separators=(',', ':')).encode()
        self.assertEqual(receipt['reused_full_validation']['proof_sha256'],
                         hashlib.sha256(canonical).hexdigest())

    def test_shadow_beta_pr_and_manual_cannot_opt_into_promotion(self):
        self.prepare()
        cases = [(self.event, 'push', 'shadow'),
                 ({**self.event, 'ref': 'refs/heads/beta'}, 'push', 'enforced'),
                 (self.pr(target='main'), 'pull_request', 'enforced'),
                 ({'inputs': {'scope': 'full'}}, 'workflow_dispatch', 'enforced')]
        for event, name, mode in cases:
            with self.subTest(name=name, mode=mode):
                plan = self.plan(event=event, name=name, mode=mode)
                self.assertEqual(plan['effective_scope'], 'full')
                self.assertNotIn('promotion', plan)

    def test_malformed_rejected_mismatched_and_nonpositive_proofs_fallback_full(self):
        self.prepare()
        bad = [('accepted', False), ('full_required', True), ('runtime_identity_match', False),
               ('promotion_sha', self.base), ('artifact_source_sha', self.base),
               ('image', 'ghcr.io/kanterlabs/helm:latest'), ('receipt_sha256', 'bad'),
               ('receipt_sha256', int('1' * 64)),
               ('producer_run_id', 0), ('producer_run_attempt', -1), ('artifact_id', True)]
        for key, item in bad:
            with self.subTest(key=key):
                proof = {**self.proof, key: item}
                plan = self.plan(proof)
                self.assertEqual(plan['effective_scope'], 'full')
                self.assertNotIn('promotion', plan)
        self.assertEqual(self.plan(raw='not json')['effective_scope'], 'full')
        self.changed('README.md')
        self.event.update(before=self.event['after'], after=self.git('rev-parse', 'HEAD'))
        self.assertEqual(self.plan()['effective_scope'], 'full')

    def test_missing_changed_fresh_proof_and_selected_job_failures_rejected(self):
        self.prepare()
        plan = self.plan()
        self.verify(plan, missing=True, okay=False)
        changes = [('accepted', False), ('full_required', True), ('runtime_identity_match', False),
                   ('promotion_sha', self.base), ('artifact_source_sha', self.base),
                   ('image', 'ghcr.io/kanterlabs/helm@sha256:' + 'c' * 64),
                   ('receipt_sha256', 'd' * 64), ('producer_run_id', 102),
                   ('producer_run_attempt', 3), ('artifact_id', 304)]
        for key, item in changes:
            with self.subTest(key=key):
                self.verify(plan, {**self.proof, key: item}, okay=False)
        for job in ('checks', 'container'):
            for status in (None, 'skipped', 'cancelled', 'failure'):
                results = {'checks': {'result': 'success'}, 'container': {'result': 'success'}}
                if status is None:
                    del results[job]
                else:
                    results[job]['result'] = status
                self.verify(plan, results=results, okay=False)

    def test_gate_rejects_tampered_plan_eligibility_jobs_and_inventory(self):
        self.prepare()
        plan = self.plan()
        cases = [('mode', 'shadow'), ('selected_suites', ['e2e/helm-shell.spec.ts']),
                 ('expected_jobs', ['checks']), ('publish', False),
                 ('event', {**plan['event'], 'name': 'pull_request', 'base_ref': 'main'}),
                 ('event', {**plan['event'], 'ref': 'refs/heads/beta'})]
        for key, item in cases:
            with self.subTest(key=key):
                forged = copy.deepcopy(plan)
                forged[key] = item
                self.verify(forged, okay=False)


if __name__ == '__main__':
    unittest.main(verbosity=2)
