#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
requested_artifact_dir=${HELM_E2E_ARTIFACT_DIR:-}
if [[ -n "$requested_artifact_dir" ]]; then
  [[ "$requested_artifact_dir" = /* ]] || requested_artifact_dir="$root/$requested_artifact_dir"
  if [[ -e "$requested_artifact_dir" ]]; then
    printf 'HELM_E2E_ARTIFACT_DIR already exists: %s\n' "$requested_artifact_dir" >&2
    exit 64
  fi
  artifact_dir=$requested_artifact_dir
  install -d -m 0755 "$artifact_dir"
else
  install -d -m 0755 "$root/.artifacts"
  artifact_dir=$(mktemp -d "$root/.artifacts/helm-e2e.XXXXXX")
fi

work_dir=$(mktemp -d)
server_pid=
server_log=
server_db=
embedded_dist="$root/internal/webassets/dist"
embedded_backup="$work_dir/original-webassets-dist"
embedded_dist_existed=false
if [[ -d "$embedded_dist" ]]; then
  cp -a "$embedded_dist" "$embedded_backup"
  embedded_dist_existed=true
fi

stop_server() {
  if [[ -n "$server_pid" ]]; then
    kill -TERM "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" >/dev/null 2>&1 || true
    server_pid=
  fi
}

collect_evidence() {
  local exit_status=$?
  trap - EXIT
  stop_server
  for file in "$work_dir"/*.log "$work_dir"/*.db; do
    [[ -f "$file" ]] && cp -a "$file" "$artifact_dir/"
  done
  if [[ -n "${HELM_E2E_METADATA_FILE:-}" && -f "$HELM_E2E_METADATA_FILE" ]]; then
    cp -a "$HELM_E2E_METADATA_FILE" "$artifact_dir/source.metadata"
  fi
  {
    printf 'revision=%s\n' "${HELM_E2E_REVISION:-$(git -C "$root" rev-parse HEAD)}"
    printf 'command=test/e2e/run.sh\n'
    printf 'exit_status=%s\n' "$exit_status"
    printf 'auth_modes=disabled,local\n'
  } > "$artifact_dir/evidence.metadata"
  (
    cd "$artifact_dir"
    find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS
    sha256sum --check --strict SHA256SUMS
  ) || exit_status=1
  rm -rf -- "$embedded_dist"
  if [[ "$embedded_dist_existed" == true ]]; then
    cp -a "$embedded_backup" "$embedded_dist"
  fi
  rm -rf -- "$work_dir"
  printf 'Helm E2E evidence: %s\n' "$artifact_dir"
  exit "$exit_status"
}
trap collect_evidence EXIT

if [[ "${HELM_E2E_SKIP_WEB_BUILD:-false}" != true ]]; then
  (cd "$root/web" && npm run build)
fi
rm -rf -- "$embedded_dist"
install -d "$embedded_dist"
cp -a "$root/web/dist/." "$embedded_dist/"

binary=${HELM_E2E_BINARY:-}
if [[ -z "$binary" ]]; then
  binary="$work_dir/helm"
  read -r -a go_flags <<< "${HELM_E2E_GOFLAGS:-}"
  if command -v go >/dev/null 2>&1; then
    (cd "$root" && go build "${go_flags[@]}" -trimpath -o "$binary" ./cmd/helm)
  elif command -v docker >/dev/null 2>&1; then
    docker run --rm \
      --volume "$root:/src:ro" \
      --volume "$work_dir:/out" \
      --workdir /src \
      golang:1.25.14-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437 \
      go build "${go_flags[@]}" -buildvcs=false -trimpath -o /out/helm ./cmd/helm
  else
    printf 'Helm E2E requires Go or Docker to build the application\n' >&2
    exit 69
  fi
fi
[[ "$binary" = /* ]] || binary="$root/$binary"
[[ -x "$binary" ]] || { printf 'Helm E2E binary is not executable: %s\n' "$binary" >&2; exit 64; }
[[ -x "$root/test/e2e/fake-codex" ]] || { printf 'Codex E2E fixture is not executable\n' >&2; exit 64; }

start_server() {
  local port=$1
  local auth_mode=$2
  local name=$3
  server_log="$work_dir/$name-server.log"
  server_db="$work_dir/$name-roadmap.db"
  HELM_ADDR="127.0.0.1:$port" \
    HELM_DB="$server_db" \
    HELM_AUTH_MODE="$auth_mode" \
    HELM_PUBLIC_ORIGIN="http://127.0.0.1:$port" \
    HELM_SECURE_COOKIES=false \
    HELM_DEMO_SEED=false \
    HELM_LUNA_ENABLED=true \
    HELM_CODEX_BINARY="$root/test/e2e/fake-codex" \
    HELM_CODEX_HOME_ROOT="$work_dir/$name-codex-users" \
    "$binary" >"$server_log" 2>&1 &
  server_pid=$!
  for attempt in $(seq 1 30); do
    if curl --fail --silent "http://127.0.0.1:$port/readyz" >/dev/null; then
      return 0
    fi
    if [[ "$attempt" -eq 30 ]]; then
      cat "$server_log" >&2
      return 1
    fi
    sleep 1
  done
}

start_server 18080 disabled disabled
(
  cd "$root/web"
  HELM_E2E_BASE_URL=http://127.0.0.1:18080 \
    HELM_E2E_DB="$server_db" \
    HELM_E2E_ARTIFACT_DIR="$artifact_dir/disabled" \
    npm run e2e -- "$@"
)
stop_server

start_server 18081 local local
(
  cd "$root/web"
  HELM_E2E_BASE_URL=http://127.0.0.1:18081 \
    HELM_E2E_ARTIFACT_DIR="$artifact_dir/local" \
    npx playwright test e2e/onboarding.spec.ts
)
stop_server

if rg -n 'WARNING: DATA RACE' "$work_dir"/*.log; then
  printf 'Go race detector reported a data race\n' >&2
  exit 1
fi
