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
# One phase: start the harness in a credential mode, run its specs, stop it.
# The device-flow phase runs second, on the same port, after the state
# directory is emptied, and keeps its evidence in a device-flow/ subdirectory.
run_phase() {
  local mode=$1 evidence=$2
  shift 2
  mkdir -p "$evidence"
  local mode_args=()
  [[ "$mode" == operator-token ]] || mode_args=(-credential-mode "$mode")
  GOMAXPROCS="${HELM_E2E_SERVER_GOMAXPROCS:-2}" \
    "$binary" -addr "127.0.0.1:$port" -work-dir "$work_dir" "${mode_args[@]}" >"$evidence/server.log" 2>&1 &
  server_pid=$!
  for attempt in $(seq 1 30); do
    if kill -0 "$server_pid" 2>/dev/null && curl --fail --silent --max-time 2 "http://127.0.0.1:$port/readyz" >/dev/null; then break; fi
    if ! kill -0 "$server_pid" 2>/dev/null || [[ "$attempt" == 30 ]]; then
      printf 'GitHub beta harness did not become ready; inspect %s/server.log\n' "$evidence" >&2
      exit 1
    fi
    sleep 1
  done
  (
    cd "$root/web"
    HELM_GITHUB_BETA_E2E=1 HELM_E2E_BASE_URL="http://127.0.0.1:$port" HELM_E2E_ARTIFACT_DIR="$evidence" \
      npx playwright test "$@" --workers=1
  ) &
  browser_pid=$!
  wait "$browser_pid" || result=$?
  browser_pid=
  # Secrets must not reach SQLite; a user code must not reach SQLite or the
  # log. Scan while the files still exist, without printing any secret.
  scan_state "$evidence/server.log" || result=1
  kill -TERM "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  server_pid=
}
scan_state() {
  python3 - "$work_dir" "$1" <<'PYSTATE'
import pathlib, re, sys
secrets = [b'HELM_GITHUB_BETA_SENTINEL_DO_NOT_EXPOSE_74bf8c', b'HELM_DEVICE_ACCESS_SENTINEL_DO_NOT_EXPOSE_9d41e2',
           b'HELM_DEVICE_REFRESH_SENTINEL_DO_NOT_EXPOSE_5ac07b', b'HELM_DEVICE_CODE_SENTINEL_DO_NOT_EXPOSE_e37f10']
code = re.compile(rb'WPFV-[0-9]{4}')
leaks = []
files = [p for p in pathlib.Path(sys.argv[1]).glob('helm.db*') if p.is_file()] + [pathlib.Path(sys.argv[2])]
for path in files:
    data = path.read_bytes()
    if any(secret in data for secret in secrets) or code.search(data):
        leaks.append(path.name)
if leaks:
    print('Synthetic secret or user code reached harness state: ' + ', '.join(leaks), file=sys.stderr)
    sys.exit(1)
PYSTATE
}
operator_specs=()
device_specs=()
if [[ "$#" == 0 ]]; then
  operator_specs=(e2e/github-beta-integration.spec.ts e2e/github-agent-audit.spec.ts e2e/github-branches.spec.ts)
  device_specs=(e2e/github-device-flow-beta.spec.ts)
fi
# Spec operands choose the phases; every other argument (an option or its
# value) goes to each phase. Without a spec operand, the arguments run in the
# operator-token phase exactly as before.
options=()
for arg in "$@"; do
  if [[ "$arg" == *github-device-flow-beta.spec.ts ]]; then device_specs+=("$arg")
  elif [[ "$arg" == *.spec.ts ]]; then operator_specs+=("$arg")
  else options+=("$arg"); fi
done
if (("$#" && ! ${#operator_specs[@]} && ! ${#device_specs[@]})); then operator_specs=("$@") options=(); fi
result=0
if ((${#operator_specs[@]})); then
  run_phase operator-token "$artifact_dir" "${operator_specs[@]}" "${options[@]}"
fi
if ((${#device_specs[@]})); then
  find "$work_dir" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
  run_phase device-flow "$artifact_dir/device-flow" "${device_specs[@]}" "${options[@]}"
fi
# Scan compressed traces as well as plain-text logs/responses. Do not print the
# sentinel or credential contents, including when a leak is detected.
python3 - "$artifact_dir" <<'PY' || result=1
import pathlib, sys, zipfile
sentinels = [b'HELM_GITHUB_BETA_SENTINEL_DO_NOT_EXPOSE_74bf8c', b'HELM_DEVICE_ACCESS_SENTINEL_DO_NOT_EXPOSE_9d41e2',
             b'HELM_DEVICE_REFRESH_SENTINEL_DO_NOT_EXPOSE_5ac07b', b'HELM_DEVICE_CODE_SENTINEL_DO_NOT_EXPOSE_e37f10']
found = lambda data: any(sentinel in data for sentinel in sentinels)
leaks = []
for path in pathlib.Path(sys.argv[1]).rglob('*'):
    if not path.is_file():
        continue
    if found(path.read_bytes()):
        leaks.append(str(path))
    if zipfile.is_zipfile(path):
        with zipfile.ZipFile(path) as archive:
            if any(found(archive.read(name)) for name in archive.namelist()):
                leaks.append(str(path))
if leaks:
    print('Synthetic credential leaked into artifacts: ' + ', '.join(leaks), file=sys.stderr)
    sys.exit(1)
PY
if grep -rqF --include=server.log 'WARNING: DATA RACE' "$artifact_dir"; then
  printf 'Go race detector reported a data race\n' >&2
  result=1
fi
printf 'GitHub beta artifacts: %s\n' "$artifact_dir"
exit "$result"
