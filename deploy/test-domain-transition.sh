#!/usr/bin/env bash
# Network-free transition contract tests for the reviewed production phases.
set -Eeuo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
PROFILE="$ROOT_DIR/deploy/domain-profile.sh"
CLOUDFLARE="$ROOT_DIR/deploy/cloudflare.sh"
VALIDATE="$ROOT_DIR/deploy/validate-live.sh"

fail() {
	printf '[domain-transition] %s\n' "$*" >&2
	exit 1
}

assert_eq() {
	local expected=$1 actual=$2 message=${3:-values differ}
	[[ "$actual" = "$expected" ]] || fail "$message: expected <$expected>, got <$actual>"
}

assert_fails() {
	local output status
	set +e
	output=$("$@" 2>&1)
	status=$?
	set -e
	[[ "$status" -ne 0 ]] || fail "unexpected success: $*"
	printf '%s' "$output"
}

fixture=$(mktemp -d "${TMPDIR:-/tmp}/helm-domain-transition.XXXXXX")
cleanup_fixture() { rm -rf -- "$fixture"; }
trap cleanup_fixture EXIT

source "$PROFILE"
unset HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN HELM_PUBLIC_HOST ROADMAP_PUBLIC_HOST \
	HELM_PUBLIC_URL ROADMAP_PUBLIC_URL PUBLIC_HOST PUBLIC_URL HELM_LEGACY_ORIGIN ROADMAP_LEGACY_ORIGIN
domain_profile_load production
assert_eq legacy "$DOMAIN_PROFILE_PHASE" 'repository phase must remain legacy'
assert_eq 0 "$DOMAIN_PROFILE_DUAL_HOST" 'legacy production must remain single-host'
domain_profile_load beta
assert_eq beta.shanekanterman.dev "$PUBLIC_HOST" 'beta host must remain isolated'
assert_eq 0 "$DOMAIN_PROFILE_DUAL_HOST" 'beta must remain single-host'
assert_eq '' "$DOMAIN_PROFILE_CANONICAL_HOST" 'beta must not expose canonical host'

domain_profile_set_common
DOMAIN_PROFILE_ENVIRONMENT=production
unset HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN HELM_PUBLIC_HOST ROADMAP_PUBLIC_HOST \
	HELM_PUBLIC_URL ROADMAP_PUBLIC_URL PUBLIC_HOST PUBLIC_URL HELM_LEGACY_ORIGIN ROADMAP_LEGACY_ORIGIN
domain_profile_apply_phase staged
assert_eq staged "$DOMAIN_PROFILE_PHASE" 'staged profile phase'
assert_eq tc.shanekanterman.dev "$DOMAIN_PROFILE_PUBLIC_HOST" 'staged public host'
assert_eq 1 "$DOMAIN_PROFILE_DUAL_HOST" 'staged dual-host flag'
assert_eq 4 "$DOMAIN_PROFILE_AUDIENCE_COUNT" 'staged audience count'
assert_eq '' "$DOMAIN_PROFILE_LEGACY_ORIGIN" 'staged legacy origin'
staged_legacy_host=$DOMAIN_PROFILE_LEGACY_HOST
staged_canonical_host=$DOMAIN_PROFILE_CANONICAL_HOST
domain_profile_apply_phase canonical
assert_eq canonical "$DOMAIN_PROFILE_PHASE" 'canonical profile phase'
assert_eq helm.shanekanterman.dev "$DOMAIN_PROFILE_PUBLIC_HOST" 'canonical public host'
assert_eq https://tc.shanekanterman.dev "$DOMAIN_PROFILE_LEGACY_ORIGIN" 'canonical retained origin'
assert_eq 1 "$DOMAIN_PROFILE_DUAL_HOST" 'canonical dual-host flag'
canonical_public_url=$DOMAIN_PROFILE_PUBLIC_URL

# Pull only pure Cloudflare predicates and reconciliation helpers. Every API
# response below is supplied by cf_request; this test never contacts a
# provider.
source <(awk '/^validate_tunnel_config\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^configure_tunnel\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^capture_tunnel_before_state\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^tunnel_config_digest\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^assert_tunnel_config_unchanged\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^capture_tunnel_after_state\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^validate_tunnel_shape\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^reconcile_tunnel_config\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^validate_live_tunnel_config\(\)/,/^}/' "$VALIDATE")

