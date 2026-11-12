"""Read-only collection of GitHub and Gitea CI runs for the shadow soak ledger.

Contract: docs/GITEA_CI_SOAK_FAILURES.md ("Collection and integrity"). Every
list is checked for completeness, transient errors are retried and then stop
the tool, and the Gitea token is only ever sent as a request header. Nothing
here writes to either service.
"""
from __future__ import annotations

from datetime import datetime, timezone
import json
import re
import subprocess
import time
import urllib.error
import urllib.request

GITEA_API = "https://gitea.home.shanekanterman.dev/api/v1/"
REPO = "repos/KanterLabs/helm"
ORG = "orgs/KanterLabs"
SECRET_SCOPES = {
    "repo_secrets": REPO + "/actions/secrets?limit=50&page=1",
    "repo_variables": REPO + "/actions/variables?limit=50&page=1",
    "org_secrets": ORG + "/actions/secrets?limit=50&page=1",
    "org_variables": ORG + "/actions/variables?limit=50&page=1",
}
RETRIES = 3


class CollectionError(Exception):
    """The evidence could not be collected completely; no ledger is written."""


def parse_time(value):
    """Epoch seconds, or None for a missing or zero time (Gitea's "not yet")."""
    if not value:
        return None
    stamp = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if stamp.tzinfo is None:
        stamp = stamp.replace(tzinfo=timezone.utc)
    return int(stamp.timestamp()) if stamp.year >= 2000 else None


