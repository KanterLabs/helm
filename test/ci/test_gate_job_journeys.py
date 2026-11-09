"""Subprocess proofs for the job wrapper and its real strict gate verifier."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import unittest

import test_plan_journeys as fixtures

HERE, ARTIFACTS = fixtures.HERE, fixtures.ARTIFACTS
SECRET = 'synthetic-env-value-must-not-leak'


class GateJobJourneys(unittest.TestCase):
    setUp = fixtures.PlanJourneys.setUp
    git = fixtures.PlanJourneys.git
    write = fixtures.PlanJourneys.write
    commit = fixtures.PlanJourneys.commit
    changed = fixtures.PlanJourneys.changed
    pr = fixtures.PlanJourneys.pr
    plan = fixtures.PlanJourneys.plan

    def run_gate(self, plan, needs=None, env=None, script=None, okay=True):
        self.sequence += 1
        planned = self.root / 'gate-plan.json'
        planned.write_text(json.dumps(plan))
        output = self.root / 'gate-receipt.json'
        output.unlink(missing_ok=True)
        environment = {key: item for key, item in os.environ.items()
                       if not key.startswith('GITHUB_') and key != 'NEEDS_JSON'}
        if needs is not None:
            environment['NEEDS_JSON'] = needs if isinstance(needs, str) else json.dumps(needs)
        environment.update(env or {})
        command = [sys.executable, str(script or HERE / 'gate-job.py'), '--root', str(self.repo),
                   '--plan', str(planned), '--browser-evidence', str(self.root / 'absent-browser'),
                   '--output', str(output)]
        result = subprocess.run(command, capture_output=True, text=True, env=environment)
        self.assertEqual(result.returncode == 0, okay, result.stderr)
        self.assertTrue(output.is_file(), result.stderr)
        value = json.loads(output.read_text())
        self.assertEqual(value['passed'], okay)
        self.assertNotIn(SECRET, result.stdout + result.stderr + output.read_text())
        (ARTIFACTS / f'{self._testMethodName}-{self.sequence}.receipt.json').write_text(
            json.dumps(value, indent=2) + '\n')
        return value

    def docs_plan(self):
        self.changed('README.md')
        return self.plan()

    def promotion_plan(self):
        self.changed('internal/runtime.go')
        sha = self.git('rev-parse', 'HEAD')
        proof = {'accepted': True, 'full_required': False, 'runtime_identity_match': True,
                 'promotion_sha': sha, 'artifact_source_sha': sha,
                 'image': 'ghcr.io/kanterlabs/helm@sha256:' + 'a' * 64,
                 'receipt_sha256': 'b' * 64, 'producer_run_id': 101,
                 'producer_run_attempt': 2, 'artifact_id': 303}
        event = self.root / 'promotion-event.json'
        event.write_text(json.dumps({'ref': 'refs/heads/main', 'before': self.base, 'after': sha}))
        supplied = self.root / 'promotion-proof.json'
        supplied.write_text(json.dumps(proof))
        output = self.root / 'promotion-plan.json'
        result = subprocess.run([sys.executable, str(HERE / 'plan.py'), '--event', str(event),
            '--event-name', 'push', '--repository', str(self.repo), '--mode', 'enforced',
            '--promotion', str(supplied), '--output', str(output)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(output.read_text()), supplied

    def isolated_tools(self):
        tools = self.root / 'tools'
        tools.mkdir()
        for name in ('gate-job.py', 'verify-plan.py', 'plan.py', 'changes.py', 'suites.json'):
            shutil.copyfile(HERE / name, tools / name)
        candidate = tools / 'promotion-candidate.py'
        candidate.write_text('''import argparse, json, os, pathlib
p=argparse.ArgumentParser()
for key in ('root','output','mode','run-id'): p.add_argument('--'+key, required=True)
a=p.parse_args()
out=pathlib.Path(a.output)
needs=out.parent/'needs.json'
pathlib.Path(os.environ['CANDIDATE_MARKER']).write_text(json.dumps({
 'run_id':a.run_id,'mode':a.mode,'temporary_directory':str(out.parent),
 'directory_mode':out.parent.stat().st_mode & 0o777,
 'needs_mode':needs.stat().st_mode & 0o777}))
out.write_bytes(pathlib.Path(os.environ['CANDIDATE_PROOF']).read_bytes())
''')
        return tools / 'gate-job.py'

    def test_docs_success_and_failed_cancelled_skipped_missing_checks(self):
        plan = self.docs_plan()
        receipt = self.run_gate(plan, {'checks': {'result': 'success'}})
        self.assertEqual(receipt['effective_scope'], 'docs')
        for status in ('failure', 'cancelled', 'skipped', SECRET, None):
            needs = {} if status is None else {'checks': {'result': status}}
            self.run_gate(plan, needs, okay=False)

    def test_missing_malformed_nonobject_and_duplicate_needs_are_sanitized(self):
        plan = self.docs_plan()
        for needs in (None, SECRET, ['bad'], 'null', '{"checks":{},"checks":{}}'):
            self.run_gate(plan, needs, okay=False)

    def test_stale_dirty_and_github_sha_mismatch_are_rejected(self):
        plan = self.docs_plan()
        needs = {'checks': {'result': 'success'}}
        self.run_gate(plan, needs, env={'GITHUB_SHA': self.base}, okay=False)
        self.write('README.md', 'dirty\n')
        self.run_gate(plan, needs, okay=False)
        self.commit()
        self.run_gate(plan, needs, okay=False)

    def test_shadow_full_cannot_pass_without_browser_artifact(self):
        self.changed('README.md')
        plan = self.plan(mode='shadow')
        needs = {job: {'result': 'success'} for job in plan['expected_jobs']}
        self.run_gate(plan, needs, okay=False)

    def test_promotion_rechecks_private_proof_then_uses_actual_verifier(self):
        plan, supplied = self.promotion_plan()
        script = self.isolated_tools()
        marker = self.root / 'candidate.json'
        environment = {'GITHUB_EVENT_NAME': 'push', 'GITHUB_REF': 'refs/heads/main',
                       'GITHUB_SHA': plan['source']['sha'], 'CANDIDATE_MARKER': str(marker),
                       'CANDIDATE_PROOF': str(supplied)}
        needs = {'checks': {'result': 'success'}, 'container': {'result': 'success'}}
        receipt = self.run_gate(plan, needs, environment, script)
        self.assertEqual(receipt['effective_scope'], 'promotion')
        invocation = json.loads(marker.read_text())
        self.assertEqual(invocation['run_id'], '101')
        self.assertEqual(invocation['mode'], 'enforced')
        self.assertEqual(invocation['directory_mode'], 0o700)
        self.assertEqual(invocation['needs_mode'], 0o600)
        self.assertFalse(Path(invocation['temporary_directory']).exists())

    def test_manual_pr_wrong_ref_or_missing_sha_promotion_never_resolves(self):
        plan, supplied = self.promotion_plan()
        script = self.isolated_tools()
        marker = self.root / 'candidate.json'
        environment = {'GITHUB_EVENT_NAME': 'push', 'GITHUB_REF': 'refs/heads/main',
                       'GITHUB_SHA': plan['source']['sha'], 'CANDIDATE_MARKER': str(marker),
                       'CANDIDATE_PROOF': str(supplied)}
        cases = [('GITHUB_EVENT_NAME', 'workflow_dispatch'), ('GITHUB_EVENT_NAME', 'pull_request'),
                 ('GITHUB_REF', 'refs/heads/beta'), ('GITHUB_SHA', '')]
        for key, item in cases:
            self.run_gate(plan, {'checks': {'result': 'success'}, 'container': {'result': 'success'}},
                          {**environment, key: item}, script, okay=False)
            self.assertFalse(marker.exists())


if __name__ == '__main__':
    unittest.main(verbosity=2)
