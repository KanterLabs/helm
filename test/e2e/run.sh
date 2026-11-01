#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# Whole-suite runs use four isolated fixtures. Focused invocations preserve
# their scope and default to one, unless the caller explicitly requests more.
default_workers=4
[[ "$#" == 0 ]] || default_workers=1
workers=${HELM_E2E_WORKERS:-$default_workers}
base_port=${HELM_E2E_BASE_PORT:-18080}
include_deadlines=${HELM_E2E_INCLUDE_DEADLINES:-false}
[[ "$include_deadlines" == true || "$include_deadlines" == false ]] || {
  printf 'HELM_E2E_INCLUDE_DEADLINES must be true or false\n' >&2; exit 64;
}
[[ "$include_deadlines" != true || "$#" == 0 ]] || {
  printf 'Deadline coordination requires a full suite invocation\n' >&2; exit 64;
}
[[ "$workers" =~ ^[1-8]$ ]] || { printf 'HELM_E2E_WORKERS must be 1..8\n' >&2; exit 64; }
[[ "$base_port" =~ ^[0-9]{1,5}$ ]] || { printf 'Invalid HELM_E2E_BASE_PORT\n' >&2; exit 64; }
base_port=$((10#$base_port))
((base_port >= 1024 && base_port + (workers + 2) * 100 + 30 <= 65535)) || {
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
  # All reporter, console and database-copy writers have finished. Do not
  # retain credential-bearing failure output for the CI uploader.
  python3 "$root/test/e2e/evidence_credentials.py" "$artifact_dir" "$work_dir" || exit_status=1
  {
    printf 'revision=%s\n' "${HELM_E2E_REVISION:-$(git -C "$root" rev-parse HEAD)}"
    printf 'command=test/e2e/run.sh\n'
    printf 'exit_status=%s\n' "$exit_status"
    printf 'suite_results=suites/suites.json\n'
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

python3 -m unittest discover -s "$root/test/e2e" -p 'test_*.py' \
  2>&1 | tee "$artifact_dir/supervisor-checks.log"

if [[ "${HELM_E2E_SKIP_WEB_BUILD:-false}" != true ]]; then
  (cd "$root/web" && npm run build)
fi
rm -rf -- "$embedded_dist"
install -d "$embedded_dist"
cp -a "$root/web/dist/." "$embedded_dist/"

bundle=${HELM_E2E_PREPARED_DIR:-"$work_dir/prepared"}
[[ "$bundle" = /* ]] || bundle="$root/$bundle"
if [[ -n "${HELM_E2E_BINARY:-}" && -z "${HELM_E2E_PREPARED_DIR:-}" ]]; then
  printf 'HELM_E2E_BINARY requires its verified HELM_E2E_PREPARED_DIR bundle; use prepare.py prepare.\n' >&2
  exit 64
fi
if ! command -v go >/dev/null 2>&1 && [[ -z "${HELM_E2E_GO_COMMAND:-}" ]]; then
  command -v docker >/dev/null 2>&1 || { printf 'Helm E2E requires Go or Docker\n' >&2; exit 69; }
  export HELM_E2E_DOCKER_WORK_DIR="$work_dir"
  export HELM_E2E_DOCKER_BUNDLE_PARENT="$(dirname "$bundle")"
  HELM_E2E_GO_COMMAND=$(python3 -c 'import shlex,sys; print(shlex.quote(sys.argv[1]))' "$root/test/e2e/go-command.sh")
  export HELM_E2E_GO_COMMAND
fi
prepare_args=(--root "$root" --output "$bundle" "--go-flags=${HELM_E2E_GOFLAGS:-}")
[[ "$#" != 0 ]] || prepare_args+=(--github)
prepare_action=prepare
[[ ! -e "$bundle" ]] || prepare_action=verify
python3 "$root/test/e2e/prepare.py" "$prepare_action" "${prepare_args[@]}" > "$work_dir/prepared-paths.json" &
supervisor_pid=$!
prepare_status=0
wait "$supervisor_pid" || prepare_status=$?
supervisor_pid=
((prepare_status == 0)) || exit "$prepare_status"
binary=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["helm"])' "$work_dir/prepared-paths.json")
if [[ -n "${HELM_E2E_BINARY:-}" ]]; then
  supplied=$HELM_E2E_BINARY
  [[ "$supplied" = /* ]] || supplied="$root/$supplied"
  [[ "$(realpath "$supplied")" == "$binary" ]] || { printf 'Supplied binary is outside the verified bundle\n' >&2; exit 64; }
fi
cp "$bundle/metadata.json" "$artifact_dir/build-provenance.json"
suite_args=(--workers "$workers" --base-port "$base_port" --work-dir "$work_dir/suites"
  --artifact-dir "$artifact_dir/suites" --binary "$binary")
[[ "$#" != 0 ]] || suite_args+=(--github-binary "$bundle/helm-githubbeta")
[[ "$include_deadlines" != true ]] || suite_args+=(--include-deadline)
python3 "$root/test/e2e/run-suites.py" "${suite_args[@]}" -- "$@" &
supervisor_pid=$!
result=0
wait "$supervisor_pid" || result=$?
supervisor_pid=
exit "$result"
