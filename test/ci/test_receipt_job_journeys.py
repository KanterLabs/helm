"""Subprocess journeys for full receipts versus truthful promotion acceptance."""
import copy
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import unittest

from promotion_fixture import Fixture, IMAGE, REPOSITORY, WORKFLOW, write

ARTIFACTS = Path(__file__).resolve().parents[2] / '.artifacts/ci-promotion'


def proof_digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


class ReceiptJobJourneys(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='helm-receipt-job-e2e-')
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Fixture(self.temporary.name)
        self.output = self.fixture.base / 'job-receipt.json'
        self.gate_path = self.fixture.base / 'gate.json'
        self.proof = {'accepted': True, 'full_required': False, 'runtime_identity_match': True,
                      'promotion_sha': self.fixture.sha, 'artifact_source_sha': self.fixture.sha,
                      'image': IMAGE, 'receipt_sha256': 'a' * 64, 'producer_run_id': 123,
                      'producer_run_attempt': 1, 'artifact_id': 456}
        self.gate = {'schema_version': 1, 'format': 1, 'accepted': True, 'passed': True,
                     'effective_scope': 'promotion', 'scope': 'promotion',
                     'source': {'sha': self.fixture.sha, 'tree': self.fixture.tree},
                     'source_sha': self.fixture.sha, 'source_tree': self.fixture.tree,
                     'selected_suites': [], 'executed_suites': [], 'browser': {},
                     'jobs': {'checks': 'success', 'container': 'success'},
                     'results': {'checks': 'success', 'container': 'success'},
                     'plan_sha256': 'b' * 64, 'policy_hash': 'c' * 64,
                     'reused_full_validation': {**self.proof, 'proof_sha256': proof_digest(self.proof)}}
        ARTIFACTS.mkdir(parents=True, exist_ok=True)

    def main_producer(self):
        self.fixture.env.update(GITHUB_EVENT_NAME='push', GITHUB_REF='refs/heads/main',
                                GITHUB_RUN_ID='777', GITHUB_RUN_ATTEMPT='2',
                                GITHUB_WORKFLOW_REF=REPOSITORY + '/' + WORKFLOW + '@refs/heads/main')

    def invoke(self, gate=None, scope='promotion', image=IMAGE, settings=True):
        write(self.gate_path, gate if gate is not None else self.gate)
        self.output.unlink(missing_ok=True)
        args = ['--root', str(self.fixture.repo), '--validation', str(self.gate_path),
                '--image', image, '--scope', scope, '--output', str(self.output)]
        if settings:
            args += ['--build-settings', str(self.fixture.settings)]
        return self.fixture.invoke('receipt-job.py', args)

    def accepted(self, result):
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(result.stderr, '')
        return json.loads(self.output.read_text())

    def refused(self, result):
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())
        self.assertIn('Receipt job refused:', result.stderr)
        return {'exit': result.returncode, 'error': result.stderr.strip()}

    def capture(self, name, value):
        write(ARTIFACTS / ('receipt-job-' + name + '.json'), value)

    def test_promotion_acceptance_records_reuse_without_claiming_current_full_suite(self):
        self.main_producer()
        value = self.accepted(self.invoke())
        self.assertEqual(value['kind'], 'helm-promotion-acceptance')
        self.assertEqual(value['source'], {'sha': self.fixture.sha, 'tree': self.fixture.tree})
        self.assertEqual(value['producer']['branch'], 'main')
        self.assertEqual(value['producer']['run_id'], 777)
        self.assertEqual(value['artifacts'], {'image': IMAGE})
        self.assertEqual(value['validation']['scope'], 'promotion')
        self.assertEqual(value['validation']['selected_suites'], [])
        self.assertEqual(value['validation']['executed_suites'], [])
        self.assertEqual(value['reused_full_validation'], self.gate['reused_full_validation'])
        self.assertEqual(value['gate_sha256'], hashlib.sha256(self.gate_path.read_bytes()).hexdigest())
        self.assertFalse((self.fixture.repo / 'dist/helm').exists())
        self.assertFalse((self.fixture.repo / 'web/dist').exists())
        self.assertFalse(self.fixture.log.exists())
        self.capture('promotion-acceptance', value)

    def test_full_path_keeps_existing_strict_full_receipt_and_build_outputs(self):
        fixture = self.fixture
        (fixture.repo / 'dist').mkdir()
        shutil.copyfile(fixture.base / 'helm', fixture.repo / 'dist/helm')
        shutil.copytree(fixture.base / 'frontend', fixture.repo / 'web/dist')
        gate = json.loads(fixture.validation.read_text())
        value = self.accepted(self.invoke(gate, scope='full'))
        self.assertEqual(value['kind'], 'helm-trusted-validation')
        self.assertEqual(value['validation']['scope'], 'full')
        self.assertEqual(value['validation']['executed_suites'], fixture.inventory)
        self.assertEqual(value['artifacts']['image'], IMAGE)
        self.assertIn('binary_sha256', value['artifacts'])
        self.assertIn('frontend_sha256', value['artifacts'])
        self.assertFalse(fixture.log.exists())
        self.capture('full-validation', value)

    def test_source_image_scope_aliases_and_dirty_checkout_are_refused(self):
        self.main_producer()
        captured = []
        for name in ['source', 'tree', 'source-alias', 'tree-alias', 'scope', 'effective-scope',
                     'selected', 'executed', 'accepted', 'passed']:
            gate = copy.deepcopy(self.gate)
            if name == 'source':
                gate['source']['sha'] = '0' * 40
            elif name == 'tree':
                gate['source']['tree'] = '0' * 40
            elif name == 'source-alias':
                gate['source_sha'] = '0' * 40
            elif name == 'tree-alias':
                gate['source_tree'] = '0' * 40
            elif name == 'scope':
                gate['scope'] = 'full'
            elif name == 'effective-scope':
                gate['effective_scope'] = 'full'
            elif name == 'selected':
                gate['selected_suites'] = ['e2e/task.spec.ts']
            elif name == 'executed':
                gate['executed_suites'] = ['e2e/task.spec.ts']
            else:
                gate[name] = False
            captured.append({'case': name, 'result': self.refused(self.invoke(gate))})
        captured.append({'case': 'image', 'result': self.refused(self.invoke(image=
                         'ghcr.io/kanterlabs/helm@sha256:' + 'b' * 64))})
        write(self.fixture.repo / 'VERSION', 'changed\n')
        captured.append({'case': 'dirty-checkout', 'result': self.refused(self.invoke())})
        self.capture('source-and-scope-refusals', captured)

    def test_beta_manual_pr_mismatched_revision_and_workflow_producers_refused(self):
        fixture = self.fixture
        baseline = dict(fixture.env)
        cases = [('beta', {'GITHUB_REF': 'refs/heads/beta'}),
                 ('manual', {'GITHUB_EVENT_NAME': 'workflow_dispatch'}),
                 ('pr', {'GITHUB_EVENT_NAME': 'pull_request'}),
                 ('revision', {'GITHUB_SHA': '0' * 40}),
                 ('workflow', {'GITHUB_WORKFLOW_SHA': '0' * 40})]
        captured = []
        for name, mutation in cases:
            fixture.env = dict(baseline)
            self.main_producer()
            fixture.env.update(mutation)
            captured.append({'case': name, 'result': self.refused(self.invoke())})
        self.capture('producer-refusals', captured)

    def test_incomplete_jobs_forged_full_and_incomplete_reuse_proof_refused(self):
        self.main_producer()
        captured = []
        for job in ['checks', 'container']:
            for status in [None, 'failure', 'cancelled', 'skipped']:
                gate = copy.deepcopy(self.gate)
                for alias in ['jobs', 'results']:
                    if status is None:
                        gate[alias].pop(job)
                    else:
                        gate[alias][job] = status
                captured.append({'case': job + '-' + str(status),
                                 'result': self.refused(self.invoke(gate))})
        # Relabeling a promotion gate as full still lacks full inventory and outputs.
        forged = copy.deepcopy(self.gate)
        forged.update(scope='full', effective_scope='full')
        captured.append({'case': 'forged-full', 'result': self.refused(self.invoke(forged, scope='full'))})
        for key, mutation in [('accepted', False), ('full_required', True),
                ('runtime_identity_match', False), ('artifact_source_sha', '0' * 40),
                ('promotion_sha', '0' * 40), ('image', IMAGE + '\naccepted=true'),
                ('receipt_sha256', 'invalid'), ('producer_run_id', 0),
                ('producer_run_attempt', True), ('artifact_id', -1), ('proof_sha256', '0' * 64)]:
            gate = copy.deepcopy(self.gate)
            gate['reused_full_validation'][key] = mutation
            captured.append({'case': 'proof-' + key, 'result': self.refused(self.invoke(gate))})
        self.assertFalse(self.fixture.log.exists())
        self.capture('jobs-and-proof-refusals', captured)


if __name__ == '__main__':
    unittest.main()
