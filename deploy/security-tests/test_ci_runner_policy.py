#!/usr/bin/env python3
"""Subprocess policy proofs, defined before the checker implementation.

Failure inventory: remove a trust/ref/opt-in gate, widen the runner group,
change the hosted fallback, route privileged jobs to the test VM, add unknown
jobs, hide/duplicate runner selectors, or drop a required job. All must fail.
Only synthetic mutations of repository workflow text are used; no jobs run.
"""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
CHECK = ROOT / "deploy/test-ci-runner-policy.py"
WORKFLOW = ROOT / ".github/workflows/ci.yml"


def replace_once(source, old, new):
    if source.count(old) != 1:
        raise AssertionError(f"proof fixture must match once: {old}")
    return source.replace(old, new, 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact", type=Path, required=True)
    args = parser.parse_args()
    source = WORKFLOW.read_text()
    cases = [("reviewed-workflow", source, True),
             ("comments-preserve-policy", source + "\n# unrelated comment\n", True)]
    selector = next(line for line in source.splitlines() if "runs-on: ${{ fromJSON" in line)
    mutations = {
        "public-pr-admission": selector.replace("github.event_name != 'pull_request'", "true"),
        "arbitrary-ref-admission": selector.replace("(github.ref == 'refs/heads/beta' || github.ref == 'refs/heads/main')", "true"),
        "missing-opt-in": selector.replace("vars.HELM_WARM_RUNNER_ENABLED == 'true'", "true"),
        "different-runner-group": selector.replace('"helm-runners"', '"homelab-runners"'),
        "broad-fallback": selector.replace('"ubuntu-24.04"', '"homelab-heavy"'),
        "unconditional-heavy": "    runs-on: homelab-heavy",
        "missing-selector": "    # selector removed",
        "duplicate-selector": selector + "\n    runs-on: ubuntu-24.04",
        "multiline-selector": "    runs-on:\n      group: helm-runners\n      labels: homelab-heavy",
    }
    for name, replacement in mutations.items():
        cases.append((name, replace_once(source, selector, replacement), False))
    for job in ("checks", "race", "container", "beta_deploy", "deploy"):
        begin = source.index(f"  {job}:\n")
        # Search within the selected job for its first exact scalar selector.
        index = source.index("    runs-on: ubuntu-24.04", begin)
        changed = source[:index] + source[index:].replace("    runs-on: ubuntu-24.04", "    runs-on: homelab", 1)
        cases.append((f"{job}-retains-hosted-boundary", changed, False))
    cases.extend([
        ("quoted-unknown-job", source + "\n  'unreviewed':\n    runs-on: homelab-heavy\n    steps: []\n", False),
        ("unknown-job", source + "\n  unreviewed:\n    runs-on: homelab-heavy\n    steps: []\n", False),
        ("missing-job", replace_once(source, "  race:\n", "  renamed_race:\n"), False),
        ("duplicate-job", source + "\n  browser:\n    runs-on: ubuntu-24.04\n", False),
    ])
    results = []
    with tempfile.TemporaryDirectory(prefix="helm-runner-policy-") as directory:
        for name, text, accepted in cases:
            path = Path(directory) / "workflow.yml"
            path.write_text(text)
            result = subprocess.run([sys.executable, str(CHECK), str(path)],
                                    capture_output=True, text=True, timeout=5)
            results.append({"case": name, "expected_accept": accepted,
                            "exit_status": result.returncode,
                            "passed": (result.returncode == 0) == accepted})
    artifact = {"checks": results, "passed": all(x["passed"] for x in results),
                "external_jobs_started": 0}
    args.artifact.parent.mkdir(parents=True, exist_ok=True)
    args.artifact.write_text(json.dumps(artifact, indent=2) + "\n")
    for result in results:
        if not result["passed"]:
            print("FAILED: " + result["case"], file=sys.stderr)
    print(f"runner-policy proofs: {sum(x['passed'] for x in results)}/{len(results)} passed")
    return 0 if artifact["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
