"""Subprocess proofs for private Docker fallback cleanup; no Docker daemon."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import unittest


WRAPPER = Path(__file__).with_name("go-command.sh")
FAKE_DOCKER = r'''#!/usr/bin/env python3
import json, os, pathlib, signal, sys, time
args = sys.argv[1:]
state = pathlib.Path(os.environ["FAKE_DOCKER_STATE"])
with (state / "calls.jsonl").open("a") as stream:
    stream.write(json.dumps(args) + "\n")
cid = "a" * 64
if args[0] == "run":
    if "--cidfile" in args:
        pathlib.Path(args[args.index("--cidfile") + 1]).write_text(cid + "\n")
    active = state / cid
    active.touch()
    (state / "started").touch()
    if os.environ.get("FAKE_DOCKER_WAIT"):
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        while active.exists(): time.sleep(0.01)
    active.unlink(missing_ok=True)
    print("synthetic Go response")
    sys.exit(int(os.environ.get("FAKE_DOCKER_EXIT", "0")))
elif args[0] == "rm":
    (state / args[-1]).unlink(missing_ok=True)
else:
    sys.exit(7)
'''


class DockerCommandProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.work = self.base / "work with spaces"
        self.work.mkdir()
        self.state = self.base / "daemon"
        self.state.mkdir()
        docker = self.base / "docker"
        docker.write_text(FAKE_DOCKER)
        docker.chmod(0o755)
        self.env = {**os.environ, "PATH": str(self.base) + ":" + os.environ["PATH"],
                    "FAKE_DOCKER_STATE": str(self.state),
                    "HELM_E2E_DOCKER_WORK_DIR": str(self.work),
                    "HELM_E2E_DOCKER_BUNDLE_PARENT": str(self.work)}

    def calls(self):
        return [json.loads(line) for line in (self.state / "calls.jsonl").read_text().splitlines()]

    def test_literal_argv_unique_mounts_and_private_cid_cleanup(self):
        injected = self.base / "injected"
        args = ["build", "-ldflags=two words;$(touch " + str(injected) + ")", "./cmd/helm"]
        result = subprocess.run(["bash", str(WRAPPER), *args], env=self.env,
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "synthetic Go response\n")
        calls = self.calls()
        run = calls[0]
        self.assertEqual(run[-len(args):], args)
        mounts = [run[index + 1] for index, value in enumerate(run) if value == "--volume"]
        self.assertEqual(len(mounts), len(set(mounts)))
        self.assertFalse(injected.exists())
        self.assertIn("--cidfile", run)
        self.assertTrue(any(call[0] == "rm" and call[-1] == "a" * 64 for call in calls))
        self.assertFalse(list(self.work.glob(".docker-go.*")))

    def test_failed_docker_status_is_preserved(self):
        result = subprocess.run(["bash", str(WRAPPER), "version"],
                                env={**self.env, "FAKE_DOCKER_EXIT": "7"},
                                capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 7)
        self.assertFalse(list(self.work.glob(".docker-go.*")))

    def test_cancellation_removes_only_own_daemon_container(self):
        unrelated = self.state / ("b" * 64)
        unrelated.touch()
        for signum in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signal=signum):
                (self.state / "started").unlink(missing_ok=True)
                process = subprocess.Popen(["bash", str(WRAPPER), "build", "./cmd/helm"],
                    env={**self.env, "FAKE_DOCKER_WAIT": "1"},
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
                try:
                    deadline = time.monotonic() + 3
                    while not (self.state / "started").exists():
                        self.assertIsNone(process.poll())
                        self.assertLess(time.monotonic(), deadline)
                        time.sleep(0.01)
                    process.send_signal(signum)
                    self.assertEqual(process.wait(timeout=3), 128 + signum)
                    self.assertFalse((self.state / ("a" * 64)).exists())
                    self.assertTrue(unrelated.exists())
                    self.assertFalse(list(self.work.glob(".docker-go.*")))
                finally:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait()


if __name__ == "__main__":
    unittest.main()