ACCOUNT_ID=fixture-account
ZONE_ID=fixture-zone
PUBLIC_HOST=$staged_legacy_host
TUNNEL_BEFORE_STATE=
TUNNEL_BEFORE_CONFIG_DIGEST=
TUNNEL_LATEST_CONFIG=
TUNNEL_AFTER_CONFIG_DIGEST=
transition_team=team
transition_tunnel_id=tunnel-transition
transition_legacy_ui=legacy-ui
transition_legacy_api=legacy-api
transition_canonical_ui=canonical-ui
transition_canonical_api=canonical-api

make_single_config() {
	jq -cn --arg host "$staged_legacy_host" --arg team "$transition_team" \
		--arg ui "$transition_legacy_ui" --arg api "$transition_legacy_api" \
		'{result:{config:{ingress:[
			{hostname:$host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$ui,$api]}}},
			{service:"http_status:404"}
		]}}}'
}

make_dual_config() {
	jq -cn --arg legacy_host "$staged_legacy_host" --arg canonical_host "$staged_canonical_host" \
		--arg team "$transition_team" --arg legacy_ui "$transition_legacy_ui" --arg legacy_api "$transition_legacy_api" \
		--arg canonical_ui "$transition_canonical_ui" --arg canonical_api "$transition_canonical_api" \
		'{result:{config:{ingress:[
			{hostname:$legacy_host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$legacy_ui,$legacy_api]}}},
			{hostname:$canonical_host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$canonical_ui,$canonical_api]}}},
			{service:"http_status:404"}
		]}}}'
}

transition_single=$(make_single_config)
transition_dual=$(make_dual_config)
transition_response=$transition_single
transition_puts=0
transition_flip=0
transition_put_failure=0
transition_readback_failure=0
cf_request() {
	local method=$1 path=$2 body=${3:-}
	if [[ "$method" = GET && "$path" = "/accounts/$ACCOUNT_ID/cfd_tunnel/$transition_tunnel_id/configurations" ]]; then
		if [[ "$transition_readback_failure" = 1 && "$transition_puts" -gt 0 ]]; then return 1; fi
		if [[ "$transition_flip" = 1 && "$transition_puts" = 0 && -n "${TUNNEL_BEFORE_CONFIG_DIGEST:-}" ]]; then
			printf '%s' "$transition_dual"
		else
			printf '%s' "$transition_response"
		fi
		return 0
	fi
	if [[ "$method" = PUT && "$path" = "/accounts/$ACCOUNT_ID/cfd_tunnel/$transition_tunnel_id/configurations" ]]; then
		printf 'PUT\n' >> "$fixture/provider-writes"
		if [[ "$transition_put_failure" = 1 ]]; then return 1; fi
		transition_puts=$((transition_puts + 1))
		transition_response=$(jq -cn --argjson body "$body" '{success:true,result:{config:$body.config}}')
		printf '%s' '{"success":true,"result":{}}'
		return 0
	fi
	return 1
}

DOMAIN_PROFILE_PHASE=staged
DOMAIN_PROFILE_DUAL_HOST=1
PUBLIC_HOST=$staged_legacy_host
validate_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" "$transition_tunnel_id" \
	'' '' '' '' '' '' \
	"$staged_legacy_host" "$transition_single" \
	>"$fixture/single-valid.out" 2>&1 || fail 'reviewed single-host topology was rejected'
validate_tunnel_shape "$transition_team" "$transition_tunnel_id" "$staged_legacy_host" \
	>"$fixture/single-shape.out" 2>&1 || fail 'reviewed single-host shape was rejected'
validate_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" "$transition_tunnel_id" \
	"$staged_legacy_host" "$transition_legacy_ui" "$transition_legacy_api" \
	"$staged_canonical_host" "$transition_canonical_ui" "$transition_canonical_api" \
	"$staged_legacy_host" "$transition_dual" \
	>"$fixture/dual-valid.out" 2>&1 || fail 'reviewed dual-host topology was rejected'
transition_response=$transition_dual
validate_tunnel_shape "$transition_team" "$transition_tunnel_id" "$staged_legacy_host" "$staged_canonical_host" \
	>"$fixture/dual-shape.out" 2>&1 || fail 'reviewed dual-host shape was rejected'
validate_live_tunnel_config "$transition_tunnel_id" "$transition_team" "$staged_legacy_host" \
	"$transition_legacy_ui" "$transition_legacy_api" "$staged_canonical_host" \
	"$transition_canonical_ui" "$transition_canonical_api" \
	>"$fixture/live-dual-valid.out" 2>&1 || fail 'live dual-host predicate rejected reviewed topology'

unsafe_top_level=$(jq -c '.result.config["warp-routing"]={enabled:false}' <<<"$transition_single")
transition_response=$unsafe_top_level
assert_fails validate_tunnel_shape "$transition_team" "$transition_tunnel_id" "$staged_legacy_host" \
	>"$fixture/unsafe-top-level.out"
