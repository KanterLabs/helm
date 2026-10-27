# Exercise the output writer without contacting Cloudflare. Source only the
# production function definitions and provide a controlled API response so an
# HTTPS issuer proves literal replacement (including its slashes). Then force
# both API and template failures from a conditional call: neither may return
# success or replace existing valid outputs.
source <(awk '/^valid_email\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^validate_owner_env\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^restore_prepare_output\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^write_prepare_outputs\(\)/,/^}/' "$CLOUDFLARE")
cf_request() {
	if [[ "${CF_REQUEST_FAIL:-0}" = 1 ]]; then
		return 1
	fi
	printf '{"success":true,"result":"fixture-tunnel-token"}'
}
ACCOUNT_ID=fixture-account
cloudflare_test_root_dir=$ROOT_DIR
TOKEN_OUTPUT="$fixture/generated.token"
OWNER_ENV_OUTPUT="$fixture/generated.owner.env"
CF_REQUEST_FAIL=0
if ! write_prepare_outputs tunnel-id owner@example.com \
	'https://team.cloudflareaccess.com' ui-audience api_audience; then
	fail 'owner-environment writer rejected an HTTPS issuer'
fi
grep -Fx 'ROADMAP_CLOUDFLARE_ISSUER=https://team.cloudflareaccess.com' "$OWNER_ENV_OUTPUT" >/dev/null \
	|| fail 'owner-environment writer did not preserve HTTPS issuer'
grep -Fx 'ROADMAP_CF_ACCESS_AUDIENCES=ui-audience,api_audience' "$OWNER_ENV_OUTPUT" >/dev/null \
	|| fail 'owner-environment writer did not preserve both audiences'
[[ "$(stat -c '%a' -- "$TOKEN_OUTPUT")" = 600 && "$(stat -c '%a' -- "$OWNER_ENV_OUTPUT")" = 600 ]] \
	|| fail 'owner-environment outputs are not mode 0600'

# CI must not create a one-time service-token secret on an ephemeral runner.
# The guard must fail before the POST while the default/manual mode remains
# available for the explicitly persisted first bootstrap.
source <(awk '/^ensure_service_token\(\)/,/^}/' "$CLOUDFLARE")
SERVICE_TOKEN_NAME='Roadmap agents'
SERVICE_TOKEN_OUTPUT="$fixture/guard-service-token.env"
REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE=1
SERVICE_TOKEN_CALL_LOG="$fixture/service-token-calls.log"
: > "$SERVICE_TOKEN_CALL_LOG"
cf_request() {
	printf '%s\n' "$1 $2" >> "$SERVICE_TOKEN_CALL_LOG"
	if [[ "$1" = GET ]]; then
		printf '{"success":true,"result":[]}'
	else
		printf '{"success":true,"result":{"id":"unexpected"}}'
	fi
}
if ensure_service_token >"$fixture/service-token-guard.out" 2>&1; then
	fail 'service-token durability guard allowed an unpersistable token creation'
fi
[[ "$(wc -l < "$SERVICE_TOKEN_CALL_LOG")" = 1 && "$(cut -d' ' -f1 < "$SERVICE_TOKEN_CALL_LOG")" = GET && ! -e "$SERVICE_TOKEN_OUTPUT" ]] \
	|| fail 'service-token durability guard did not stop before token creation'
REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE=0
cf_request() {
	if [[ "${CF_REQUEST_FAIL:-0}" = 1 ]]; then
		return 1
	fi
	printf '{"success":true,"result":"fixture-tunnel-token"}'
}

printf 'old-token\n' > "$TOKEN_OUTPUT"
printf 'old-owner\n' > "$OWNER_ENV_OUTPUT"
CF_REQUEST_FAIL=1
if write_prepare_outputs tunnel-id owner@example.com \
	'https://team.cloudflareaccess.com' ui-audience api_audience \
	>"$fixture/cloudflare-api-failure.out" 2>&1; then
	fail 'failed Cloudflare request was reported as successful'
