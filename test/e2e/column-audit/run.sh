#!/usr/bin/env bash
# TC-310 column-audit browser journeys against the real Helm router/store/worker.
# Usage: test/e2e/column-audit/run.sh [playwright args...]
#   HELM_COLUMN_AUDIT_DIST=/abs/built/dist  serve a production build (default: Vite dev on :5310)
#   HELM_COLUMN_AUDIT_ARTIFACTS=/abs/dir    screenshots, traces and sanitized receipts
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
addr=${HELM_COLUMN_AUDIT_BROWSER_ADDR:-127.0.0.1:18310}
go=${HELM_GO:-go}
work=$(mktemp -d)
artifacts=${HELM_COLUMN_AUDIT_ARTIFACTS:-"$root/web/e2e-artifacts/column-audit"}
mkdir -p "$artifacts"
harness_pid= vite_pid=
cleanup() {
  local status=$?
  curl -s -X POST "http://$addr/__columnaudit/stop" >/dev/null 2>&1 || true
  for pid in "$harness_pid" "$vite_pid"; do
    [[ -n "$pid" ]] && { kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }
  done
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
(cd "$root" && "$go" test -c -o "$work/httpapi.test" ./internal/httpapi)
if [[ -z "${HELM_COLUMN_AUDIT_DIST:-}" ]]; then
  (cd "$root/web" && exec node node_modules/vite/bin/vite.js --port 5310 --strictPort --host 127.0.0.1) >"$artifacts/vite.log" 2>&1 &
  vite_pid=$!
  export HELM_COLUMN_AUDIT_VITE=http://127.0.0.1:5310
fi
(cd "$root/internal/httpapi" && HELM_COLUMN_AUDIT_BROWSER_ADDR=$addr exec "$work/httpapi.test" \
  -test.run TestColumnAuditBrowserHarness -test.count=1 -test.timeout 60m) >"$artifacts/harness.log" 2>&1 &
harness_pid=$!
for _ in $(seq 1 60); do
  curl -sf "http://$addr/readyz" >/dev/null && break
  kill -0 "$harness_pid" 2>/dev/null || { echo "harness exited; see $artifacts/harness.log" >&2; exit 1; }
  sleep 1
done
cd "$root/web"
HELM_COLUMN_AUDIT_E2E=1 HELM_E2E_BASE_URL="http://$addr" HELM_COLUMN_AUDIT_ARTIFACTS="$artifacts" \
  npx playwright test -c e2e/column-audit.config.ts "$@"
