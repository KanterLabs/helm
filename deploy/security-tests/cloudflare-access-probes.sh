# Cloudflare API bearer material is supplied through a mode-0600 curl config
# header file, never in a curl argv. Policy checks are exact-set checks: any
# unknown or bypass policy causes prepare/live validation to fail.
for file in "$CLOUDFLARE" "$VALIDATE"; do
	contains 'CF_HEADER_FILE=$(mktemp' "$file"
	contains 'chmod 0600 "$CF_HEADER_FILE"' "$file"
	contains '--header "@$CF_HEADER_FILE"' "$file"
	contains 'trap cleanup EXIT' "$file"
	not_contains '--header "Authorization: Bearer' "$file"
done
contains "SERVICE_POLICY_NAME='Helm agents Service Auth'" "$CLOUDFLARE"
contains 'audTag:[$ui,$aud]' "$CLOUDFLARE"
contains 'validate_policy_set' "$CLOUDFLARE"
contains 'duration:"8760h"' "$CLOUDFLARE"
contains 'HELM_REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE' "$CLOUDFLARE"
contains 'without a durable secret-capture destination' "$CLOUDFLARE"
contains 'expires_at // .expiration_time' "$CLOUDFLARE"
contains 'validate_owner_env()' "$CLOUDFLARE"
contains 'gsub(/@CLOUDFLARE_ISSUER@/, issuer, line)' "$CLOUDFLARE"
contains 'if ! awk -v owner_email=' "$CLOUDFLARE"
contains 'if ! cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/token"' "$CLOUDFLARE"
contains 'if ! validate_owner_env "$owner_tmp"' "$CLOUDFLARE"
contains 'restore_prepare_output()' "$CLOUDFLARE"
contains 'token_backup=' "$CLOUDFLARE"
contains 'owner_backup=' "$CLOUDFLARE"
contains 'if ! mv -T -- "$token_tmp" "$TOKEN_OUTPUT"' "$CLOUDFLARE"
contains 'if ! mv -T -- "$owner_tmp" "$OWNER_ENV_OUTPUT"' "$CLOUDFLARE"
contains 'prior outputs restored' "$CLOUDFLARE"
contains 'if ! write_prepare_outputs' "$CLOUDFLARE"
not_contains 'sed -e "s/@CLOUDFLARE_ISSUER@/' "$CLOUDFLARE"
contains '@CLOUDFLARE_ISSUER@' "$DEPLOY_DIR/helm.env.template"
contains '@UI_AUDIENCE@' "$DEPLOY_DIR/helm.env.template"
contains '@API_AUDIENCE@' "$DEPLOY_DIR/helm.env.template"
contains 'HELM_CF_ACCESS_AUDIENCES' "$DEPLOY_DIR/helm.env.template"
contains 'API Access app policy set is not exactly owner Allow plus Service Auth' "$VALIDATE"
contains 'tunnel_config=' "$VALIDATE"
contains 'CF_ACCESS_CLIENT_ID' "$VALIDATE"
contains 'CF_ACCESS_CLIENT_SECRET' "$VALIDATE"
contains 'HELM_REQUIRE_SERVICE_AUTH_PROBE' "$VALIDATE"
contains 'X-Request-ID: $expected_request_id' "$VALIDATE"
contains 'JSON unauthorized' "$VALIDATE"
contains 'validate_access_app()' "$VALIDATE"
contains 'identity_provider_id()' "$VALIDATE"
contains 'access_team_name()' "$VALIDATE"
contains 'restrict_to_account_members' "$CLOUDFLARE"
contains 'restrict_to_account_members' "$VALIDATE"
contains 'identity providers are ambiguous or nonconforming' "$CLOUDFLARE"
contains 'identity provider is missing, ambiguous, or nonconforming' "$VALIDATE"
contains 'PUBLIC_HOST=beta-helm.home.shanekanterman.dev' "$CLOUDFLARE"
contains 'PUBLIC_HOST=beta-helm.home.shanekanterman.dev' "$VALIDATE"
contains 'HELM_PUBLIC_ORIGIN=https://beta-helm.home.shanekanterman.dev' "$ROOT_DIR/.helm-beta-deploy.env.example"
contains 'HELM_RELEASE_RETENTION=20' "$ROOT_DIR/.helm-beta-deploy.env.example"
contains 'RETENTION=$(compat_env HELM_RELEASE_RETENTION ROADMAP_RELEASE_RETENTION 20)' "$INSTALL"
contains 'RETENTION=$(compat_env HELM_RELEASE_RETENTION ROADMAP_RELEASE_RETENTION 5)' "$INSTALL"
contains 'beta-helm.home.shanekanterman.dev' "$ROOT_DIR/docs/BETA_DEPLOYMENT_PLAN.md"
contains 'private Tailnet/split-DNS hostname' "$CLOUDFLARE"
contains 'private Tailnet/split-DNS hostname' "$VALIDATE"
contains 'owned by a different tunnel' "$CLOUDFLARE"
not_contains 'onetimepin' "$CLOUDFLARE"
not_contains 'onetimepin' "$VALIDATE"
contains '.result.session_duration == "168h"' "$VALIDATE"
contains '.result.allowed_idps // []) == [$idp]' "$VALIDATE"
contains '.result.service_auth_401_redirect // false) == $service401' "$VALIDATE"
contains '$ingress[0].originRequest.access.teamName == $team' "$VALIDATE"
contains '! -L "$SSH_CONFIG"' "$DEPLOY_CI"
contains 'HELM_SSH_CONFIG must be owned by the invoking user and mode 0600 or stricter' "$DEPLOY_CI"

