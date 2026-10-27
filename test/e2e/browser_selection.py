"""Discover selected files with the installed Playwright, without running tests."""
import json
import os
from pathlib import Path
import signal
import subprocess

from browser_timings import load_profile, plan_files, report_files, unique_object


def option_tokens(arguments):
    """Keep option operands opaque, including grep text resembling CLI flags."""
    operands = {"--browser", "--global-timeout", "-g", "--grep", "-G", "--grep-invert",
                "--max-failures", "--repeat-each", "--retries", "--timeout", "--trace",
                "--tsconfig"}
    iterator = iter(arguments)
    for value in iterator:
        yield value
        if value in operands:
            next(iterator, None)


def validate_options(arguments):
    # These modes/configuration sources alter selection or execution between the
    # discovery and runtime phases, or replace the file intersection we own.
    forbidden = {"--config", "--test-list", "--test-list-invert", "--last-failed",
                 "--only-changed", "--list", "--list-files", "--ui", "--ui-host",
                 "--ui-port", "--debug", "--fully-parallel", "--help", "-h",
                 "--output", "--reporter", "--last-failed-file", "--run-agents",
                 "--update-snapshots", "--update-source-method", "-u"}
    for value in option_tokens(arguments):
        option = value.split("=", 1)[0]
        if option in forbidden or value == "--" or value.startswith(("-c", "-u")):
            raise ValueError("timed sharding does not support configuration, list, incremental or interactive overrides")


def discover_files(arguments, web, base_port):
    cli = web / "node_modules/playwright/cli.js"
    if not cli.is_file():
        raise ValueError("installed Playwright CLI is required for timed sharding")
    environment = {key: value for key, value in os.environ.items()
                   if not key.startswith("PLAYWRIGHT_JSON_OUTPUT_")}
    origin = f"http://127.0.0.1:{base_port}"
    environment.update(HELM_E2E_BASE_URL=origin, ROADMAP_E2E_BASE_URL=origin)
    command = ["node", str(cli), "test", *arguments, "--list", "--reporter=json", "--workers=1"]
    process = subprocess.Popen(command, cwd=web, env=environment, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True)
    previous = {}

    def interrupted(signum, _frame):
        raise SystemExit(128 + signum)

    try:
        for signum in (signal.SIGTERM, signal.SIGINT):
            previous[signum] = signal.signal(signum, interrupted)
        try:
            stdout, _stderr = process.communicate(timeout=60)
        except subprocess.TimeoutExpired as error:
            raise ValueError("Playwright discovery timed out") from error
        if process.returncode:
            raise ValueError("Playwright discovery failed; no fixtures started")
        try:
            report = json.loads(stdout, object_pairs_hook=unique_object)
            if report.get("errors") or Path(report["config"]["rootDir"]).resolve() != (web / "e2e").resolve():
                raise ValueError("Playwright discovery returned errors or an unexpected test root")
            selected = sorted({name for name, spec in report_files(report, web) if spec.get("tests")})
        except (ValueError, KeyError, TypeError, AttributeError) as error:
            raise ValueError("Playwright discovery did not return a valid suite inventory") from error
        if not selected:
            raise ValueError("No browser tests matched the requested selection")
        return selected
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate()
        for signum, handler in previous.items():
            signal.signal(signum, handler)


def timing_plan(args):
    validate_options(args.playwright)
    web = Path(__file__).resolve().parents[2] / "web"
    profile = load_profile(args.timings, web)
    selected = discover_files(args.playwright, web, args.base_port)
    return plan_files(selected, profile, web, args.workers)
