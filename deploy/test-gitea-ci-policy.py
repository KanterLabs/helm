#!/usr/bin/env python3
"""Verify the secret-free boundary of the Gitea shadow workflow.

Failure contract: docs/GITEA_CI_SHADOW_FAILURES.md. Like the GitHub runner
policy, this accepts only the reviewed simple layout and needs no YAML library:
a structural change (anchors, flow job maps, reusable workflows, unknown keys)
requires a policy review, not a permissive parser. Proofs live in
deploy/security-tests/test_gitea_ci_policy.py.
"""
import argparse
from pathlib import Path
import re
import sys

JOBS = {"checks", "browser", "race"}
SHORT = {"ar-helm-canary-short", "homelab"}
HEAVY = {"ar-helm-canary-heavy", "homelab-heavy"}
PLACEMENT = {"checks": SHORT, "race": SHORT, "browser": HEAVY}
JOB_KEYS = {"runs-on", "timeout-minutes", "needs", "if", "steps"}
TOP_KEYS = ["name", "on", "permissions", "concurrency", "env", "jobs"]
TRIGGERS = ["pull_request", "push", "workflow_dispatch"]
CONCURRENCY = [
    "group: helm-shadow-${{ gitea.workflow }}-${{ gitea.ref }}",
    "cancel-in-progress: ${{ gitea.ref != 'refs/heads/main' && gitea.ref != 'refs/heads/beta' }}",
]
# Repositories proven on this Gitea server by the wiki, DCT and Portfolio
# (setup-go is the GitHub pipeline's own pin; see the shadow guide).
ACTIONS = {"actions/checkout", "actions/setup-go", "actions/setup-node",
           "ChristopherHX/gitea-upload-artifact"}
REMOTE = re.compile(r"https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([0-9a-f]{40})(?:\s+#[^\n]*)?")
LOCAL_ACTIONS = {"./.github/actions/browser-validation"}
# The only reviewed references into deploy/: syntax checks and the test suite.
DEPLOY_REFERENCES = {
    "python3 -m py_compile deploy/coolify-deploy.py",
    "bash -n deploy/*.sh deploy/helm-deploy-gateway",
    "./deploy/test-deployment-security.sh",
}
ARTIFACTS = {"checks": "helm-binary-${{ gitea.sha }}", "browser": "helm-e2e-${{ gitea.sha }}"}
FORBIDDEN = [
    (r"\bsecrets\s*[.\[]", "reads a secret"),
    (r"\bvars\s*[.\[]", "reads a variable"),
    (r"\b(?:github|gitea)\.token\b|\b(?:GITHUB|GITEA)_TOKEN\b|ACTIONS_RUNTIME_TOKEN", "reads a token"),
    (r"^\s*environment\s*:", "declares an environment"),
    (r"docker\s+login|login-action|--password-stdin", "logs in to a registry"),
    (r"docker\s+push|ghcr\.io|\b(?:crane|skopeo|oras)\b", "publishes an image"),
    (r"tailscale|COOLIFY_|cloudflared?\b|\bssh\b|\bscp\b|\brsync\b|\bpct\b", "contains a deploy step"),
    (r"pull_request_target|workflow_run", "uses a privileged trigger"),
    (r":\s*write\b", "grants write permission"),
    (r"docker\.sock|--privileged|\bsudo\b", "needs a privileged runner path"),
    (r"GITHUB_WORKFLOW_SHA|runner\.environment", "depends on a context Gitea does not provide"),
    (r"persist-credentials:\s*true", "persists checkout credentials"),
    (r"<<\s*:|:\s*[&*][A-Za-z_]", "uses YAML anchors or merge keys"),
]


class PolicyError(ValueError):
    pass


def blocks(lines, indent):
    """Split lines into (key, body) at exactly `indent` spaces."""
    result, current = [], None
    for line in lines:
        if not line.strip():
            continue
        depth = len(line) - len(line.lstrip(" "))
        if depth < indent:
            raise PolicyError(f"unexpected indentation: {line.strip()}")
        if depth == indent:
            match = re.fullmatch(r"([A-Za-z_][A-Za-z0-9_-]*):(?:\s+(.*))?", line.strip())
            if not match:
                raise PolicyError(f"unsupported declaration: {line.strip()}")
            current = [match.group(1), match.group(2), []]
            result.append(current)
        elif current is None:
            raise PolicyError(f"unexpected indentation: {line.strip()}")
        else:
            current[2].append(line)
    names = [key for key, _, _ in result]
    if len(names) != len(set(names)):
        raise PolicyError(f"duplicate keys: {names}")
    return result


def steps_of(lines):
    steps, current = [], None
    for line in lines:
        if not line.strip():
            continue
        if re.match(r"      - ", line):
            current = [line.replace("      - ", "        ", 1)]
            steps.append(current)
        elif current is None or not line.startswith("        "):
            raise PolicyError(f"unsupported step layout: {line.strip()}")
        else:
            current.append(line)
    return steps


def step_value(step, key):
    values = [m.group(1).strip() for line in step
              if (m := re.fullmatch(rf"        {key}:\s*(.*)", line))]
    if len(values) > 1:
        raise PolicyError(f"step repeats {key}")
    return values[0] if values else None


def with_values(step):
    inside, values = False, {}
    for line in step:
        if re.fullmatch(r"        with:\s*", line):
            inside = True
        elif inside and (m := re.fullmatch(r"          ([A-Za-z0-9_-]+):\s*(.*)", line)):
            values[m.group(1)] = m.group(2).strip()
        elif not line.startswith("          "):
            inside = False
    return values


