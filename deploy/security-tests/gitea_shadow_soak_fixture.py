"""Replay fixtures for the Gitea shadow soak proofs.

The base scenario is fixtures/gitea-shadow-soak-recorded.json: trimmed real
responses recorded read-only on 2026-11-12 (Gitea runs 389 to 394, GitHub runs
43166299908 to 43168423982, and Gitea's 403 answers for secrets and
variables). A Scenario holds runs and jobs as lists, lets a proof change them,
and renders the request-path map that `gitea-shadow-soak.py --replay` reads,
paging lists the way the APIs do. No network is used.
"""
import copy
import json
from pathlib import Path

RECORDED = Path(__file__).resolve().parent / "fixtures/gitea-shadow-soak-recorded.json"
SINCE = "2026-11-12T06:00:00Z"
REPO = "repos/KanterLabs/helm"
GITEA_RUNS = REPO + "/actions/runs?limit={size}&page={page}"
GITEA_JOBS = REPO + "/actions/runs/{id}/jobs?limit=50&page=1"
GITHUB_RUNS = REPO + "/actions/workflows/ci.yml/runs?created=%3E%3D{since}&per_page={size}&page={page}"
GITHUB_SHA = REPO + "/actions/workflows/ci.yml/runs?head_sha={sha}&per_page=100&page=1"
GITHUB_JOBS = REPO + "/actions/runs/{id}/jobs?per_page=100&page=1"
GITHUB_ATTEMPT = REPO + "/actions/runs/{id}/attempts/{n}"
SECRET_PATHS = [REPO + "/actions/secrets?limit=50&page=1", REPO + "/actions/variables?limit=50&page=1",
                "orgs/KanterLabs/actions/secrets?limit=50&page=1",
                "orgs/KanterLabs/actions/variables?limit=50&page=1"]
ZERO = "1970-01-01T00:00:00Z"
SHA_7B6 = "7b6ec06dfa7696687368e1154c9cc505c157c34d"
SHA_498 = "49816fd973f9e054cc1e9c994893fbc07f92524e"
SHA_912 = "912116c7749700f847579c311bc9eeadbebc23db"
SHA_9A6 = "9a6dbe8c"  # prefix of a GitHub-only commit in the recording


def at(minute, second=0):
    """A timestamp on 2026-11-12 at 07:00 plus the given minutes."""
    hour, minute = divmod(7 * 60 + minute, 60)
    return f"2026-11-12T{hour:02d}:{minute:02d}:{second:02d}Z"


def plus(stamp, seconds):
    from datetime import datetime, timedelta, timezone
    base = datetime.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
    return (base + timedelta(seconds=seconds)).strftime("%Y-%m-%dT%H:%M:%SZ")


def fake_sha(n):
    return f"{n:040x}"


def jobs_for(created, results, labels=None, github=False, slow=0):
    """Completed jobs with realistic timing: checks 160 s, browser 600 s, race after browser."""
    jobs, times = [], {}
    plan = {"checks": (10, 160, None), "browser": (10, 600 + slow, None),
            "race": (2, 3, "browser"), "container": (2, 150, "checks")}
    for index, (name, conclusion) in enumerate(results.items()):
        queue, length, needs = plan.get(name, (1, 1, None))
        ready = times[needs] if needs in times else created
        start = plus(ready, queue)
        end = plus(start, length)
        times[name] = end
        job = {"id": 10_000 + index, "name": name, "run_attempt": 1, "status": "completed",
               "conclusion": conclusion, "created_at": ready if github else created,
               "started_at": start, "completed_at": end}
        job["labels"] = labels or (["ubuntu-24.04"] if github else
                                   ["ar-helm-canary-heavy" if name == "browser" else "ar-helm-canary-short"])
        jobs.append(job)
    return jobs


