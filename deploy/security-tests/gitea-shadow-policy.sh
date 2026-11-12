# Gitea shadow workflow (Helm H3): secret-free, pinned actions, reviewed jobs
# and canary or production labels only. Contract:
# docs/GITEA_CI_SHADOW_FAILURES.md. This module runs in both pipelines and
# leaves the GitHub workflow checks above untouched.
GITEA_WORKFLOW="$ROOT_DIR/.gitea/workflows/ci.yml"
GITEA_PREPARE="$ROOT_DIR/.gitea/scripts/prepare-runner.sh"
BROWSER_ACTION="$ROOT_DIR/.github/actions/browser-validation/action.yml"
for file in "$GITEA_WORKFLOW" "$GITEA_PREPARE" "$BROWSER_ACTION"; do
	[[ -f "$file" && ! -L "$file" ]] || fail "Gitea shadow file is missing: $file"
done
python3 "$DEPLOY_DIR/test-gitea-ci-policy.py" "$GITEA_WORKFLOW"
python3 "$DEPLOY_DIR/security-tests/test_gitea_ci_policy.py" --artifact "$fixture/gitea-ci-policy-proofs.json"
bash -n "$GITEA_PREPARE"

# Shadow soak ledger (H4): offline proofs over recorded API responses; no
# network. Contract: docs/GITEA_CI_SOAK_FAILURES.md.
python3 -m py_compile "$DEPLOY_DIR/gitea-shadow-soak.py" "$DEPLOY_DIR/shadow_soak_ledger.py" "$DEPLOY_DIR/shadow_soak_sources.py"
python3 "$DEPLOY_DIR/security-tests/test_gitea_shadow_soak.py" --artifact "$fixture/gitea-shadow-soak-proofs.json"

# The shared composite keeps uploading on GitHub unless a caller opts out.
contains "default: 'true'" "$BROWSER_ACTION"
count_contains 1 "if: always() && inputs.upload-evidence == 'true'" "$BROWSER_ACTION"
count_contains 1 'actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1' "$BROWSER_ACTION"

# Parity: every command of GitHub's two checks steps must run in the shadow
# job too, so the shadow cannot pass by doing less. The only reviewed change
# is the standalone Compose binary on a runner without a Docker CLI.
python3 - "$WORKFLOW" "$GITEA_WORKFLOW" <<'PARITY' || fail 'Gitea checks job diverged from GitHub checks commands'
import re, sys
from pathlib import Path

def commands(path, step):
    text = Path(path).read_text()
    match = re.search(rf"- name: {re.escape(step)}\n(?:        [^\n]*\n)*?        run: \|\n((?:          [^\n]*\n)+)", text)
    if not match:
        sys.exit(f"step {step!r} not found in {path}")
    lines = [line.strip() for line in match.group(1).splitlines()]
    return [line for line in lines if line and not line.startswith("#")]

for step in ("Frontend checks and build", "Embed frontend and run Go checks"):
    github = [c.replace("docker compose config", "docker-compose config") for c in commands(sys.argv[1], step)]
    gitea = commands(sys.argv[2], step)
    if github != gitea:
        sys.exit(f"{step}: GitHub {github} != Gitea {gitea}")
print("gitea_checks_parity=ok")
PARITY
printf 'gitea_shadow_policy=ok\n'
