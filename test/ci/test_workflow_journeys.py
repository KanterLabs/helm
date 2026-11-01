"""Compile and inspect the actual event-to-deployment workflow dependency graph."""
import itertools
import json
import re
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
ARTIFACTS = ROOT / '.artifacts/ci-workflows'
REQUIRED = {'checks', 'browser', 'race', 'container'}
BROWSER_RUNNER = "${{ fromJSON(github.event_name != 'pull_request' && (github.ref == 'refs/heads/beta' || github.ref == 'refs/heads/main') && vars.HELM_WARM_RUNNER_ENABLED == 'true' && '{\"group\":\"helm-runners\",\"labels\":\"homelab-heavy\"}' || '\"ubuntu-24.04\"') }}"


def needs(job):
    value = job.get('needs', [])
    return {value} if isinstance(value, str) else set(value)


class WorkflowJourneys(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        result = subprocess.run(['node', str(ROOT / 'test/ci/workflow_inspect.mjs'), str(ROOT)],
                                text=True, capture_output=True)
        if result.returncode:
            raise AssertionError(result.stderr)
        cls.snapshot = json.loads(result.stdout)
        cls.documents = cls.snapshot['documents']
        cls.workflow = cls.documents['.github/workflows/ci.yml']
        cls.jobs = cls.workflow['jobs']
        ARTIFACTS.mkdir(parents=True, exist_ok=True)
        (ARTIFACTS / 'compiled-workflow.json').write_text(result.stdout)

    def tearDown(self):
        # unittest retains each failing assertion; persist its result beside the
        # parsed graph so baseline red and later green are repeatable evidence.
        result = self._outcome.result
        errors = [text for case, text in result.failures + result.errors
                  if case is self or getattr(case, 'test_case', None) is self]
        (ARTIFACTS / (self._testMethodName + '.json')).write_text(json.dumps({
            'journey': self._testMethodName, 'passed': not errors, 'errors': errors,
            'workflow': '.github/workflows/ci.yml'}, indent=2) + '\n')

    def job(self, name):
        self.assertTrue(name in self.jobs, f'missing workflow job: {name}')
        return self.jobs[name]

    def ancestors(self, name, seen=None):
        seen = set() if seen is None else seen
        self.assertNotIn(name, seen, 'cyclic workflow job graph')
        parents = needs(self.job(name))
        return parents | set().union(*(self.ancestors(parent, seen | {name}) for parent in parents))

    def expanded(self, value):
        """Follow real local references instead of assuming checks remain inline."""
        if isinstance(value, list):
            return [self.expanded(item) for item in value]
        if not isinstance(value, dict):
            return value
        result = {key: self.expanded(item) for key, item in value.items()}
        if isinstance(value.get('run'), str):
            pending = re.findall(r'\btest/ci/[A-Za-z0-9_./-]+\.(?:sh|py)\b', value['run'])
            included = {}
            while pending:
                name = pending.pop()
                if name not in included:
                    included[name] = self.snapshot['scripts'][name]
                    pending += self.snapshot['scriptDependencies'][name]
            result['local_scripts'] = included
        ref = value.get('uses', '')
        if ref.startswith('./'):
            relative = ref[2:]
            candidates = [relative] if relative.endswith(('.yml', '.yaml')) else [
                relative + '/action.yml', relative + '/action.yaml']
            dependency = next(name for name in candidates if name in self.documents)
            result['local_definition'] = self.expanded(self.documents[dependency])
        return result

    def text(self, value):
        return json.dumps(self.expanded(value), sort_keys=True)

    def conditions(self, queries, artifact):
        result = subprocess.run(['node', str(ROOT / 'test/ci/workflow_inspect.mjs'),
                                 str(ROOT), '--conditions'], input=json.dumps(queries),
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        captured = json.loads(result.stdout)
        (ARTIFACTS / (artifact + '.json')).write_text(json.dumps({
            'queries': queries, 'decisions': captured}, indent=2) + '\n')
        return [value['admitted'] for value in captured]

    def context(self, job, event='push', branch='main'):
        outputs = {key: 'true' for key in self.job('plan').get('outputs', {})}
        outputs.update(effective_scope='full', scope='full', publish='true', deploy='true')
        return {'job': job, 'github': {'event_name': event, 'ref': 'refs/heads/' + branch,
                'event': {'pull_request': {'draft': False}}}, 'vars': {}, 'inputs': {},
                'needs': {name: {'result': 'success', 'outputs': outputs.copy()}
                          for name in self.jobs}}

    def test_real_publication_conditions_reject_untrusted_or_unsuccessful_journeys(self):
        self.job('publish')
        queries, expected = [], []
        for event, branch in itertools.product(('pull_request', 'workflow_dispatch', 'push'),
                                                ('main', 'beta', 'feature')):
            queries.append(self.context('publish', event, branch))
            expected.append(event == 'push' and branch in ('main', 'beta'))
        for dependency, status in itertools.product(sorted(REQUIRED | {'plan', 'pr-gate'}),
                                                    ('failure', 'cancelled', 'skipped')):
            query = self.context('publish')
            query['needs'][dependency]['result'] = status
            queries.append(query)
            expected.append(False)
        query = self.context('publish')
        query['needs']['plan']['outputs']['publish'] = 'false'
        queries.append(query)
        expected.append(False)
        self.assertEqual(self.conditions(queries, 'publication-admission-matrix'), expected)

    def test_real_deployment_conditions_honor_manual_pause_and_failed_evidence(self):
        self.job('validation_receipt')
        queries, expected = [], []
        for job, branch, pause in (('beta_deploy', 'beta', 'HELM_BETA_DEPLOY_PAUSED'),
                                   ('deploy', 'main', 'HELM_PRODUCTION_DEPLOY_PAUSED')):
            for event in ('pull_request', 'workflow_dispatch', 'push'):
                queries.append(self.context(job, event, branch))
                expected.append(event == 'push')
            query = self.context(job, branch=branch)
            query['vars'][pause] = 'true'
            queries.append(query)
            expected.append(False)
            for dependency, status in itertools.product(('publish', 'validation_receipt'),
                                                        ('failure', 'cancelled', 'skipped')):
                query = self.context(job, branch=branch)
                query['needs'][dependency]['result'] = status
                queries.append(query)
                expected.append(False)
        self.assertEqual(self.conditions(queries, 'deployment-admission-matrix'), expected)

    def test_verified_promotion_survives_deliberately_skipped_browser_needs(self):
        queries = []
        for job in ('publish', 'validation_receipt', 'deploy'):
            self.job(job)
            query = self.context(job)
            query['needs']['plan']['outputs']['effective_scope'] = 'promotion'
            for dependency in ('browser', 'race'):
                query['needs'][dependency]['result'] = 'skipped'
            queries.append(query)
        self.assertEqual(self.conditions(queries, 'promotion-admission-matrix'), [True] * 3)

    def test_planner_failure_still_admits_historical_checks_and_gate(self):
        queries = []
        for job, status in itertools.product(('checks', 'pr-gate'),
                                             ('failure', 'cancelled', 'skipped')):
            self.job(job)
            query = self.context(job, 'pull_request', 'feature')
            query['needs']['plan']['result'] = status
            queries.append(query)
        self.assertEqual(self.conditions(queries, 'planner-failure-admission'), [True] * len(queries))

    def test_event_router_and_manual_validation_contract(self):
        self.assertEqual(self.workflow['name'], 'Helm CI and deploy')
        events = self.workflow['on']
        self.assertEqual(set(events['push']['branches']), {'main', 'beta'})
        self.assertTrue({'opened', 'synchronize', 'reopened', 'ready_for_review',
                         'converted_to_draft'} <= set((events['pull_request'] or {}).get('types', [])))
        inputs = events['workflow_dispatch']['inputs']
        scopes = [value for value in inputs.values() if isinstance(value, dict) and
                  set(value.get('options', [])) == {'full', 'fast', 'focused'}]
        self.assertEqual(len(scopes), 1, 'manual validation needs one bounded scope selector')
        self.assertEqual(scopes[0]['default'], 'full')

    def test_plan_captures_revisions_and_keeps_historical_context_fail_closed(self):
        plan = self.job('plan')
        plan_text = self.text(plan)
        self.assertIn('test/ci/plan.py', plan_text)
        self.assertIn('HELM_CI_PLAN_MODE', plan_text)
        self.assertIn('shadow', plan_text)
        self.assertTrue(any(step.get('with', {}).get('fetch-depth') == 0
                            for step in plan.get('steps', []) if 'checkout@' in step.get('uses', '')),
                        'classifier needs complete actual Git history')
        for name in REQUIRED:
            self.job(name)
        checks = self.job('checks')
        self.assertIn('plan', self.ancestors('checks'))
        self.assertIn('always()', str(checks.get('if', '')),
                      'historical checks context must fail when plan fails')
        self.assertIn('needs.plan.result', self.text(checks))

    def test_gate_waits_for_every_validation_and_verifies_execution_evidence(self):
        gate = self.job('pr-gate')
        self.assertTrue(REQUIRED | {'plan'} <= self.ancestors('pr-gate'))
        self.assertIn('always()', str(gate.get('if', '')))
        gate_text = self.text(gate)
        self.assertIn('test/ci/verify-plan.py', gate_text)
        self.assertIn('toJSON(needs)', gate_text)
        self.assertIn('--browser-evidence', gate_text)
        self.assertIn('download-artifact@', gate_text)

    def test_build_context_cannot_publish_or_use_registry_write_credentials(self):
        container = self.job('container')
        self.assertNotEqual(container.get('permissions', {}).get('packages'), 'write')
        text = self.text(container)
        for forbidden in ('docker login', 'docker push', 'GHCR_TOKEN', 'packages: write'):
            self.assertNotIn(forbidden, text)
        for required in ('docker build', 'trivy', 'healthcheck', 'codex.sha256',
                         'cloudflared.sha256', 'upload-artifact@'):
            self.assertIn(required, text)

    def test_publication_is_push_only_and_after_complete_validation(self):
        publish = self.job('publish')
        self.assertEqual(publish.get('permissions', {}).get('packages'), 'write')
        self.assertEqual(self.workflow.get('permissions', {}).get('contents'), 'read')
        self.assertNotEqual(self.workflow.get('permissions', {}).get('packages'), 'write')
        condition = str(publish.get('if', ''))
        for guard in ("github.event_name == 'push'", 'refs/heads/main', 'refs/heads/beta',
                      'needs.plan.outputs.publish', 'needs.pr-gate.result'):
            self.assertIn(guard, condition)
        self.assertTrue(REQUIRED | {'pr-gate', 'plan'} <= self.ancestors('publish'))
        text = self.text(publish)
        self.assertIn('docker push', text)
        self.assertIn('sha256:', text)
        for name, job in self.jobs.items():
            if name != 'publish':
                self.assertNotEqual(job.get('permissions', {}).get('packages'), 'write', name)

    def test_receipt_is_created_only_after_gate_and_publication(self):
        receipt = self.job('validation_receipt')
        self.assertTrue({'publish', 'pr-gate'} <= self.ancestors('validation_receipt'))
        text = self.text(receipt)
        self.assertIn('test/ci/receipt.py', text)
        self.assertIn('upload-artifact@', text)
        self.assertIn('needs.publish', text)

    def test_browser_keeps_reviewed_runner_and_real_execution(self):
        browser = self.job('browser')
        self.assertEqual(browser['runs-on'], BROWSER_RUNNER)
        text = self.text(browser)
        self.assertIn('browser-validation', text)
        self.assertIn('needs.plan.outputs', text)
        all_text = self.text(self.workflow)
        self.assertIn('test/e2e/run.sh', all_text)
        runner = (ROOT / 'test/e2e/run.sh').read_text()
        self.assertIn('test/e2e/run-suites.py', runner)
        self.assertIn('HELM_E2E_INCLUDE_DEADLINES', text)
        invocations = [step for step in browser.get('steps', [])
                       if 'browser-validation' in step.get('uses', '')]
        self.assertEqual(len(invocations), 1)
        action = self.expanded(invocations[0])['local_definition']
        workers = invocations[0].get('with', {}).get('workers',
                    action.get('inputs', {}).get('workers', {}).get('default'))
        self.assertTrue(str(workers).isdigit() and 1 <= int(workers) <= 4,
                        'reviewed browser invocation must explicitly cap workers at four')
        self.assertIn('test-deployment-security.sh', all_text)
        self.assertIn('actionlint', all_text)

    def test_protected_deployments_need_receipt_tip_pause_and_non_cancel_lock(self):
        for name, branch, environment, pause in (
                ('beta_deploy', 'beta', 'beta', 'HELM_BETA_DEPLOY_PAUSED'),
                ('deploy', 'main', 'production', 'HELM_PRODUCTION_DEPLOY_PAUSED')):
            with self.subTest(job=name):
                job = self.job(name)
                self.assertTrue({'publish', 'validation_receipt'} <= self.ancestors(name))
                condition = str(job.get('if', ''))
                self.assertIn("github.event_name == 'push'", condition)
                self.assertIn('refs/heads/' + branch, condition)
                self.assertIn(pause, condition)
                text = self.text(job)
                for required in ('ls-remote', 'GITHUB_SHA', 'coolify-deploy.py',
                                 'steps.tip.outputs.current', 'HELM_RELEASE_SHA'):
                    self.assertIn(required, text)
                expanded = self.expanded(job)
                leaves = list(expanded.get('local_definition', {}).get('jobs', {}).values()) or [expanded]
                self.assertTrue(any('environment' in leaf for leaf in leaves),
                                'deployment must use a real job-level protected environment')
                for leaf in leaves:
                    protected = leaf.get('environment')
                    if isinstance(protected, dict):
                        protected = protected.get('name')
                    if protected and 'inputs.' in protected:
                        key = protected.split('inputs.', 1)[1].split()[0].rstrip('}')
                        protected = job.get('with', {}).get(key)
                    self.assertEqual(protected, environment)
                    lock = leaf.get('concurrency', {})
                    self.assertIs(lock.get('cancel-in-progress'), False)
                    self.assertTrue(lock.get('group'))
                self.assertIn('needs.publish', text)
                self.assertIn('validation_receipt', text)


if __name__ == '__main__':
    unittest.main(verbosity=2)