# A freshly-published DNS record can briefly return resolver/connect failures
# or a non-Access status. Exercise the bounded retry loop with a curl sequence
# mock and a no-op sleep, so these regressions never use the network or wait.
source <(awk '/^access_probe_backoff\(\)/,/^}/' "$VALIDATE")
source <(awk '/^access_status\(\)/,/^}/' "$VALIDATE")
source <(awk '/^expect_access\(\)/,/^}/' "$VALIDATE")
PUBLIC_URL=https://tc.shanekanterman.dev
ACCESS_PROBE_MAX_ATTEMPTS=3
ACCESS_PROBE_INITIAL_DELAY_SECONDS=1
ACCESS_PROBE_MAX_DELAY_SECONDS=30
probe_curl() {
	local call event output_file=header_file= arg
	call=$(wc -l < "$PROBE_CALL_LOG")
	call=$((call + 1))
	printf '%s\n' "$call" >> "$PROBE_CALL_LOG"
	event=$(sed -n "${call}p" "$PROBE_RESPONSES")
	if [[ "$PROBE_KIND" = service ]]; then
		while [[ $# -gt 0 ]]; do
			arg=$1
			case "$arg" in
				--output|--dump-header)
					if [[ "$arg" = --output ]]; then
						output_file=$2
					else
						header_file=$2
					fi
					shift 2
					;;
				--write-out) shift 2 ;;
				*) shift ;;
			esac
		done
	fi
	case "$event" in
		dns) return 6 ;;
		connect) return 7 ;;
		302|303|401|500)
			if [[ "$PROBE_KIND" = service ]]; then
				printf 'HTTP/1.1 %s Fixture\r\nContent-Type: application/json\r\nX-Request-ID: helm-service-auth-probe\r\n\r\n' \
					"$event" > "$header_file"
				printf '%s' '{"error":{"code":"unauthorized","message":"fixture"}}' > "$output_file"
			fi
			printf '%s' "$event"
			;;
		*) return 1 ;;
	esac
}
sleep() { printf '%s\n' "$1" >> "$PROBE_SLEEP_LOG"; }
curl() { probe_curl "$@"; }
run_access_probe_case() {
	local name=$1 sequence=$2 expected=$3 case_dir="$fixture/live-probe-$1" output status
	install -d -m 0700 "$case_dir"
	printf '%s\n' "$sequence" > "$case_dir/responses"
	: > "$case_dir/calls"
	: > "$case_dir/sleeps"
	PROBE_KIND=access PROBE_RESPONSES="$case_dir/responses" PROBE_CALL_LOG="$case_dir/calls" \
		PROBE_SLEEP_LOG="$case_dir/sleeps"
	if output=$(expect_access /healthz 2>"$case_dir/error"); then
		status=0
	else
		status=$?
	fi
	if [[ "$expected" = success ]]; then
		[[ "$status" = 0 && "$output" = 'access_healthz=302' ]] \
			|| fail "live Access retry $name did not recover to the expected status"
	else
		[[ "$status" -ne 0 ]] || fail "live Access retry $name unexpectedly succeeded"
		contains 'after 3 attempts' "$case_dir/error"
		contains 'HTTP 500' "$case_dir/error"
	fi
	[[ "$(wc -l < "$case_dir/calls")" = 3 ]] || fail "live Access retry $name did not honor its attempt bound"
	[[ "$(<"$case_dir/sleeps")" = $'1\n2' ]] || fail "live Access retry $name did not use bounded exponential backoff"
}
run_access_probe_case transient-success $'dns\n500\n302' success
run_access_probe_case eventual-failure $'dns\nconnect\n500' failure

