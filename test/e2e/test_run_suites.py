"""Executable subprocess proofs for bounded suites; synthetic data only."""
import json
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

RUNNER = Path(__file__).with_name("run-suites.py")
FAKE = r'''#!/usr/bin/env python3
import argparse, json, os, pathlib, signal, subprocess, sys, time
p = argparse.ArgumentParser()
for key in ("mode", "workers", "base-port", "work-dir", "artifact-dir", "binary"):
    p.add_argument("--" + key)
p.add_argument("args", nargs=argparse.REMAINDER)
a = p.parse_args()
artifacts = pathlib.Path(a.artifact_dir or os.environ["HELM_E2E_ARTIFACT_DIR"])
artifacts.mkdir(parents=True, exist_ok=True)
name = artifacts.name
work = pathlib.Path(a.work_dir or os.environ["HELM_GITHUB_BETA_WORK_DIR"])
assert work.is_dir() and not list(work.iterdir())
(work / "private").touch()
state = {"name": name, "pid": os.getpid(), "start": time.monotonic(),
         "work": str(work), "port": a.base_port or os.environ.get("HELM_GITHUB_BETA_PORT"),
         "workers": a.workers or "1", "args": a.args,
         "stall": os.environ.get("HELM_E2E_CODEX_STALL_ACCOUNT"), "binary": a.binary}
if os.environ.get("FAKE_NESTED"):
    code = ("import os,pathlib,signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); "
            f"pathlib.Path({str(artifacts / 'nested.ready')!r}).write_text(str(os.getpid())); "
            "time.sleep(120)")
    child = subprocess.Popen([sys.executable, "-c", code], start_new_session=True)
    state["child"] = child.pid
    while not (artifacts / "nested.ready").exists(): time.sleep(.01)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
(artifacts / "started.json").write_text(json.dumps(state))
print("synthetic " + name, flush=True)
if os.environ.get("FAKE_CRASH") == name:
    sys.exit(7)
duration = os.environ.get("FAKE_MAIN_DURATION") if name == "main" else None
time.sleep(float(duration or os.environ.get("FAKE_DURATION", ".18")))
(artifacts / "finished").write_text(str(time.monotonic()))
sys.exit(9 if os.environ.get("FAKE_FAIL") == name else 0)
'''


class SuiteProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        if os.environ.get("HELM_E2E_PROOF_ARTIFACT_DIR"):
            destination = Path(os.environ["HELM_E2E_PROOF_ARTIFACT_DIR"]) / self.id()
            self.addCleanup(shutil.copytree, self.root, destination)
        self.fake = self.root / "fake.py"
        self.fake.write_text(FAKE)
        self.fake.chmod(0o700)
        self.artifacts = self.root / "evidence"

    def command(self, workers=3, extra=(), selection=()):
        return [sys.executable, str(RUNNER), "--workers", str(workers),
                "--base-port", "21080", "--work-dir", str(self.root / "state"),
                "--artifact-dir", str(self.artifacts), "--binary", "/bin/true",
                "--github-binary", "/bin/true", "--browser-script", str(self.fake),
                "--github-script", str(self.fake), "--cleanup-grace", ".3",
                *extra, "--", *selection]

    def start(self, **kwargs):
        log = (self.root / "proof.log").open("w")
        self.addCleanup(log.close)
        process = subprocess.Popen(self.command(**kwargs), stdout=log, stderr=log,
                                   env={**os.environ, **getattr(self, "env", {})})
        self.addCleanup(self.cleanup_processes, process)
        return process

    def cleanup_processes(self, process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
        process.wait()
        for path in self.artifacts.glob("*/started.json"):
            state = json.loads(path.read_text())
            for pid in (state["pid"], state.get("child")):
                if pid:
                    try:
                        os.killpg(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass

    def finish(self, process):
        status = process.wait(timeout=15)
        summary = json.loads((self.artifacts / "suites.json").read_text())
        return status, {p["name"]: p for p in summary["phases"]}

    def starts(self):
        return {p.parent.name: json.loads(p.read_text())
                for p in self.artifacts.glob("*/started.json")}

    @unittest.skipUnless(sys.platform.startswith("linux"), "Linux ownership proof")
    def test_killed_ancillary_wrapper_stops_scheduling_before_cleanup(self):
        self.env = {"FAKE_NESTED": "true", "FAKE_MAIN_DURATION": ".02", "FAKE_DURATION": "120"}
        process = self.start(workers=1)
        deadline = time.monotonic() + 6
        path = self.artifacts / "onboarding/started.json"
        while not path.exists():
            self.assertIsNone(process.poll())
            self.assertLess(time.monotonic(), deadline)
            time.sleep(.02)
        state = json.loads(path.read_text())
        stat = Path(f"/proc/{state['pid']}/stat").read_text().rsplit(")", 1)[1].split()
        os.kill(int(stat[1]), signal.SIGKILL)
        deadline = time.monotonic() + 6
        while process.poll() is None:
            self.assertFalse((self.artifacts / "github/started.json").exists(),
                             "A killed wrapper must not release a live fixture's slot")
            self.assertLess(time.monotonic(), deadline)
            time.sleep(.02)
        status, phases = self.finish(process)
        self.assertNotEqual(status, 0)
        self.assertEqual(phases["onboarding"]["exit_status"], -signal.SIGKILL)
        self.assertEqual(phases["github"]["status"], "skipped")
        self.assertFalse(Path(f"/proc/{state['pid']}").exists())
        self.assertFalse(Path(f"/proc/{state['child']}").exists())

    def test_phase_output_is_forwarded_before_long_child_finishes(self):
        self.env = {"FAKE_MAIN_DURATION": "2"}
        process = self.start()
        deadline = time.monotonic() + 1
        while "[suite main] synthetic main" not in (self.root / "proof.log").read_text():
            self.assertIsNone(process.poll())
            self.assertLess(time.monotonic(), deadline, "Child output was buffered until phase exit")
            time.sleep(.02)
        self.assertFalse((self.artifacts / "main/finished").exists())
        self.assertEqual((self.artifacts / "main.console.log").read_text(), "synthetic main\n")
        self.assertEqual(self.finish(process)[0], 0)

    def test_full_ci_orders_main_then_bounds_overlap_and_isolates(self):
        self.env = {"HELM_E2E_CODEX_STALL_ACCOUNT": "true"}
        status, phases = self.finish(self.start(extra=("--include-deadline",)))
        self.assertEqual(status, 0, (self.root / "proof.log").read_text())
        starts = self.starts()
        self.assertEqual(set(starts), {"main", "onboarding", "github", "deadline"})
        self.assertEqual(len({s["work"] for s in starts.values()}), 4)
        self.assertEqual(len({s["port"] for s in starts.values()}), 4)
        main_end = float((self.artifacts / "main/finished").read_text())
        self.assertTrue(all(s["start"] >= main_end for n, s in starts.items() if n != "main"))
        side_end = min(float((self.artifacts / n / "finished").read_text())
                       for n in ("onboarding", "github", "deadline"))
        self.assertLess(max(s["start"] for n, s in starts.items() if n != "main"), side_end)
        self.assertEqual(starts["main"]["workers"], "3")
        self.assertTrue(all(s["stall"] is None for n, s in starts.items() if n != "deadline"))
        self.assertEqual(starts["deadline"]["stall"], "true")
        for phase in phases.values():
            self.assertEqual(phase["status"], "passed")
            self.assertEqual(phase["exit_status"], 0)
            self.assertGreater(phase["elapsed_seconds"], 0)

    def test_one_worker_serializes_ancillary_and_local_omits_deadline(self):
        status, phases = self.finish(self.start(workers=1))
        self.assertEqual(status, 0)
        self.assertEqual(phases["deadline"]["status"], "skipped")
        self.assertFalse(phases["deadline"]["required"])
        starts = self.starts()
        self.assertGreaterEqual(starts["github"]["start"],
                                float((self.artifacts / "onboarding/finished").read_text()))

    def test_focus_keeps_arguments_and_onboarding_compatibility(self):
        selected = ("e2e/board.spec.ts", "--grep", "--workers=4", "--grep-invert", "--shard=2/4")
        status, phases = self.finish(self.start(selection=selected))
        self.assertEqual(status, 0)
        self.assertEqual(self.starts()["main"]["args"], ["--", *selected])
        self.assertEqual(set(self.starts()), {"main", "onboarding"})
        self.assertEqual(phases["github"]["status"], "skipped")

    def test_failure_keeps_siblings_and_fails_closed(self):
        self.env = {"FAKE_FAIL": "onboarding"}
        status, phases = self.finish(self.start(extra=("--include-deadline",)))
        self.assertNotEqual(status, 0)
        self.assertEqual(phases["onboarding"]["exit_status"], 9)
        self.assertEqual(phases["onboarding"]["status"], "failed")
        self.assertEqual(phases["github"]["status"], "passed")
        self.assertEqual(phases["deadline"]["status"], "passed")

    def test_spawn_error_retains_all_phase_results(self):
        process = self.start(extra=("--github-script", str(self.root / "missing"),))
        status, phases = self.finish(process)
        self.assertNotEqual(status, 0)
        self.assertEqual(phases["github"]["status"], "failed")
        self.assertIn("error", phases["github"])
        self.assertEqual(phases["onboarding"]["status"], "passed")

    def assert_dead(self, pid):
        path = Path(f"/proc/{pid}/stat")
        self.assertFalse(path.exists(), f"Owned process {pid} was not reaped")

    def test_cancel_reaps_nested_groups_and_preserves_unrelated(self):
        self.env = {"FAKE_NESTED": "true", "FAKE_DURATION": "120"}
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"])
        try:
            process = self.start(extra=("--include-deadline",))
            deadline = time.monotonic() + 8
            while "main" not in self.starts():
                self.assertIsNone(process.poll())
                self.assertLess(time.monotonic(), deadline)
                time.sleep(.02)
            owned = self.starts()["main"]
            process.send_signal(signal.SIGTERM)
            status, phases = self.finish(process)
            self.assertEqual(status, 143)
            self.assertIsNone(unrelated.poll())
            self.assert_dead(owned["pid"])
            self.assert_dead(owned["child"])
            self.assertEqual(phases["main"]["status"], "canceled")
            self.assertTrue(all(p["status"] == "canceled" for p in phases.values()))
        finally:
            unrelated.terminate()
            unrelated.wait(timeout=5)

    def test_crashed_supervisor_cannot_leave_nested_group(self):
        self.env = {"FAKE_NESTED": "true", "FAKE_CRASH": "main", "FAKE_DURATION": ".02"}
        status, phases = self.finish(self.start(selection=("e2e/board.spec.ts",)))
        self.assertNotEqual(status, 0)
        self.assertEqual(phases["main"]["exit_status"], 7)
        for state in self.starts().values():
            self.assert_dead(state["child"])

    def test_crashed_ownership_wrapper_is_recovered_by_outer_subreaper(self):
        self.env = {"FAKE_NESTED": "true", "FAKE_MAIN_DURATION": "120", "FAKE_DURATION": ".02"}
        process = self.start(selection=("e2e/board.spec.ts",))
        deadline = time.monotonic() + 8
        while "main" not in self.starts():
            self.assertIsNone(process.poll())
            self.assertLess(time.monotonic(), deadline)
            time.sleep(.02)
        main = self.starts()["main"]
        fields = Path(f"/proc/{main['pid']}/stat").read_text().rsplit(")", 1)[1].split()
        wrapper = int(fields[1])
        os.kill(wrapper, signal.SIGKILL)
        status, phases = self.finish(process)
        self.assertNotEqual(status, 0)
        self.assertEqual(phases["main"]["exit_status"], -signal.SIGKILL)
        self.assert_dead(main["pid"])
        self.assert_dead(main["child"])
        self.assertEqual(phases["onboarding"]["status"], "passed")

    def test_focused_deadline_preserves_opt_in_without_poisoning_onboarding(self):
        self.env = {"HELM_E2E_CODEX_STALL_ACCOUNT": "true"}
        status, _ = self.finish(self.start(selection=(
            "e2e/luna-account-deadline-stability.spec.ts",)))
        self.assertEqual(status, 0)
        starts = self.starts()
        self.assertEqual(starts["main"]["stall"], "true")
        self.assertIsNone(starts["onboarding"]["stall"])
        self.assertEqual(set(starts), {"main", "onboarding"})

    def test_stubborn_ancillary_descendants_hold_their_budget_slot(self):
        self.env = {"FAKE_NESTED": "true", "FAKE_DURATION": ".02"}
        status, phases = self.finish(self.start(workers=1, extra=("--include-deadline",)))
        self.assertEqual(status, 0)
        starts = self.starts()
        # Each wrapper must finish its .3s escalation before the next starts;
        # otherwise a completed supervisor incorrectly frees a still-live slot.
        for previous, following in (("main", "onboarding"), ("onboarding", "github"),
                                    ("github", "deadline")):
            finished = float((self.artifacts / previous / "finished").read_text())
            self.assertGreaterEqual(starts[following]["start"] - finished, .25)
        for state in starts.values():
            self.assert_dead(state["child"])

    def test_port_validation_happens_before_any_child(self):
        process = self.start(extra=("--base-port", "65500",))
        self.assertNotEqual(process.wait(timeout=5), 0)
        self.assertEqual(self.starts(), {})


if __name__ == "__main__":
    unittest.main()
