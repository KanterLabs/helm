"""Synthetic isolated proofs; failure inventory is TIMING_FAILURES.md."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import browser_timings as timing


class TimingProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.web = self.repo / "web"
        (self.web / "e2e").mkdir(parents=True)
        for name in ("a", "b", "c", "new"):
            (self.web / f"e2e/{name}.spec.ts").write_text(f"// {name}\n")

    def entry(self, name, seconds):
        path = self.web / f"e2e/{name}.spec.ts"
        return {"seconds": seconds, "samples": 1,
                "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}

    def profile(self):
        return {"schema": 1, "source_revision": "a" * 40,
                "collector": "helm-playwright-file-timings-v1",
                "playwright_versions": ["1.62.1"], "report_sha256": ["b" * 64],
                "files": {f"web/e2e/{name}.spec.ts": self.entry(name, seconds)
                          for name, seconds in (("a", 9), ("b", 5), ("c", 4))}}

    def load(self, data):
        path = self.repo / "profile.json"
        path.write_text(json.dumps(data))
        return timing.load_profile(path, self.web)

    def test_balanced_complete_stable_and_new_file_fallback(self):
        profile = self.load(self.profile())
        selected = [f"web/e2e/{name}.spec.ts" for name in ("new", "c", "b", "a")]
        plan = timing.plan_files(selected, profile, self.web, 2)
        self.assertEqual(plan["workers"], [
            {"files": ["web/e2e/a.spec.ts", "web/e2e/c.spec.ts"], "estimated_seconds": 13},
            {"files": ["web/e2e/b.spec.ts", "web/e2e/new.spec.ts"], "estimated_seconds": 10}])
        self.assertEqual(plan["estimated_files"], ["web/e2e/new.spec.ts"])
        self.assertEqual(plan, timing.plan_files(list(reversed(selected)), profile, self.web, 2))
        (self.web / "e2e/a.spec.ts").write_text("// changed\n")
        plan = timing.plan_files(selected, profile, self.web, 8)
        self.assertEqual(plan["estimated_files"], ["web/e2e/a.spec.ts", "web/e2e/new.spec.ts"])
        self.assertEqual(sorted(f for w in plan["workers"] for f in w["files"]), sorted(selected))
        self.assertEqual(len([w for w in plan["workers"] if w["files"]]), 4)

    def test_rejects_unsafe_or_corrupt_profiles_including_unselected_files(self):
        for bad in ("../a.spec.ts", "/tmp/a.spec.ts", "web/e2e/../a.spec.ts",
                    "web/e2e/a.spec.ts\nsecret", "web/e2e/a.spec.ts:1", "web/e2e/missing.spec.ts"):
            profile = self.profile()
            profile["files"][bad] = self.entry("a", 1)
            with self.subTest(path=bad), self.assertRaises(ValueError):
                self.load(profile)
        outside = self.repo / "outside.spec.ts"
        outside.write_text("// outside")
        (self.web / "e2e/escape.spec.ts").symlink_to(outside)
        profile = self.profile()
        profile["files"]["web/e2e/escape.spec.ts"] = self.entry("a", 1)
        with self.assertRaises(ValueError):
            self.load(profile)
        for seconds in (0, -1, float("nan"), float("inf"), True, "5"):
            profile = self.profile()
            profile["files"]["web/e2e/a.spec.ts"]["seconds"] = seconds
            with self.subTest(seconds=seconds), self.assertRaises(ValueError):
                self.load(profile)
        path = self.repo / "duplicate.json"
        path.write_text('{"schema":1,"schema":1}')
        with self.assertRaises(ValueError):
            timing.load_profile(path, self.web)

    def test_collector_walks_nested_retries_averages_and_excludes_payloads(self):
        subprocess.run(["git", "init", "-q", str(self.repo)], check=True)
        subprocess.run(["git", "-C", str(self.repo), "add", "web"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "-c", "user.name=Proof", "-c",
                        "user.email=proof@example.invalid", "commit", "-qm", "Synthetic"], check=True)
        revision = subprocess.check_output(["git", "-C", str(self.repo), "rev-parse", "HEAD"], text=True).strip()
        reports = []
        for index, durations in enumerate(([1000, 2000], [5000])):
            data = {"config": {"rootDir": "/old/checkout/web/e2e", "version": "1.62.1"},
                    "errors": [], "suites": [{"suites": [{"specs": [
                        {"file": "a.spec.ts", "title": "SECRET TEST", "tests": [
                            {"results": [{"duration": d, "stdout": ["SECRET TOKEN"]}
                                         for d in durations]}]}]}]}]}
            path = self.repo / f"report-{index}.json"
            path.write_text(json.dumps(data))
            reports.append(path)
        # Hashes come from the explicitly named source, not current mutable bytes.
        (self.web / "e2e/a.spec.ts").write_text("// changed after run\n")
        profile = timing.collect_reports(reports, self.repo, revision)
        self.assertEqual(profile["files"]["web/e2e/a.spec.ts"]["seconds"], 4)
        self.assertEqual(profile["files"]["web/e2e/a.spec.ts"]["samples"], 2)
        serialized = json.dumps(profile)
        for secret in ("SECRET", "old/checkout", "report-0", "title", "stdout"):
            self.assertNotIn(secret, serialized)
        self.load(profile)
        self.assertEqual(timing.plan_files(["web/e2e/a.spec.ts"], profile, self.web, 1)
                         ["estimated_files"], ["web/e2e/a.spec.ts"])


if __name__ == "__main__":
    unittest.main()