fi
[[ "$(<"$TOKEN_OUTPUT")" = old-token && "$(<"$OWNER_ENV_OUTPUT")" = old-owner ]] \
	|| fail 'failed Cloudflare request replaced existing outputs'

CF_REQUEST_FAIL=0
ROOT_DIR="$fixture/missing-template-root"
if write_prepare_outputs tunnel-id owner@example.com \
	'https://team.cloudflareaccess.com' ui-audience api_audience \
	>"$fixture/template-failure.out" 2>&1; then
	fail 'template generation failure was reported as successful'
fi
[[ "$(<"$TOKEN_OUTPUT")" = old-token && "$(<"$OWNER_ENV_OUTPUT")" = old-owner ]] \
	|| fail 'template failure replaced existing outputs'
ROOT_DIR="$cloudflare_test_root_dir"

# Force only the second output rename to fail. The writer must restore both
# pre-existing files byte-for-byte from its same-directory backup names.
printf 'old-token\n' > "$fixture/old-token.ref"
printf 'old-owner\n' > "$fixture/old-owner.ref"
cp -- "$fixture/old-token.ref" "$TOKEN_OUTPUT"
cp -- "$fixture/old-owner.ref" "$OWNER_ENV_OUTPUT"
mv() {
	local move_args=("$@") source destination
	source=${move_args[$(( ${#move_args[@]} - 2 ))]}
	destination=${move_args[$(( ${#move_args[@]} - 1 ))]}
	if [[ "${CLOUDFLARE_FAIL_OWNER_COMMIT:-0}" = 1 && "$destination" = "$OWNER_ENV_OUTPUT" && "$source" != *'.backup.'* ]]; then
		return 1
	fi
	command mv "$@"
}
CLOUDFLARE_FAIL_OWNER_COMMIT=1
if write_prepare_outputs tunnel-id owner@example.com \
	'https://team.cloudflareaccess.com' ui-audience api_audience \
	>"$fixture/owner-commit-failure.out" 2>&1; then
	fail 'second owner-environment rename failure was reported as successful'
fi
unset -f mv
cmp -s "$fixture/old-token.ref" "$TOKEN_OUTPUT" \
	|| fail 'second rename failure did not restore the prior tunnel token byte-for-byte'
cmp -s "$fixture/old-owner.ref" "$OWNER_ENV_OUTPUT" \
	|| fail 'second rename failure did not restore the prior owner environment byte-for-byte'

# Exercise the prepare caller itself with a deliberately failed output writer.
# This keeps the test independent of Cloudflare while proving the final
# cloudflare_prepare=ok marker is unreachable when a critical child fails.
source <(awk '/^prepare\(\)/,/^}/' "$CLOUDFLARE")
PUBLIC_HOST=tc.shanekanterman.dev
API_PATH="$PUBLIC_HOST/api/v1/*"
UI_APP_NAME='Roadmap owner UI'
API_APP_NAME='Roadmap agents API'
owner_email() { printf 'owner@example.com'; }
identity_provider_id() { printf 'fixture-idp'; }
access_team_domain() { printf 'team.cloudflareaccess.com'; }
ensure_service_token() { SERVICE_TOKEN_ID=fixture-service-token; }
ensure_app() {
	if [[ "$2" = "$PUBLIC_HOST" ]]; then
		APP_ID=fixture-ui-id
		APP_AUD=fixture-ui-aud
	else
		APP_ID=fixture-api-id
		APP_AUD=fixture-api-aud
	fi
}
upsert_owner_policy() { :; }
upsert_service_policy() { :; }
validate_policy_set() { :; }
find_tunnel_id() { printf 'fixture-tunnel-id'; }
configure_tunnel() { :; }
write_prepare_outputs() { return 1; }
if prepare >"$fixture/prepare-failure.out" 2>&1; then
	fail 'prepare reported success after a failed output writer'
fi
not_contains 'cloudflare_prepare=ok' "$fixture/prepare-failure.out"

