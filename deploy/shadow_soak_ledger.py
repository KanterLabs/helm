"""Pair GitHub and Gitea shadow CI results per commit and evaluate the H4 gate.

Pure functions over normalized runs (shadow_soak_sources.normalize). The
definitions, classes and streak rule are those of
docs/GITEA_CI_SOAK_FAILURES.md; every rule is also written into the ledger so
a reader does not need this file to interpret it.
"""
from __future__ import annotations

import math

JOBS = ("checks", "browser", "race", "container")
NEEDS = {"race": ("browser",), "container": ("checks",)}
SHADOW_LABELS = {"ar-helm-canary-short", "ar-helm-canary-heavy", "homelab", "homelab-heavy"}
EVENTS = ("push", "pull_request")
STREAK_REQUIRED = 20
TIMING_FACTOR = 1.2
RULES = {
    "agreement": "Every compared job has the same conclusion on both sides; both failing is agreement "
                 "and is not green.",
    "streak": "Newest to oldest: agree adds one, disagree ends the streak; unpaired, incomplete and "
              "excluded neither count nor reset it.",
    "order": "By the latest run creation time on either side, then SHA; the commit's own date is not used.",
    "timing": "Wall clock is the last compared job's completion minus run creation on both sides, so it "
              "includes queue time and the same jobs. Per-job queue starts at run creation or when the "
              "needed jobs end. Samples are agreeing commits, one first-attempt run per side.",
    "percentile": "Nearest rank; with fewer than 20 samples p95 is the slowest sample.",
    "gate": f"At least {STREAK_REQUIRED} in the current streak; Gitea run wall-clock p95 at most "
            f"{TIMING_FACTOR} times GitHub's with at least one sample; no secret or variable in the Gitea "
            "repository or organization, all four read.",
    "rerun": "Any run attempt above 1 makes the commit disagree: Gitea keeps no earlier attempt.",
}


def gitea_kind(run):
    """shadow, pre-shadow (a .github/workflows run Gitea started) or no-jobs."""
    if not run["jobs"]:
        return "no-jobs"
    names = {j["name"] for j in run["jobs"]}
    labelled = all(j["labels"] and set(j["labels"]) <= SHADOW_LABELS for j in run["jobs"])
    return "shadow" if names <= set(JOBS) and labelled else "pre-shadow"


def _order(runs):
    return sorted(runs, key=lambda r: (r["created"] or 0, r["id"]))


def side_state(side, runs):
    candidates, ignored, reasons = [], [], set()
    for run in _order(runs):
        reason = None
        if side == "gitea" and gitea_kind(run) != "shadow":
            reason = gitea_kind(run)
        elif run["event"] not in EVENTS:
            reason = "dispatch-only" if run["event"] == "workflow_dispatch" else f"event-{run['event']}"
        if reason:
            ignored.append({"id": run["id"], "reason": reason})
            reasons.add(reason)
        else:
            candidates.append(run)
    live = [r for r in candidates if not (r["status"] == "completed" and r["conclusion"] == "cancelled")]
    ignored += [{"id": r["id"], "reason": "cancelled"} for r in candidates if r not in live]
    if not runs:
        state = "absent"
    elif not candidates:
        state = "excluded"
    elif not live:
        state, reasons = "excluded", {"cancelled"}
    elif any(r["status"] != "completed" for r in live):
        state = "running"
    else:
        state = "done"
    shown = live or _order(runs)  # what the summary shows; only live runs are ever compared
    return {"state": state, "reasons": sorted(reasons), "live": live, "shown": shown[:1],
            "ignored": sorted(ignored, key=lambda x: x["id"])}


def results(run):
    return {j["name"]: j["conclusion"] or j["status"] for j in run["jobs"]}


def run_view(run):
    return {"id": run["id"], "event": run["event"], "branch": run["branch"], "status": run["status"],
            "conclusion": run["conclusion"], "attempt": run["attempt"], "url": run["url"],
            "previous_attempts": run["previous_attempts"]}


