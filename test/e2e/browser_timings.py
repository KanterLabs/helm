"""Safe per-file timing profiles and deterministic whole-file shard planning."""
import hashlib
import json
import math
from pathlib import Path, PurePosixPath
import re
import statistics
import subprocess


COLLECTOR = "helm-playwright-file-timings-v1"
SHA256 = re.compile(r"[0-9a-f]{64}\Z")
REVISION = re.compile(r"[0-9a-f]{40}\Z")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?\Z")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON object key")
        result[key] = value
    return result


def read_json(path):
    return json.loads(Path(path).read_text(), object_pairs_hook=unique_object)


def file_path(name, web):
    """Accept only canonical tracked-style paths inside the real suite root."""
    if not isinstance(name, str) or not re.fullmatch(r"web/e2e/[a-zA-Z0-9_./-]+\.spec\.ts", name):
        raise ValueError("timing file must be a relative web/e2e/*.spec.ts path")
    parts = PurePosixPath(name).parts
    if any(part in (".", "..") for part in parts) or str(PurePosixPath(name)) != name:
        raise ValueError("noncanonical timing file path")
    path = web.parent / name
    if not path.is_file() or not path.resolve().is_relative_to((web / "e2e").resolve()):
        raise ValueError("timing file must exist inside web/e2e")
    return path


def number(value):
    return type(value) in (int, float) and math.isfinite(value) and value > 0


def load_profile(path, web):
    profile = read_json(path)
    required = {"schema", "source_revision", "collector", "playwright_versions",
                "report_sha256", "files"}
    if not isinstance(profile, dict) or set(profile) != required:
        raise ValueError("invalid timing profile fields")
    if type(profile["schema"]) is not int or profile["schema"] != 1 or profile["collector"] != COLLECTOR:
        raise ValueError("unsupported timing profile schema/collector")
    if not isinstance(profile["source_revision"], str) or not REVISION.fullmatch(profile["source_revision"]):
        raise ValueError("timing profile requires an explicit source revision")
    for key, pattern in (("report_sha256", SHA256), ("playwright_versions", VERSION)):
        values = profile[key]
        if not isinstance(values, list) or not values or any(
                not isinstance(v, str) or not pattern.fullmatch(v) for v in values):
            raise ValueError("invalid timing profile provenance")
    if not isinstance(profile["files"], dict) or not profile["files"]:
        raise ValueError("timing profile has no measured files")
    for name, entry in profile["files"].items():
        file_path(name, web)
        if not isinstance(entry, dict) or set(entry) != {"seconds", "samples", "sha256"}:
            raise ValueError("invalid timing file fields")
        if not number(entry["seconds"]) or type(entry["samples"]) is not int or entry["samples"] < 1:
            raise ValueError("invalid timing duration/sample count")
        if not isinstance(entry["sha256"], str) or not SHA256.fullmatch(entry["sha256"]):
            raise ValueError("invalid timing source hash")
    return profile


def specs(suite):
    for spec in suite.get("specs", []):
        yield spec
    for child in suite.get("suites", []):
        yield from specs(child)


def report_files(report, web):
    for suite in report.get("suites", []):
        for spec in specs(suite):
            name = "web/e2e/" + spec["file"]
            file_path(name, web)
            yield name, spec


def collect_reports(paths, repo, source_revision):
    """Caller attests report source; Git binds hashes to that explicit revision."""
    if not REVISION.fullmatch(source_revision):
        raise ValueError("source revision must be a full Git commit SHA")
    repo, totals, hashes, versions = Path(repo).resolve(), {}, [], set()
    for path in paths:
        payload = Path(path).read_bytes()
        report_hash = digest(payload)
        if report_hash in hashes:
            raise ValueError("duplicate source report")
        hashes.append(report_hash)
        report = json.loads(payload, object_pairs_hook=unique_object)
        version = report.get("config", {}).get("version", "")
        if not VERSION.fullmatch(version):
            raise ValueError("report lacks valid Playwright version")
        versions.add(version)
        measured = {}
        for name, spec in report_files(report, repo / "web"):
            for test in spec.get("tests", []):
                for result in test.get("results", []):
                    duration = result.get("duration")
                    if type(duration) not in (int, float) or not math.isfinite(duration) or duration < 0:
                        raise ValueError("invalid report duration")
                    measured[name] = measured.get(name, 0) + duration / 1000
        for name, seconds in measured.items():
            if seconds > 0:
                totals.setdefault(name, []).append(seconds)
    files = {}
    for name, samples in sorted(totals.items()):
        source = subprocess.run(["git", "-C", str(repo), "show", f"{source_revision}:{name}"],
                                capture_output=True, check=False)
        if source.returncode:
            raise ValueError("report file does not exist at the supplied source revision")
        files[name] = {"seconds": statistics.mean(samples), "samples": len(samples),
                       "sha256": digest(source.stdout)}
    if not files:
        raise ValueError("reports contain no positive measured file durations")
    return {"schema": 1, "source_revision": source_revision, "collector": COLLECTOR,
            "playwright_versions": sorted(versions), "report_sha256": hashes, "files": files}


def plan_files(selected, profile, web, workers):
    if len(set(selected)) != len(selected) or not selected:
        raise ValueError("selection must contain unique files and at least one test")
    if not 1 <= workers <= 8:
        raise ValueError("workers must be between 1 and 8")
    fallback = statistics.median(entry["seconds"] for entry in profile["files"].values())
    weights, estimated = {}, []
    for name in sorted(selected):
        path = file_path(name, web)
        entry = profile["files"].get(name)
        if entry and entry["sha256"] == digest(path.read_bytes()):
            weights[name] = entry["seconds"]
        else:
            weights[name] = fallback
            estimated.append(name)
    assignments = [{"files": [], "estimated_seconds": 0} for _ in range(workers)]
    for name in sorted(selected, key=lambda name: (-weights[name], name)):
        index = min(range(workers), key=lambda i: (assignments[i]["estimated_seconds"], i))
        assignments[index]["files"].append(name)
        assignments[index]["estimated_seconds"] += weights[name]
    for assignment in assignments:
        assignment["files"].sort()
        assignment["estimated_seconds"] = round(assignment["estimated_seconds"], 3)
    return {"schema": 1, "source_revision": profile["source_revision"],
            "fallback_seconds": fallback, "estimated_files": estimated, "workers": assignments}
