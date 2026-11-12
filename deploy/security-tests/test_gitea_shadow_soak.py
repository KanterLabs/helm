#!/usr/bin/env python3
"""Offline proofs for the Gitea shadow soak ledger, written before the tool.

Failure contract: docs/GITEA_CI_SOAK_FAILURES.md. Each case changes the
recorded scenario (gitea_shadow_soak_fixture.py), runs
deploy/gitea-shadow-soak.py against it with --replay, and checks that the
ledger says what the contract requires for the stated reason: the class and
reason code of the targeted commit, the streak, the timing, the secrets claim
or the exit status. A sentinel token stands in for the real one. No network.
"""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import tempfile

import gitea_shadow_soak_fixture as fx
from gitea_shadow_soak_fixture import SHA_7B6, SHA_498, SHA_912, Scenario, at, fake_sha

ROOT = Path(__file__).resolve().parents[2]
TOOL = ROOT / "deploy/gitea-shadow-soak.py"
TOKEN = "sentinel-token-9f8e7d6c5b4a"
GREEN = {"checks": "success", "browser": "success", "race": "success"}


def commit(ledger, sha):
    found = [c for c in ledger["commits"] if c["sha"] == sha or c["sha"].startswith(sha)]
    if len(found) != 1:
        raise AssertionError(f"expected one ledger commit for {sha}, found {len(found)}")
    return found[0]


def expect(condition, message):
    if not condition:
        raise AssertionError(message)


def is_class(sha, klass, reason=None):
    def check(result):
        entry = commit(result["ledger"], sha)
        expect(entry["class"] == klass, f"{sha[:8]} is {entry['class']} {entry['reasons']}, want {klass}")
        if reason:
            expect(reason in entry["reasons"], f"{sha[:8]} reasons {entry['reasons']} lack {reason}")
    return check


def streak(count, ended_by=None):
    def check(result):
        got = result["ledger"]["streak"]
        expect(got["count"] == count, f"streak {got['count']}, want {count}")
        end = (got["ended_by"] or {}).get("sha")
        expect(end == ended_by, f"streak ended by {end}, want {ended_by}")
    return check


def both(*checks):
    def check(result):
        for item in checks:
            item(result)
    return check


def exits(status, phrase=None, ledger_written=None):
    def check(result):
        expect(result["exit"] == status, f"exit {result['exit']}, want {status}: {result['stderr'][-300:]}")
        if phrase:
            expect(phrase in result["stderr"], f"stderr lacks {phrase!r}: {result['stderr'][-300:]}")
        if ledger_written is not None:
            expect((result["ledger"] is not None) == ledger_written, "ledger presence is wrong")
    return check


def gate_unmet(part):
    def check(result):
        gate = result["ledger"]["gate"]
        expect(gate["met"] is False and gate[part] is False, f"gate {gate}")
    return check


# Scenario edits. Each returns (scenario, extra args, annotations or None).
def base(s):
    return s


def rerun_gitea(s):
    s.gitea_run(393)["run_attempt"] = 2
    return s


def rerun_github(s):
    run = s.github_run(43166616500)
    run["run_attempt"] = 2
    s.overrides["github"][fx.GITHUB_ATTEMPT.format(id=run["id"], n=1)] = {
        "body": dict(run, run_attempt=1, conclusion="failure")}
    return s


def gitea_browser_fails(s):
    s.set_job("gitea", 393, "browser", conclusion="failure")
    s.set_job("gitea", 393, "race", conclusion="failure")
    s.gitea_run(393)["conclusion"] = "failure"
    return s


def both_fail(s):
    gitea_browser_fails(s)
    for name in ("browser", "race"):
        s.set_job("github", 43166616500, name, conclusion="failure")
    return s


def job_missing(s):
    s.gitea_jobs[393] = [j for j in s.gitea_jobs[393] if j["name"] != "race"]
    return s


def inconsistent(s):
    s.add_gitea(SHA_7B6, {"checks": "success", "browser": "failure", "race": "failure"}, created=at(0))
    return s


