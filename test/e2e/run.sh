#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# Whole-suite runs use four isolated fixtures. Focused invocations preserve
# their scope and default to one, unless the caller explicitly requests more.
default_workers=4
[[ "$#" == 0 ]] || default_workers=1
workers=${HELM_E2E_WORKERS:-$default_workers}
base_port=${HELM_E2E_BASE_PORT:-18080}
[[ "$workers" =~ ^[1-8]$ ]] || { printf 'HELM_E2E_WORKERS must be 1..8\n' >&2; exit 64; }
[[ "$base_port" =~ ^[0-9]{1,5}$ ]] || { printf 'Invalid HELM_E2E_BASE_PORT\n' >&2; exit 64; }
base_port=$((10#$base_port))
((base_port >= 1024 && base_port + (workers - 1) * 100 + 30 <= 65535 && base_port + 31 <= 65535)) || {
  printf 'Browser fixture port blocks are outside the allowed range\n' >&2; exit 64;
}
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
supervisor_pid=
embedded_dist="$root/internal/webassets/dist"
embedded_backup="$work_dir/original-webassets-dist"
embedded_dist_existed=false
if [[ -d "$embedded_dist" ]]; then
  cp -a "$embedded_dist" "$embedded_backup"
  embedded_dist_existed=true
fi

collect_evidence() {
  local exit_status=$?
  trap - EXIT
  if [[ -n "$supervisor_pid" ]]; then
    kill -TERM "$supervisor_pid" >/dev/null 2>&1 || true
    wait "$supervisor_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "${HELM_E2E_METADATA_FILE:-}" && -f "$HELM_E2E_METADATA_FILE" ]]; then
    cp -a "$HELM_E2E_METADATA_FILE" "$artifact_dir/source.metadata"
  fi
  {
    printf 'revision=%s\n' "${HELM_E2E_REVISION:-$(git -C "$root" rev-parse HEAD)}"
    printf 'command=test/e2e/run.sh\n'
    printf 'exit_status=%s\n' "$exit_status"
    printf 'auth_modes=disabled,local\n'
    printf 'browser_workers=%s\n' "$workers"
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
trap 'exit 143' TERM
trap 'exit 130' INT

python3 "$root/test/e2e/test_run_browsers.py" 2>&1 | tee "$artifact_dir/supervisor-checks.log"

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
      golang:1.26.10-bookworm@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51 \
      go build "${go_flags[@]}" -buildvcs=false -trimpath -o /out/helm ./cmd/helm
  else
    printf 'Helm E2E requires Go or Docker to build the application\n' >&2
    exit 69
  fi
fi
[[ "$binary" = /* ]] || binary="$root/$binary"
[[ -x "$binary" ]] || { printf 'Helm E2E binary is not executable: %s\n' "$binary" >&2; exit 64; }
[[ -x "$root/test/e2e/fake-codex" ]] || { printf 'Codex E2E fixture is not executable\n' >&2; exit 64; }

run_browsers() {
  local auth_mode=$1 count=$2 port=$3
  shift 3
  python3 "$root/test/e2e/run-browsers.py" \
    --mode "$auth_mode" --workers "$count" --base-port "$port" \
    --work-dir "$work_dir/$auth_mode" --artifact-dir "$artifact_dir/$auth_mode" \
    --binary "$binary" -- "$@" &
  supervisor_pid=$!
  local result=0
  wait "$supervisor_pid" || result=$?
  supervisor_pid=
  return "$result"
}

run_browsers disabled "$workers" "$base_port" "$@"
run_browsers local 1 "$((base_port + 1))" e2e/onboarding.spec.ts

# The complete browser suite also exercises the real GitHub provider/worker
# HTTP path. External GitHub and inference are synthetic; no live token is used.
# Focused runs (for example account deadline checks) keep their existing scope.
if [[ "$#" == 0 ]]; then
  github_binary="$work_dir/helm-githubbeta"
  read -r -a github_go_flags <<< "${HELM_E2E_GOFLAGS:-}"
  if command -v go >/dev/null 2>&1; then
    (cd "$root" && go build "${github_go_flags[@]}" -trimpath -o "$github_binary" ./test/githubbeta)
  else
    docker run --rm --volume "$root:/src:ro" --volume "$work_dir:/out" --workdir /src \
      golang:1.26.10-bookworm@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51 \
      go build "${github_go_flags[@]}" -buildvcs=false -trimpath -o /out/helm-githubbeta ./test/githubbeta
  fi
  HELM_GITHUB_BETA_BINARY="$github_binary" HELM_E2E_ARTIFACT_DIR="$artifact_dir/github" \
    "$root/test/githubbeta/run.sh"
fi

# Each runtime worker checks its retained evidence for credential leaks and races.