def basis(gitea_runs, github_runs):
    gitea = {r["event"] for r in gitea_runs}
    github = {r["event"] for r in github_runs}
    if gitea & github:
        return "same-event"
    if gitea and github:
        return "github-merge-ref" if gitea == {"push"} else "mixed-events"
    return None


def pick(runs, event=None):
    same = [r for r in runs if r["event"] == event]
    return (same or runs)[0]


def run_timing(run, compared):
    jobs = {j["name"]: j for j in run["jobs"]}
    if run["created"] is None or any(n not in jobs or jobs[n]["started"] is None
                                     or jobs[n]["completed"] is None for n in compared):
        return None
    out = {}
    for name in compared:
        needed = [jobs[n]["completed"] for n in NEEDS.get(name, ()) if n in jobs and jobs[n]["completed"]]
        ready = max(needed) if needed else run["created"]
        job = jobs[name]
        out[name] = {"queue_s": job["started"] - ready, "run_s": job["completed"] - job["started"]}
    wall = max(jobs[n]["completed"] for n in compared) - run["created"]
    return {"run_id": run["id"], "wall_s": wall, "jobs": out}


def classify(sha, github, gitea):
    gh, gt = side_state("github", github), side_state("gitea", gitea)
    gitea_names = {j["name"] for r in gt["live"] for j in r["jobs"]}
    github_names = {j["name"] for r in gh["live"] for j in r["jobs"]}
    compared = [j for j in JOBS if j != "container" or "container" in gitea_names]
    entry = {"sha": sha, "basis": basis(gt["live"], gh["live"]), "reasons": [], "timing": None,
             "github": {"runs": [run_view(r) for r in gh["live"]], "ignored": gh["ignored"]},
             "gitea": {"runs": [run_view(r) for r in gt["live"]], "ignored": gt["ignored"]},
             "not_compared": [{"job": j, "reason": "absent from the Gitea shadow workflow"}
                              for j in JOBS if j not in compared and j in github_names]}
    first = {side: results(s["shown"][0]) if s["shown"] else {} for side, s in (("github", gh), ("gitea", gt))}
    entry["jobs"] = {name: {"github": first["github"].get(name), "gitea": first["gitea"].get(name),
                            "agree": None} for name in compared}
    states = {"github": gh, "gitea": gt}
    if any(s["state"] == "excluded" for s in states.values()):
        entry["class"] = "excluded"
        entry["reasons"] = sorted({r for s in states.values() if s["state"] == "excluded" for r in s["reasons"]})
        return entry
    if any(s["state"] == "absent" for s in states.values()):
        entry["class"] = "unpaired"
        entry["reasons"] = [f"{side}-absent" for side, s in sorted(states.items()) if s["state"] == "absent"]
        return entry
    if any(s["state"] == "running" for s in states.values()):
        entry["class"] = "incomplete"
        entry["reasons"] = [f"{side}-running" for side, s in sorted(states.items()) if s["state"] == "running"]
        return entry
    reasons = set()
    if any(r["attempt"] > 1 or any(j["attempt"] > 1 for j in r["jobs"]) for r in gh["live"] + gt["live"]):
        reasons.add("rerun")
    for side, state in states.items():
        if any({k: v for k, v in results(r).items() if k in compared}
               != {k: v for k, v in first[side].items() if k in compared} for r in state["live"][1:]):
            reasons.add("side-inconsistent")
    for name, cell in entry["jobs"].items():
        if cell["github"] is None or cell["gitea"] is None:
            reasons.add("job-missing")
            cell["agree"] = False
        else:
            cell["agree"] = cell["github"] == cell["gitea"]
            if not cell["agree"]:
                reasons.add("job-conclusion")
    entry["reasons"] = sorted(reasons)
    entry["class"] = "disagree" if reasons else "agree"
    if entry["class"] == "agree":
        gitea_run = gt["live"][0]
        timing = {"gitea": run_timing(gitea_run, compared),
                  "github": run_timing(pick(gh["live"], gitea_run["event"]), compared)}
        entry["timing"] = timing
        entry["green"] = all(c["gitea"] == "success" for c in entry["jobs"].values())
    return entry


def percentile(values, p):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(p / 100 * len(ordered)) - 1)] if ordered else None


