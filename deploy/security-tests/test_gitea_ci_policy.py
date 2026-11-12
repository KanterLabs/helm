#!/usr/bin/env python3
"""Mutation proofs for the Gitea shadow workflow policy, written before the checker.

Failure inventory: docs/GITEA_CI_SHADOW_FAILURES.md. Each case mutates the
reviewed `.gitea/workflows/ci.yml` text and runs deploy/test-gitea-ci-policy.py
on the result. Reviewed text and the cutover relabel must be accepted; every
secret, variable, environment, registry, deploy, unpinned or unreviewed action,
credential-persisting checkout, job, label, trigger and placement change must
be rejected. Only synthetic workflow text is used; no job runs.
"""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
CHECK = ROOT / "deploy/test-gitea-ci-policy.py"
WORKFLOW = ROOT / ".gitea/workflows/ci.yml"
SHA = "0123456789abcdef0123456789abcdef01234567"
CHECKOUT = "https://github.com/actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"


def replace_once(source, old, new):
    if source.count(old) != 1:
        raise AssertionError(f"proof fixture must match once: {old!r}")
    return source.replace(old, new, 1)


def replace_first(source, old, new):
    if old not in source:
        raise AssertionError(f"proof fixture must match: {old!r}")
    return source.replace(old, new, 1)


def add_step(source, job, step):
    """Insert a step as the first step of a job."""
    begin = source.index(f"\n  {job}:\n")
    index = source.index("    steps:\n", begin) + len("    steps:\n")
    return source[:index] + step + source[index:]


def run_step(command):
    return f"      - name: Injected step\n        run: {command}\n"


def uses_step(reference):
    return f"      - name: Injected action\n        uses: {reference}\n"


