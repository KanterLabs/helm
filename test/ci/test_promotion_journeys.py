"""Subprocess journeys for trusted receipts and read-only API promotion selection."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from promotion_fixture import Fixture, IMAGE, git, write

ARTIFACTS = Path(__file__).resolve().parents[2] / '.artifacts/ci-promotion'


class PromotionJourneys(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='helm-promotion-e2e-')
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Fixture(self.temporary.name)
        ARTIFACTS.mkdir(parents=True, exist_ok=True)

    def capture(self, name, value):
        write(ARTIFACTS / (name + '.json'), value)

    def test_branch_receipt_binds_actual_checkout_and_outputs(self):
        receipt = self.fixture.produce()
        self.assertEqual(receipt['source']['sha'], self.fixture.sha)
        self.assertEqual(receipt['source']['tree'], self.fixture.tree)
        self.assertEqual(receipt['artifacts']['image'], IMAGE)
        self.assertEqual(receipt['artifacts']['binary_sha256'],
                         hashlib.sha256((self.fixture.base / 'helm').read_bytes()).hexdigest())
        self.assertEqual(receipt['validation']['executed_suites'], self.fixture.inventory)
        self.capture('branch-receipt', receipt)

    def test_source_claim_dirty_checkout_and_partial_validation_refused(self):
        fixture = self.fixture
        cases = [('revision', lambda: fixture.env.update(GITHUB_SHA='0' * 40)),
                 ('dirty', lambda: write(fixture.repo / 'VERSION', '9.9.9\n')),
                 ('partial', lambda: write(fixture.validation, {'accepted': True, 'scope': 'focused'}))]
        results = []
        for name, mutate in cases:
            with self.subTest(name=name):
                fixture.env['GITHUB_SHA'] = fixture.sha
                git(fixture.repo, 'checkout', '--', 'VERSION')
                mutate()
                result = fixture.invoke('receipt.py', fixture.create_args())
                self.assertNotEqual(result.returncode, 0)
                results.append({'case': name, 'exit': result.returncode,
                                'error': result.stderr.strip()})
        self.capture('creation-refusals', results)

    def test_same_source_selects_immutable_trusted_image(self):
        fixture = self.fixture
        fixture.prepare_api()
        promotion_sha = fixture.sha
        result = fixture.resolve()
        self.assertTrue(result['accepted'], result)
        self.assertFalse(result['full_required'])
        self.assertEqual(result['image'], IMAGE)
        self.assertEqual(result['artifact_source_sha'], fixture.sha)
        self.assertEqual(result['promotion_sha'], promotion_sha)
        self.assertEqual(len(result['receipt_sha256']), 64)
        self.assertIn('admission', result['fresh_gates_required'])
        calls = fixture.log.read_text().splitlines()
        self.assertIn('repos/KanterLabs/helm/actions/artifacts/456/zip', calls)
        self.capture('trusted-promotion', {'result': result, 'api_calls': calls})

    def test_equal_tree_merge_sha_requires_runtime_identity_migration(self):
        fixture = self.fixture
        fixture.prepare_api()
        git(fixture.repo, 'commit', '--allow-empty', '-qm', 'Synthetic production merge')
        result = fixture.resolve()
        self.assertFalse(result['accepted'], result)
        self.assertTrue(result['full_required'])
        self.assertTrue(result['inputs_match'])
        self.assertFalse(result['runtime_identity_match'])
        self.assertIn('runtime', result['reason'])
        self.capture('merge-runtime-identity-fallback', result)

    def test_untrusted_runs_and_unsuccessful_jobs_fall_back_full(self):
        fixture = self.fixture
        fixture.prepare_api()
        baseline = copy.deepcopy(fixture.api)
        cases = [('pull-request', {'event': 'pull_request'}),
                 ('manual', {'event': 'workflow_dispatch'}),
                 ('main', {'head_branch': 'main'}),
                 ('failed', {'conclusion': 'failure'}),
                 ('running', {'status': 'in_progress'}),
                 ('other-repository', {'repository': {'full_name': 'other/helm'}}),
                 ('fork', {'head_repository': {'full_name': 'other/helm'}}),
                 ('workflow', {'path': '.github/workflows/untrusted.yml'}),
                 ('rerun', {'run_attempt': 2})]
        results = []
        for name, changes in cases:
            with self.subTest(name=name):
                fixture.api = copy.deepcopy(baseline)
                fixture.api[fixture.run_endpoint].update(changes)
                result = fixture.resolve()
                self.assertFalse(result['accepted'], result)
                self.assertTrue(result['full_required'])
                results.append({'case': name, 'result': result})
        for conclusion in ['failure', 'cancelled', 'skipped']:
            fixture.api = copy.deepcopy(baseline)
            fixture.api[fixture.jobs_endpoint]['jobs'][1]['conclusion'] = conclusion
            result = fixture.resolve()
            self.assertFalse(result['accepted'], result)
            results.append({'case': 'job-' + conclusion, 'result': result})
        self.capture('untrusted-provenance', results)

    def test_missing_expired_ambiguous_mismatched_and_tampered_artifacts(self):
        fixture = self.fixture
        fixture.prepare_api()
        baseline = copy.deepcopy(fixture.api)
        results = []
        cases = ['missing', 'expired', 'time-expired', 'ambiguous', 'wrong-run', 'digest', 'corrupt']
        for name in cases:
            with self.subTest(name=name):
                fixture.api = copy.deepcopy(baseline)
                listing = fixture.api[fixture.artifact_endpoint]
                artifact = listing['artifacts'][0]
                if name == 'missing':
                    listing.update(total_count=0, artifacts=[])
                elif name == 'expired':
                    artifact['expired'] = True
                elif name == 'time-expired':
                    artifact['expires_at'] = '2000-01-01T00:00:00Z'
                elif name == 'ambiguous':
                    listing['artifacts'].append(copy.deepcopy(artifact))
                    listing['total_count'] = 2
                elif name == 'wrong-run':
                    artifact['workflow_run']['id'] = 999
                elif name == 'digest':
                    artifact['digest'] = 'sha256:' + '0' * 64
                elif name == 'corrupt':
                    fixture.archive.write_bytes(b'invalid archive')
                result = fixture.resolve()
                self.assertFalse(result['accepted'], result)
                self.assertTrue(result['full_required'])
                results.append({'case': name, 'result': result})
        self.capture('artifact-fallbacks', results)

    def test_version_runtime_policy_flags_and_tools_require_full(self):
        fixture = self.fixture
        fixture.prepare_api()
        results = []
        for name, path in [('version', 'VERSION'), ('runtime', 'Dockerfile'),
                           ('policy', 'test/ci/suites.json')]:
            write(fixture.repo / path, 'changed\n')
            git(fixture.repo, 'add', path)
            git(fixture.repo, 'commit', '-qm', 'Synthetic changed ' + name)
            result = fixture.resolve()
            self.assertFalse(result['accepted'], result)
            results.append({'case': name, 'result': result})
            git(fixture.repo, 'reset', '--hard', fixture.sha)
        write(fixture.settings, {'go_flags': '-race'})
        result = fixture.resolve()
        self.assertFalse(result['accepted'], result)
        results.append({'case': 'flags', 'result': result})
        write(fixture.settings, {'go_flags': '-trimpath', 'ldflags': '-s -w',
                                'cgo_enabled': '0', 'docker_platform': 'linux/amd64'})
        write(fixture.bin / 'node', '#!/bin/sh\necho v99.0.0\n')
        result = fixture.resolve()
        self.assertFalse(result['accepted'], result)
        results.append({'case': 'toolchain', 'result': result})
        self.capture('changed-inputs', results)

    def test_downloaded_receipt_tampering_and_missing_source_object_are_rejected(self):
        fixture = self.fixture
        receipt = fixture.produce()
        results = []
        for name in ['producer', 'source', 'policy', 'inventory', 'image']:
            changed = copy.deepcopy(receipt)
            if name == 'producer':
                changed['producer']['workflow_sha'] = '0' * 40
            elif name == 'source':
                changed['source']['tree'] = '0' * 40
            elif name == 'policy':
                changed['inputs']['policies']['Dockerfile'] = '0' * 64
            elif name == 'inventory':
                changed['validation']['executed_suites'] = []
            else:
                changed['artifacts']['image'] = 'ghcr.io/kanterlabs/helm:beta'
            fixture.prepare_api(changed)
            result = fixture.resolve()
            self.assertFalse(result['accepted'], result)
            results.append({'case': name, 'result': result})
        # A valid-looking source identity absent from the local object database
        # must fall back even when a trusted API response points at it.
        absent = 'f' * 40
        changed = copy.deepcopy(receipt)
        changed['source']['sha'] = absent
        changed['producer'].update(workflow_sha=absent)
        fixture.prepare_api(changed)
        run = fixture.api[fixture.run_endpoint]
        run['head_sha'] = absent
        for job in fixture.api[fixture.jobs_endpoint]['jobs']:
            job['head_sha'] = absent
        artifact = fixture.api[fixture.artifact_endpoint]['artifacts'][0]
        artifact['name'] = 'helm-validation-' + absent + '-1'
        artifact['workflow_run']['head_sha'] = absent
        result = fixture.resolve()
        self.assertFalse(result['accepted'], result)
        results.append({'case': 'missing-source-object', 'result': result})
        self.capture('receipt-source-tampering', results)

    def test_api_unavailable_is_explicit_fallback(self):
        fixture = self.fixture
        fixture.prepare_api()
        fixture.api = {}
        result = fixture.resolve()
        self.assertFalse(result['accepted'], result)
        self.assertTrue(result['full_required'])
        self.capture('unavailable-api', result)


if __name__ == '__main__':
    unittest.main()