assert_fails validate_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" "$transition_tunnel_id" \
	>"$fixture/unsafe-top-level-validation.out"
unsafe_origin_key=$(jq -c '.result.config.ingress[0].originRequest.connectTimeout="10s"' <<<"$transition_single")
transition_response=$unsafe_origin_key
assert_fails validate_tunnel_shape "$transition_team" "$transition_tunnel_id" "$staged_legacy_host" \
	>"$fixture/unsafe-origin-key.out"
wrong_host=$(jq -c '.result.config.ingress[1].hostname="unexpected.example"' <<<"$transition_dual")
transition_response=$wrong_host
assert_fails validate_tunnel_shape "$transition_team" "$transition_tunnel_id" "$staged_legacy_host" "$staged_canonical_host" \
	>"$fixture/unsafe-host.out"

# A transition starts from the exact legacy topology, writes the reviewed dual
# topology once, and is idempotent once the dual topology is already present.
transition_response=$transition_single
transition_puts=0
transition_flip=0
reconcile_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" \
	"$transition_tunnel_id" 0 "$staged_legacy_host" "$transition_legacy_ui" "$transition_legacy_api" \
	"$staged_canonical_host" "$transition_canonical_ui" "$transition_canonical_api" \
	>"$fixture/reconcile-transition.out" 2>&1 || fail 'single-to-dual transition failed'
assert_eq 1 "$transition_puts" 'transition must write exactly once'
transition_response_before_idempotence=$transition_response
reconcile_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" \
	"$transition_tunnel_id" 0 "$staged_legacy_host" "$transition_legacy_ui" "$transition_legacy_api" \
	"$staged_canonical_host" "$transition_canonical_ui" "$transition_canonical_api" \
	>"$fixture/reconcile-idempotent.out" 2>&1 || fail 'dual-host reconcile was not idempotent'
assert_eq 1 "$transition_puts" 'idempotent dual-host reconcile must not write'
assert_eq "$transition_response_before_idempotence" "$transition_response" \
	'idempotent reconcile must preserve provider config'

# A changed complete config between the initial read and the pre-write read
# must fail closed without a PUT. This is a read/compare guard, not a CAS
# claim.
transition_response=$transition_single
transition_puts=0
transition_flip=1
TUNNEL_BEFORE_CONFIG_DIGEST=
: > "$fixture/provider-writes"
assert_fails reconcile_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" \
	"$transition_tunnel_id" 0 "$staged_legacy_host" "$transition_legacy_ui" "$transition_legacy_api" \
	"$staged_canonical_host" "$transition_canonical_ui" "$transition_canonical_api" \
	>"$fixture/reconcile-drift.out"
assert_eq 0 "$transition_puts" 'drift guard must not overwrite provider config'
[[ ! -s "$fixture/provider-writes" ]] || fail 'drift guard attempted a provider write'
transition_flip=0

# These functions run in an if/command-substitution context where Bash turns
# off errexit. A later successful GET must never hide a failed provider PUT.
for failure_mode in put readback; do
	transition_response=$transition_single
	transition_puts=0
	transition_put_failure=0
	transition_readback_failure=0
	[[ "$failure_mode" = put ]] && transition_put_failure=1
	[[ "$failure_mode" = readback ]] && transition_readback_failure=1
	assert_fails reconcile_tunnel_config "$transition_team" "$transition_legacy_ui" "$transition_legacy_api" \
		"$transition_tunnel_id" 0 "$staged_legacy_host" "$transition_legacy_ui" "$transition_legacy_api" \
		"$staged_canonical_host" "$transition_canonical_ui" "$transition_canonical_api" \
		>"$fixture/reconcile-$failure_mode-failure.out"
done
transition_put_failure=0
transition_readback_failure=0

# Render the signed owner environment for both transition phases. The legacy
# template remains unchanged because empty optional fields are omitted.
source <(awk '/^valid_email\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^validate_owner_env\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^restore_prepare_output\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^write_prepare_outputs\(\)/,/^}/' "$CLOUDFLARE")
TOKEN_OUTPUT="$fixture/transition.token"
OWNER_ENV_OUTPUT="$fixture/staged.owner.env"
cf_request() {
	printf '%s' '{"success":true,"result":"fixture-tunnel-token"}'
}
domain_profile_apply_phase staged
PUBLIC_HOST=$DOMAIN_PROFILE_PUBLIC_HOST
PUBLIC_URL=$DOMAIN_PROFILE_PUBLIC_URL
write_prepare_outputs transition-tunnel owner@example.com https://team.cloudflareaccess.com \
	"$transition_legacy_ui" "$transition_legacy_api" "$PUBLIC_URL" \
	"$transition_legacy_ui" "$transition_legacy_api" "$transition_canonical_ui" "$transition_canonical_api" \
	"$staged_legacy_host=$transition_legacy_ui,$transition_legacy_api;$staged_canonical_host=$transition_canonical_ui,$transition_canonical_api" \
	'' || fail 'staged owner environment rendering failed'
