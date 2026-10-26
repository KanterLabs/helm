#!/usr/bin/env bash
# Run only after the parent has built current frontend assets and this harness.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary=${HELM_GITHUB_BETA_BINARY:-}
if [[ -z "$binary" || ! -x "$binary" ]]; then
  printf 'Set HELM_GITHUB_BETA_BINARY to a prebuilt test/githubbeta executable.\n' >&2
  exit 2
fi
artifact_dir=${HELM_E2E_ARTIFACT_DIR:-"$root/.artifacts/github-beta/$(date -u +%Y%m%dT%H%M%SZ)"}
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
work_dir=$(mktemp -d)
server_pid=
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT
port=${HELM_GITHUB_BETA_PORT:-18082}
"$binary" -addr "127.0.0.1:$port" -work-dir "$work_dir" >"$artifact_dir/server.log" 2>&1 &
server_pid=$!
for attempt in $(seq 1 30); do
  if curl --fail --silent "http://127.0.0.1:$port/readyz" >/dev/null; then break; fi
  if ! kill -0 "$server_pid" 2>/dev/null || [[ "$attempt" == 30 ]]; then
    printf 'GitHub beta harness did not become ready; inspect %s/server.log\n' "$artifact_dir" >&2
    exit 1
  fi
  sleep 1
done
if [[ "$#" == 0 ]]; then set -- e2e/github-beta-integration.spec.ts; fi
result=0
(
  cd "$root/web"
  HELM_GITHUB_BETA_E2E=1 HELM_E2E_BASE_URL="http://127.0.0.1:$port" HELM_E2E_ARTIFACT_DIR="$artifact_dir" \
    npx playwright test "$@"
) || result=$?
# Scan compressed traces as well as plain-text logs/responses. Do not print the
# sentinel or credential contents, including when a leak is detected.
python3 - "$artifact_dir" <<'PY' || result=1
import pathlib, sys, zipfile
sentinel = b'HELM_GITHUB_BETA_SENTINEL_DO_NOT_EXPOSE_74bf8c'
leaks = []
for path in pathlib.Path(sys.argv[1]).rglob('*'):
    if not path.is_file():
        continue
    if sentinel in path.read_bytes():
        leaks.append(str(path))
    if zipfile.is_zipfile(path):
        with zipfile.ZipFile(path) as archive:
            if any(sentinel in archive.read(name) for name in archive.namelist()):
                leaks.append(str(path))
if leaks:
    print('Synthetic credential leaked into artifacts: ' + ', '.join(leaks), file=sys.stderr)
    sys.exit(1)
PY
if grep -qF 'WARNING: DATA RACE' "$artifact_dir/server.log"; then
  printf 'Go race detector reported a data race\n' >&2
  result=1
fi
printf 'GitHub beta artifacts: %s\n' "$artifact_dir"
exit "$result"
