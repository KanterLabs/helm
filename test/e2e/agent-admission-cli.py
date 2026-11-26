#!/usr/bin/env python3
"""Exercise the installed-style Helm CLI against isolated candidate and baseline servers."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid

from agent_admission_fixture import Fixture


CLI = Path(__file__).resolve().parents[2] / "skills/helm/scripts/helm.py"


def invoke(base_url, token, directory, *args, session="synthetic-cli-1", succeeds=True, require=False, state_dir=None):
    env = {
        key: value for key, value in os.environ.items()
        if not key.startswith(("HELM_", "TC_ROADMAP_", "ROADMAP_", "CODEX_"))
    }
    config = Path(directory) / "credentials.json"
    config.write_text(json.dumps({"base_url": base_url, "token": token}))
    config.chmod(0o600)
    env.update(HELM_CONFIG=str(config), HELM_STATE_DIR=str(state_dir or Path(directory) / "state"),
               HELM_AGENT_SESSION_ID=session, CODEX_SESSION_ID=session)
    if require:
        env["HELM_REQUIRE_AGENT_ADMISSION"] = "1"
    result = subprocess.run([sys.executable, str(CLI), *args], env=env, text=True, capture_output=True, timeout=30)
    assert (result.returncode == 0) == succeeds, (args[0], result.returncode)
    if succeeds:
        return json.loads(result.stdout)
    assert not result.stdout, args[0]
    return json.loads(result.stderr.splitlines()[-1])["error"]


def exercise_candidate(fixture, directory):
    project = fixture.project("CLI")
    token = fixture.token(fixture.agent("CLI synthetic agent", [project]), [project])

    def cli(*args, **kwargs):
        return invoke(fixture.url, token, directory, *args, **kwargs)

    operation = str(uuid.uuid4())
    start_args = (
        "start", "--project", "CLI", "--title", "Verify synthetic work",
        "--goal", "Publish the verification receipt", "--step", "Run the synthetic check",
        "--step", "Inspect the receipt", "--operation-id", operation,
    )
    started = cli(*start_args)
    assert started["action"] == "started"
    assert "fence" not in started
    assert started["next_action"] == "Run the synthetic check"
    assert started["finish_condition"] == "Publish the verification receipt"
    assert started["task"]["description"] == (
        "Goal: Publish the verification receipt\n\nCheckpoints\n"
        "- [ ] Run the synthetic check\n- [ ] Inspect the receipt"
    )
    assert cli(*start_args)["task"]["id"] == started["task"]["id"]
    key = started["task"]["key"]
    competing = cli(
        "start", "--project", "CLI", "--title", "Denied competing task", "--goal", "Complete work",
        "--next-action", "Inspect the input", session="synthetic-cli-2", succeeds=False,
    )
    assert competing["status"] == 409, competing
    assert not cli("tasks", "--project", "CLI", "--query", "Denied competing task")["tasks"]
    rejected_update = cli("progress", "--task", key, "--state", "working", "--message", "Check started",
                          session="synthetic-cli-2", succeeds=False)
    assert rejected_update["status"] in {403, 409}, rejected_update
    cli("progress", "--task", key, "--state", "verifying", "--message", "Check passed",
        "--operation-id", operation)
    cli("complete", "--task", key, "--comment", "Receipt published")
    stale = cli(*start_args, succeeds=False)
    assert stale.get("status", 409) in {404, 409}, stale

    unplanned = cli("backlog", "--project", "CLI", "--title", "Review synthetic output", "--goal", "Confirm quality")
    key = unplanned["task"]["key"]
    fixture.call("PATCH", "/tasks/" + key, {"column_id": fixture.column(project, "ready")},
                 version=unplanned["task"]["version"]).expect(200)
    selection = cli("select-next", "--project", "CLI")
    assert selection["task"]["key"] == key
    assert not cli("tasks", "--project", "CLI", "--query", "Review synthetic output")["tasks"][0].get("claimed_by")
    existing = cli("start", "--project", "CLI", "--title", "Leave existing work behind", "--goal", "Complete work",
                   "--next-action", "Inspect the input", succeeds=False)
    assert existing["code"] == "eligible_task_exists", existing
    assert not cli("tasks", "--project", "CLI", "--query", "Leave existing work behind")["tasks"]
    incomplete = cli("resume", "--task", key, succeeds=False)
    assert "--next-action" in incomplete["message"], incomplete
    resumed = cli("resume", "--task", key, "--next-action", "Inspect the output")
    assert resumed["action"] == "resumed"
    assert resumed["finish_condition"] == "Confirm quality"
    return {"started": started["action"], "competing": competing["code"], "rejected_update": rejected_update["code"],
            "stale_replay": stale["code"], "selection": selection["task"]["key"], "resumed": resumed["action"],
            "existing_work": existing["code"]}


def exercise_baseline(fixture, directory):
    project = fixture.project("OLD")
    token = fixture.token(fixture.agent("CLI legacy synthetic agent", [project]), [project])

    def cli(*args, **kwargs):
        return invoke(fixture.url, token, directory, *args, **kwargs)

    started = cli("start", "--project", "OLD", "--title", "Legacy CLI task", "--goal", "Verify compatibility")
    key = started["task"]["key"]
    assert started["task"]["claimed_by"]
    unavailable = cli("select-next", "--project", "OLD", succeeds=False)
    assert unavailable["code"] == "admission_unavailable", unavailable
    strict = cli("start", "--project", "OLD", "--title", "Require admission", "--goal", "Verify enforcement",
                 require=True, succeeds=False)
    assert strict["code"] == "admission_unavailable", strict
    assert not cli("tasks", "--project", "OLD", "--query", "Require admission")["tasks"]
    cli("release", "--task", key)
    resumed = cli("resume", "--task", key)
    assert resumed["task"]["claimed_by"]
    return {"legacy_start": key, "legacy_resume": resumed["task"]["key"], "discovery": unavailable["code"]}, token


def exercise_recovery(fixture, directory):
    project = fixture.project("REC")
    agent = fixture.agent("CLI synthetic recovery", [project])
    token = fixture.token(agent, [project])
    operation = str(uuid.uuid4())
    start = ("start", "--project", "REC", "--title", "Verify receipt recovery", "--goal", "Recover the live task",
             "--step", "Replay the original request", "--operation-id", operation)
    broken_state = Path(directory) / "unavailable-state"
    broken_state.touch(mode=0o600)
    failure = invoke(fixture.url, token, directory, *start, succeeds=False, state_dir=broken_state)
    assert failure["code"] == "admission_state_unavailable" and operation in failure["message"], failure
    tasks = fixture.call("GET", "/projects/REC/tasks").expect(200)["data"]
    assert len(tasks) == 1 and tasks[0]["claimed_by"] == agent["id"]
    repaired = invoke(fixture.url, token, directory, *start)
    assert repaired["task"]["id"] == tasks[0]["id"] and "fence" not in repaired
    return {"state_failure": failure["code"], "recovered": repaired["task"]["key"]}


def exercise_upgraded_claim(fixture, directory, token, key):
    def cli(*args, **kwargs):
        return invoke(fixture.url, token, directory, *args, **kwargs)

    renewed = cli("renew", "--task", key, "--lease-seconds", "300")
    assert renewed["task"]["claimed_by"]
    completed = cli("complete", "--task", key, "--comment", "Grandfathered claim finished")
    assert completed["task"]["completed_at"] and not completed["task"].get("claimed_by")
    return {"legacy_renew": key, "legacy_complete": completed["task"]["key"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="helm-admission-cli-") as directory:
        candidate_dir = Path(directory) / "candidate"
        candidate_dir.mkdir()
        baseline_dir = Path(directory) / "baseline"
        baseline_dir.mkdir()
        with Fixture(candidate_dir).server(args.candidate.resolve()) as fixture:
            candidate = exercise_candidate(fixture, candidate_dir)
            candidate.update(exercise_recovery(fixture, candidate_dir))
        upgrade_fixture = Fixture(baseline_dir)
        with upgrade_fixture.server(args.baseline.resolve()) as fixture:
            baseline, token = exercise_baseline(fixture, baseline_dir)
        with upgrade_fixture.server(args.candidate.resolve()) as fixture:
            baseline.update(exercise_upgraded_claim(fixture, baseline_dir, token, baseline["legacy_resume"]))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as output:
        json.dump({"candidate": candidate, "baseline": baseline}, output, indent=2)
        output.write("\n")


if __name__ == "__main__":
    main()