def github_push_too(s):
    s.add_github(SHA_7B6, {"checks": "success", "browser": "success", "race": "success",
                           "container": "success"}, created=at(0), timestamp="2026-11-12T06:13:20Z")
    return s


def cancelled_beside(s):
    s.add_github(SHA_7B6, {"checks": "cancelled", "browser": "cancelled", "race": "cancelled"},
                 event="pull_request", created=at(0), conclusion="cancelled",
                 timestamp="2026-11-12T06:13:20Z")
    return s


def with_gitea_container(s):
    s.gitea_jobs[393].append(dict(s.gitea_jobs[393][0], id=9999, name="container"))
    return s


def unpaired(s):
    s.add_github(fake_sha(1), dict(GREEN, container="success"), created=at(5))
    return s


def dispatch_only(s):
    s.add_github(fake_sha(2), GREEN, created=at(5))
    s.add_gitea(fake_sha(2), GREEN, event="workflow_dispatch", created=at(6))
    return s


def dispatch_does_not_stand_in(s):
    s.add_github(fake_sha(3), GREEN, created=at(5))
    s.add_gitea(fake_sha(3), GREEN, event="workflow_dispatch", created=at(6))
    s.add_gitea(fake_sha(3), {"checks": "success", "browser": "failure", "race": "failure"}, created=at(7))
    return s


def pre_shadow_labels(s):
    # Shadow job names, but the hosted label a .github/workflows run asks for.
    s.add_github(fake_sha(4), dict(GREEN, container="success"), created=at(5))
    s.add_gitea(fake_sha(4), GREEN, labels=["ubuntu-24.04"], created=at(6))
    return s


def pre_shadow_jobs(s):
    s.add_github(fake_sha(5), GREEN, created=at(5))
    s.add_gitea(fake_sha(5), dict(GREEN, beta_deploy="skipped"), labels=["ar-helm-canary-short"], created=at(6))
    return s


def other_workflow(s):
    s.add_gitea(SHA_7B6, {"checks": "failure"}, path="generic.yml", created=at(0))
    return s


def hidden_github(s):
    s.add_github(fake_sha(6), dict(GREEN, container="success"), created="2026-11-11T22:00:00Z",
                 timestamp="2026-11-11T21:59:00Z", hidden=True)
    s.add_gitea(fake_sha(6), GREEN, created=at(5))
    return s


def streak_sequence(s):
    # Oldest to newest: agree, disagree, agree, unpaired, agree, incomplete.
    s.add_pair(10, 10)
    s.add_pair(11, 20, gitea_results={"checks": "failure", "browser": "success", "race": "success"})
    s.add_pair(12, 30)
    s.add_github(fake_sha(13), dict(GREEN, container="success"), created=at(40))
    s.add_pair(14, 50)
    s.add_gitea(fake_sha(15), GREEN, created=at(60), status="in_progress", conclusion="")
    s.add_github(fake_sha(15), dict(GREEN, container="success"), created=at(60))
    return s


def twenty(s, slow=0):
    for n in range(20):
        sha = s.add_pair(100 + n, 10 * n)
        if slow:
            run = [r for r in s.gitea_runs if r["head_sha"] == sha][0]
            s.gitea_jobs[run["id"]] = fx.jobs_for(run["created_at"], GREEN, slow=slow)
    return s


def passing(s):
    twenty(s)
    s.secrets_empty()
    return s


def slow_gitea(s):
    twenty(s, slow=400)
    s.secrets_empty()
    return s


def secret_present(s):
    passing(s)
    s.secrets[fx.SECRET_PATHS[0]] = {"body": [{"name": "HELM_DEPLOY", "created_at": at(0)}],
                                     "headers": {"X-Total-Count": "1"}}
    return s


def no_queue_time(s):
    s.gitea_run(393)["created_at"] = fx.ZERO
    return s


def gitea_page_gap(s):
    s.drop_gitea_from_listing.add(391)
    return s


def github_page_short(s):
    s.github_total_delta = 1
    return s


def transient_then_ok(s):
    path = fx.GITEA_JOBS.format(id=393)
    good = s.replay()["gitea"][path]
    s.overrides["gitea"][path] = [{"status": 502, "body": {"message": "bad gateway"}}, good]
    return s