grep -Fx "HELM_CF_ACCESS_AUDIENCES=$transition_legacy_ui,$transition_legacy_api,$transition_canonical_ui,$transition_canonical_api" "$OWNER_ENV_OUTPUT" >/dev/null \
	|| fail 'staged owner environment did not contain four audiences'
grep -Fx "HELM_CF_ACCESS_HOST_AUDIENCES=$staged_legacy_host=$transition_legacy_ui,$transition_legacy_api;$staged_canonical_host=$transition_canonical_ui,$transition_canonical_api" "$OWNER_ENV_OUTPUT" >/dev/null \
	|| fail 'staged owner environment host map was wrong'
! grep -q '^HELM_LEGACY_ORIGIN=' "$OWNER_ENV_OUTPUT" \
	|| fail 'staged owner environment unexpectedly contained a legacy origin'

OWNER_ENV_OUTPUT="$fixture/canonical.owner.env"
domain_profile_apply_phase canonical
PUBLIC_HOST=$DOMAIN_PROFILE_PUBLIC_HOST
PUBLIC_URL=$DOMAIN_PROFILE_PUBLIC_URL
write_prepare_outputs transition-tunnel owner@example.com https://team.cloudflareaccess.com \
	"$transition_legacy_ui" "$transition_legacy_api" "$PUBLIC_URL" \
	"$transition_legacy_ui" "$transition_legacy_api" "$transition_canonical_ui" "$transition_canonical_api" \
	"$staged_legacy_host=$transition_legacy_ui,$transition_legacy_api;$staged_canonical_host=$transition_canonical_ui,$transition_canonical_api" \
	"$DOMAIN_PROFILE_LEGACY_ORIGIN" || fail 'canonical owner environment rendering failed'
grep -Fx "HELM_LEGACY_ORIGIN=$DOMAIN_PROFILE_LEGACY_ORIGIN" "$OWNER_ENV_OUTPUT" >/dev/null \
	|| fail 'canonical owner environment omitted legacy origin'
grep -Fx "ROADMAP_CF_ACCESS_HOST_AUDIENCES=$staged_legacy_host=$transition_legacy_ui,$transition_legacy_api;$staged_canonical_host=$transition_canonical_ui,$transition_canonical_api" "$OWNER_ENV_OUTPUT" >/dev/null \
	|| fail 'canonical Roadmap host map alias was wrong'

# Missing retained resources are an inventory failure, not permission to
# silently replace an existing tunnel, edge credential or old Access app.
source <(awk '/^ensure_service_token\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^ensure_app\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^identity_provider_id\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^prepare\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^publish\(\)/,/^}/' "$CLOUDFLARE")
SERVICE_TOKEN_NAME='Helm agents'
LEGACY_SERVICE_TOKEN_NAME='Roadmap agents'
: > "$fixture/provider-writes"
cf_request() {
	if [[ "$1" != GET ]]; then printf '%s\n' "$1" >> "$fixture/provider-writes"; return 1; fi
	printf '%s' '{"success":true,"result":[]}'
}
assert_fails ensure_service_token > "$fixture/missing-retained-token.out"
assert_fails identity_provider_id > "$fixture/missing-retained-idp.out"
assert_fails ensure_app 'Helm owner UI' "$DOMAIN_PROFILE_LEGACY_HOST" fixture-idp false > "$fixture/missing-retained-ui.out"
assert_fails ensure_app 'Helm agents API' "$DOMAIN_PROFILE_LEGACY_API_PATH" fixture-idp true > "$fixture/missing-retained-api.out"
owner_email() { printf 'owner@example.com'; }
identity_provider_id() { printf fixture-idp; }
access_team_domain() { printf team.cloudflareaccess.com; }
find_tunnel_id() { :; }
assert_fails prepare > "$fixture/missing-retained-tunnel.out"
assert_fails publish > "$fixture/publication-gated.out"
[[ ! -s "$fixture/provider-writes" ]] || fail 'missing retained resource caused a provider write'

printf 'domain_transition_tests=ok\n'
