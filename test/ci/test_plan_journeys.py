"""Failure-first real Git/subprocess journeys; sanitized receipts are retained."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
ARTIFACTS = HERE.parents[1] / '.artifacts/ci-plans'
COMPONENT = 'web/src/lib/components/NotificationItem.svelte'
SMOKE = 'e2e/helm-shell.spec.ts'
NOTIFICATIONS = 'e2e/notifications.spec.ts'


class PlanJourneys(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        self.git('init', '-q', '-b', 'beta')
        self.git('config', 'user.email', 'ci@example.invalid')
        self.git('config', 'user.name', 'CI Journey')
        for name in ('README.md', COMPONENT, 'web/' + SMOKE, 'web/' + NOTIFICATIONS,
                     'web/e2e/onboarding.spec.ts', 'web/e2e/membership-sessions.spec.ts',
                     'web/e2e/luna-account-deadline-stability.spec.ts', 'docs/CONFIGURATION.md'):
            self.write(name)
        self.base = self.commit()
        ARTIFACTS.mkdir(parents=True, exist_ok=True)
        self.sequence = 0

    def git(self, *args):
        result = subprocess.run(['git', '-C', str(self.repo), *args], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def write(self, name, content='initial\n'):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic journey')
        return self.git('rev-parse', 'HEAD')

    def pr(self, draft=False, target='beta'):
        return {'action': 'synchronize', 'pull_request': {'draft': draft,
            'head': {'sha': self.git('rev-parse', 'HEAD'), 'ref': 'feature'},
            'base': {'sha': self.base, 'ref': target}}}

    def plan(self, event=None, name='pull_request', mode='enforced', okay=True):
        self.sequence += 1
        event_path = self.root / 'event.json'
        event_path.write_text(json.dumps(event if event is not None else self.pr()))
        output = self.root / 'plan.json'
        output.unlink(missing_ok=True)
        github = self.root / 'github-output'
        command = [sys.executable, str(HERE / 'plan.py'), '--event', str(event_path),
                   '--event-name', name, '--repository', str(self.repo), '--output', str(output),
                   '--github-output', str(github), '--mode', mode]
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, okay, result.stderr)
        if not okay:
            return None
        value = json.loads(output.read_text())
        destination = ARTIFACTS / f'{self._testMethodName}-{self.sequence}.plan.json'
        destination.write_text(json.dumps(value, indent=2) + '\n')
        self.assertEqual(value['source']['sha'], self.git('rev-parse', 'HEAD'))
        self.assertEqual(value['source']['tree'], self.git('rev-parse', 'HEAD^{tree}'))
        self.assertNotIn('command', github.read_text())
        return value

    def changed(self, path):
        self.write(path, 'changed\n')
        self.commit()

    def test_docs_pr_push_and_shadow_contexts(self):
        self.changed('README.md')
        self.assertEqual(self.plan()['proposed_scope'], 'docs')
        shadow = self.plan(mode='shadow')
        self.assertEqual(shadow['effective_scope'], 'full')
        self.assertEqual(shadow['expected_jobs'], ['checks', 'browser', 'race', 'container'])
        push = {'ref': 'refs/heads/beta', 'before': self.base, 'after': self.git('rev-parse', 'HEAD')}
        plan = self.plan(push, 'push')
        self.assertEqual(plan['effective_scope'], 'docs')
        self.assertFalse(plan['publish'])
        self.assertFalse(plan['deploy'])

    def test_bounded_ui_includes_core_smoke_and_complete_diff(self):
        self.changed(COMPONENT)
        plan = self.plan()
        self.assertEqual(plan['effective_scope'], 'focused')
        self.assertEqual(plan['selected_suites'], sorted([SMOKE, NOTIFICATIONS]))
        self.changed('internal/auth/session.go')
        self.changed('README.md')
        plan = self.plan()
        self.assertEqual(plan['effective_scope'], 'full')
        self.assertEqual(len(plan['diff']['paths']), 3)

    def test_highrisk_unknown_embedded_config_and_helpers(self):
        for path in ('web/src/app.css', 'internal/auth/a.go', 'openapi.yaml',
                     '.github/workflows/ci.yml', 'new-unknown', 'web/e2e/support/helper.ts',
                     'docs/PUBLIC_ACCESS.md', 'docs/TICKET_WEBHOOKS.md', 'docs/CONFIGURATION.md',
                     'web/src/lib/components/TaskWatchToggle.svelte'):
            with self.subTest(path=path):
                self.git('reset', '--hard', self.base)
                self.changed(path)
                self.assertEqual(self.plan()['effective_scope'], 'full')

    def test_draft_ready_main_and_base_advance(self):
        self.changed(COMPONENT)
        draft = self.pr(True)
        self.assertEqual(self.plan(draft)['effective_scope'], 'fast')
        self.assertFalse(self.plan(draft)['merge_ready'])
        ready = self.pr()
        ready['action'] = 'ready_for_review'
        self.assertEqual(self.plan(ready)['effective_scope'], 'focused')
        self.assertEqual(self.plan(self.pr(target='main'))['effective_scope'], 'full')
        head = self.git('rev-parse', 'HEAD')
        self.git('checkout', '-qb', 'advance', self.base)
        self.changed('internal/server.go')
        self.base = self.git('rev-parse', 'HEAD')
        self.git('checkout', '--detach', head)
        ready['pull_request']['base']['sha'] = self.base
        self.assertEqual(self.plan(ready)['effective_scope'], 'full')
        self.git('checkout', 'advance')
        self.git('merge', '--no-ff', '-qm', 'synthetic merge', head)
        ready['pull_request']['base']['sha'] = self.base
        plan = self.plan(ready)
        self.assertEqual(plan['source']['base_sha'], self.base)
        self.assertEqual(plan['source']['merge_sha'], self.git('rev-parse', 'HEAD'))
        self.assertNotEqual(plan['source']['sha'], head)

    def test_rename_deletion_missing_and_empty_diff_are_full(self):
        self.git('mv', 'README.md', 'docs/readme.md')
        self.commit()
        self.assertEqual(self.plan()['effective_scope'], 'full')
        self.git('reset', '--hard', self.base)
        self.git('rm', 'README.md')
        self.commit()
        self.assertEqual(self.plan()['effective_scope'], 'full')
        event = self.pr()
        event['pull_request']['base']['sha'] = 'a' * 40
        self.assertFalse(self.plan(event)['diff']['complete'])
        self.git('reset', '--hard', self.base)
        self.assertEqual(self.plan()['effective_scope'], 'full')

    def test_incomplete_count_and_wrong_pr_checkout_fail_closed(self):
        self.changed(COMPONENT)
        event = self.pr()
        event['pull_request']['changed_files'] = 2
        plan = self.plan(event)
        self.assertFalse(plan['diff']['complete'])
        self.assertEqual(plan['effective_scope'], 'full')
        self.git('checkout', '-qb', 'unrelated', self.base)
        self.changed('another-file')
        self.plan(event, okay=False)

    def test_shallow_captured_merge_retains_source_but_requires_full(self):
        self.changed(COMPONENT)
        head = self.git('rev-parse', 'HEAD')
        self.git('checkout', '-qb', 'advance', self.base)
        self.changed('README.md')
        self.base = self.git('rev-parse', 'HEAD')
        self.git('merge', '--no-ff', '-qm', 'captured merge', head)
        event = self.pr()
        event['pull_request']['head']['sha'] = head
        shallow = self.root / 'shallow'
        result = subprocess.run(['git', 'clone', '-q', '--depth=1', self.repo.as_uri(),
                                 str(shallow)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.repo = shallow
        plan = self.plan(event)
        self.assertEqual(plan['effective_scope'], 'full')
        self.assertFalse(plan['diff']['complete'])
        self.assertEqual(plan['source']['merge_sha'], plan['source']['sha'])

    def test_pushes_manual_and_unsupported_fail_closed(self):
        self.changed(COMPONENT)
        for branch in ('beta', 'main'):
            push = {'ref': 'refs/heads/' + branch, 'before': self.base,
                    'after': self.git('rev-parse', 'HEAD')}
            plan = self.plan(push, 'push')
            self.assertEqual(plan['effective_scope'], 'full')
            self.assertTrue(plan['publish'])
            self.assertTrue(plan['deploy'])
            push['before'] = '0' * 40
            self.assertFalse(self.plan(push, 'push')['diff']['complete'])
        for scope in ('fast', 'focused', 'full'):
            plan = self.plan({'inputs': {'scope': scope}}, 'workflow_dispatch')
            self.assertEqual(plan['effective_scope'], scope)
            self.assertFalse(plan['publish'])
            self.assertFalse(plan['deploy'])
        self.assertEqual(self.plan({}, 'workflow_dispatch')['effective_scope'], 'full')
        self.plan({}, 'schedule', okay=False)
        self.plan(self.pr(target='other'), okay=False)
        self.plan({'inputs': {'scope': '$(invalid)'}}, 'workflow_dispatch', okay=False)
        self.plan({'ref': 'refs/heads/beta', 'before': self.base, 'after': self.base},
                  'push', okay=False)

    def evidence(self, plan):
        directory = self.root / 'evidence'
        directory.mkdir(exist_ok=True)
        (directory / 'evidence.metadata').write_text(
            f"revision={plan['source']['sha']}\nexit_status=0\n")
        provenance = {'schema': 1, 'source': {'head': plan['source']['sha'],
            'fingerprint': 'a' * 64}, 'configuration': {'race': True},
            'binaries': {key: {'sha256': 'b' * 64} for key in ('helm', 'helm-githubbeta')}}
        (directory / 'build-provenance.json').write_text(json.dumps(provenance))
        phases = ['main', 'onboarding', 'github', 'deadline', 'membership']
        suites = directory / 'suites'
        suites.mkdir(exist_ok=True)
        (suites / 'suites.json').write_text(json.dumps({'signal': 0, 'phases': [
            {'name': p, 'required': True, 'status': 'passed', 'exit_status': 0} for p in phases]}))
        specs = [name for name in plan['selected_suites'] if name.endswith('.spec.ts')]
        report = {'errors': [], 'stats': {'unexpected': 0, 'expected': len(specs)},
                  'suites': [{'file': name, 'specs': [{'file': name, 'tests': [
                      {'status': 'expected', 'results': [{'status': 'passed'}]}]}]} for name in specs]}
        (suites / 'results.json').write_text(json.dumps(report))
        return directory

    def verify(self, plan, results, evidence=None, okay=True):
        self.sequence += 1
        path = self.root / 'verify-plan.json'
        path.write_text(json.dumps(plan))
        jobs = self.root / 'results.json'
        jobs.write_text(json.dumps(results))
        output = self.root / 'receipt.json'
        command = [sys.executable, str(HERE / 'verify-plan.py'), '--plan', str(path),
                   '--results', str(jobs), '--output', str(output)]
        if evidence:
            command += ['--browser-evidence', str(evidence)]
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, okay, result.stderr)
        receipt = json.loads(output.read_text())
        self.assertEqual(receipt['passed'], okay)
        (ARTIFACTS / f'{self._testMethodName}-{self.sequence}.receipt.json').write_text(
            json.dumps(receipt, indent=2) + '\n')
        return receipt

    def test_gate_rejects_missing_cancelled_skipped_failed_and_absent_evidence(self):
        self.changed('internal/server.go')
        plan = self.plan()
        results = {job: {'result': 'success'} for job in plan['expected_jobs']}
        self.verify(plan, results, okay=False)
        evidence = self.evidence(plan)
        receipt = self.verify(plan, results, evidence)
        self.assertEqual(receipt['effective_scope'], 'full')
        self.assertEqual(receipt['executed_suites'], plan['selected_suites'])
        for status in ('cancelled', 'skipped', 'failure', None):
            failed = copy.deepcopy(results)
            if status:
                failed['browser']['result'] = status
            else:
                del failed['browser']
            self.verify(plan, failed, evidence, okay=False)

    def test_gate_rejects_missing_reports_phases_wrong_source_and_scope_forgery(self):
        self.changed('internal/server.go')
        plan = self.plan()
        results = {job: {'result': 'success'} for job in plan['expected_jobs']}
        evidence = self.evidence(plan)
        (evidence / 'suites/results.json').unlink()
        self.verify(plan, results, evidence, okay=False)
        self.evidence(plan)
        (evidence / 'suites/suites.json').write_text('{"phases":[]}')
        self.verify(plan, results, evidence, okay=False)
        self.evidence(plan)
        provenance = json.loads((evidence / 'build-provenance.json').read_text())
        provenance['source']['head'] = '0' * 40
        (evidence / 'build-provenance.json').write_text(json.dumps(provenance))
        self.verify(plan, results, evidence, okay=False)
        self.evidence(plan)
        forged = copy.deepcopy(plan)
        forged['expected_jobs'] = ['checks']
        self.verify(forged, results, evidence, okay=False)

    def test_gate_rejects_nonrace_and_skipped_or_failing_selected_specs(self):
        self.changed(COMPONENT)
        plan = self.plan()
        results = {job: {'result': 'success'} for job in plan['expected_jobs']}
        evidence = self.evidence(plan)
        self.verify(plan, results, evidence)
        provenance_path = evidence / 'build-provenance.json'
        provenance = json.loads(provenance_path.read_text())
        provenance['configuration']['race'] = False
        provenance_path.write_text(json.dumps(provenance))
        self.verify(plan, results, evidence, okay=False)
        self.evidence(plan)
        report_path = evidence / 'suites/results.json'
        report = json.loads(report_path.read_text())
        report['suites'][0]['specs'][0]['tests'][0] = {
            'status': 'skipped', 'results': [{'status': 'skipped'}]}
        report_path.write_text(json.dumps(report))
        self.verify(plan, results, evidence, okay=False)
        self.evidence(plan)
        report = json.loads(report_path.read_text())
        report['stats']['unexpected'] = 1
        report_path.write_text(json.dumps(report))
        self.verify(plan, results, evidence, okay=False)

    def test_gate_rejects_forged_release_flags_and_focused_inventory(self):
        self.changed(COMPONENT)
        plan = self.plan()
        results = {job: {'result': 'success'} for job in plan['expected_jobs']}
        evidence = self.evidence(plan)
        for key, item in [('publish', True), ('deploy', True),
                          ('selected_suites', [SMOKE]),
                          ('event', {**plan['event'], 'name': 'schedule'})]:
            with self.subTest(key=key):
                forged = copy.deepcopy(plan)
                forged[key] = item
                self.verify(forged, results, evidence, okay=False)


if __name__ == '__main__':
    unittest.main(verbosity=2)