def transient_forever(s):
    s.overrides["gitea"][fx.GITEA_JOBS.format(id=393)] = [{"status": 503, "body": {}}] * 4
    return s


def unexpected_404(s):
    s.overrides["github"][fx.GITHUB_JOBS.format(id=43166616500)] = {"status": 404, "body": {"message": "Not Found"}}
    return s


def token_in_response(s):
    s.gitea_run(393)["head_branch"] = "leak-" + TOKEN
    return s


ANNOTATION_498 = {SHA_498: {"note": "setup-go installed Go 1.26.0; fixed in 7b6ec06",
                            "evidence": "Gitea run 391"}}


def annotated_disagreement(s):
    gitea_browser_fails(s)
    return s


def annotation_kept(result):
    entry = commit(result["ledger"], SHA_498)
    expect(entry["annotation"]["note"].startswith("setup-go"), "annotation missing from ledger")
    expect("setup-go installed Go 1.26.0" in result["summary"], "annotation missing from summary")


def timing_exact(result):
    entry = commit(result["ledger"], SHA_7B6)["timing"]
    expect(entry["gitea"]["wall_s"] == 650, f"gitea wall {entry['gitea']['wall_s']}, want 650 (06:13:23 to 06:24:13)")
    expect(entry["github"]["wall_s"] == 871, f"github wall {entry['github']['wall_s']}, want 871 without container")
    expect(entry["gitea"]["jobs"]["race"]["queue_s"] == 17, "gitea race queue must start when browser ends")
    expect(entry["github"]["jobs"]["race"]["queue_s"] == 2, "github race queue must start when browser ends")
    expect(entry["gitea"]["jobs"]["checks"]["queue_s"] == 18, "gitea checks queue must start at run creation")


def secrets_not_checked(result):
    secrets = result["ledger"]["secrets"]
    expect(secrets["claim"] == "not checked", f"claim {secrets['claim']}")
    scope = secrets["scopes"]["repo_secrets"]
    expect(scope["state"] == "not checked" and scope["status"] == 403, f"repo secrets {scope}")
    expect("owner" in scope["reason"], "the reason must carry Gitea's message")


def merge_ref(result):
    entry = commit(result["ledger"], SHA_7B6)
    expect(entry["basis"] == "github-merge-ref", f"basis {entry['basis']}")
    expect(result["ledger"]["streak"]["merge_ref_basis"] == 1, "streak must count merge-ref commits")
    expect("github-merge-ref" in result["summary"], "summary must show the basis")


def container_not_compared(result):
    entry = commit(result["ledger"], SHA_7B6)
    expect("container" not in entry["jobs"], "container must not be compared")
    expect([n["job"] for n in entry["not_compared"]] == ["container"], f"not compared {entry['not_compared']}")


