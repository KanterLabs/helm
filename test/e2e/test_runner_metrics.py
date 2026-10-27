"""Subprocess proofs for metrics pass-through, failure and owned cancellation."""
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest

RUNNER = Path(__file__).with_name("runner-metrics.py")


class MetricsProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / "metrics"
        if os.environ.get("HELM_E2E_PROOF_ARTIFACT_DIR"):
            destination = Path(os.environ["HELM_E2E_PROOF_ARTIFACT_DIR"]) / self.id()
            self.addCleanup(shutil.copytree, self.root, destination)

    def command(self, code):
        return [sys.executable, str(RUNNER), "--output", str(self.output),
                "--", sys.executable, "-c", code]

    def summary(self):
        return json.loads((self.output / "metrics.json").read_text())

    def test_streams_both_channels_and_returns_failure_with_samples(self):
        code = ('import sys,time; print("live stdout",flush=True); '
                'print("live stderr",file=sys.stderr,flush=True); time.sleep(1.4); sys.exit(7)')
        out, err = self.root / "stdout", self.root / "stderr"
        with out.open("w") as stdout, err.open("w") as stderr:
            process = subprocess.Popen(self.command(code), stdout=stdout, stderr=stderr)
            try:
                deadline = time.monotonic() + 1
                while not out.read_text() or not err.read_text():
                    self.assertIsNone(process.poll())
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.01)
                self.assertIsNone(process.poll())
                self.assertEqual(process.wait(timeout=5), 7)
            finally:
                if process.poll() is None:
                    process.terminate()
                process.wait(timeout=5)
        self.assertEqual(out.read_text(), "live stdout\n")
        self.assertEqual(err.read_text(), "live stderr\n")
        receipt = self.summary()
        self.assertEqual(receipt["exit_status"], 7)
        self.assertEqual(receipt["signal"], 0)
        self.assertIn("before", receipt)
        self.assertIn("after", receipt)
        self.assertIn("differences", receipt)
        self.assertGreaterEqual(len((self.output / "samples.jsonl").read_text().splitlines()), 1)
        self.assertNotIn(code, json.dumps(receipt))
        self.assertNotIn("live stdout", json.dumps(receipt))
        self.assertIn("available_kib", receipt["before"]["memory"])

    def test_missing_collectors_do_not_mask_success_or_record_arguments(self):
        # Substitute source roots, then execute the same wrapper entry point.
        bootstrap = ('import importlib.util,pathlib,sys; '
                     f's=importlib.util.spec_from_file_location("metrics",{str(RUNNER)!r}); '
                     'm=importlib.util.module_from_spec(s); s.loader.exec_module(m); '
                     'm.PROC=pathlib.Path("/unavailable-proof"); '
                     'm.CGROUP=pathlib.Path("/unavailable-proof"); '
                     'sys.exit(m.main())')
        command = [sys.executable, "-c", bootstrap, "--output", str(self.output),
                   "--", sys.executable, "-c", 'pass', "private-argument-sentinel"]
        result = subprocess.run(command, capture_output=True, text=True, timeout=5,
                                env={**os.environ, "PYTHONPATH": str(RUNNER.parent),
                                     "PRIVATE_SECRET": "private-env-sentinel"})
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = self.summary()
        self.assertEqual(receipt["before"]["cpu"], {"unavailable": "FileNotFoundError"})
        serialized = json.dumps(receipt)
        self.assertNotIn("private-argument-sentinel", serialized)
        self.assertNotIn("private-env-sentinel", serialized)

    def test_unwritable_metrics_destination_preserves_workload_result(self):
        self.output.write_text("not a directory")
        result = subprocess.run(self.command("import sys; sys.exit(7)"),
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 7)
        self.assertNotIn("Traceback", result.stderr)

    def test_child_signal_status_is_preserved_after_final_receipt(self):
        result = subprocess.run(self.command("import os,signal; os.kill(os.getpid(),signal.SIGTERM)"),
                                capture_output=True, timeout=5)
        self.assertEqual(result.returncode, -signal.SIGTERM)
        self.assertEqual(self.summary()["exit_status"], -signal.SIGTERM)
        self.assertEqual(self.summary()["signal"], 0)

    def test_spawn_failure_still_retains_final_json(self):
        result = subprocess.run([sys.executable, str(RUNNER), "--output", str(self.output),
                                 "--", "/unavailable-proof-executable"],
                                capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 127)
        self.assertEqual(self.summary()["exit_status"], 127)
        self.assertEqual(self.summary()["workload_error"], "FileNotFoundError")

    def test_cancel_cleans_owned_detached_child_and_preserves_unrelated(self):
        ready = self.root / "owned.json"
        code = ('import json,os,pathlib,subprocess,sys,time; '
                'p=subprocess.Popen([sys.executable,"-c","import time; time.sleep(120)"],'
                'start_new_session=True); '
                f'pathlib.Path({str(ready)!r}).write_text(json.dumps([os.getpid(),p.pid])); '
                'time.sleep(120)')
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"])
        process = subprocess.Popen(self.command(code), stdout=subprocess.DEVNULL,
                                   stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 5
            while not ready.exists():
                self.assertIsNone(process.poll())
                self.assertLess(time.monotonic(), deadline)
                time.sleep(.01)
            owned = json.loads(ready.read_text())
            process.send_signal(signal.SIGTERM)
            self.assertEqual(process.wait(timeout=8), 143)
            self.assertIsNone(unrelated.poll())
            self.assertEqual(self.summary()["signal"], signal.SIGTERM)
            for pid in owned:
                self.assertFalse(Path(f"/proc/{pid}/stat").exists())
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            unrelated.terminate()
            unrelated.wait(timeout=5)
            if ready.exists():
                for pid in json.loads(ready.read_text()):
                    try:
                        os.killpg(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass


if __name__ == "__main__":
    unittest.main()