def check_step(job, step):
    if any(re.search(r"\buses\s*:", line) for line in step) and step_value(step, "uses") is None:
        raise PolicyError(f"job {job} has an action reference outside a step")
    reference = step_value(step, "uses")
    if reference is None:
        return None
    options = with_values(step)
    if reference.startswith("./"):
        if reference not in LOCAL_ACTIONS:
            raise PolicyError(f"job {job} uses unreviewed local action {reference}")
        if options.get("upload-evidence") != "'false'":
            raise PolicyError(f"job {job} must disable the composite's GitHub artifact upload")
        return reference
    match = REMOTE.fullmatch(reference)
    if not match:
        raise PolicyError(f"job {job} uses an action not pinned by full URL and commit: {reference}")
    if match.group(1) not in ACTIONS:
        raise PolicyError(f"job {job} uses unreviewed action {match.group(1)}")
    if match.group(1) == "actions/checkout":
        if options.get("persist-credentials") != "false" or set(options) != {"persist-credentials"}:
            raise PolicyError(f"job {job} checkout must set only persist-credentials: false")
    return match.group(1)


def check_job(name, header, body):
    if header:
        raise PolicyError(f"job {name} must be a plain mapping")
    keys = blocks(body, 4)
    unknown = {key for key, _, _ in keys} - JOB_KEYS
    if unknown:
        raise PolicyError(f"job {name} has unreviewed keys {sorted(unknown)}")
    values = {key: (value, rest) for key, value, rest in keys}
    label = values.get("runs-on", (None, None))[0]
    if label is None or values["runs-on"][1]:
        raise PolicyError(f"job {name} requires exactly one plain runner label")
    if label not in PLACEMENT[name]:
        raise PolicyError(f"job {name} label {label} is outside {sorted(PLACEMENT[name])}")
    if values.get("timeout-minutes", (None,))[0] != "40":
        raise PolicyError(f"job {name} must keep timeout-minutes: 40")
    if "steps" not in values or values["steps"][0]:
        raise PolicyError(f"job {name} needs a steps list")
    used = []
    for step in steps_of(values["steps"][1]):
        used.append(check_step(name, step))
    if name == "race":
        if values.get("needs", (None,))[0] != "browser" or values.get("if", (None,))[0] != "${{ always() }}":
            raise PolicyError("race must always run after browser")
        text = "\n".join(values["steps"][1])
        for needle in ("BROWSER_RESULT: ${{ needs.browser.result }}", 'run: test "$BROWSER_RESULT" = success'):
            if needle not in text:
                raise PolicyError("race must require browser success")
    elif set(values) - {"runs-on", "timeout-minutes", "steps"}:
        raise PolicyError(f"job {name} must not be conditional or depend on other jobs")
    else:
        if used[:3] != ["actions/checkout", "actions/setup-go", "actions/setup-node"]:
            raise PolicyError(f"job {name} must start with checkout, setup-go and setup-node")
        text = "\n".join(values["steps"][1])
        if text.count("ChristopherHX/gitea-upload-artifact@") != 1 \
                or not re.search(rf"^ +name: {re.escape(ARTIFACTS[name])}$", text, re.MULTILINE) \
                or "if-no-files-found: error" not in text:
            raise PolicyError(f"job {name} must upload {ARTIFACTS[name]} and fail if it is missing")


def validate(text):
    if "\t" in text:
        raise PolicyError("tabs are not reviewed YAML")
    lines = [line for line in text.splitlines() if not line.lstrip().startswith("#")]
    body = "\n".join(lines)
    for pattern, reason in FORBIDDEN:
        if re.search(pattern, body, re.MULTILINE):
            raise PolicyError(f"workflow {reason}")
    for line in lines:
        if "deploy/" in line and line.strip().removeprefix("run: ") not in DEPLOY_REFERENCES:
            raise PolicyError(f"unreviewed deploy reference: {line.strip()}")
    top = blocks(lines, 0)
    if [key for key, _, _ in top] != TOP_KEYS:
        raise PolicyError(f"top-level keys must be {TOP_KEYS}")
    sections = {key: (value, rest) for key, value, rest in top}
    triggers = blocks(sections["on"][1], 2)
    if sections["on"][0] or [key for key, _, _ in triggers] != TRIGGERS \
            or any(value or rest for _, value, rest in triggers):
        raise PolicyError(f"triggers must be exactly unfiltered {TRIGGERS}")
    if sections["permissions"][0] or [x.strip() for x in sections["permissions"][1]] != ["contents: read"]:
        raise PolicyError("permissions must be exactly contents: read")
    if [x.strip() for x in sections["concurrency"][1]] != CONCURRENCY:
        raise PolicyError("concurrency must cancel superseded runs except on main and beta")
    if [x.strip() for x in sections["env"][1]] != ["GOTOOLCHAIN: local"]:
        raise PolicyError("workflow env must be exactly GOTOOLCHAIN: local")
    jobs = blocks(sections["jobs"][1], 2)
    names = [key for key, _, _ in jobs]
    if set(names) != JOBS:
        raise PolicyError(f"job set {names} differs from {sorted(JOBS)}")
    for name, header, body in jobs:
        check_job(name, header, body)
    return names


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("workflow", type=Path)
    args = parser.parse_args()
    try:
        names = validate(args.workflow.read_text())
    except (OSError, ValueError) as error:
        print(f"[gitea-ci-policy] {error}", file=sys.stderr)
        return 1
    print(f"gitea_ci_policy=ok ({len(names)} jobs; secret-free, pinned, canary or production labels)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
