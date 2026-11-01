#!/usr/bin/env python3
"""Run a command unchanged while recording allowlisted runner resource counters."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

from suite_processes import OwnedProcesses

PROC = Path("/proc")
CGROUP = Path("/sys/fs/cgroup")
CPU_FIELDS = ("user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal", "guest", "guest_nice")
CGROUP_CPU = {"usage_usec", "user_usec", "system_usec", "nr_periods", "nr_throttled",
              "throttled_usec", "nr_bursts", "burst_usec"}
CGROUP_EVENTS = {"low", "high", "max", "oom", "oom_kill", "oom_group_kill", "sock_throttled"}


def available(collect):
    try:
        return collect()
    except Exception as exc:
        # Never include exception messages: those can contain workload paths.
        return {"unavailable": type(exc).__name__}


def counters(path, allowed):
    return {key: int(value) for line in path.read_text().splitlines()
            for key, value in [line.split()] if key in allowed}


def cpu():
    line = next(line for line in (PROC / "stat").read_text().splitlines() if line.startswith("cpu "))
    return dict(zip(CPU_FIELDS, map(int, line.split()[1:])))


def memory():
    names = {"MemAvailable": "available_kib", "SwapTotal": "swap_total_kib", "SwapFree": "swap_free_kib"}
    return {names[fields[0].rstrip(":")]: int(fields[1])
            for line in (PROC / "meminfo").read_text().splitlines()
            if (fields := line.split())[0].rstrip(":") in names}


def disks():
    result = {}
    for line in (PROC / "diskstats").read_text().splitlines():
        fields = line.split()
        if len(fields) < 14 or not re.fullmatch(r"[a-zA-Z0-9_.-]+", fields[2]):
            continue
        result[fields[2]] = {key: int(fields[index]) for key, index in (
            ("read_sectors", 5), ("read_ms", 6), ("write_sectors", 9),
            ("write_ms", 10), ("in_flight", 11), ("io_ms", 12), ("weighted_io_ms", 13))}
    return result


def filesystem(path):
    info = os.statvfs(path)
    return {"total_bytes": info.f_blocks * info.f_frsize,
            "available_bytes": info.f_bavail * info.f_frsize,
            "free_bytes": info.f_bfree * info.f_frsize,
            "free_inodes": info.f_favail}


def cgroup_directory():
    # Resolve the unified cgroup relative to its mount without retaining any
    # host/container identity in the receipt. Cgroup namespaces often use '/'.
    try:
        for line in (PROC / "self/cgroup").read_text().splitlines():
            if line.startswith("0::"):
                relative = line[3:].lstrip("/")
                candidate = (CGROUP / relative).resolve()
                if candidate.is_relative_to(CGROUP.resolve()) and candidate.is_dir():
                    return candidate
    except (OSError, ValueError):
        pass
    return CGROUP


def snapshot():
    group = cgroup_directory()
    return {"cpu": available(cpu), "memory": available(memory),
            "swap": available(lambda: counters(PROC / "vmstat", {"pswpin", "pswpout"})),
            "disk": available(disks),
            "cgroup": {"cpu": available(lambda: counters(group / "cpu.stat", CGROUP_CPU)),
                       "memory_current_bytes": available(lambda: int((group / "memory.current").read_text())),
                       "memory_peak_bytes": available(lambda: int((group / "memory.peak").read_text())),
                       "memory_events": available(lambda: counters(group / "memory.events", CGROUP_EVENTS))},
            "filesystems": {"cwd": available(lambda: filesystem(Path.cwd())),
                            "cache": available(lambda: filesystem(Path("/cache")))}}


def differences(before, after, path=()):
    if isinstance(before, dict) and isinstance(after, dict):
        if "unavailable" in before or "unavailable" in after:
            return {"unavailable": "source unavailable"}
        return {key: differences(value, after[key], (*path, key))
                for key, value in before.items() if key in after}
    if isinstance(before, (int, float)) and isinstance(after, (int, float)):
        delta = after - before
        cumulative = path[:1] in (("cpu",), ("swap",), ("disk",)) or path[:2] in (
            ("cgroup", "cpu"), ("cgroup", "memory_events"))
        if delta < 0 and cumulative and path[-1] != "in_flight":
            return {"unavailable": "counter reset"}
        return delta
    return {"unavailable": "incompatible samples"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.command[:1] == ["--"]:
        args.command.pop(0)
    if not args.command:
        parser.error("a command is required after --")
    output_error = None
    try:
        args.output.mkdir(parents=True, exist_ok=True)
        if any((args.output / name).exists() for name in ("metrics.json", "samples.jsonl")):
            parser.error("metrics output files must be fresh")
    except OSError as exc:
        output_error = type(exc).__name__
    began = time.monotonic()
    receipt = {"started_at_epoch": time.time(), "signal": 0, "exit_status": 127,
               "sample_interval_seconds": 1, "before": available(snapshot)}
    if output_error:
        receipt["output_unavailable"] = output_error
    owner = OwnedProcesses(45)
    process = None
    samples = None

    def interrupt(signum, _frame):
        receipt["signal"] = signum

    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGINT, interrupt)
    try:
        try:
            samples = (args.output / "samples.jsonl").open("x")
        except OSError as exc:
            receipt["sampling_unavailable"] = type(exc).__name__
        try:
            # Inherit each output channel directly, with no buffering, logging,
            # argument recording or environment recording by this wrapper.
            process = subprocess.Popen(args.command, start_new_session=True)
            owner.children.append(process)
            owner.groups.add(process.pid)
        except OSError as exc:
            receipt["workload_error"] = type(exc).__name__
        if process is not None:
            next_sample = began + 1
            while process.poll() is None and not receipt["signal"]:
                owner.discover()
                now = time.monotonic()
                if now >= next_sample:
                    sample = {"elapsed_seconds": round(now - began, 3), "metrics": available(snapshot)}
                    if samples is not None:
                        try:
                            samples.write(json.dumps(sample) + "\n")
                            samples.flush()
                        except OSError as exc:
                            receipt["sampling_unavailable"] = type(exc).__name__
                    next_sample = now + 1
                time.sleep(.05)
            if process.returncode is not None:
                receipt["exit_status"] = process.returncode
    finally:
        try:
            owner.cleanup()
        except Exception as exc:
            receipt["cleanup_unavailable"] = type(exc).__name__
        if process is not None:
            receipt["exit_status"] = process.returncode
        if receipt["signal"]:
            receipt["exit_status"] = 128 + receipt["signal"]
        if samples is not None:
            try:
                samples.close()
            except OSError as exc:
                receipt["sampling_unavailable"] = type(exc).__name__
        receipt.update(finished_at_epoch=time.time(), elapsed_seconds=round(time.monotonic() - began, 3),
                       after=available(snapshot))
        receipt["differences"] = differences(receipt["before"], receipt["after"])
        cpu_delta = receipt["differences"].get("cpu", {})
        if all(isinstance(cpu_delta.get(key), int) for key in CPU_FIELDS[:8]):
            ticks = sum(cpu_delta[key] for key in CPU_FIELDS[:8])
            if ticks:
                receipt["cpu_busy_percent"] = round(100 * (ticks - cpu_delta["idle"] - cpu_delta["iowait"]) / ticks, 3)
                receipt["cpu_steal_percent"] = round(100 * cpu_delta["steal"] / ticks, 3)
        try:
            temporary = args.output / "metrics.json.tmp"
            temporary.write_text(json.dumps(receipt, indent=2) + "\n")
            temporary.replace(args.output / "metrics.json")
        except OSError:
            print("Runner metrics receipt unavailable", file=sys.stderr, flush=True)
    status = receipt["exit_status"]
    if status is not None and status < 0:
        # Preserve a child's signal termination, including subprocess-visible
        # negative return codes, after writing its final receipt.
        signum = -status
        signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)
    return status if status is not None else 127


if __name__ == "__main__":
    sys.exit(main())