def cases_for(source):
    yield "reviewed-workflow", source, True
    yield "comments-preserve-policy", source + "\n# unrelated comment\n", True
    production = source.replace("ar-helm-canary-short", "homelab").replace(
        "ar-helm-canary-heavy", "homelab-heavy")
    yield "cutover-production-labels", production, True

    rejected = {
        # Trust boundary.
        "secret-read": replace_once(source, "  GOTOOLCHAIN: local\n",
                                    "  GOTOOLCHAIN: local\n  HELM_TOKEN: ${{ secrets.HELM_TOKEN }}\n"),
        "secret-index-read": add_step(source, "checks", run_step("echo ${{ secrets['HELM'] }}")),
        "vars-read": replace_once(source, "  GOTOOLCHAIN: local\n",
                                  "  GOTOOLCHAIN: local\n  HELM_X: ${{ vars.HELM_X }}\n"),
        "vars-gate": replace_once(source, "  race:\n    if: ${{ always() }}\n",
                                  "  race:\n    if: ${{ always() && vars.HELM_RACE == 'true' }}\n"),
        "gitea-token": add_step(source, "checks", run_step("echo ${{ gitea.token }}")),
        "github-token-env": add_step(source, "checks", run_step('test -n "$GITHUB_TOKEN"')),
        "environment": replace_once(source, "  checks:\n    runs-on: ar-helm-canary-short\n",
                                    "  checks:\n    runs-on: ar-helm-canary-short\n    environment: beta\n"),
        "registry-login": add_step(source, "checks", run_step("printf x | docker login ghcr.io -u x --password-stdin")),
        "login-action": add_step(source, "checks", uses_step(f"https://github.com/docker/login-action@{SHA}")),
        "image-push": add_step(source, "checks", run_step("docker push example/helm:1")),
        "coolify-deploy": add_step(source, "checks", run_step("python3 deploy/coolify-deploy.py")),
        "coolify-setting": add_step(source, "checks", run_step("echo $COOLIFY_API_URL")),
        "release-tooling": add_step(source, "checks", run_step("python3 deploy/release.py status")),
        "unreviewed-deploy-path": add_step(source, "checks", run_step("./deploy/validate-live.sh")),
        "tailscale": add_step(source, "checks", uses_step(f"https://github.com/tailscale/github-action@{SHA}")),
        "ssh": add_step(source, "checks", run_step("ssh root@10.0.0.1 true")),
        "write-permission": replace_once(source, "permissions:\n  contents: read\n",
                                         "permissions:\n  contents: write\n"),
        "extra-permission": replace_once(source, "permissions:\n  contents: read\n",
                                         "permissions:\n  contents: read\n  packages: read\n"),
        "job-permissions": replace_once(source, "  checks:\n    runs-on: ar-helm-canary-short\n",
                                        "  checks:\n    permissions: {}\n    runs-on: ar-helm-canary-short\n"),
        "pull-request-target": replace_once(source, "  pull_request:\n", "  pull_request_target:\n"),
        "workflow-run": replace_once(source, "  workflow_dispatch:\n",
                                     "  workflow_dispatch:\n  workflow_run:\n"),
        "job-container": replace_once(source, "  checks:\n    runs-on: ar-helm-canary-short\n",
                                      "  checks:\n    runs-on: ar-helm-canary-short\n    container: docker:dind\n"),
        "job-services": replace_once(source, "  checks:\n    runs-on: ar-helm-canary-short\n",
                                     "  checks:\n    runs-on: ar-helm-canary-short\n    services:\n      docker:\n        image: docker:dind\n"),
        "docker-socket": add_step(source, "checks", run_step("ls /var/run/docker.sock")),
        "sudo": add_step(source, "checks", run_step("sudo true")),
        "yaml-anchor": replace_once(source, "  GOTOOLCHAIN: local\n", "  GOTOOLCHAIN: &tool local\n"),
        "yaml-merge-key": replace_once(source, "  GOTOOLCHAIN: local\n", "  <<: {}\n  GOTOOLCHAIN: local\n"),
        # Supply chain.
        "tag-pinned-action": replace_first(source, f"uses: {CHECKOUT}", "uses: https://github.com/actions/checkout@v7"),
        "short-sha-action": replace_first(source, f"uses: {CHECKOUT}", "uses: https://github.com/actions/checkout@3d3c42e"),
        "short-form-action": replace_first(source, f"uses: {CHECKOUT}",
                                           "uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"),
        "other-host-action": replace_first(source, f"uses: {CHECKOUT}",
                                           "uses: https://gitea.example/actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"),
        "docker-action": add_step(source, "checks", uses_step("docker://alpine:3")),
        "unreviewed-action": add_step(source, "checks", uses_step(f"https://github.com/example/action@{SHA}")),
        "upload-artifact-v7": add_step(source, "checks", uses_step(
            "https://github.com/actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a")),
        "cache-v6": add_step(source, "browser", uses_step("https://github.com/actions/cache@v6")),
        "unreviewed-local-action": add_step(source, "browser", uses_step("./.github/actions/prepared-runner")),
        "local-action-uploads-v7": replace_once(source, "          upload-evidence: 'false'\n", ""),
        "reusable-workflow": source + "\n  reused:\n    uses: ./.gitea/workflows/other.yml\n",
        # Checkouts.
        "checkout-persists-by-default": replace_first(
            source, f"uses: {CHECKOUT} # v7.0.1\n        with:\n          persist-credentials: false\n",
            f"uses: {CHECKOUT} # v7.0.1\n"),
        "checkout-persists-explicitly": replace_first(source, "persist-credentials: false", "persist-credentials: true"),
        "checkout-token": replace_first(source, "          persist-credentials: false\n",
                                        "          persist-credentials: false\n          token: abc\n"),
        "unguarded-extra-checkout": add_step(source, "race", uses_step(f"{CHECKOUT} # v7.0.1")),
        # Job set and placement.
        "container-job": source + "\n  container:\n    runs-on: ar-helm-canary-heavy\n    timeout-minutes: 40\n    steps: []\n",
        "beta-deploy-job": source + "\n  beta_deploy:\n    runs-on: ar-helm-canary-short\n    timeout-minutes: 40\n    steps: []\n",
        "deploy-job": source + "\n  deploy:\n    runs-on: ar-helm-canary-short\n    timeout-minutes: 40\n    steps: []\n",
        "quoted-unknown-job": source + "\n  'unreviewed':\n    runs-on: ar-helm-canary-short\n    steps: []\n",
        "missing-job": replace_once(source, "\n  race:\n", "\n  renamed_race:\n"),
        "duplicate-job": source + "\n  race:\n    runs-on: ar-helm-canary-short\n    timeout-minutes: 40\n    steps: []\n",
        "hosted-label": replace_first(source, "runs-on: ar-helm-canary-short\n    timeout-minutes: 40\n    steps:",
                                     "runs-on: ubuntu-24.04\n    timeout-minutes: 40\n    steps:"),
        "unknown-label": replace_once(source, "runs-on: ar-helm-canary-heavy", "runs-on: ar-dct-canary-heavy"),
        "expression-label": replace_once(source, "runs-on: ar-helm-canary-heavy",
                                         "runs-on: ${{ gitea.event_name == 'push' && 'homelab-heavy' || 'ar-helm-canary-heavy' }}"),
        "list-label": replace_once(source, "runs-on: ar-helm-canary-heavy", "runs-on: [ar-helm-canary-heavy]"),
        "group-label": replace_once(source, "runs-on: ar-helm-canary-heavy",
                                    "runs-on:\n      group: helm\n      labels: ar-helm-canary-heavy"),
        "browser-on-short": replace_once(source, "runs-on: ar-helm-canary-heavy", "runs-on: ar-helm-canary-short"),
        "checks-on-heavy": replace_first(source, "runs-on: ar-helm-canary-short", "runs-on: ar-helm-canary-heavy"),
        "missing-runs-on": replace_once(source, "  browser:\n    runs-on: ar-helm-canary-heavy\n", "  browser:\n"),
        "duplicate-runs-on": replace_once(source, "    runs-on: ar-helm-canary-heavy\n",
                                          "    runs-on: ar-helm-canary-heavy\n    runs-on: ar-helm-canary-heavy\n"),
        "missing-timeout": replace_once(source, "    runs-on: ar-helm-canary-heavy\n    timeout-minutes: 40\n",
                                        "    runs-on: ar-helm-canary-heavy\n"),
        "platform-timeout": replace_once(source, "    runs-on: ar-helm-canary-heavy\n    timeout-minutes: 40\n",
                                         "    runs-on: ar-helm-canary-heavy\n    timeout-minutes: 180\n"),
        "push-branch-filter": replace_once(source, "  push:\n", "  push:\n    branches: [main, beta]\n"),
        "missing-dispatch": replace_once(source, "  workflow_dispatch:\n", ""),
        "missing-pull-request": replace_once(source, "  pull_request:\n", ""),
        "cancels-main": replace_once(source, "gitea.ref != 'refs/heads/main' && ", ""),
        "never-cancels": replace_once(
            source, "cancel-in-progress: ${{ gitea.ref != 'refs/heads/main' && gitea.ref != 'refs/heads/beta' }}",
            "cancel-in-progress: false"),
        "race-without-browser": replace_once(source, "    needs: browser\n", ""),
        "race-ignores-result": replace_once(source, 'run: test "$BROWSER_RESULT" = success', "run: true"),
        # Gitea differences and evidence.
        "workflow-sha": add_step(source, "checks", run_step('echo "$GITHUB_WORKFLOW_SHA"')),
        "runner-environment": replace_once(source, "  race:\n    if: ${{ always() }}\n",
                                           "  race:\n    if: ${{ always() && runner.environment == 'self-hosted' }}\n"),
        "binary-artifact-renamed": replace_once(source, "name: helm-binary-${{ gitea.sha }}", "name: helm-binary"),
        "evidence-artifact-renamed": replace_once(source, "          name: helm-e2e-${{ gitea.sha }}\n",
                                                  "          name: helm-e2e\n"),
        "missing-artifact-tolerated": replace_first(source, "if-no-files-found: error", "if-no-files-found: ignore"),
        "tab-indentation": replace_once(source, "  GOTOOLCHAIN: local\n", "\tGOTOOLCHAIN: local\n"),
    }
    for name, text in rejected.items():
        yield name, text, False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact", type=Path, required=True)
    args = parser.parse_args()
    source = WORKFLOW.read_text()
    results = []
    with tempfile.TemporaryDirectory(prefix="helm-gitea-policy-") as directory:
        for name, text, accepted in cases_for(source):
            path = Path(directory) / "ci.yml"
            path.write_text(text)
            result = subprocess.run([sys.executable, str(CHECK), str(path)],
                                    capture_output=True, text=True, timeout=5)
            results.append({"case": name, "expected_accept": accepted,
                            "exit_status": result.returncode,
                            "reason": (result.stderr or result.stdout).strip()[:200],
                            "passed": (result.returncode == 0) == accepted})
    artifact = {"checks": results, "passed": all(x["passed"] for x in results),
                "external_jobs_started": 0}
    args.artifact.parent.mkdir(parents=True, exist_ok=True)
    args.artifact.write_text(json.dumps(artifact, indent=2) + "\n")
    for result in results:
        if not result["passed"]:
            print(f"FAILED: {result['case']}: {result['reason']}", file=sys.stderr)
    print(f"gitea-ci-policy proofs: {sum(x['passed'] for x in results)}/{len(results)} passed")
    return 0 if artifact["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