class Scenario:
    def __init__(self):
        recorded = json.loads(RECORDED.read_text())
        gitea, github = recorded["gitea"], recorded["github"]
        self.extras = recorded["extras"]
        self.gitea_runs = copy.deepcopy(gitea[GITEA_RUNS.format(size=4, page=1)]["body"]["workflow_runs"])
        self.gitea_total = 61
        self.gitea_jobs = {r["id"]: copy.deepcopy(gitea[GITEA_JOBS.format(id=r["id"])]["body"]["jobs"])
                           for r in self.gitea_runs if r["created_at"] >= SINCE}
        listing = github[GITHUB_RUNS.format(since=SINCE, size=4, page=1)]["body"]["workflow_runs"]
        self.github_runs = copy.deepcopy(listing)
        self.github_jobs = {r["id"]: copy.deepcopy(github[GITHUB_JOBS.format(id=r["id"])]["body"]["jobs"])
                            for r in self.github_runs}
        self.github_hidden = []  # reachable only by head_sha lookup
        self.secrets = {p: copy.deepcopy(gitea[p]) for p in SECRET_PATHS}
        self.overrides = {"gitea": {}, "github": {}}
        self.drop_gitea_from_listing = set()
        self.github_total_delta = 0
        self.next_gitea = 400
        self.next_github = 50_000_000_000

    # Lookups and edits.
    def gitea_run(self, run_id):
        return next(r for r in self.gitea_runs if r["id"] == run_id)

    def github_run(self, run_id):
        return next(r for r in self.github_runs + self.github_hidden if r["id"] == run_id)

    def add_gitea(self, sha, results, event="push", branch="beta", created=None, labels=None,
                  path="ci.yml", conclusion=None, status="completed", slow=0):
        created = created or at(len(self.gitea_runs))
        run_id, number = self.next_gitea, 62 + self.next_gitea - 400
        self.next_gitea += 1
        if conclusion is None:
            conclusion = "success" if all(v == "success" for v in results.values()) else "failure"
        run = {"id": run_id, "run_number": number, "path": f"{path}@refs/heads/{branch}", "event": event,
               "run_attempt": 1, "head_sha": sha, "head_branch": branch, "status": status,
               "conclusion": conclusion, "created_at": created, "started_at": plus(created, 10),
               "completed_at": plus(created, 900), "previous_attempt_url": None,
               "html_url": f"https://gitea.example/runs/{run_id}"}
        self.gitea_runs.append(run)
        self.gitea_jobs[run_id] = jobs_for(created, results, labels, slow=slow)
        return run

    def add_github(self, sha, results, event="push", branch="beta", created=None, timestamp=None,
                   hidden=False, conclusion=None):
        created = created or at(len(self.github_runs) + len(self.github_hidden))
        run_id = self.next_github
        self.next_github += 1
        if conclusion is None:
            conclusion = "success" if all(v in ("success", "skipped") for v in results.values()) else "failure"
        run = {"id": run_id, "run_number": run_id % 1000, "name": "Helm CI and deploy",
               "path": ".github/workflows/ci.yml", "event": event, "run_attempt": 1, "head_sha": sha,
               "head_branch": branch, "status": "completed", "conclusion": conclusion,
               "created_at": created, "run_started_at": created, "updated_at": plus(created, 900),
               "html_url": f"https://github.example/runs/{run_id}", "previous_attempt_url": None,
               "head_commit": {"timestamp": timestamp or plus(created, -5)}}
        (self.github_hidden if hidden else self.github_runs).append(run)
        self.github_jobs[run_id] = jobs_for(created, results, github=True)
        return run

    def add_pair(self, n, minute, gitea_results=None, github_results=None):
        sha = fake_sha(n)
        gitea_results = gitea_results or {"checks": "success", "browser": "success", "race": "success"}
        github_results = github_results or {"checks": "success", "browser": "success", "race": "success",
                                            "container": "success", "beta_deploy": "skipped",
                                            "deploy": "skipped"}
        self.add_github(sha, github_results, created=at(minute))
        self.add_gitea(sha, gitea_results, created=at(minute, 20))
        return sha

    def set_job(self, side, run_id, name, **fields):
        jobs = (self.gitea_jobs if side == "gitea" else self.github_jobs)[run_id]
        next(j for j in jobs if j["name"] == name).update(fields)

    def secrets_empty(self):
        for path in SECRET_PATHS:
            self.secrets[path] = {"body": [], "headers": {"X-Total-Count": "0"}}

    # Rendering.
    def replay(self, page_size=4):
        gitea, github = {}, {}
        runs = sorted(self.gitea_runs, key=lambda r: -r["id"])
        listed = [r for r in runs if r["id"] not in self.drop_gitea_from_listing]
        pages = [runs[i:i + page_size] for i in range(0, len(runs), page_size)] or [[]]
        for number, page in enumerate(pages, 1):
            shown = [r for r in page if r in listed]
            gitea[GITEA_RUNS.format(size=page_size, page=number)] = {
                "body": {"workflow_runs": shown, "total_count": self.gitea_total},
                "headers": {"X-Total-Count": str(self.gitea_total)}}
        for run_id, jobs in self.gitea_jobs.items():
            gitea[GITEA_JOBS.format(id=run_id)] = {"body": {"jobs": jobs, "total_count": len(jobs)},
                                                   "headers": {"X-Total-Count": str(len(jobs))}}
        gitea.update(self.secrets)
        ordered = sorted(self.github_runs, key=lambda r: r["created_at"], reverse=True)
        total = len(ordered) + self.github_total_delta
        gh_pages = [ordered[i:i + page_size] for i in range(0, len(ordered), page_size)] or [[]]
        for number, page in enumerate(gh_pages + [[]], 1):
            github[GITHUB_RUNS.format(since=SINCE, size=page_size, page=number)] = {
                "body": {"total_count": total, "workflow_runs": page}}
        everything = self.github_runs + self.github_hidden
        for sha in {r["head_sha"] for r in self.gitea_runs} | {r["head_sha"] for r in everything}:
            matches = [r for r in everything if r["head_sha"] == sha]
            github[GITHUB_SHA.format(sha=sha)] = {"body": {"total_count": len(matches), "workflow_runs": matches}}
        for run_id, jobs in self.github_jobs.items():
            github[GITHUB_JOBS.format(id=run_id)] = {"body": {"total_count": len(jobs), "jobs": jobs}}
        gitea.update(self.overrides["gitea"])
        github.update(self.overrides["github"])
        return {"gitea": gitea, "github": github}