# Service Auth runs after the unauthenticated probes but can hit the same
# propagation window. It gets the same bounded retry treatment while retaining
# exact status, header, and JSON assertions on the successful attempt.
source <(awk '/^service_auth_probe\(\)/,/^}/' "$VALIDATE")
ACCESS_PROBE_MAX_ATTEMPTS=2
CF_ACCESS_CLIENT_ID=fixture-client-id
CF_ACCESS_CLIENT_SECRET=fixture-client-secret
run_service_probe_case() {
	local name=$1 sequence=$2 expected=$3 case_dir="$fixture/service-probe-$1" output status
	install -d -m 0700 "$case_dir"
	printf '%s\n' "$sequence" > "$case_dir/responses"
	: > "$case_dir/calls"
	: > "$case_dir/sleeps"
	PROBE_KIND=service PROBE_RESPONSES="$case_dir/responses" PROBE_CALL_LOG="$case_dir/calls" \
		PROBE_SLEEP_LOG="$case_dir/sleeps"
	CF_SERVICE_HEADER_FILE=
	CF_SERVICE_RESPONSE_HEADERS=
	CF_SERVICE_RESPONSE_BODY=
	if service_auth_probe >"$case_dir/output" 2>"$case_dir/error"; then
		status=0
	else
		status=$?
	fi
	rm -f -- "$CF_SERVICE_HEADER_FILE" "$CF_SERVICE_RESPONSE_HEADERS" "$CF_SERVICE_RESPONSE_BODY"
	if [[ "$expected" = success ]]; then
		[[ "$status" = 0 ]] || fail "Service Auth retry $name did not recover"
		contains 'service_auth_probe=ok' "$case_dir/output"
	else
		[[ "$status" -ne 0 ]] || fail "Service Auth retry $name unexpectedly succeeded"
		contains 'after 2 attempts' "$case_dir/error"
		contains 'curl exit 7' "$case_dir/error"
	fi
	[[ "$(wc -l < "$case_dir/calls")" = 2 ]] || fail "Service Auth retry $name did not honor its attempt bound"
	[[ "$(<"$case_dir/sleeps")" = 1 ]] || fail "Service Auth retry $name did not back off once"
	not_contains 'fixture-client-secret' "$case_dir/output"
	not_contains 'fixture-client-secret' "$case_dir/error"
}
run_service_probe_case transient-success $'dns\n401' success
run_service_probe_case eventual-failure $'dns\nconnect' failure
unset -f curl sleep probe_curl
unset CF_ACCESS_CLIENT_ID CF_ACCESS_CLIENT_SECRET PROBE_KIND PROBE_RESPONSES PROBE_CALL_LOG PROBE_SLEEP_LOG

