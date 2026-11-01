"""Failure-first subprocess proofs for immutable E2E build bundles."""
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import unittest


PREPARE = Path(__file__).with_name("prepare.py")
FAKE_GO = r'''#!/usr/bin/env python3
import json, os, pathlib, signal, subprocess, sys, time
args = sys.argv[1:]
settings = {"GOVERSION": "go1.26.10", "GOOS": "linux", "GOARCH": "amd64",
    "CGO_ENABLED": "1", "GOEXPERIMENT": "", "GOFLAGS": os.environ.get("GOFLAGS", ""),
    "GOTOOLCHAIN": "auto", "GOAMD64": "v1", "GOARM64": "v8.0"}
for key in ("CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS",
            "GOWORK", "GOMOD", "GOENV", "GOTOOLDIR", "GOFIPS140"):
    settings[key] = os.environ.get(key, "")
settings["GOVERSION"] = os.environ.get("FAKE_VERSION", settings["GOVERSION"])
if args[0] == "env":
    print(json.dumps({key: settings[key] for key in args[2:]}))
elif args[:2] == ["version", "-m"]:
    print(pathlib.Path(args[-1]).read_text().split("\n", 1)[1], end="")
elif args[0] == "build":
    if os.environ.get("FAKE_BUILD_FAIL"): sys.exit(7)
    process_state = os.environ.get("FAKE_PROCESS_STATE")
    if process_state:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        ready = pathlib.Path(process_state + ".ready")
        child = subprocess.Popen([sys.executable, "-c",
            "import pathlib, signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            + f"pathlib.Path({str(ready)!r}).touch(); time.sleep(120)"])
        while not ready.exists(): time.sleep(0.01)
        pathlib.Path(process_state).write_text(json.dumps({"pid": os.getpid(), "child": child.pid}))
    marker = os.environ.get("FAKE_MARKER")
    if marker: pathlib.Path(marker).touch()
    time.sleep(float(os.environ.get("FAKE_DELAY", "0")))
    path = pathlib.Path(args[args.index("-o") + 1])
    race = "-race" in args and not os.environ.get("FAKE_NON_RACE")
    lines = [str(path) + ": " + settings["GOVERSION"], "\tpath\t" + args[-1]]
    for key in ("GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOARM64"):
        lines.append("\tbuild\t" + key + "=" + settings[key])
    lines += ["\tbuild\t-trimpath=true", "\tbuild\t-race=" + str(race).lower()]
    path.write_text("#!/bin/false\n" + "\n".join(lines) + "\n")
    path.chmod(0o755)
    log = os.environ.get("FAKE_LOG")
    if log:
        with open(log, "a") as out:
            out.write(json.dumps({"args": args, "gomaxprocs": os.environ.get("GOMAXPROCS")}) + "\n")
else:
    sys.exit(8)
'''


class PreparedBuildProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.root = self.base / "repo"
        self.root.mkdir()
        self.bundle = self.base / "bundle"
        self.go = self.base / "go"
        self.go.write_text(FAKE_GO)
        self.go.chmod(0o755)
        self.log = self.base / "builds.jsonl"
        self.env = {**os.environ, "PATH": str(self.base) + ":" + os.environ["PATH"],
                    "FAKE_LOG": str(self.log), "GITHUB_RUN_ID": "123",
                    "GITHUB_RUN_ATTEMPT": "1", "GOFLAGS": "-p=2",
                    "HELM_E2E_GO_COMMAND": "go"}
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        (self.root / "main.go").write_text("package main\n")
        (self.root / ".gitignore").write_text("internal/webassets/dist/\n")
        self.git("add", ".")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "commit", "-qm", "fixture")
        self.assets = self.root / "internal/webassets/dist"
        self.assets.mkdir(parents=True)
        (self.assets / "index.html").write_text("synthetic frontend")

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root), *args], check=True,
                              capture_output=True, text=True)

    def command(self, action="prepare", *args):
        return [sys.executable, str(PREPARE), action, "--root", str(self.root),
                "--output", str(self.bundle), "--go-flags=-race", *args]

    def run_cli(self, action="prepare", *args, env=None):
        return subprocess.run(self.command(action, *args), env={**self.env, **(env or {})},
                              capture_output=True, text=True, timeout=10)

    def prepare(self, *args):
        result = self.run_cli("prepare", *args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def reject(self, action="verify", *args, env=None):
        result = self.run_cli(action, *args, env=env)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_build_once_verify_exact_outputs_and_build_budget(self):
        paths = self.prepare("--github")
        self.assertEqual(paths["helm"], str(self.bundle / "helm"))
        self.assertEqual(paths["github"], str(self.bundle / "helm-githubbeta"))
        result = self.run_cli("verify", "--github")
        self.assertEqual(result.returncode, 0, result.stderr)
        builds = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(len(builds), 2)
        for build in builds:
            self.assertEqual(build["gomaxprocs"], "6")
            self.assertIn("-p=6", build["args"])
            self.assertIn("-race", build["args"])
        self.reject("prepare", "--github")
        self.assertEqual(len(self.log.read_text().splitlines()), 2)

    def test_source_changes_and_revision_environment_do_not_authorize_reuse(self):
        self.prepare()
        (self.root / "main.go").write_text("package changed\n")
        self.reject(env={"HELM_E2E_REVISION": self.git("rev-parse", "HEAD").stdout.strip()})

    def test_go_command_prefix_preserves_literal_argv_and_is_provenance(self):
        wrapper = self.base / "go wrapper.py"
        prefix_log = self.base / "prefix.jsonl"
        injection = self.base / "injected"
        literal = f"two words;$(touch {injection})"
        wrapper.write_text(
            "import json, os, sys\n"
            f"with open({str(prefix_log)!r}, 'a') as stream:\n"
            "    stream.write(json.dumps(sys.argv[1:]) + '\\n')\n"
            f"os.execv({str(self.go)!r}, [{str(self.go)!r}, *sys.argv[2:]])\n")
        prefix = [sys.executable, str(wrapper), literal]
        env = {"HELM_E2E_GO_COMMAND": shlex.join(prefix)}
        result = self.run_cli(env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        result = self.run_cli("verify", env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        invocations = [json.loads(line) for line in prefix_log.read_text().splitlines()]
        self.assertTrue(all(args[0] == literal for args in invocations))
        self.assertEqual({args[1] for args in invocations}, {"env", "build", "version"})
        self.assertFalse(injection.exists())
        metadata = json.loads((self.bundle / "metadata.json").read_text())
        self.assertEqual(metadata["configuration"]["go_command"], prefix)
        self.reject(env={"HELM_E2E_GO_COMMAND": shlex.join(prefix[:-1] + ["changed"])})

    def test_empty_go_command_prefix_is_rejected(self):
        self.reject("prepare", env={"HELM_E2E_GO_COMMAND": "  "})
        self.assertFalse(self.bundle.exists())

    def test_cancel_removes_partial_bundle_and_owned_commands_only(self):
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"])
        try:
            for signum in (signal.SIGTERM, signal.SIGINT):
                with self.subTest(signal=signum):
                    state_path = self.base / f"owned-{signum}.json"
                    process = subprocess.Popen(self.command(), env={**self.env, "FAKE_DELAY": "120",
                        "FAKE_PROCESS_STATE": str(state_path)},
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                    state = None
                    try:
                        deadline = time.monotonic() + 5
                        while not state_path.exists():
                            self.assertIsNone(process.poll())
                            self.assertLess(time.monotonic(), deadline)
                            time.sleep(0.01)
                        state = json.loads(state_path.read_text())
                        process.send_signal(signum)
                        self.assertEqual(process.wait(timeout=8), 128 + signum)
                        self.assertFalse(self.bundle.exists())
                        self.assertFalse(list(self.base.glob(".bundle.prepare-*")))
                        self.assertIsNone(unrelated.poll())
                        for pid in state.values():
                            status = Path(f"/proc/{pid}/stat")
                            if status.exists():
                                self.assertEqual(status.read_text().split()[2], "Z",
                                                 f"owned process {pid} is still alive")
                    finally:
                        if process.poll() is None:
                            process.kill()
                        process.wait()
                        # A failed proof must clean up its synthetic processes too.
                        if state:
                            for pid in state.values():
                                try:
                                    os.kill(pid, signal.SIGKILL)
                                except ProcessLookupError:
                                    pass
        finally:
            unrelated.terminate()
            unrelated.wait(timeout=5)

    def test_dirty_source_can_be_prepared_but_must_remain_identical(self):
        (self.root / "untracked.go").write_text("package dirty\n")
        self.prepare()
        self.assertEqual(self.run_cli("verify").returncode, 0)
        (self.root / "untracked.go").write_text("package changed\n")
        self.reject()

    def test_head_and_file_mode_changes_are_stale(self):
        self.prepare()
        (self.root / "main.go").chmod(0o755)
        self.reject()
        (self.root / "main.go").chmod(0o644)
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "commit", "--allow-empty", "-qm", "new revision")
        self.reject()

    def test_stale_frontend_and_missing_frontend_fail(self):
        self.prepare()
        (self.assets / "index.html").write_text("changed frontend")
        self.reject()
        (self.assets / "index.html").unlink()
        self.reject()

    def test_binary_tampering_missing_metadata_and_nonexecutable_fail(self):
        self.prepare()
        binary = self.bundle / "helm"
        binary.chmod(0o644)
        self.reject()
        binary.chmod(0o755)
        binary.write_text(binary.read_text() + "tampering")
        self.reject()
        (self.bundle / "metadata.json").unlink()
        self.reject()

    def test_non_race_substitution_and_partial_build_fail_closed(self):
        self.reject("prepare", env={"FAKE_NON_RACE": "1"})
        self.assertFalse(self.bundle.exists())
        self.reject("prepare", env={"FAKE_BUILD_FAIL": "1"})
        self.assertFalse(self.bundle.exists())

    def test_toolchain_flags_run_attempt_and_required_harness_must_match(self):
        self.prepare()
        self.reject(env={"FAKE_VERSION": "go1.27.0"})
        self.reject(env={"GITHUB_RUN_ATTEMPT": "2"})
        self.reject(env={"GITHUB_RUN_ID": "456"})
        self.reject(env={"CGO_CFLAGS": "-DSYNTHETIC_CHANGED=1"})
        self.reject("verify", "--github")
        command = self.command("verify")
        command[-1] = "--go-flags="
        result = subprocess.run(command, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)

    def test_mutation_during_build_and_concurrent_reuse_fail(self):
        marker = self.base / "started"
        process = subprocess.Popen(self.command(), env={**self.env, "FAKE_DELAY": "0.6",
                                                       "FAKE_MARKER": str(marker)},
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(lambda: process.kill() if process.poll() is None else None)
        deadline = time.monotonic() + 5
        while not marker.exists():
            self.assertIsNone(process.poll(), process.stderr.read() if process.poll() is not None else "")
            self.assertLess(time.monotonic(), deadline)
            time.sleep(0.01)
        self.reject("prepare")
        self.reject("verify")
        (self.root / "main.go").write_text("changed during build")
        _, errors = process.communicate(timeout=5)
        self.assertNotEqual(process.returncode, 0, errors)
        self.assertFalse(self.bundle.exists())

    def test_frontend_symlinks_and_unsafe_flags_are_rejected(self):
        (self.assets / "escape").symlink_to(self.root / "main.go")
        self.reject("prepare")
        (self.assets / "escape").unlink()
        for flags in ("-o /tmp/overwrite", "-p=99", "./cmd/other", "-C /tmp"):
            command = self.command()
            command[-1] = "--go-flags=" + flags
            result = subprocess.run(command, env=self.env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.bundle.exists())


if __name__ == "__main__":
    unittest.main()
