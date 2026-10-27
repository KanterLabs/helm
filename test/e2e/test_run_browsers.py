"""Subprocess proofs for the isolated browser supervisor (synthetic data only)."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


SUPERVISOR = Path(__file__).with_name("run-browsers.py")
FAKE_WORKER = r'''#!/usr/bin/env python3
import json, os, pathlib, signal, subprocess, sys, time
mode, port, work, artifacts, binary, *args = sys.argv[1:]
work, artifacts = pathlib.Path(work), pathlib.Path(artifacts)
assert work.is_dir() and not list(work.iterdir())
artifacts.mkdir(parents=True, exist_ok=True)
(artifacts / "results.json").write_text(json.dumps({"stats": {
    "expected": 0 if os.environ.get("FAKE_EMPTY") else 1,
    "unexpected": 0, "skipped": 0, "flaky": 0}}))
state = {"mode": mode, "port": int(port), "work": str(work), "args": args,
         "pid": os.getpid(), "started": time.monotonic()}
orphan = os.environ.get("FAKE_ORPHAN") == "true"
child_code = "import time; time.sleep(120)"
if orphan:
    child_code = ("import pathlib, signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                  f"pathlib.Path({str(work / 'child.ready')!r}).touch(); time.sleep(120)")
child = subprocess.Popen([sys.executable, "-c", child_code])
state["child"] = child.pid
(artifacts / "started.json").write_text(json.dumps(state))
if orphan:
    while not (work / "child.ready").exists(): time.sleep(0.01)
    sys.exit(7)
def finish(signum, frame):
    child.terminate()
    child.wait(timeout=5)
    (artifacts / "cancelled").write_text(str(signum))
    sys.exit(128 + signum)
signal.signal(signal.SIGTERM, finish)
time.sleep(float(os.environ.get("FAKE_DURATION", "0.5")))
child.terminate()
child.wait(timeout=5)
(artifacts / "finished").write_text(str(time.monotonic()))
sys.exit(7 if port == os.environ.get("FAKE_FAIL_PORT") else 0)
'''


class SupervisorProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.worker = self.root / "worker.py"
        self.worker.write_text(FAKE_WORKER)
        self.worker.chmod(0o700)
        self.artifacts = self.root / "evidence"

    def command(self, workers=4, *args):
        return [sys.executable, str(SUPERVISOR), "--mode", "disabled",
                "--workers", str(workers), "--base-port", "21080",
                "--work-dir", str(self.root / "state"),
                "--artifact-dir", str(self.artifacts), "--binary", "/bin/true",
                "--worker-script", str(self.worker), "--", *args]

    def run_command(self, workers=4, args=(), env=None):
        command = self.command(workers, *args)
        output = self.root / "supervisor.log"
        with output.open("w") as log:
            process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT,
                                       env={**os.environ, **(env or {})})
            try:
                status = process.wait(timeout=15)
            finally:
                if process.poll() is None:
                    process.kill()
                process.wait()
                # A cleanup regression must fail this proof without leaking
                # the proof's own synthetic child processes onto the host.
                for path in self.artifacts.glob("*/started.json"):
                    try:
                        os.killpg(json.loads(path.read_text())["pid"], signal.SIGKILL)
                    except ProcessLookupError:
                        pass
        return subprocess.CompletedProcess(command, status, output.read_text(), "")

    def test_overlap_isolation_and_focused_arguments(self):
        result = self.run_command(args=("e2e/board.spec.ts", "--grep", "two words"))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        starts = [json.loads(p.read_text()) for p in self.artifacts.glob("*/started.json")]
        finishes = [float(p.read_text()) for p in self.artifacts.glob("*/finished")]
        self.assertEqual(len(starts), 4)
        self.assertLess(max(s["started"] for s in starts), min(finishes))
        self.assertEqual(len({s["work"] for s in starts}), 4)
        self.assertEqual({s["port"] for s in starts}, {21080, 21180, 21280, 21380})
        for index, state in enumerate(sorted(starts, key=lambda s: s["port"]), 1):
            self.assertEqual(state["args"][:3], ["e2e/board.spec.ts", "--grep", "two words"])
            self.assertIn(f"--shard={index}/4", state["args"])
            self.assertIn("--workers=1", state["args"])
        receipt = json.loads((self.artifacts / "workers.json").read_text())
        self.assertEqual([w["exit_status"] for w in receipt["workers"]], [0] * 4)

    def test_single_worker_preserves_focused_scope(self):
        result = self.run_command(1, ("e2e/onboarding.spec.ts",))
        self.assertEqual(result.returncode, 0, result.stderr)
        state = json.loads((self.artifacts / "worker-1/started.json").read_text())
        self.assertEqual(state["args"], ["e2e/onboarding.spec.ts", "--workers=1"])

    def test_failure_retains_successful_sibling_evidence_and_fails_parent(self):
        result = self.run_command(env={"FAKE_FAIL_PORT": "21180"})
        self.assertNotEqual(result.returncode, 0)
        receipt = json.loads((self.artifacts / "workers.json").read_text())
        self.assertEqual([w["exit_status"] for w in receipt["workers"]], [0, 7, 0, 0])
        self.assertEqual(len(list(self.artifacts.glob("*/finished"))), 4)

    def test_all_empty_shards_do_not_turn_a_bad_filter_into_success(self):
        result = self.run_command(env={"FAKE_EMPTY": "true"})
        self.assertNotEqual(result.returncode, 0)

    def test_cancel_stops_owned_descendants_only_and_retains_receipt(self):
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"])
        process = subprocess.Popen(self.command(), stdout=subprocess.DEVNULL,
                                   stderr=subprocess.PIPE, env={**os.environ, "FAKE_DURATION": "120"})
        try:
            deadline = time.monotonic() + 10
            while len(list(self.artifacts.glob("*/started.json"))) != 4:
                self.assertIsNone(process.poll(), "Supervisor exited before workers started")
                self.assertLess(time.monotonic(), deadline)
                time.sleep(0.02)
            starts = [json.loads(p.read_text()) for p in self.artifacts.glob("*/started.json")]
            process.send_signal(signal.SIGTERM)
            _, errors = process.communicate(timeout=20)
            self.assertNotEqual(process.returncode, 0, errors)
            self.assertIsNone(unrelated.poll())
            for state in starts:
                for pid in (state["pid"], state["child"]):
                    with self.assertRaises(ProcessLookupError):
                        os.kill(pid, 0)
            self.assertEqual(len(list(self.artifacts.glob("*/cancelled"))), 4)
            self.assertTrue((self.artifacts / "workers.json").is_file())
        finally:
            process.kill() if process.poll() is None else None
            process.communicate()
            unrelated.terminate()
            unrelated.wait(timeout=5)

    def test_rejects_unsafe_overrides_before_start(self):
        for args in (("--workers=4",), ("--shard=1/2",), ("-j", "4")):
            with self.subTest(args=args):
                result = self.run_command(args=args)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(list(self.artifacts.glob("*/started.json")))

    def test_failed_worker_cannot_leave_stubborn_descendant_holding_output(self):
        result = self.run_command(1, env={"FAKE_ORPHAN": "true"})
        self.assertNotEqual(result.returncode, 0)
        state = json.loads((self.artifacts / "worker-1/started.json").read_text())
        status = Path(f"/proc/{state['child']}/stat")
        if status.exists():
            self.assertEqual(status.read_text().split()[2], "Z", "Descendant is still running")
        receipt = json.loads((self.artifacts / "workers.json").read_text())
        self.assertEqual(receipt["workers"][0]["exit_status"], 7)


class TimedSupervisorProof(SupervisorProof):
    """Exercise opt-in planning through a synthetic Playwright discovery CLI."""

    def setUp(self):
        super().setUp()
        import hashlib
        web = SUPERVISOR.resolve().parents[2] / "web"
        self.files = sorted(web.glob("e2e/*.spec.ts"))[:3]
        self.profile = self.root / "timings.json"
        self.profile.write_text(json.dumps({
            "schema": 1, "source_revision": "a" * 40,
            "collector": "helm-playwright-file-timings-v1",
            "report_sha256": ["b" * 64], "playwright_versions": ["1.62.1"],
            "files": {"web/" + p.relative_to(web).as_posix(): {
                "seconds": seconds, "samples": 1,
                "sha256": hashlib.sha256(p.read_bytes()).hexdigest()}
                for p, seconds in zip(self.files, (9, 5, 4))}}))
        self.node = self.root / "node"
        self.node.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
pathlib.Path(os.environ["FAKE_DISCOVERY_ARGS"]).write_text(json.dumps(sys.argv[1:]))
print(os.environ["FAKE_DISCOVERY_JSON"])
sys.exit(int(os.environ.get("FAKE_DISCOVERY_EXIT", "0")))
''')
        self.node.chmod(0o700)

    def timed_env(self, **extra):
        web = SUPERVISOR.resolve().parents[2] / "web"
        return {"HELM_E2E_TIMING_PROFILE": str(self.profile),
                "PATH": str(self.root) + os.pathsep + os.environ["PATH"],
                "FAKE_DISCOVERY_ARGS": str(self.root / "discovery-args.json"),
                "FAKE_DISCOVERY_JSON": json.dumps({"config": {
                    "rootDir": str(web / "e2e"), "version": "1.62.1"}, "errors": [],
                    "suites": [{"specs": [{"file": p.name, "tests": [{}]}]}
                               for p in self.files]}), **extra}

    def test_timed_file_intersection_preserves_focused_options_and_omits_empty_fixtures(self):
        focused = ("e2e/a.*:4", "--grep", "two words", "--grep-invert=other", "--retries=1")
        result = self.run_command(4, focused, self.timed_env())
        self.assertEqual(result.returncode, 0, result.stdout)
        states = [json.loads(p.read_text()) for p in sorted(self.artifacts.glob("*/started.json"))]
        self.assertEqual(len(states), 3)
        assigned = []
        for state in states:
            self.assertEqual(state["args"][:len(focused)], list(focused))
            self.assertIn("--workers=1", state["args"])
            self.assertFalse(any(a.startswith("--shard") for a in state["args"]))
            flag = next(a for a in state["args"] if a.startswith("--test-list="))
            assigned.extend(Path(flag.split("=", 1)[1]).read_text().splitlines())
        self.assertEqual(sorted(assigned), sorted(p.name for p in self.files))
        discovery = json.loads((self.root / "discovery-args.json").read_text())
        self.assertEqual(discovery[2:2 + len(focused)], list(focused))
        self.assertEqual(discovery[-3:], ["--list", "--reporter=json", "--workers=1"])
        receipt = json.loads((self.artifacts / "shard-plan.json").read_text())
        self.assertEqual(len(receipt["workers"]), 4)

    def test_timed_discovery_failures_start_no_fixtures(self):
        for output, status in (("not json", "0"), ("{}", "1"),
                               (json.dumps({"errors": [], "suites": []}), "0")):
            with self.subTest(output=output):
                result = self.run_command(env=self.timed_env(
                    FAKE_DISCOVERY_JSON=output, FAKE_DISCOVERY_EXIT=status))
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(list(self.artifacts.glob("*/started.json")))

    def test_timed_rejects_selection_and_config_overrides(self):
        for args in (("--test-list=elsewhere",), ("-c", "other.ts"), ("--config=other",),
                     ("--last-failed",), ("--only-changed=HEAD",), ("--list",), ("--ui",),
                     ("--fully-parallel",), ("--output=shared",), ("--reporter=json",),
                     ("--last-failed-file=shared",), ("--run-agents=all",), ("-u",)):
            with self.subTest(args=args):
                result = self.run_command(args=args, env=self.timed_env())
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(list(self.artifacts.glob("*/started.json")))

    def test_timed_preserves_filter_values_that_resemble_options(self):
        focused = ("--grep", "--list", "--grep-invert", "--workers=4")
        result = self.run_command(1, focused, self.timed_env())
        self.assertEqual(result.returncode, 0, result.stdout)
        state = json.loads((self.artifacts / "worker-1/started.json").read_text())
        self.assertEqual(state["args"][:len(focused)], list(focused))

    def test_explicit_timing_option_and_worker_failure_remain_failure(self):
        env = self.timed_env(FAKE_FAIL_PORT="21180")
        env["HELM_E2E_TIMING_PROFILE"] = ""
        command = self.command(2)
        separator = command.index("--")
        command[separator:separator] = ["--timings", str(self.profile)]
        result = subprocess.run(command, env={**os.environ, **env}, capture_output=True,
                                text=True, timeout=15)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        receipt = json.loads((self.artifacts / "workers.json").read_text())
        self.assertEqual([w["exit_status"] for w in receipt["workers"]], [0, 7])


if __name__ == "__main__":
    unittest.main()
