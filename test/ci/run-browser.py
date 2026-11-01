#!/usr/bin/env python3
"""Execute the exact declared browser scope through the existing coordinator."""
import argparse
import os
from pathlib import Path
import subprocess
from changes import full_specs, read_json
from plan import validate_plan


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path, required=True)
    args = parser.parse_args()
    plan = read_json(args.plan)
    validate_plan(plan)
    root = Path(__file__).resolve().parents[2]
    scope = plan['effective_scope']
    if scope not in ('full', 'focused'):
        parser.error('browser execution requires a full or focused plan')
    selected = plan['selected_suites']
    available = full_specs(root, plan['source']['sha'])
    if any(name != 'github' and name not in available for name in selected):
        parser.error('selected browser file is absent from source')
    env = dict(os.environ, HELM_E2E_INCLUDE_DEADLINES='true' if scope == 'full' else 'false')
    command = [str(root / 'test/e2e/run.sh')]
    if scope == 'focused':
        command.extend(selected)
    # Arguments are a validated spec inventory; no shell parsing or eval.
    return subprocess.run(command, cwd=root, env=env).returncode


if __name__ == '__main__':
    raise SystemExit(main())
