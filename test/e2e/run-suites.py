#!/usr/bin/env python3
"""Run prepared Helm browser suites under one global runtime worker budget."""
import argparse
import json
import os
from pathlib import Path
import signal
import sys
import time

from suite_processes import OwnedProcesses
from browser_selection import option_tokens

HERE = Path(__file__).resolve().parent
DEADLINE = "e2e/luna-account-deadline-stability.spec.ts"
ONBOARDING = "e2e/onboarding.spec.ts"
# Genuine human membership journeys need a fresh local-auth fixture. Full runs
# require it; focused runs add it only for this exact selected spec operand.
MEMBERSHIP = "e2e/membership-sessions.spec.ts"
LOCAL_SPECS = {"onboarding": ONBOARDING, "membership": MEMBERSHIP}


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workers", type=int, required=True)
    parser.add_argument("--base-port", type=int, required=True)
    for name in ("work-dir", "artifact-dir", "binary"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--github-binary", type=Path)
    parser.add_argument("--include-deadline", action="store_true")
    parser.add_argument("--browser-script", type=Path, default=HERE / "run-browsers.py")
    parser.add_argument("--github-script", type=Path, default=HERE.parent / "githubbeta/run.sh")
    parser.add_argument("--cleanup-grace", type=float, default=22)
    parser.add_argument("playwright", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.playwright[:1] == ["--"]:
        args.playwright.pop(0)
    if not 1 <= args.workers <= 8:
        parser.error("workers must be 1..8")
    # Ancillary blocks start after the entire main allocation. Every browser
    # fixture consumes base,+10,+20,+30; GitHub owns its entire block as well.
    if args.base_port < 1024 or args.base_port + (args.workers + 2) * 100 + 30 > 65535:
        parser.error("suite port blocks are outside the allowed range")
    if args.cleanup_grace <= 0 or args.cleanup_grace > 60:
        parser.error("cleanup grace must be >0 and <=60 seconds")
    if args.playwright and args.include_deadline:
        parser.error("include-deadline is for full suite invocations")
    if any(a == "-j" or a.startswith(("--workers", "--shard", "-j=")) or
           (a.startswith("-j") and a[2:].isdigit()) for a in option_tokens(args.playwright)):
        parser.error("inner worker/shard overrides violate the suite worker budget")
    for name in ("work_dir", "artifact_dir"):
        path = getattr(args, name).resolve()
        if path.exists() and (not path.is_dir() or any(path.iterdir())):
            parser.error(f"{name} must be absent or empty")
        setattr(args, name, path)
    if (args.work_dir == args.artifact_dir or args.work_dir in args.artifact_dir.parents or
            args.artifact_dir in args.work_dir.parents):
        parser.error("state and evidence directories must be separate")
    for name in ("binary", "github_binary", "browser_script", "github_script"):
        value = getattr(args, name)
        if value is not None:
            setattr(args, name, value.resolve())
    if not os.access(args.binary, os.X_OK):
        parser.error("Helm binary must be executable")
    if not args.playwright and (args.github_binary is None or
                               not os.access(args.github_binary, os.X_OK)):
        parser.error("full suites require an executable prepared GitHub binary")
    return args


def run(args):
    args.work_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    args.work_dir.chmod(0o700)
    args.artifact_dir.mkdir(parents=True, exist_ok=True)
    owner = OwnedProcesses(args.cleanup_grace + 2)
    interrupted = 0
    began = time.monotonic()
    phases = []
    running = []
    outputs = {}
    error = None

    def interrupt(signum, _frame):
        nonlocal interrupted
        interrupted = signum

    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGINT, interrupt)
    full = not args.playwright
    membership_focus = MEMBERSHIP in option_tokens(args.playwright)
    single_membership_focus = membership_focus and all(
        value.startswith("-") or value == MEMBERSHIP for value in option_tokens(args.playwright))
    for index, name in enumerate(("main", "onboarding", "github", "deadline", "membership")):
        required = name in ("main", "onboarding") or (name == "github" and full) or (
            name == "deadline" and args.include_deadline) or (
            name == "membership" and (full or membership_focus))
        port = args.base_port if name == "main" else args.base_port + (args.workers + index - 1) * 100
        if name == "membership":
            # Onboarding's fixture binds +0..+30 of its block; membership owns
            # the unused upper half so existing range checks stay unchanged.
            port = args.base_port + args.workers * 100 + 50
        phases.append({"name": name, "required": required,
                       "status": "pending" if required else "skipped",
                       "exit_status": None, "started_at_epoch": None,
                       "finished_at_epoch": None, "elapsed_seconds": 0,
                       "workers": args.workers if name == "main" else 1,
                       "base_port": port,
                       "reason": None if required else "not selected"})

    def persist():
        summary = {"requested_workers": args.workers, "signal": interrupted,
                   "elapsed_seconds": round(time.monotonic() - began, 3), "phases": phases}
        if error:
            summary["error"] = error
        temporary = args.artifact_dir / "suites.json.tmp"
        temporary.write_text(json.dumps(summary, indent=2) + "\n")
        temporary.replace(args.artifact_dir / "suites.json")

    def launch(phase):
        name = phase["name"]
        work = args.work_dir / name
        artifacts = args.artifact_dir / name
        work.mkdir(mode=0o700)
        artifacts.mkdir()
        env = dict(os.environ)
        env.pop("HELM_E2E_CODEX_STALL_ACCOUNT", None)
        env.pop("HELM_E2E_MEMBERSHIP_REQUIRED", None)
        if name == "membership":
            env["HELM_E2E_MEMBERSHIP_REQUIRED"] = "true"
        # Preserve the historical explicit focused deadline invocation. Ordinary
        # main selections and all ancillary fixtures never inherit this flag.
        deadline_focus = (DEADLINE in args.playwright and
                          all(not a.endswith(".spec.ts") or a == DEADLINE for a in args.playwright))
        if name == "deadline" or (name == "main" and deadline_focus and
                                  os.environ.get("HELM_E2E_CODEX_STALL_ACCOUNT") == "true"):
            env["HELM_E2E_CODEX_STALL_ACCOUNT"] = "true"
        if name == "github":
            env.update(HELM_GITHUB_BETA_BINARY=str(args.github_binary),
                       HELM_GITHUB_BETA_PORT=str(phase["base_port"]),
                       HELM_GITHUB_BETA_WORK_DIR=str(work),
                       HELM_E2E_ARTIFACT_DIR=str(artifacts))
            command = [str(args.github_script)]
        else:
            selection = args.playwright if name == "main" or (
                name == "membership" and single_membership_focus) else [LOCAL_SPECS.get(name, DEADLINE)]
            command = [sys.executable, str(args.browser_script), "--mode",
                       "local" if name in LOCAL_SPECS else "disabled", "--workers",
                       str(phase["workers"]), "--base-port", str(phase["base_port"]),
                       "--work-dir", str(work), "--artifact-dir", str(artifacts),
                       "--binary", str(args.binary), "--", *selection]
        phase.update(status="running", started_at_epoch=time.time())
        started = time.monotonic()
        log = (args.artifact_dir / f"{name}.console.log").open("wb")
        try:
            # Validate before wrapping so spawn failures retain explicit phase
            # error evidence; the wrapper owns cleanup of nested browser groups.
            if not command[0] or not os.access(command[0], os.X_OK):
                raise FileNotFoundError(f"suite executable is unavailable: {command[0]}")
            wrapped = [sys.executable, str(HERE / "suite_processes.py"),
                       str(args.cleanup_grace), *command]
            process = owner.start(wrapped, env, log)
        except OSError as exc:
            log.close()
            phase.update(status="failed", exit_status=127, error=str(exc),
                         finished_at_epoch=time.time(),
                         elapsed_seconds=round(time.monotonic() - started, 3))
        else:
            outputs[name] = (args.artifact_dir / f"{name}.console.log").open("rb")
            running.append((process, phase, log, started))
            print(f"[suite {name}] starting with {phase['workers']} worker(s)", flush=True)
        persist()

    def forward(name):
        # Tail regular files rather than pipes: a crashed/nested supervisor
        # cannot hold a pipe open and prevent cleanup. Limit each phase to one
        # 64 KiB chunk per polling turn so siblings keep making progress.
        chunk = outputs[name].read(65536)
        if chunk:
            for line in chunk.decode("utf-8", errors="replace").splitlines():
                print(f"[suite {name}] {line}", flush=True)
        return len(chunk)

    def collect():
        owner.discover()
        for child in list(running):
            process, phase, log, started = child
            status = process.poll()
            forwarded = forward(phase["name"])
            if status is not None and forwarded < 65536:
                phase.update(status="passed" if status == 0 else "failed", exit_status=status,
                             finished_at_epoch=time.time(),
                             elapsed_seconds=round(time.monotonic() - started, 3))
                log.close()
                outputs.pop(phase["name"]).close()
                running.remove(child)
                print(f"[suite {phase['name']}] finished: exit {status}", flush=True)
                persist()
                if status < 0 and phase["name"] != "main":
                    # A killed ownership wrapper cannot certify its descendants
                    # are gone. Keep capacity closed until final cleanup; do not
                    # launch another fixture into an apparently released slot.
                    raise RuntimeError(f"suite {phase['name']} ownership wrapper was killed")

    try:
        persist()
        launch(phases[0])
        while running and not interrupted:
            collect()
            time.sleep(.04)
        # A completed/failed supervisor may have orphaned nested worker sessions.
        # Clean those before allowing another fixture to consume its capacity.
        owner.cleanup()
        pending = [p for p in phases[1:] if p["required"]]
        while (pending or running) and not interrupted:
            while pending and len(running) < args.workers and not interrupted:
                launch(pending.pop(0))
            collect()
            time.sleep(.04)
    except Exception as exc:
        error = f"{type(exc).__name__}: {exc}"
        print(error, file=sys.stderr)
    finally:
        try:
            owner.cleanup()
        except Exception as exc:
            error = error or f"cleanup: {type(exc).__name__}: {exc}"
        for process, phase, log, started in running:
            phase.update(status="canceled" if interrupted else "failed",
                         exit_status=process.returncode, finished_at_epoch=time.time(),
                         elapsed_seconds=round(time.monotonic() - started, 3))
            log.close()
            try:
                while forward(phase["name"]):
                    pass
            except OSError as exc:
                error = error or f"output: {type(exc).__name__}: {exc}"
            finally:
                outputs.pop(phase["name"]).close()
        for phase in phases:
            if phase["status"] == "pending":
                phase.update(status="canceled" if interrupted else "skipped",
                             reason="interrupted" if interrupted else "coordinator error")
        persist()
    if interrupted:
        return 128 + interrupted
    return int(bool(error) or any(p["required"] and p["status"] != "passed" for p in phases))


if __name__ == "__main__":
    sys.exit(run(arguments()))
