#!/usr/bin/env bash
# Validate the beta guest's private Tailnet profile without contacting DNS,
# Cloudflare, or the public origin. The gateway runs this as root inside CT
# 106 after an install or rollback.
set -Eeuo pipefail

CONFIG_FILE=/etc/roadmap/roadmap.env
OWNER_CONFIG_FILE=/etc/roadmap/tailnet-owner.env
PRIVATE_ORIGIN=https://beta-helm.home.shanekanterman.dev
ASSERTION_KEY_SOURCE=/etc/roadmap/tailnet.key
TLS_CERT_SOURCE=/etc/roadmap/tailnet-origin.crt
TLS_KEY_SOURCE=/etc/roadmap/tailnet-origin.key
ASSERTION_KEY_RUNTIME=$ASSERTION_KEY_SOURCE
TLS_KEY_RUNTIME=$TLS_KEY_SOURCE

fail() {
	printf '[helm-beta-private-ready] %s\n' "$*" >&2
	exit 1
}

single_kv_value() {
	local key=$1
	awk -F= -v key="$key" '
		$1 == key { count++; value = substr($0, index($0, "=") + 1) }
		END {
			if (count != 1) exit 1
			print value
		}
	' "$CONFIG_FILE"
}

regular_file() {
	local path=$1 label=$2
	[[ -f "$path" && ! -L "$path" ]] || fail "$label is missing or not a regular file"
}

[[ "$(id -u)" -eq 0 ]] || fail 'must run as root'
regular_file "$CONFIG_FILE" roadmap-environment
[[ "$(stat -c '%U:%G' -- "$CONFIG_FILE")" = root:roadmap ]] || fail 'roadmap environment owner is invalid'
[[ "$(stat -c '%a' -- "$CONFIG_FILE")" = 640 ]] || fail 'roadmap environment mode is invalid'
regular_file "$OWNER_CONFIG_FILE" Tailnet-owner-environment
[[ "$(stat -c '%U:%G' -- "$OWNER_CONFIG_FILE")" = root:root ]] || fail 'Tailnet owner environment owner is invalid'
[[ "$(stat -c '%a' -- "$OWNER_CONFIG_FILE")" = 600 ]] || fail 'Tailnet owner environment mode is invalid'

[[ "$(single_kv_value HELM_AUTH_MODE)" = tailnet ]] || fail 'beta guest is not configured for tailnet authentication'
[[ "$(single_kv_value ROADMAP_AUTH_MODE)" = tailnet ]] || fail 'beta guest has a conflicting legacy authentication mode'
for key in HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN HELM_TAILNET_AUDIENCE ROADMAP_TAILNET_AUDIENCE; do
	[[ "$(single_kv_value "$key")" = "$PRIVATE_ORIGIN" ]] || fail 'beta guest has an unexpected private origin'
done
[[ "$(single_kv_value HELM_TAILNET_ASSERTION_KEY_FILE)" = "$ASSERTION_KEY_RUNTIME" ]] || fail 'beta guest has an unexpected assertion-key runtime path'
[[ "$(single_kv_value ROADMAP_TAILNET_ASSERTION_KEY_FILE)" = "$ASSERTION_KEY_RUNTIME" ]] || fail 'beta guest has a conflicting assertion-key runtime path'
[[ "$(single_kv_value HELM_TAILNET_TLS_CERT_FILE)" = "$TLS_CERT_SOURCE" ]] || fail 'beta guest has an unexpected TLS certificate path'
[[ "$(single_kv_value ROADMAP_TAILNET_TLS_CERT_FILE)" = "$TLS_CERT_SOURCE" ]] || fail 'beta guest has a conflicting TLS certificate path'
[[ "$(single_kv_value HELM_TAILNET_TLS_KEY_FILE)" = "$TLS_KEY_RUNTIME" ]] || fail 'beta guest has an unexpected TLS key runtime path'
[[ "$(single_kv_value ROADMAP_TAILNET_TLS_KEY_FILE)" = "$TLS_KEY_RUNTIME" ]] || fail 'beta guest has a conflicting TLS key runtime path'

