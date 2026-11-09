"""Real CLI journeys for main-candidate discovery, fresh recheck and safe outputs."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from promotion_fixture import Fixture, IMAGE, git, write

ARTIFACTS = Path(__file__).resolve().parents[2] / '.artifacts/ci-promotion'


class CandidateJourneys(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='helm-candidate-e2e-')
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Fixture(self.temporary.name)
        self.fixture.prepare_api()
        self.fixture.env.update(GITHUB_EVENT_NAME='push', GITHUB_REF='refs/heads/main')
        self.output = self.fixture.base / 'candidate.json'
        self.github_output = self.fixture.base / 'github-output'
        self.discovery = ('repos/KanterLabs/helm/actions/workflows/ci.yml/runs?branch=beta'
                          '&event=push&status=success&head_sha=' + self.fixture.sha + '&per_page=10')
        run = copy.deepcopy(self.fixture.api[self.fixture.run_endpoint])
        self.fixture.api[self.discovery] = {'total_count': 1, 'workflow_runs': [run]}
        ARTIFACTS.mkdir(parents=True, exist_ok=True)

    def capture(self, name, value):
        write(ARTIFACTS / ('candidate-' + name + '.json'), value)

    def invoke(self, *extra, settings=True):
        # GitHub provides a fresh step output file for each wrapper invocation.
        self.github_output.unlink(missing_ok=True)
        args = ['--root', str(self.fixture.repo), '--output', str(self.output),
                '--github-output', str(self.github_output)]
        if settings:
            args += ['--build-settings', str(self.fixture.settings)]
        self.fixture.flush()
        return self.fixture.invoke('promotion-candidate.py', args + list(extra))

    def read(self, result):
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(result.stderr, '')
        value = json.loads(self.output.read_text())
        self.assertIs(type(value['accepted']), bool)
        self.assertEqual(value['full_required'], not value['accepted'])
        expected_image = IMAGE if value['accepted'] else ''
        self.assertEqual(self.github_output.read_text(),
                         'accepted=' + str(value['accepted']).lower() + '\nimage=' + expected_image + '\n')
        return value

    def calls(self):
        return self.fixture.log.read_text().splitlines() if self.fixture.log.exists() else []

    def test_shadow_pr_manual_wrong_ref_never_call_github(self):
        cases = [('default-shadow', [], 'push', 'refs/heads/main'),
                 ('explicit-shadow', ['--mode', 'shadow'], 'push', 'refs/heads/main'),
                 ('pr', ['--mode', 'enforced'], 'pull_request', 'refs/heads/main'),
                 ('manual', ['--mode', 'enforced'], 'workflow_dispatch', 'refs/heads/main'),
                 ('beta', ['--mode', 'enforced'], 'push', 'refs/heads/beta'),
                 ('hostile-ref', ['--mode', 'enforced'], 'push', 'refs/heads/main\nimage=bad')]
        captured = []
        for name, extra, event, ref in cases:
            self.fixture.env.update(GITHUB_EVENT_NAME=event, GITHUB_REF=ref)
            value = self.read(self.invoke(*extra))
            self.assertFalse(value['accepted'])
            self.assertTrue(value['full_required'])
            self.assertEqual(value['reason'], 'not eligible')
            self.assertEqual(self.calls(), [])
            captured.append({'case': name, 'result': value, 'api_calls': self.calls()})
        self.capture('ineligible', captured)

    def test_trusted_main_discovers_actual_sha_and_accepts_one_latest_run(self):
        fixture = self.fixture
        run = copy.deepcopy(fixture.api[fixture.run_endpoint])
        older = dict(run, id=122)
        unrelated = dict(run, id=999, head_sha='0' * 40)
        fixture.api[self.discovery] = {'total_count': 3, 'workflow_runs': [older, unrelated, run]}
        # Discovery must ignore a caller's unverified revision string.
        fixture.env['GITHUB_SHA'] = '0' * 40
        value = self.read(self.invoke('--mode', 'enforced'))
        self.assertTrue(value['accepted'], value)
        self.assertEqual(value['promotion_sha'], fixture.sha)
        self.assertEqual(value['artifact_source_sha'], fixture.sha)
        self.assertEqual(value['producer_run_id'], 123)
        self.assertEqual(value['producer_run_attempt'], 1)
        self.assertEqual(value['artifact_id'], 456)
        calls = self.calls()
        self.assertEqual(calls[0], self.discovery)
        self.assertNotIn('repos/KanterLabs/helm/actions/runs/122', calls)
        self.assertNotIn('repos/KanterLabs/helm/actions/runs/999', calls)
        self.capture('trusted-main', {'result': value, 'api_calls': calls,
                                      'github_output': self.github_output.read_text()})

    def test_explicit_run_rechecks_fresh_provenance_without_discovery(self):
        value = self.read(self.invoke('--mode', 'enforced', '--run-id', '123'))
        self.assertTrue(value['accepted'], value)
        self.assertNotIn(self.discovery, self.calls())
        self.assertGreaterEqual(self.calls().count(self.fixture.run_endpoint), 2)
        self.capture('fresh-gate-recheck', {'result': value, 'api_calls': self.calls()})

    def test_default_settings_path_is_local_to_actual_root(self):
        write(self.fixture.repo / 'test/ci/build-settings.json',
              json.loads(self.fixture.settings.read_text()))
        value = self.read(self.invoke('--mode', 'enforced', settings=False))
        self.assertTrue(value['accepted'], value)
        self.capture('default-build-settings', value)

    def test_missing_malformed_unavailable_discovery_falls_back(self):
        fixture = self.fixture
        original = copy.deepcopy(fixture.api)
        cases = [('missing', {'total_count': 0, 'workflow_runs': []}),
                 ('malformed-list', {'total_count': 1, 'workflow_runs': 'invalid'}),
                 ('malformed-row', {'total_count': 1, 'workflow_runs': [None]}),
                 ('malformed-id', {'total_count': 1, 'workflow_runs': [
                     dict(original[fixture.run_endpoint], id='123\nimage=bad')]}),
                 ('unavailable', None),
                 ('hostile-api-error', {'secret': 'SYNTHETIC_PRIVATE_MARKER\naccepted=true'})]
        captured = []
        for name, listing in cases:
            fixture.api = copy.deepcopy(original)
            if listing is None:
                del fixture.api[self.discovery]
            else:
                fixture.api[self.discovery] = listing
            fixture.log.unlink(missing_ok=True)
            result = self.invoke('--mode', 'enforced')
            value = self.read(result)
            self.assertFalse(value['accepted'], value)
            self.assertEqual(self.calls(), [self.discovery])
            self.assertNotIn('SYNTHETIC_PRIVATE_MARKER', result.stdout + result.stderr + self.output.read_text())
            captured.append({'case': name, 'result': value, 'api_calls': self.calls()})
        self.capture('discovery-fallbacks', captured)

    def test_missing_receipt_scope_sha_and_toolchain_proof_fall_back(self):
        fixture = self.fixture
        receipt = json.loads(fixture.output.read_text())
        captured = []
        for name in ['missing-receipt', 'scope', 'sha', 'toolchain']:
            modified = copy.deepcopy(receipt)
            if name == 'scope':
                modified['validation'].update(scope='focused', effective_scope='focused')
            elif name == 'sha':
                modified['source']['sha'] = '0' * 40
            elif name == 'toolchain':
                modified['build']['toolchain']['node'] = 'v999.0.0'
            fixture.prepare_api(modified)
            if name == 'missing-receipt':
                fixture.api[fixture.artifact_endpoint].update(total_count=0, artifacts=[])
            fixture.log.unlink(missing_ok=True)
            value = self.read(self.invoke('--mode', 'enforced', '--run-id', '123'))
            self.assertFalse(value['accepted'], value)
            self.assertNotIn('producer_run_id', value)
            captured.append({'case': name, 'result': value, 'api_calls': self.calls()})
        self.capture('proof-fallbacks', captured)

    def test_nonpositive_and_hostile_run_ids_never_call_github(self):
        captured = []
        for run_id in ['0', '-1', '123\naccepted=true', '123; touch PWNED']:
            self.fixture.log.unlink(missing_ok=True)
            value = self.read(self.invoke('--mode', 'enforced', '--run-id', run_id))
            self.assertFalse(value['accepted'], value)
            self.assertEqual(self.calls(), [])
            self.assertFalse((self.fixture.repo / 'PWNED').exists())
            captured.append({'supplied_type': 'nonpositive-or-hostile', 'result': value})
        self.capture('invalid-run-ids', captured)

    def test_dirty_checkout_and_newline_image_never_emit_an_image(self):
        fixture = self.fixture
        baseline = copy.deepcopy(fixture.api)
        write(fixture.repo / 'VERSION', 'changed\n')
        value = self.read(self.invoke('--mode', 'enforced'))
        self.assertFalse(value['accepted'])
        self.assertEqual(self.calls(), [])
        git(fixture.repo, 'checkout', '--', 'VERSION')
        receipt = json.loads(fixture.output.read_text())
        receipt['artifacts']['image'] = IMAGE + '\naccepted=true\nimage=unsafe'
        fixture.prepare_api(receipt)
        fixture.api[self.discovery] = baseline[self.discovery]
        value = self.read(self.invoke('--mode', 'enforced'))
        self.assertFalse(value['accepted'])
        self.assertNotIn('unsafe', self.github_output.read_text() + self.output.read_text())
        self.capture('dirty-or-malicious-image', value)


if __name__ == '__main__':
    unittest.main()
