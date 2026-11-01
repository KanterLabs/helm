#!/usr/bin/env python3
"""Verify the reviewed runner boundary without requiring a YAML dependency.

This intentionally accepts only the current simple job/runs-on declarations.
A structural or expression change requires a policy review, not a permissive
fallback parser. Actionlint separately validates the complete workflow syntax.
"""
import argparse
from pathlib import Path
import re
import sys

JOBS = {"checks", "browser", "race", "container", "beta_deploy", "deploy"}
# Preserve the explicit opt-in, source-ref restriction, PR exclusion and exact
# Helm-only group together. Merely counting hosted jobs cannot prove this.
BROWSER_SELECTOR = '''${{ fromJSON(github.event_name != 'pull_request' && (github.ref == 'refs/heads/beta' || github.ref == 'refs/heads/main') && vars.HELM_WARM_RUNNER_ENABLED == 'true' && '{"group":"helm-runners","labels":"homelab-heavy"}' || '"ubuntu-24.04"') }}'''


def validate(text):
    headings = list(re.finditer(r"^jobs:\s*$", text, re.MULTILINE))
    if len(headings) != 1:
        raise ValueError("workflow must contain one reviewed jobs section")
    section = text[headings[0].end():]
    declarations = re.findall(r"^  ([^\s#][^\n]*)$", section, re.MULTILINE)
    if any(not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_-]*:\s*", line)
           for line in declarations):
        raise ValueError("unsupported job declaration; runner policy review required")
    matches = list(re.finditer(r"^  ([A-Za-z_][A-Za-z0-9_-]*):\s*$", section, re.MULTILINE))
    names = [match.group(1) for match in matches]
    if len(names) != len(set(names)) or set(names) != JOBS:
        raise ValueError("workflow job set changed; runner policy review required")
    for index, match in enumerate(matches):
        name = match.group(1)
        end = matches[index + 1].start() if index + 1 < len(matches) else len(section)
        block = section[match.end():end]
        selectors = re.findall(r"^    runs-on:([^\n]*)$", block, re.MULTILINE)
        if len(selectors) != 1:
            raise ValueError(f"job {name} requires exactly one explicit runner selector")
        expected = BROWSER_SELECTOR if name == "browser" else "ubuntu-24.04"
        if selectors[0].strip() != expected:
            raise ValueError(f"job {name} changed its reviewed runner boundary")
    return names


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("workflow", type=Path)
    args = parser.parse_args()
    try:
        names = validate(args.workflow.read_text())
    except (OSError, ValueError) as error:
        print(f"[runner-policy] {error}", file=sys.stderr)
        return 1
    print(f"runner_policy=ok ({len(names)} jobs; gated trusted browser, hosted fallback/deploy)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
