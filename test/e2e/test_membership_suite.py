"""Synthetic proofs for the local membership browser phase; no servers or browsers."""
import json
import unittest

import test_run_suites as base

SPEC = "e2e/membership-sessions.spec.ts"
# Record the requested auth mode as well; the shared fake ignores it.
FAKE = base.FAKE.replace('"binary": a.binary}', '"binary": a.binary, "mode": a.mode, "membership_required": os.environ.get("HELM_E2E_MEMBERSHIP_REQUIRED")}')


class MembershipSuiteProof(unittest.TestCase):
    # Reuse helpers only; inheriting SuiteProof would rerun its tests here.
    command = base.SuiteProof.command
    start = base.SuiteProof.start
    cleanup_processes = base.SuiteProof.cleanup_processes
    finish = base.SuiteProof.finish
    starts = base.SuiteProof.starts

    def setUp(self):
        base.SuiteProof.setUp(self)
        self.fake.write_text(FAKE)

    def log(self):
        return (self.root / "proof.log").read_text()

    def test_full_run_requires_one_local_membership_fixture(self):
        status, phases = self.finish(self.start(extra=("--include-deadline",)))
        self.assertEqual(status, 0, self.log())
        starts = self.starts()
        self.assertEqual(set(starts), {"main", "onboarding", "github", "deadline", "membership"})
        member = starts["membership"]
        self.assertTrue(phases["membership"]["required"])
        self.assertEqual(phases["membership"]["status"], "passed")
        self.assertEqual(member["args"], ["--", SPEC])
        self.assertEqual(member["mode"], "local")
        self.assertEqual(member["workers"], "1")
        self.assertIsNone(member["stall"])
        self.assertEqual(starts["onboarding"]["args"], ["--", "e2e/onboarding.spec.ts"])
        self.assertEqual(int(member["port"]), 21080 + 3 * 100 + 50)
        self.assertEqual(len({s["port"] for s in starts.values()}), 5)
        self.assertEqual(len({s["work"] for s in starts.values()}), 5)
        main_end = float((self.artifacts / "main/finished").read_text())
        self.assertGreaterEqual(member["start"], main_end)
        # Three workers cannot host four ancillary fixtures at once.
        first_end = min(float((self.artifacts / n / "finished").read_text())
                        for n in ("onboarding", "github", "deadline"))
        self.assertGreaterEqual(member["start"], first_end)
        summary = json.loads((self.artifacts / "suites.json").read_text())
        self.assertIn("membership", [p["name"] for p in summary["phases"]])

    def test_one_worker_budget_serializes_membership(self):
        status, _ = self.finish(self.start(workers=1))
        self.assertEqual(status, 0, self.log())
        starts = self.starts()
        for name in ("onboarding", "github"):
            finished = float((self.artifacts / name / "finished").read_text())
            self.assertGreaterEqual(starts["membership"]["start"], finished)

    def test_unrelated_focus_does_not_acquire_membership(self):
        selection = ("e2e/board.spec.ts", "--grep", SPEC)
        status, phases = self.finish(self.start(selection=selection))
        self.assertEqual(status, 0, self.log())
        self.assertEqual(set(self.starts()), {"main", "onboarding"})
        self.assertEqual(phases["membership"]["status"], "skipped")
        self.assertFalse(phases["membership"]["required"])

    def test_explicit_focus_runs_the_local_membership_phase(self):
        status, phases = self.finish(self.start(selection=(SPEC,)))
        self.assertEqual(status, 0, self.log())
        starts = self.starts()
        self.assertEqual(set(starts), {"main", "onboarding", "membership"})
        self.assertEqual(starts["main"]["args"], ["--", SPEC])
        self.assertEqual(starts["membership"]["mode"], "local")
        self.assertEqual(phases["github"]["status"], "skipped")

    def test_membership_failure_fails_closed_and_keeps_siblings(self):
        self.env = {"FAKE_FAIL": "membership"}
        status, phases = self.finish(self.start())
        self.assertNotEqual(status, 0)
        self.assertEqual(phases["membership"]["status"], "failed")
        self.assertEqual(phases["membership"]["exit_status"], 9)
        for name in ("main", "onboarding", "github"):
            self.assertEqual(phases[name]["status"], "passed")

    def test_required_auth_marker_is_owned_by_membership_phase(self):
        self.env = {"HELM_E2E_MEMBERSHIP_REQUIRED": "true"}
        status, _ = self.finish(self.start(selection=(SPEC,)))
        self.assertEqual(status, 0, self.log())
        starts = self.starts()
        self.assertEqual(starts["membership"]["membership_required"], "true")
        self.assertIsNone(starts["main"]["membership_required"])
        self.assertIsNone(starts["onboarding"]["membership_required"])

    def test_single_membership_focus_preserves_opaque_options(self):
        selection = (SPEC, "--grep", "review:", "--retries", "0")
        status, _ = self.finish(self.start(selection=selection))
        self.assertEqual(status, 0, self.log())
        self.assertEqual(self.starts()["membership"]["args"], ["--", *selection])


if __name__ == "__main__":
    unittest.main()
