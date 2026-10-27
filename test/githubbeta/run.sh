#!/usr/bin/env bash
# Run only after the parent has built current frontend assets and this harness.
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary=${HELM_GITHUB_BETA_BINARY:-}
if [[ -z "$binary" || ! -x "$binary" ]]; then
  printf 'Set HELM_GITHUB_BETA_BINARY to a prebuilt test/githubbeta executable.\n' >&2
  exit 2
fi
artifact_dir=${HELM_E2E_ARTIFACT_DIR:-"$root/.artifacts/github-beta/$(date -u +%Y%m%dT%H%M%SZ)"}
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
work_dir=${HELM_GITHUB_BETA_WORK_DIR:-}
if [[ -n "$work_dir" ]]; then
  [[ ! -e "$work_dir" || -d "$work_dir" ]] || { printf 'Invalid GitHub state directory\n' >&2; exit 64; }
  install -d -m 0700 "$work_dir"
  [[ -z "$(find "$work_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]] || {
    printf 'GitHub state directory must be empty\n' >&2; exit 64;
  }
  chmod 0700 "$work_dir"
else
  work_dir=$(mktemp -d)
fi
started_at=$(date +%s)
browser_pid=
server_pid=
cleanup() {
  local exit_status=$?
  trap - EXIT TERM INT
  set +e
  for pid in "$browser_pid" "$server_pid"; do
    if [[ -n "$pid" ]]; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  {
    printf 'base_port=%s\n' "$port"
    printf 'started_at_epoch=%s\n' "$started_at"
    printf 'finished_at_epoch=%s\n' "$(date +%s)"
    printf 'elapsed_seconds=%s\n' "$(( $(date +%s) - started_at ))"
    printf 'exit_status=%s\n' "$exit_status"
  } > "$artifact_dir/worker.metadata"
  rm -rf "$work_dir"
  exit "$exit_status"
}
port=${HELM_GITHUB_BETA_PORT:-18082}
[[ "$port" =~ ^[0-9]{1,5}$ ]] || { printf 'Invalid GitHub fixture port\n' >&2; exit 64; }
port=$((10#$port))
((port >= 1024 && port <= 65535)) || { printf 'Invalid GitHub fixture port\n' >&2; exit 64; }
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
python3 - "$port" <<'PYPORT'
import socket, sys
with socket.socket() as listener:
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        listener.bind(("127.0.0.1", int(sys.argv[1])))
        listener.listen()
    except OSError:
        print("GitHub fixture port is occupied", file=sys.stderr)
        sys.exit(69)
PYPORT
GOMAXPROCS="${HELM_E2E_SERVER_GOMAXPROCS:-2}" \
  "$binary" -addr "127.0.0.1:$port" -work-dir "$work_dir" >"$artifact_dir/server.log" 2>&1 &
server_pid=$!
for attempt in $(seq 1 30); do
  if kill -0 "$server_pid" 2>/dev/null && curl --fail --silent --max-time 2 "http://127.0.0.1:$port/readyz" >/dev/null; then break; fi
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
    npx playwright test "$@" --workers=1
) &
browser_pid=$!
wait "$browser_pid" || result=$?
browser_pid=
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
