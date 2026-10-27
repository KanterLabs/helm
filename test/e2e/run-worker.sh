#!/usr/bin/env bash
# One isolated runtime fixture. The coordinator owns builds and asset changes.
set -Eeuo pipefail

if [[ "$#" -lt 5 ]]; then
  printf 'Usage: run-worker.sh AUTH_MODE BASE_PORT WORK_DIR ARTIFACT_DIR BINARY [Playwright args...]\n' >&2
  exit 64
fi
auth_mode=$1
base_port=$2
work_dir=$3
artifact_dir=$4
binary=$5
shift 5
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
[[ "$auth_mode" == disabled || "$auth_mode" == local ]] || { printf 'Invalid worker auth mode\n' >&2; exit 64; }
[[ "$base_port" =~ ^[0-9]{1,5}$ ]] || { printf 'Invalid worker base port\n' >&2; exit 64; }
base_port=$((10#$base_port))
((base_port >= 1024 && base_port <= 65505)) || { printf 'Worker port block is outside the allowed range\n' >&2; exit 64; }
[[ -d "$work_dir" ]] || { printf 'Worker directory must already exist\n' >&2; exit 64; }
work_dir=$(cd "$work_dir" && pwd)
[[ -z "$(find "$work_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]] || { printf 'Worker directory must be empty\n' >&2; exit 64; }
chmod 0700 "$work_dir"
install -d -m 0755 "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
[[ -z "$(find "$artifact_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]] || { printf 'Worker artifact directory must be empty\n' >&2; exit 64; }
[[ "$work_dir" != "$artifact_dir" ]] || { printf 'Worker state and evidence directories must differ\n' >&2; exit 64; }
[[ "$binary" = /* ]] || binary="$root/$binary"
[[ -x "$binary" && -x "$root/test/e2e/fake-codex" ]] || { printf 'Worker binaries must be executable\n' >&2; exit 64; }

cf_port=$((base_port + 10))
hooks_port=$((base_port + 20))
relay_port=$((base_port + 30))
origin="http://127.0.0.1:$base_port"
cf_origin="http://127.0.0.1:$cf_port"
hooks_origin="http://127.0.0.1:$hooks_port"
relay_url="http://127.0.0.1:$relay_port/cloudflare/callback"
started_at=$(date +%s)
server_pid=
fake_cf_pid=
relay_pid=
browser_pid=
coolify_secret=
fake_cf_token=
fake_cf_zoneless=
fake_cf_norules=
server_log="$work_dir/$auth_mode-server.log"
server_db="$work_dir/$auth_mode-roadmap.db"

collect_evidence() {
  local exit_status=$?
  trap - EXIT TERM INT
  set +e
  # The coordinator also signals this worker's process group on cancellation.
  # Stop Helm before copying SQLite so its WAL is checkpointed and closed.
  local pid file secret
  for pid in "$browser_pid" "$server_pid" "$fake_cf_pid" "$relay_pid"; do
    if [[ -n "$pid" ]]; then
      kill -TERM "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  for file in "$work_dir"/*.log "$work_dir"/*.db "$work_dir"/*.db-wal "$work_dir"/*.db-shm; do
    if [[ -f "$file" ]]; then
      cp -a "$file" "$artifact_dir/" || exit_status=1
    fi
  done
  for secret in "$coolify_secret" "$fake_cf_token" "$fake_cf_zoneless" "$fake_cf_norules"; do
    if [[ -n "$secret" ]] && grep -rqF -- "$secret" "$artifact_dir"; then
      printf 'Worker credential leaked into retained evidence\n' >&2
      exit_status=1
    fi
  done
  if grep -rqF 'WARNING: DATA RACE' "$artifact_dir"; then
    printf 'Worker Go race detector reported a data race\n' >&2
    exit_status=1
  fi
  {
    printf 'auth_mode=%s\n' "$auth_mode"
    printf 'base_port=%s\n' "$base_port"
    printf 'started_at_epoch=%s\n' "$started_at"
    printf 'finished_at_epoch=%s\n' "$(date +%s)"
    printf 'elapsed_seconds=%s\n' "$(( $(date +%s) - started_at ))"
    printf 'exit_status=%s\n' "$exit_status"
  } > "$artifact_dir/worker.metadata"
  exit "$exit_status"
}
trap collect_evidence EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# Bind all four sockets together before starting anything. In particular, a
# readyz response from an existing Helm process must never authorize a test run.
python3 - "$base_port" "$cf_port" "$hooks_port" "$relay_port" <<'PY'
import socket
import sys

sockets = []
try:
    for value in sys.argv[1:]:
        listener = socket.socket()
        sockets.append(listener)
        # Match the real servers: recently closed connections are safe to reuse,
        # but a live listener must still make bind/listen fail.
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind(("127.0.0.1", int(value)))
        listener.listen()
except OSError:
    print("Worker port block is occupied", file=sys.stderr)
    sys.exit(69)
finally:
    for listener in sockets:
        listener.close()
PY

coolify_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
coolify_secret_file="$work_dir/coolify-webhook-secret"
(umask 077 && printf '%s\n' "$coolify_secret" > "$coolify_secret_file")
fake_cf_token="cf-e2e-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
fake_cf_zoneless="cf-e2e-zoneless-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
fake_cf_norules="cf-e2e-norules-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
FAKE_OAUTH_CLIENT_ID=e2e-client FAKE_OAUTH_REDIRECT="$relay_url" \
  python3 "$root/test/e2e/fake-cloudflare" "$cf_port" "$fake_cf_token" "$fake_cf_zoneless" "$fake_cf_norules" \
  >"$work_dir/cloudflare.private" 2>&1 &
fake_cf_pid=$!
node "$root/test/e2e/relay-server.mjs" "$relay_port" >"$work_dir/relay.private" 2>&1 &
relay_pid=$!
cloudflared_wrapper="$work_dir/cloudflared"
printf '#!/usr/bin/env bash\nexec %q %q "$@"\n' "$root/test/e2e/fake-cloudflared" "$work_dir/fake-cloudflared.jsonl" > "$cloudflared_wrapper"
chmod +x "$cloudflared_wrapper"

HELM_PUBLIC_HOOKS_ADDR="127.0.0.1:$hooks_port" \
  HELM_CLOUDFLARE_API_BASE="$cf_origin/client/v4" \
  HELM_CLOUDFLARED_BINARY="$cloudflared_wrapper" \
  HELM_PUBLIC_PROBE_ORIGIN="$hooks_origin" \
  HELM_CLOUDFLARE_OAUTH_CLIENT_ID=e2e-client \
  HELM_CLOUDFLARE_OAUTH_RELAY_URL="$relay_url" \
  HELM_CLOUDFLARE_DASHBOARD_URL="$cf_origin" \
  HELM_COOLIFY_WEBHOOK_SECRET_FILE="$coolify_secret_file" \
  HELM_COOLIFY_PROJECT=COOLIFYE2E \
  HELM_COOLIFY_ASSIGNEE=actor-disabled-mode \
  HELM_ADDR="127.0.0.1:$base_port" \
  HELM_DB="$server_db" \
  HELM_AUTH_MODE="$auth_mode" \
  HELM_PUBLIC_ORIGIN="$origin" \
  HELM_SECURE_COOKIES=false \
  HELM_DEMO_SEED=false \
  HELM_LUNA_ENABLED=true \
  HELM_CODEX_BINARY="$root/test/e2e/fake-codex" \
  HELM_CODEX_HOME_ROOT="$work_dir/$auth_mode-codex-users" \
  "$binary" >"$server_log" 2>&1 &
server_pid=$!

ready=false
for attempt in $(seq 1 30); do
  for pid in "$server_pid" "$fake_cf_pid" "$relay_pid"; do
    kill -0 "$pid" >/dev/null 2>&1 || { printf 'Worker fixture exited during startup\n' >&2; exit 1; }
  done
  if curl --fail --silent --max-time 2 "$origin/readyz" >/dev/null && \
    curl --fail --silent --max-time 2 "$cf_origin/__state" >/dev/null && \
    curl --silent --output /dev/null --max-time 2 "$relay_url"; then
    ready=true
    break
  fi
  sleep 1
done
[[ "$ready" == true ]] || { printf 'Worker fixtures did not become ready\n' >&2; exit 1; }
for pid in "$server_pid" "$fake_cf_pid" "$relay_pid"; do
  kill -0 "$pid" >/dev/null 2>&1 || { printf 'Worker fixture exited before browser tests\n' >&2; exit 1; }
done

(
  cd "$root/web"
  export HELM_E2E_BASE_URL="$origin" ROADMAP_E2E_BASE_URL="$origin"
  export HELM_E2E_DB="$server_db" HELM_E2E_SERVER_LOG="$server_log"
  export HELM_E2E_HOOKS_URL="$hooks_origin" HELM_E2E_FAKE_CF_URL="$cf_origin"
  export HELM_E2E_FAKE_CF_TOKEN="$fake_cf_token"
  export HELM_E2E_FAKE_CF_ZONELESS_TOKEN="$fake_cf_zoneless"
  export HELM_E2E_FAKE_CF_NORULES_TOKEN="$fake_cf_norules"
  export HELM_E2E_CLOUDFLARED_LOG="$work_dir/fake-cloudflared.jsonl"
  export HELM_E2E_ARTIFACT_DIR="$artifact_dir"
  export HELM_E2E_COOLIFY_SECRET="$coolify_secret" HELM_E2E_COOLIFY_PROJECT=COOLIFYE2E
  npm run e2e -- "$@" 2>&1 | tee "$artifact_dir/browser.console.log"
) &
browser_pid=$!
wait "$browser_pid"
browser_pid=
