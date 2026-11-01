"""Synthetic shell-harness proofs; no browser or real Helm server is launched."""
import json
import os
from pathlib import Path
import signal
import shutil
import socket
import subprocess
import tempfile
import time
import unittest

HARNESS = Path(__file__).resolve().parents[1] / "githubbeta/run.sh"
SERVER = '''#!/usr/bin/env python3
import json, os, pathlib, signal, sys, time
pathlib.Path(os.environ["PROOF_SERVER"]).write_text(json.dumps({
    "args": sys.argv[1:], "gomaxprocs": os.environ.get("GOMAXPROCS")}))
signal.signal(signal.SIGTERM, lambda *args: sys.exit(0))
while True: time.sleep(.02)
'''


class GithubRuntimeProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        if os.environ.get("HELM_E2E_PROOF_ARTIFACT_DIR"):
            destination = Path(os.environ["HELM_E2E_PROOF_ARTIFACT_DIR"]) / self.id()
            self.addCleanup(shutil.copytree, self.root, destination)
        self.artifacts = self.root / "evidence"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for name, content in {"server": SERVER, "curl": "#!/bin/sh\nexit 0\n",
                              "npx": '#!/bin/sh\nprintf "%s" "$GOMAXPROCS" > "$PROOF_BROWSER"\n'
                              'sleep "${PROOF_SLEEP:-0}"\n'}.items():
            path = self.bin / name
            path.write_text(content)
            path.chmod(0o700)
        self.listener = socket.socket()
        self.listener.bind(("127.0.0.1", 0))
        self.port = self.listener.getsockname()[1]
        self.addCleanup(self.listener.close)
        self.env = {**os.environ, "PATH": str(self.bin) + ":" + os.environ["PATH"],
                    "HELM_GITHUB_BETA_BINARY": str(self.bin / "server"),
                    "HELM_GITHUB_BETA_PORT": str(self.port),
                    "HELM_GITHUB_BETA_WORK_DIR": str(self.root / "state"),
                    "HELM_E2E_ARTIFACT_DIR": str(self.artifacts), "GOMAXPROCS": "6",
                    "PROOF_SERVER": str(self.root / "server.json"),
                    "PROOF_BROWSER": str(self.root / "browser.txt")}

    def start(self):
        return subprocess.Popen([str(HARNESS)], env=self.env,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                start_new_session=True)

    def test_occupied_port_never_authorizes_unrelated_ready_service(self):
        self.listener.listen()
        process = self.start()
        self.assertNotEqual(process.wait(timeout=5), 0)
        self.assertFalse((self.root / "server.json").exists())

    def test_server_cap_is_scoped_and_state_private(self):
        self.listener.close()
        process = self.start()
        self.assertEqual(process.wait(timeout=5), 0)
        server = json.loads((self.root / "server.json").read_text())
        self.assertEqual(server["gomaxprocs"], "2")
        self.assertEqual((self.root / "browser.txt").read_text(), "6")
        self.assertIn(str(self.root / "state"), server["args"])
        metadata = (self.artifacts / "worker.metadata").read_text()
        self.assertIn("exit_status=0", metadata)
        self.assertFalse((self.root / "state").exists())

    def test_cancel_retains_runtime_metadata(self):
        self.listener.close()
        self.env["PROOF_SLEEP"] = "120"
        process = self.start()
        try:
            deadline = time.monotonic() + 5
            while not (self.root / "browser.txt").exists():
                self.assertIsNone(process.poll())
                self.assertLess(time.monotonic(), deadline)
                time.sleep(.01)
            process.send_signal(signal.SIGTERM)
            self.assertEqual(process.wait(timeout=5), 143)
            self.assertIn("exit_status=143", (self.artifacts / "worker.metadata").read_text())
        finally:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()


if __name__ == "__main__":
    unittest.main()
