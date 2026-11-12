#!/usr/bin/env python3
"""Evidence for Helm's shadow soak gate (card H4): GitHub CI and Gitea shadow CI, per commit.

Reads GitHub runs of .github/workflows/ci.yml through the authenticated `gh`
CLI and Gitea shadow runs with a read-only token, pairs them per full commit
SHA and job, and writes a deterministic JSON ledger plus a summary table.
Read-only: nothing is written to either service.

Exit status: 0 on success; 1 with --gate when the gate is not met; 2 when the
evidence could not be collected completely (nothing is written then).

Contract: docs/GITEA_CI_SOAK_FAILURES.md. Guide: docs/GITEA_CI_SHADOW.md.
Proofs: deploy/security-tests/test_gitea_shadow_soak.py.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import shadow_soak_ledger as ledger  # noqa: E402
import shadow_soak_sources as sources  # noqa: E402

TOKEN_FILE = Path("~/.config/actionrunners/gitea-token-helm").expanduser()
ANNOTATIONS = Path(__file__).resolve().parent / "gitea-shadow-soak-annotations.json"
SHA = re.compile(r"[0-9a-f]{40}")
CELL = {"success": "ok", "failure": "FAIL", "cancelled": "cancel", "skipped": "skip", None: "-"}


class UsageError(Exception):
    pass


def read_token(path):
    try:
        token = Path(path).read_text().strip()
    except OSError:
        token = ""
    if not token:
        raise UsageError(f"the Gitea token file {path} is missing or empty; refusing to read Gitea anonymously")
    if len(token) < 20 or any(c.isspace() for c in token):
        # A short value would also make the leak scan below match ordinary words.
        raise UsageError(f"the Gitea token file {path} does not hold a single token")
    return token


def read_annotations(path):
    if not Path(path).exists():
        return {}
    data = json.loads(Path(path).read_text())
    for sha, note in data.items():
        if not SHA.fullmatch(sha):
            raise UsageError(f"annotation key {sha!r} must be a full 40-character SHA in lower case")
        if not isinstance(note, dict) or not isinstance(note.get("note"), str):
            raise UsageError(f"annotation for {sha} needs a \"note\" string")
    return data


def resolve_since(value, client):
    if re.fullmatch(r"[0-9a-f]{7,40}", value):
        return sources.github_commit_time(client, value)
    try:
        stamp = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise UsageError(f"--since {value!r} is neither a date nor a commit") from error
    if stamp.tzinfo is None:
        stamp = stamp.replace(tzinfo=timezone.utc)
    return int(stamp.timestamp())


def cell(value):
    return CELL.get(value, {"in_progress": "run", "queued": "wait", "requested": "wait",
                            "waiting": "wait"}.get(value, str(value)[:6]))


def seconds(value):
    return "-" if value is None else f"{value:.0f}"


def summary(data):
    inputs = data["inputs"]
    out = [f"Gitea shadow soak since {inputs['since']}, branches: {', '.join(inputs['branches']) or 'all'}",
           "Job cells are Gitea/GitHub; run ids in parentheses were ignored (see below) and never compared.",
           "Classes and rules: docs/GITEA_CI_SOAK_FAILURES.md.", ""]
    jobs = [j for j in ledger.JOBS if any(j in c["jobs"] for c in data["commits"])]
    head = f"{'Commit':<9} {'Class':<11} {'Basis':<17} " + "".join(f"{j:<13}" for j in jobs)
    out.append(head + f"{'Gitea':<8} {'GitHub':<30} Reasons")
    for c in data["commits"]:
        cells = "".join(f"{cell(c['jobs'][j]['gitea']) + '/' + cell(c['jobs'][j]['github']) if j in c['jobs'] else 'n/c':<13}"
                        for j in jobs)
        gitea = ",".join(f"#{r['id']}" for r in c["gitea"]["runs"]) or \
            ",".join(f"(#{i['id']})" for i in c["gitea"]["ignored"]) or "-"
        github = ",".join(f"{r['id']} {'pr' if r['event'] == 'pull_request' else r['event']}"
                          for r in c["github"]["runs"]) or \
            ",".join(f"({i['id']})" for i in c["github"]["ignored"]) or "-"
        reasons = ", ".join(c["reasons"])
        if c["annotation"]:
            reasons += (" " if reasons else "") + "[annotated]"
        out.append(f"{c['sha'][:8]:<9} {c['class']:<11} {c['basis'] or '-':<17} {cells}{gitea:<8} {github:<30} {reasons}")
    not_compared = sorted({n["job"] for c in data["commits"] for n in c["not_compared"]})
    if not_compared:
        out.append(f"Not compared: {', '.join(not_compared)} (absent from the Gitea shadow workflow); "
                   "never counted as agreement.")
    ignored = [f"{c['sha'][:8]} {side} #{i['id']} {i['reason']}" for c in data["commits"]
               for side in ("gitea", "github") for i in c[side]["ignored"]]
    if ignored:
        out.append("Ignored runs: " + "; ".join(ignored))
    counts = data["counts"]
    out += ["", "Counts: " + ", ".join(f"{k} {v}" for k, v in counts.items())]
    notes = [c for c in data["commits"] if c["annotation"]]
    if notes or data["unused_annotations"]:
        out += ["", "Annotations (shown, never change a class or the streak):"]
        out += [f"  {c['sha'][:8]} {c['class']}: {c['annotation']['note']}"
                + (f" ({c['annotation']['evidence']})" if c["annotation"].get("evidence") else "") for c in notes]
        out += [f"  unused (no such commit in range): {sha}" for sha in data["unused_annotations"]]
    run = data["streak"]
    ended = f"{run['ended_by']['sha'][:8]} ({', '.join(run['ended_by']['reasons'])})" if run["ended_by"] \
        else "nothing (start of range)"
    out += ["", f"Streak: {run['count']} of {run['required']} required; ended by {ended}.",
            f"  {run['green']} of them green; {run['merge_ref_basis']} rest on a GitHub pull-request (merge ref) "
            "result against Gitea's pushed head.", f"  Rule: {run['rule']}"]
    timing = data["timing"]
    wall = timing["run_wall"]
    out += ["", f"Timing over {timing['samples']} agreeing commits ({timing['dropped']} dropped for missing "
            f"times); {data['rules']['percentile']}",
            f"  {'':<18}{'GitHub p50/p95 s':<20}{'Gitea p50/p95 s':<20}",
            f"  {'run wall clock':<18}{seconds(wall['github']['p50']) + '/' + seconds(wall['github']['p95']):<20}"
            f"{seconds(wall['gitea']['p50']) + '/' + seconds(wall['gitea']['p95']):<20}"
            f"limit {seconds(timing['limit_s'])} s: {'met' if timing['met'] else 'NOT met'}"]
    for name, sides in timing["jobs"].items():
        for metric in ("queue_s", "run_s"):
            g, h = sides["gitea"][metric], sides["github"][metric]
            out.append(f"  {name + ' ' + metric[:-2]:<18}{seconds(h['p50']) + '/' + seconds(h['p95']):<20}"
                       f"{seconds(g['p50']) + '/' + seconds(g['p95']):<20}")
    claim = data["secrets"]["claim"]
    out += ["", f"No secret or variable in Gitea: {claim if isinstance(claim, str) else str(claim).lower()}"]
    for scope, item in sorted(data["secrets"]["scopes"].items()):
        detail = f"HTTP {item['status']}: {item['reason']}" if item["state"] == "not checked" else \
            f"{item['count']}" + (f" ({', '.join(item['names'])})" if item["names"] else "")
        out.append(f"  {scope:<16}{item['state']:<13}{detail}")
    gate = data["gate"]
    out += ["", f"Gate: {'MET' if gate['met'] else 'NOT MET'}"] + [f"  - {r}" for r in gate["reasons"]]
    return "\n".join(out) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--since", required=True, help="ISO date or time (UTC if no offset), or a commit SHA")
    parser.add_argument("--branch", action="append", default=[], help="limit to runs on this branch (repeatable)")
    parser.add_argument("--ledger", type=Path, help="write the JSON ledger here")
    parser.add_argument("--annotations", type=Path, default=ANNOTATIONS)
    parser.add_argument("--gate", action="store_true", help="exit 1 unless the H4 gate is met")
    parser.add_argument("--page-size", type=int, default=50, choices=range(1, 51), metavar="1..50")
    parser.add_argument("--token-file", type=Path, default=TOKEN_FILE)
    parser.add_argument("--replay", type=Path, help="recorded responses instead of the network (tests)")
    args = parser.parse_args(argv)
    token = ""
    try:
        token = read_token(args.token_file)
        annotations = read_annotations(args.annotations)
        transport = sources.ReplayTransport(json.loads(args.replay.read_text())) if args.replay \
            else sources.LiveTransport(token)
        client = sources.Client(transport)
        since = resolve_since(args.since, client)
        github, gitea = sources.collect(client, since, args.page_size, set(args.branch))
        inputs = {"since": sources.format_time(since), "branches": sorted(args.branch),
                  "github": "KanterLabs/helm .github/workflows/ci.yml",
                  "gitea": "KanterLabs/helm shadow runs of .gitea/workflows/ci.yml"}
        data = ledger.build(github, gitea, sources.secrets(client), annotations, inputs)
        text = json.dumps(data, indent=2, sort_keys=True) + "\n"
        report = summary(data)
        if token in text or token in report:
            raise UsageError("the Gitea token appeared in the collected evidence; refusing to write it")
    except (sources.CollectionError, UsageError, ValueError, KeyError, TypeError) as error:
        message = str(error).replace(token, "[redacted]") if token else str(error)
        print(f"gitea-shadow-soak: {type(error).__name__}: {message}", file=sys.stderr)
        return 2
    if args.ledger:
        args.ledger.parent.mkdir(parents=True, exist_ok=True)
        temporary = args.ledger.with_name(args.ledger.name + ".tmp")
        temporary.write_text(text)
        os.replace(temporary, args.ledger)
    sys.stdout.write(report)
    return 1 if args.gate and not data["gate"]["met"] else 0


if __name__ == "__main__":
    sys.exit(main())