CASES = [
    # name, scenario edit, extra args, annotations, check
    ("recorded-agree-merge-ref", base, [], None, both(is_class(SHA_7B6, "agree"), merge_ref)),
    ("recorded-container-not-compared", base, [], None, container_not_compared),
    ("recorded-running-is-incomplete", base, [], None, is_class(SHA_912, "incomplete", "gitea-running")),
    ("recorded-cancelled-is-excluded", base, [], None, is_class(SHA_498, "excluded", "cancelled")),
    ("recorded-streak-one", base, [], None, streak(1)),
    ("recorded-secrets-not-checked", base, [], None, both(secrets_not_checked, gate_unmet("secrets_met"))),
    ("recorded-gate-exits-1", base, ["--gate"], None, both(exits(1), gate_unmet("streak_met"))),
    ("recorded-timing-includes-queue", base, [], None, timing_exact),
    ("unpaired-neither-counts-nor-resets", unpaired, [], None,
     both(is_class(fake_sha(1), "unpaired", "gitea-absent"), streak(1))),
    ("cancelled-beside-completed-ignored", cancelled_beside, [], None, is_class(SHA_7B6, "agree")),
    ("dispatch-only-excluded", dispatch_only, [], None, is_class(fake_sha(2), "excluded", "dispatch-only")),
    ("dispatch-never-stands-in", dispatch_does_not_stand_in, [], None,
     is_class(fake_sha(3), "disagree", "job-conclusion")),
    ("pre-shadow-labels-excluded", pre_shadow_labels, [], None, is_class(fake_sha(4), "excluded", "pre-shadow")),
    ("pre-shadow-jobs-excluded", pre_shadow_jobs, [], None, is_class(fake_sha(5), "excluded", "pre-shadow")),
    ("other-workflow-ignored", other_workflow, [], None, is_class(SHA_7B6, "agree")),
    ("gitea-rerun-disagrees", rerun_gitea, [], None,
     both(is_class(SHA_7B6, "disagree", "rerun"), streak(0, SHA_7B6))),
    ("github-rerun-disagrees", rerun_github, [], None, both(
        is_class(SHA_7B6, "disagree", "rerun"),
        lambda r: expect(commit(r["ledger"], SHA_7B6)["github"]["runs"][0]["previous_attempts"]
                         == [{"attempt": 1, "conclusion": "failure"}], "earlier attempt not recorded"))),
    ("side-inconsistent", inconsistent, [], None, is_class(SHA_7B6, "disagree", "side-inconsistent")),
    ("same-event-basis", github_push_too, [], None,
     lambda r: expect(commit(r["ledger"], SHA_7B6)["basis"] == "same-event", "push pair is same-event")),
    ("container-compared-when-present", with_gitea_container, [], None,
     lambda r: expect("container" in commit(r["ledger"], SHA_7B6)["jobs"], "container must be compared")),
    ("job-missing", job_missing, [], None, is_class(SHA_7B6, "disagree", "job-missing")),
    ("job-conclusion-differs", gitea_browser_fails, [], None,
     both(is_class(SHA_7B6, "disagree", "job-conclusion"), streak(0, SHA_7B6))),
    ("both-failed-is-agreement-not-green", both_fail, [], None, both(
        is_class(SHA_7B6, "agree"), lambda r: expect(r["ledger"]["streak"]["green"] == 0, "not green"))),
    ("github-run-before-since-found-by-sha", hidden_github, [], None, is_class(fake_sha(6), "agree")),
    ("streak-rule", streak_sequence, [], None, both(
        streak(2, fake_sha(11)), is_class(fake_sha(13), "unpaired"), is_class(fake_sha(15), "incomplete"))),
    ("annotation-visible-class-unchanged", base, [], ANNOTATION_498,
     both(annotation_kept, is_class(SHA_498, "excluded", "cancelled"))),
    ("annotation-does-not-excuse", annotated_disagreement, [],
     {SHA_7B6: {"note": "known cause", "evidence": "x"}},
     both(is_class(SHA_7B6, "disagree"), streak(0, SHA_7B6))),
    ("annotation-unused-listed", base, [], {fake_sha(77): {"note": "n", "evidence": "e"}},
     lambda r: expect(r["ledger"]["unused_annotations"] == [fake_sha(77)], "unused annotation")),
    ("annotation-short-sha-rejected", base, [], {"49816fd": {"note": "n", "evidence": "e"}},
     exits(2, "full 40-character SHA", ledger_written=False)),
    ("timing-sample-without-creation-dropped", no_queue_time, [], None, lambda r: expect(
        r["ledger"]["timing"]["samples"] == 0 and r["ledger"]["timing"]["dropped"] == 1, "sample kept")),
    ("timing-no-samples-unmet", rerun_gitea, [], None, gate_unmet("timing_met")),
    ("gate-met", passing, ["--gate"], None, both(exits(0), streak(21))),
    ("gate-slow-gitea", slow_gitea, ["--gate"], None, both(exits(1), gate_unmet("timing_met"))),
    ("gate-secret-present", secret_present, ["--gate"], None, both(exits(1), lambda r: expect(
        r["ledger"]["secrets"]["claim"] is False
        and r["ledger"]["secrets"]["scopes"]["repo_secrets"]["names"] == ["HELM_DEPLOY"], "secret not reported"))),
    ("gitea-page-gap-stops", gitea_page_gap, ["--page-size", "2"], None,
     exits(2, "run_number", ledger_written=False)),
    ("github-page-short-stops", github_page_short, [], None, exits(2, "total_count", ledger_written=False)),
    ("transient-retried", transient_then_ok, [], None, both(exits(0), is_class(SHA_7B6, "agree"))),
    ("transient-persistent-stops", transient_forever, [], None, exits(2, "503", ledger_written=False)),
    ("unexpected-status-stops", unexpected_404, [], None, exits(2, "404", ledger_written=False)),
    ("token-in-response-refused", token_in_response, [], None, exits(2, "token", ledger_written=False)),
    ("missing-token-stops", base, ["--token-file", "/nonexistent/helm-token"], None,
     exits(2, "token", ledger_written=False)),
    ("empty-token-stops", base, ["--empty-token"], None, exits(2, "token", ledger_written=False)),
    ("since-required", base, ["--no-since"], None, exits(2, "--since")),
]


