#!/usr/bin/env python3
"""Run file-level Playwright shards against independent Helm runtime fixtures."""
import argparse
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import sys
import time


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("disabled", "local"), required=True)
    parser.add_argument("--workers", type=int, required=True)
    parser.add_argument("--base-port", type=int, required=True)
    parser.add_argument("--work-dir", type=Path, required=True)
    parser.add_argument("--artifact-dir", type=Path, required=True)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--worker-script", type=Path,
                        default=Path(__file__).with_name("run-worker.sh"))
    parser.add_argument("playwright", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.playwright[:1] == ["--"]:
        args.playwright.pop(0)
    if not 1 <= args.workers <= 8:
        parser.error("workers must be between 1 and 8")
    if args.base_port < 1024 or args.base_port + (args.workers - 1) * 100 + 30 > 65535:
        parser.error("worker port blocks are outside the allowed range")
    if args.mode == "local" and args.workers != 1:
        parser.error("local onboarding requires one fresh fixture")
    if any(a == "-j" or a.startswith(("--workers", "--shard", "-j=")) or
           (a.startswith("-j") and a[2:].isdigit()) for a in args.playwright):
        parser.error("use HELM_E2E_WORKERS; inner workers and shard overrides share runtime state")
    for name in ("work_dir", "artifact_dir"):
        path = getattr(args, name).resolve()
        if path.exists() and any(path.iterdir()):
            parser.error(f"{name} must be absent or empty")
        setattr(args, name, path)
    if args.work_dir == args.artifact_dir or args.work_dir in args.artifact_dir.parents:
        parser.error("evidence must be outside disposable worker state")
    args.binary = args.binary.resolve()
    args.worker_script = args.worker_script.resolve()
    if not all(os.access(p, os.X_OK) for p in (args.binary, args.worker_script)):
        parser.error("binary and worker script must be executable")
    return args


def signal_group(pid, signum):
    try:
        os.killpg(pid, signum)
    except ProcessLookupError:
        pass


def run(args):
    args.work_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    args.artifact_dir.mkdir(parents=True, exist_ok=True)
    selector = selectors.DefaultSelector()
    children = []
    interrupted = 0
    stopping_at = None
    began = time.monotonic()

    def interrupt(signum, _frame):
        nonlocal interrupted
        interrupted = signum

    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGINT, interrupt)
    try:
        for index in range(1, args.workers + 1):
            if interrupted:
                break
            name = f"worker-{index}"
            state_dir = args.work_dir / name
            state_dir.mkdir(mode=0o700)
            port = args.base_port + (index - 1) * 100
            playwright = [*args.playwright, "--workers=1"]
            if args.workers > 1:
                playwright.extend([f"--shard={index}/{args.workers}", "--pass-with-no-tests"])
            command = [str(args.worker_script), args.mode, str(port), str(state_dir),
                       str(args.artifact_dir / name), str(args.binary), *playwright]
            log = (args.artifact_dir / f"{name}.console.log").open("wb")
            process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                       start_new_session=True)
            child = {"process": process, "log": log, "started": time.monotonic(),
                     "cleanup_deadline": None,
                     "receipt": {"worker": index, "base_port": port, "exit_status": None}}
            children.append(child)
            selector.register(process.stdout, selectors.EVENT_READ, child)
            print(f"[browser {index}/{args.workers}] starting on port {port}", flush=True)

        while selector.get_map() or any(c["process"].poll() is None or
                                       c["cleanup_deadline"] is not None for c in children):
            if interrupted and stopping_at is None:
                stopping_at = time.monotonic()
                for child in children:
                    signal_group(child["process"].pid, signal.SIGTERM)
            if stopping_at is not None and time.monotonic() - stopping_at > 15:
                for child in children:
                    signal_group(child["process"].pid, signal.SIGKILL)
            for key, _ in selector.select(timeout=0.1):
                chunk = os.read(key.fileobj.fileno(), 65536)
                child = key.data
                if chunk:
                    child["log"].write(chunk)
                    child["log"].flush()
                    prefix = f"[browser {child['receipt']['worker']}/{args.workers}] "
                    for line in chunk.decode("utf-8", errors="replace").splitlines():
                        print(prefix + line, flush=True)
                else:
                    selector.unregister(key.fileobj)
                    key.fileobj.close()
            for child in children:
                status = child["process"].poll()
                receipt = child["receipt"]
                if status is not None and receipt["exit_status"] is None:
                    receipt.update(exit_status=status,
                                   elapsed_seconds=round(time.monotonic() - child["started"], 3))
                    # Reap any descendants left by a failed worker before reusing its ports.
                    signal_group(child["process"].pid, signal.SIGTERM)
                    child["cleanup_deadline"] = time.monotonic() + 3
                    print(f"[browser {receipt['worker']}/{args.workers}] finished: "
                          f"exit {status}, {receipt['elapsed_seconds']}s", flush=True)
                if child["cleanup_deadline"] is not None:
                    try:
                        os.killpg(child["process"].pid, 0)
                    except ProcessLookupError:
                        child["cleanup_deadline"] = None
                    else:
                        if time.monotonic() >= child["cleanup_deadline"]:
                            signal_group(child["process"].pid, signal.SIGKILL)
                            child["cleanup_deadline"] = None
    finally:
        # Also covers spawn errors and unexpected coordinator failures. Target
        # only process groups we created; unrelated invocations stay untouched.
        for child in children:
            signal_group(child["process"].pid, signal.SIGTERM)
        deadline = time.monotonic() + 15
        for child in children:
            process = child["process"]
            try:
                process.wait(timeout=max(0.01, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                signal_group(process.pid, signal.SIGKILL)
                process.wait()
            signal_group(process.pid, signal.SIGKILL)
            child["log"].close()
            receipt = child["receipt"]
            if receipt["exit_status"] is None:
                receipt.update(exit_status=process.returncode,
                               elapsed_seconds=round(time.monotonic() - child["started"], 3))
        selector.close()
        summary = {"auth_mode": args.mode, "requested_workers": args.workers,
                   "elapsed_seconds": round(time.monotonic() - began, 3),
                   "signal": interrupted, "workers": [c["receipt"] for c in children]}
        (args.artifact_dir / "workers.json").write_text(json.dumps(summary, indent=2) + "\n")
    if interrupted:
        return 128 + interrupted
    # Empty individual shards are expected for small focused selections. A
    # selection matching nothing at all must retain Playwright's failure rule.
    reports = [args.artifact_dir / f"worker-{i}/results.json"
               for i in range(1, args.workers + 1)]
    if all(path.is_file() for path in reports):
        total = sum(sum(json.loads(path.read_text())["stats"].get(key, 0)
                        for key in ("expected", "unexpected", "skipped", "flaky"))
                    for path in reports)
        if total == 0:
            print("No browser tests matched the requested selection", file=sys.stderr)
            return 1
    return int(any(c["receipt"]["exit_status"] != 0 for c in children))


if __name__ == "__main__":
    sys.exit(run(arguments()))
