"""Verify full means every configured phase on both beta and newer feature trees."""
import json
import unittest
import test_plan_journeys as fixtures


class ConfiguredSuiteJourneys(unittest.TestCase):
    setUp = fixtures.PlanJourneys.setUp
    git = fixtures.PlanJourneys.git
    write = fixtures.PlanJourneys.write
    commit = fixtures.PlanJourneys.commit
    changed = fixtures.PlanJourneys.changed
    pr = fixtures.PlanJourneys.pr
    plan = fixtures.PlanJourneys.plan
    evidence = fixtures.PlanJourneys.evidence
    verify = fixtures.PlanJourneys.verify

    def full(self):
        self.changed('internal/feature.go')
        plan = self.plan()
        results = {job: {'result': 'success'} for job in plan['expected_jobs']}
        evidence = self.evidence(plan)
        return plan, results, evidence

    def test_full_suite_on_beta_without_unmerged_membership_phase(self):
        self.git('rm', 'web/e2e/membership-sessions.spec.ts')
        self.commit()
        plan, results, evidence = self.full()
        phases = evidence / 'suites/suites.json'
        value = json.loads(phases.read_text())
        value['phases'] = [p for p in value['phases'] if p['name'] != 'membership']
        phases.write_text(json.dumps(value))
        self.verify(plan, results, evidence)

    def test_newer_source_still_requires_membership_phase(self):
        plan, results, evidence = self.full()
        phases = evidence / 'suites/suites.json'
        value = json.loads(phases.read_text())
        value['phases'] = [p for p in value['phases'] if p['name'] != 'membership']
        phases.write_text(json.dumps(value))
        self.verify(plan, results, evidence, okay=False)


if __name__ == '__main__':
    unittest.main()