def format_time(epoch):
    return datetime.fromtimestamp(epoch, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class LiveTransport:
    """Gitea over HTTPS with the bot token; GitHub through the authenticated `gh` CLI."""

    def __init__(self, token):
        self._token = token

    def sleep(self, seconds):
        time.sleep(seconds)

    def get(self, side, path):
        return self._gitea(path) if side == "gitea" else self._github(path)

    def _gitea(self, path):
        request = urllib.request.Request(GITEA_API + path, headers={
            "Authorization": "token " + self._token, "Accept": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return response.status, dict(response.headers), json.load(response)
        except urllib.error.HTTPError as error:
            try:
                body = json.loads(error.read().decode() or "{}")
            except ValueError:
                body = {}
            return error.code, {}, body
        except (urllib.error.URLError, TimeoutError, ValueError, OSError):
            return 0, {}, {}

    def _github(self, path):
        try:
            result = subprocess.run(["gh", "api", "-H", "Accept: application/vnd.github+json", path],
                                    capture_output=True, text=True, timeout=60)
        except (OSError, subprocess.TimeoutExpired):
            return 0, {}, {}
        if result.returncode == 0:
            try:
                return 200, {}, json.loads(result.stdout)
            except ValueError:
                return 0, {}, {}
        match = re.search(r"HTTP (\d{3})", result.stderr)
        return (int(match.group(1)) if match else 0), {}, {}


class ReplayTransport:
    """Recorded responses keyed by side and request path; a list replays in order."""

    def __init__(self, data):
        self._data = data

    def sleep(self, seconds):
        pass

    def get(self, side, path):
        entry = self._data.get(side, {}).get(path)
        if isinstance(entry, list):
            if not entry:
                raise CollectionError(f"{side} {path}: recorded responses exhausted")
            entry = entry.pop(0)
        if entry is None:
            raise CollectionError(f"{side} {path}: no recorded response")
        return entry.get("status", 200), entry.get("headers", {}), entry.get("body")


class Client:
    def __init__(self, transport):
        self.transport = transport

    def get(self, side, path, accept=(200,)):
        status = 0
        for attempt in range(RETRIES + 1):
            status, headers, body = self.transport.get(side, path)
            if status in accept:
                return status, headers, body
            if status and status < 500 and status != 429:
                raise CollectionError(f"{side} {path}: unexpected HTTP {status}")
            if attempt < RETRIES:
                self.transport.sleep(2 ** attempt)
        what = f"HTTP {status}" if status else "no response"
        raise CollectionError(f"{side} {path}: {what} after {RETRIES} retries")

    def pages(self, side, template, size, key, total_of):
        """Read every page of a list and prove the count matches the reported total."""
        items, seen, page = [], set(), 1
        while True:
            _, headers, body = self.get(side, template.format(size=size, page=page))
            batch = (body or {}).get(key) or []
            total = total_of(headers, body or {})
            for item in batch:
                if item["id"] not in seen:
                    seen.add(item["id"])
                    items.append(item)
            if len(batch) < size or (total is not None and len(items) >= total):
                break
            page += 1
        if total is None or len(items) != total:
            raise CollectionError(f"{side} {template.format(size=size, page=1)}: read {len(items)} items "
                                  f"but total_count is {total}")
        return items


def _total(headers, body):
    value = headers.get("X-Total-Count", headers.get("x-total-count", body.get("total_count")))
    return int(value) if value is not None else None


def gitea_runs(client, since, page_size):
    """Gitea runs created at or after `since`, newest first, with contiguous run numbers."""
    runs, seen, page = [], set(), 1
    while True:
        _, headers, body = client.get("gitea", f"{REPO}/actions/runs?limit={page_size}&page={page}")
        batch = (body or {}).get("workflow_runs") or []
        for run in batch:
            if run["id"] not in seen:
                seen.add(run["id"])
                runs.append(run)
        boundary = any((parse_time(r.get("created_at")) or since) < since for r in batch)
        if boundary:
            break
        if len(batch) < page_size:
            total = _total(headers, body or {})
            if total is None or len(runs) != total:
                raise CollectionError(f"gitea run list: read {len(runs)} runs but X-Total-Count is {total}")
            break
        page += 1
    numbers = sorted({r["run_number"] for r in runs}, reverse=True)
    if numbers:
        missing = sorted(set(range(numbers[-1], numbers[0] + 1)) - set(numbers))
        if missing:
            raise CollectionError(f"gitea run list skipped run_number {missing}; a page shifted or a run is hidden")
    return [r for r in runs if (parse_time(r.get("created_at")) or since) >= since]


def gitea_jobs(client, run_id):
    return client.pages("gitea", REPO + f"/actions/runs/{run_id}/jobs?limit={{size}}&page={{page}}",
                        50, "jobs", _total)


def github_runs(client, since, page_size):
    template = REPO + "/actions/workflows/ci.yml/runs?created=%3E%3D" + format_time(since) + \
        "&per_page={size}&page={page}"
    return client.pages("github", template, page_size, "workflow_runs", _total)


def github_runs_for(client, sha):
    _, _, body = client.get("github", REPO + f"/actions/workflows/ci.yml/runs?head_sha={sha}&per_page=100&page=1")
    runs = body.get("workflow_runs") or []
    if body.get("total_count") != len(runs):
        raise CollectionError(f"github runs for {sha}: read {len(runs)} but total_count is {body.get('total_count')}")
    return runs


def github_jobs(client, run_id):
    return client.pages("github", REPO + f"/actions/runs/{run_id}/jobs?per_page={{size}}&page={{page}}",
                        100, "jobs", _total)


def github_attempt(client, run_id, attempt):
    _, _, body = client.get("github", REPO + f"/actions/runs/{run_id}/attempts/{attempt}")
    return {"attempt": attempt, "conclusion": body.get("conclusion")}


def github_commit_time(client, sha):
    _, _, body = client.get("github", REPO + f"/commits/{sha}")
    return parse_time(body["commit"]["committer"]["date"])


def normalize(side, run, jobs, previous=()):
    workflow = run.get("path", "")
    workflow = workflow.split("@", 1)[0] if side == "gitea" else workflow.rsplit("/", 1)[-1]
    head = (run.get("head_commit") or {}).get("timestamp")
    return {
        "side": side, "id": run["id"], "event": run.get("event"), "branch": run.get("head_branch"),
        "sha": run["head_sha"], "workflow": workflow,
        "status": "completed" if run.get("status") == "completed" else "running",
        "conclusion": run.get("conclusion") or None, "attempt": int(run.get("run_attempt") or 1),
        "created": parse_time(run.get("created_at")), "head_time": parse_time(head),
        "url": run.get("html_url"), "previous_attempts": list(previous),
        "jobs": [{"name": j["name"], "labels": list(j.get("labels") or []),
                  "status": "completed" if j.get("status") == "completed" else (j.get("status") or "unknown"),
                  "conclusion": j.get("conclusion") or None, "attempt": int(j.get("run_attempt") or 1),
                  "started": parse_time(j.get("started_at")), "completed": parse_time(j.get("completed_at"))}
                 for j in jobs],
    }


def collect(client, since, page_size, branches):
    """Normalized GitHub and Gitea CI runs for every commit with a run in range."""
    def wanted(run):
        return not branches or run.get("head_branch") in branches

    gitea = []
    for run in gitea_runs(client, since, page_size):
        if run.get("path", "").split("@", 1)[0] != "ci.yml" or not wanted(run):
            continue  # other Gitea workflows (canaries) are never CI evidence
        gitea.append(normalize("gitea", run, gitea_jobs(client, run["id"])))
    listed = [r for r in github_runs(client, since, page_size) if wanted(r)]
    known = {r["head_sha"] for r in listed}
    for sha in sorted({r["sha"] for r in gitea} - known):
        listed.extend(github_runs_for(client, sha))  # GitHub may have run before --since
    github = []
    for run in listed:
        attempt = int(run.get("run_attempt") or 1)
        previous = [github_attempt(client, run["id"], n) for n in range(1, attempt)]
        github.append(normalize("github", run, github_jobs(client, run["id"]), previous))
    return github, gitea


def secrets(client):
    """Each scope is absent, present (names and count) or not checked with the reason."""
    scopes = {}
    for scope, path in SECRET_SCOPES.items():
        status, headers, body = client.get("gitea", path, accept=(200, 401, 403, 404))
        if status != 200:
            message = body.get("message", "") if isinstance(body, dict) else ""
            scopes[scope] = {"state": "not checked", "status": status, "reason": str(message)[:200]}
            continue
        items = body if isinstance(body, list) else (body or {}).get("data") or []
        count = _total(headers, {}) or len(items)
        names = sorted(str(item.get("name")) for item in items)
        scopes[scope] = {"state": "present" if count else "absent", "count": count, "names": names}
    states = {s["state"] for s in scopes.values()}
    claim = False if "present" in states else ("not checked" if "not checked" in states else True)
    return {"claim": claim, "scopes": scopes}