def stats(values):
    return {"n": len(values), "p50": percentile(values, 50), "p95": percentile(values, 95)}


def timing_summary(commits):
    sampled = [c for c in commits if c["class"] == "agree"]
    good = [c for c in sampled if c["timing"]["github"] and c["timing"]["gitea"]]
    out = {"samples": len(good), "dropped": len(sampled) - len(good), "run_wall": {}, "jobs": {}}
    for side in ("github", "gitea"):
        out["run_wall"][side] = stats([c["timing"][side]["wall_s"] for c in good])
    for name in JOBS:
        rows = [c for c in good if name in c["timing"]["gitea"]["jobs"]]
        if not rows:
            continue
        out["jobs"][name] = {side: {metric: stats([c["timing"][side]["jobs"][name][metric] for c in rows])
                                    for metric in ("queue_s", "run_s")} for side in ("github", "gitea")}
    hosted, shadow = out["run_wall"]["github"]["p95"], out["run_wall"]["gitea"]["p95"]
    out["limit_s"] = round(hosted * TIMING_FACTOR, 1) if hosted is not None else None
    out["met"] = bool(good) and shadow <= out["limit_s"]
    return out


def streak(commits):
    count, ended_by, members = 0, None, []
    for entry in commits:
        if entry["class"] == "agree":
            count += 1
            members.append(entry)
        elif entry["class"] == "disagree":
            ended_by = {"sha": entry["sha"], "reasons": entry["reasons"]}
            break
    return {"count": count, "required": STREAK_REQUIRED, "ended_by": ended_by, "rule": RULES["streak"],
            "commits": [c["sha"] for c in members], "green": sum(1 for c in members if c.get("green")),
            "merge_ref_basis": sum(1 for c in members if c["basis"] == "github-merge-ref")}


def order_key(github, gitea):
    """When the commit was last compared; its own date only if no run has a creation time."""
    created = [r["created"] for r in github + gitea if r["created"]]
    heads = [r["head_time"] for r in github if r["head_time"]]
    return max(created) if created else (min(heads) if heads else 0)


def build(github_runs, gitea_runs, secrets, annotations, inputs):
    shas = sorted({r["sha"] for r in github_runs} | {r["sha"] for r in gitea_runs})
    rows = []
    for sha in shas:
        github = [r for r in github_runs if r["sha"] == sha]
        gitea = [r for r in gitea_runs if r["sha"] == sha]
        entry = classify(sha, github, gitea)
        entry["order_time"] = order_key(github, gitea)
        entry["annotation"] = annotations.get(sha)
        rows.append(entry)
    rows.sort(key=lambda c: (-c["order_time"], c["sha"]))
    counts = {k: sum(1 for c in rows if c["class"] == k)
              for k in ("agree", "disagree", "unpaired", "incomplete", "excluded")}
    run = streak(rows)
    timing = timing_summary(rows)
    gate = {"streak_met": run["count"] >= STREAK_REQUIRED, "timing_met": timing["met"],
            "secrets_met": secrets["claim"] is True}
    reasons = []
    if not gate["streak_met"]:
        reasons.append(f"streak is {run['count']}, {STREAK_REQUIRED} required")
    if not gate["timing_met"]:
        reasons.append("no timing sample" if not timing["samples"] else
                       f"Gitea wall-clock p95 {timing['run_wall']['gitea']['p95']} s exceeds "
                       f"{timing['limit_s']} s")
    if secrets["claim"] == "not checked":
        reasons.append("secrets and variables not checked: " + "; ".join(
            f"{k} HTTP {v['status']}" for k, v in sorted(secrets["scopes"].items()) if v["state"] == "not checked"))
    elif secrets["claim"] is False:
        reasons.append("secrets or variables present in Gitea")
    gate["met"] = all(gate.values())
    gate["reasons"] = reasons
    return {"schema": "helm-gitea-shadow-soak/1", "inputs": inputs, "rules": RULES, "commits": rows,
            "counts": counts, "streak": run, "timing": timing, "secrets": secrets, "gate": gate,
            "unused_annotations": sorted(set(annotations) - set(shas))}