for key in HELM_AUTH_MODE ROADMAP_AUTH_MODE HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN \
	HELM_TAILNET_AUDIENCE ROADMAP_TAILNET_AUDIENCE \
	HELM_TAILNET_ASSERTION_KEY_FILE ROADMAP_TAILNET_ASSERTION_KEY_FILE \
	HELM_TAILNET_TLS_ADDR ROADMAP_TAILNET_TLS_ADDR \
	HELM_TAILNET_TLS_CERT_FILE ROADMAP_TAILNET_TLS_CERT_FILE \
	HELM_TAILNET_TLS_KEY_FILE ROADMAP_TAILNET_TLS_KEY_FILE \
	HELM_TAILNET_ALLOWED_PEER_IPS ROADMAP_TAILNET_ALLOWED_PEER_IPS \
	HELM_ADMIN_EMAIL ROADMAP_ADMIN_EMAIL HELM_TAILNET_OWNER_LOGIN ROADMAP_TAILNET_OWNER_LOGIN; do
	release_value=$(single_kv_value "$key") || fail "roadmap environment is missing $key"
	owner_value=$(awk -F= -v key="$key" '$1 == key { count++; value = substr($0, index($0, "=") + 1) } END { if (count != 1) exit 1; print value }' "$OWNER_CONFIG_FILE") ||
		fail "Tailnet owner environment is missing $key"
	[[ "$release_value" = "$owner_value" ]] || fail "Tailnet owner environment conflicts for $key"
done

regular_file "$ASSERTION_KEY_SOURCE" Tailnet-assertion-key
[[ "$(stat -c '%U:%G' -- "$ASSERTION_KEY_SOURCE")" = roadmap:roadmap ]] || fail 'Tailnet assertion key owner is invalid'
[[ "$(stat -c '%a' -- "$ASSERTION_KEY_SOURCE")" = 600 ]] || fail 'Tailnet assertion key mode is invalid'
[[ "$(stat -c '%s' -- "$ASSERTION_KEY_SOURCE")" -ge 32 ]] || fail 'Tailnet assertion key is too short'
regular_file "$TLS_CERT_SOURCE" Tailnet-TLS-certificate
[[ "$(stat -c '%U:%G' -- "$TLS_CERT_SOURCE")" = root:root ]] || fail 'Tailnet TLS certificate owner is invalid'
[[ "$(stat -c '%a' -- "$TLS_CERT_SOURCE")" = 644 ]] || fail 'Tailnet TLS certificate mode is invalid'
regular_file "$TLS_KEY_SOURCE" Tailnet-TLS-key
[[ "$(stat -c '%U:%G' -- "$TLS_KEY_SOURCE")" = roadmap:roadmap ]] || fail 'Tailnet TLS key owner is invalid'
[[ "$(stat -c '%a' -- "$TLS_KEY_SOURCE")" = 600 ]] || fail 'Tailnet TLS key mode is invalid'

systemctl is-active --quiet helm.service || fail 'helm.service is not active'
systemctl is-active --quiet roadmap.service || fail 'roadmap.service compatibility alias is not active'
if systemctl is-active --quiet cloudflared.service; then
	fail 'cloudflared.service is active in the private beta profile'
fi

command -v curl >/dev/null 2>&1 || fail 'curl is required'
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:8080/healthz >/dev/null \
	|| fail 'loopback health check failed'
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:8080/readyz >/dev/null \
	|| fail 'loopback readiness check failed'

work=$(mktemp -d /run/helm-beta-private-ready.XXXXXX)
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT
status=$(curl --silent --show-error --max-time 5 --output "$work/unauthenticated.json" \
	--write-out '%{http_code}' http://127.0.0.1:8080/api/v1/roadmap) \
	|| fail 'loopback unauthenticated auth probe failed'
[[ "$status" = 401 ]] || fail 'loopback unauthenticated auth probe did not return HTTP 401'
grep -Eq '"error"[[:space:]]*:[[:space:]]*\{[^}]*"code"[[:space:]]*:[[:space:]]*"unauthorized"' "$work/unauthenticated.json" \
	|| fail 'loopback unauthenticated auth probe returned an unexpected response'

printf 'beta_private_ready=ok\n'