def run_case(directory, name, edit, extra, annotations):
    scenario = edit(Scenario())
    page_size = int(extra[extra.index("--page-size") + 1]) if "--page-size" in extra else 4
    replay = Path(directory) / f"{name}.replay.json"
    replay.write_text(json.dumps(scenario.replay(page_size)))
    token = Path(directory) / "token"
    token.write_text("" if "--empty-token" in extra else TOKEN + "\n")
    notes = Path(directory) / f"{name}.annotations.json"
    notes.write_text(json.dumps(annotations or {}))
    ledger = Path(directory) / f"{name}.ledger.json"
    args = [sys.executable, str(TOOL), "--replay", str(replay), "--token-file", str(token),
            "--annotations", str(notes), "--ledger", str(ledger), "--page-size", str(page_size)]
    if "--no-since" not in extra:
        args += ["--since", fx.SINCE]
    skip = {"--empty-token", "--no-since", "--page-size", str(page_size)}
    args += [a for a in extra if a not in skip]
    result = subprocess.run(args, capture_output=True, text=True, timeout=30)
    for stream in (result.stdout, result.stderr, ledger.read_text() if ledger.exists() else ""):
        if TOKEN in stream:
            raise AssertionError("the token appeared in output")
    return {"exit": result.returncode, "summary": result.stdout, "stderr": result.stderr,
            "ledger": json.loads(ledger.read_text()) if ledger.exists() else None, "path": ledger}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact", type=Path, required=True)
    args = parser.parse_args()
    results = []
    with tempfile.TemporaryDirectory(prefix="helm-soak-proofs-") as directory:
        for name, edit, extra, annotations, check in CASES:
            try:
                result = run_case(directory, name, edit, extra, annotations)
                if result["exit"] == 0 or "--gate" in extra or result["exit"] == 2:
                    check(result)
                else:
                    raise AssertionError(f"exit {result['exit']}: {result['stderr'][-300:]}")
                outcome = {"case": name, "passed": True, "reason": ""}
            except Exception as error:  # every failure is reported, none stops the others
                outcome = {"case": name, "passed": False, "reason": f"{type(error).__name__}: {error}"[:300]}
            results.append(outcome)
        # Determinism: the same evidence gives byte-identical ledgers.
        runs = [run_case(directory, f"determinism-{x}", base, [], None)["path"] for x in "ab"]
        same = all(p.exists() for p in runs) and runs[0].read_bytes() == runs[1].read_bytes()
        results.append({"case": "ledger-deterministic", "passed": same,
                        "reason": "" if same else "two replays gave different or no ledgers"})
    artifact = {"checks": results, "passed": all(x["passed"] for x in results), "network_requests": 0}
    args.artifact.parent.mkdir(parents=True, exist_ok=True)
    args.artifact.write_text(json.dumps(artifact, indent=2) + "\n")
    for result in results:
        if not result["passed"]:
            print(f"FAILED: {result['case']}: {result['reason']}", file=sys.stderr)
    print(f"gitea-shadow-soak proofs: {sum(x['passed'] for x in results)}/{len(results)} passed")
    return 0 if artifact["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
