#!/usr/bin/env bash
# Deploys the Helm sign-in relay Worker to <account>.workers.dev.
# Usage: CLOUDFLARE_API_TOKEN_FILE=… CLOUDFLARE_ACCOUNT_ID=… ./deploy.sh
# The token needs Account › Workers Scripts › Edit. The token is read from a
# file and never printed. See README.md.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
name=${HELM_RELAY_WORKER_NAME:-helm-connect}
api=https://api.cloudflare.com/client/v4
: "${CLOUDFLARE_ACCOUNT_ID:?set CLOUDFLARE_ACCOUNT_ID}"
: "${CLOUDFLARE_API_TOKEN_FILE:?set CLOUDFLARE_API_TOKEN_FILE}"
token=$(tr -d '\r\n' < "$CLOUDFLARE_API_TOKEN_FILE")
auth=(-H "Authorization: Bearer $token")
metadata='{"main_module":"worker.js","compatibility_date":"2026-09-01"}'
curl --fail-with-body -sS -X PUT "${auth[@]}" "$api/accounts/$CLOUDFLARE_ACCOUNT_ID/workers/scripts/$name" \
	-F "metadata=$metadata;type=application/json" \
	-F "worker.js=@$here/worker.js;type=application/javascript+module" >/dev/null
curl --fail-with-body -sS -X POST "${auth[@]}" -H 'Content-Type: application/json' \
	"$api/accounts/$CLOUDFLARE_ACCOUNT_ID/workers/scripts/$name/subdomain" \
	-d '{"enabled":true,"previews_enabled":false}' >/dev/null
subdomain=$(curl -sS "${auth[@]}" "$api/accounts/$CLOUDFLARE_ACCOUNT_ID/workers/subdomain" | tr -d '\n' | sed -n 's/.*"subdomain" *: *"\([^"]*\)".*/\1/p')
printf 'deployed https://%s.%s.workers.dev/cloudflare/callback\n' "$name" "$subdomain"
