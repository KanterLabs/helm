# Drive prepare through the real Cloudflare script with a deterministic curl
# API mock. Each injected failure occurs after the one-time service token is
# created; the EXIT trap must issue exactly one DELETE and remove any captured
# output even when prepare fails in a later API or local-output step.
cloudflare_prepare_curl() {
	local method=GET path= data=arg idp_fixture=idp-fixture
	[[ "${CF_MOCK_IDP_MODE:-reuse}" = create ]] && idp_fixture=idp-created-fixture
	while [[ $# -gt 0 ]]; do
		arg=$1
		case "$arg" in
			--request) method=$2; shift 2 ;;
			--data) data=$2; shift 2 ;;
			https://api.cloudflare.com/client/v4/*)
				path=${arg#https://api.cloudflare.com/client/v4}; shift ;;
			*) shift ;;
		esac
	done
	printf '%s %s\n' "$method" "$path" >> "$CF_MOCK_LOG"
	if [[ "$CF_MOCK_FAIL" = "$method $path" ]]; then
		return 22
	fi
	local base="/accounts/$ACCOUNT_ID"
	if [[ "$method" = DELETE ]]; then
		printf '{"success":true,"result":{}}'
	elif [[ "$method" = GET && "$path" = "$base/access/identity_providers" ]]; then
		case "${CF_MOCK_IDP_MODE:-reuse}" in
			create) printf '{"success":true,"result":[]}' ;;
			ambiguous) printf '{"success":true,"result":[{"id":"idp-fixture-a","type":"cloudflare","config":{"restrict_to_account_members":true}},{"id":"idp-fixture-b","type":"cloudflare","config":{"restrict_to_account_members":true}}]}' ;;
			nonconforming) printf '{"success":true,"result":[{"id":"idp-fixture","type":"cloudflare","config":{"restrict_to_account_members":false}}]}' ;;
			*) printf '{"success":true,"result":[{"id":"otp-fixture","name":"Cloudflare","type":"onetimepin"},{"id":"idp-fixture","name":"Account SSO","type":"cloudflare","config":{"restrict_to_account_members":true}}]}' ;;
		esac
	elif [[ "$method" = POST && "$path" = "$base/access/identity_providers" ]]; then
		[[ "${CF_MOCK_IDP_MODE:-}" = create ]] || return 1
		[[ "$data" = '{"name":"Cloudflare","type":"cloudflare","config":{"restrict_to_account_members":true}}' ]] || return 1
		printf '{"success":true,"result":{"id":"idp-created-fixture","name":"Cloudflare","type":"cloudflare","config":{"restrict_to_account_members":true}}}'
	elif [[ "$method" = GET && "$path" = "$base/access/organizations" ]]; then
		printf '{"success":true,"result":{"auth_domain":"team.cloudflareaccess.com"}}'
	elif [[ "$method" = GET && "$path" = "$base/access/service_tokens?per_page=100" ]]; then
		if [[ "${CF_MOCK_SERVICE_TOKEN_MODE:-new}" = legacy ]]; then
			if grep -Fq "PUT $base/access/service_tokens/service-token-fixture" "$CF_MOCK_LOG"; then
				printf '{"success":true,"result":[{"id":"service-token-fixture","name":"Helm agents","client_id":"client-fixture","expires_at":"%s"}]}' "$CF_MOCK_EXPIRY"
			else
				printf '{"success":true,"result":[{"id":"service-token-fixture","name":"Roadmap agents","client_id":"client-fixture","expires_at":"%s"}]}' "$CF_MOCK_EXPIRY"
			fi
		else
			printf '{"success":true,"result":[]}'
		fi
	elif [[ "$method" = POST && "$path" = "$base/access/service_tokens" ]]; then
		printf '{"success":true,"result":{"id":"service-token-fixture","client_id":"client-fixture","client_secret":"secret-fixture","expires_at":"%s"}}' "$CF_MOCK_EXPIRY"
	elif [[ "$method" = GET && "$path" = "$base/access/apps?per_page=100" ]]; then
		printf '{"success":true,"result":[]}'
	elif [[ "$method" = POST && "$path" = "$base/access/apps" ]]; then
		if [[ "$data" == *'/api/v1/'* ]]; then
			printf '{"success":true,"result":{"id":"api-app-fixture"}}'
		else
			printf '{"success":true,"result":{"id":"ui-app-fixture"}}'
		fi
	elif [[ "$method" = GET && "$path" = "$base/access/apps/ui-app-fixture" ]]; then
		printf '{"success":true,"result":{"name":"Helm owner UI","domain":"tc.shanekanterman.dev","type":"self_hosted","session_duration":"168h","auto_redirect_to_identity":true,"allowed_idps":["%s"],"app_launcher_visible":false,"service_auth_401_redirect":false,"aud":"ui-audience"}}' "$idp_fixture"
	elif [[ "$method" = GET && "$path" = "$base/access/apps/api-app-fixture" ]]; then
		printf '{"success":true,"result":{"name":"Helm agents API","domain":"tc.shanekanterman.dev/api/v1/*","type":"self_hosted","session_duration":"168h","auto_redirect_to_identity":true,"allowed_idps":["%s"],"app_launcher_visible":false,"service_auth_401_redirect":true,"aud":"api-audience"}}' "$idp_fixture"
	elif [[ "$method" = GET && "$path" = "$base/access/apps/ui-app-fixture/policies" ]]; then
		printf '%s' '{"success":true,"result":[{"id":"ui-owner-policy","name":"Helm owner only","decision":"allow","precedence":1,"include":[{"email":{"email":"owner@example.com"}}]}]}'
	elif [[ "$method" = GET && "$path" = "$base/access/apps/api-app-fixture/policies" ]]; then
		printf '%s' '{"success":true,"result":[{"id":"api-owner-policy","name":"Helm owner only","decision":"allow","precedence":2,"include":[{"email":{"email":"owner@example.com"}}]},{"id":"api-service-policy","name":"Helm agents Service Auth","decision":"non_identity","precedence":1,"include":[{"service_token":{"token_id":"service-token-fixture"}}]}]}'
	elif [[ "$method" = PUT || "$method" = POST ]]; then
		printf '{"success":true,"result":{}}'
	elif [[ "$method" = GET && "$path" = "$base/cfd_tunnel?is_deleted=false&per_page=100" ]]; then
		printf '{"success":true,"result":[{"id":"tunnel-fixture","name":"roadmap-homelab"}]}'
	elif [[ "$method" = PUT && "$path" = "$base/cfd_tunnel/tunnel-fixture/configurations" ]]; then
		printf '{"success":true,"result":{}}'
	elif [[ "$method" = GET && "$path" = "$base/cfd_tunnel/tunnel-fixture/configurations" ]]; then
		printf '%s' '{"success":true,"result":{"config":{"ingress":[{"hostname":"tc.shanekanterman.dev","service":"http://127.0.0.1:8080","originRequest":{"access":{"required":true,"teamName":"team","audTag":["ui-audience","api-audience"]}}},{"service":"http_status:404"}]}}}'
	elif [[ "$method" = GET && "$path" = "$base/cfd_tunnel/tunnel-fixture/token" ]]; then
		printf '{"success":true,"result":"tunnel-token-fixture"}'
	else
		return 1
	fi
}

cloudflare_prepare_provider_case() {
	local name=$1 mode=$2 expected_status=$3 token_mode=${4:-new} case_dir="$fixture/cloudflare-provider-$1"
	local output="$case_dir/output.log" status expected_idp_calls=1
	[[ "$mode" = create ]] && expected_idp_calls=2
	/usr/bin/install -d -m 0700 "$case_dir"
	CF_MOCK_LOG="$case_dir/api.calls"
	CF_MOCK_FAIL=
	CF_MOCK_EXPIRY=$(date -u -d '+30 days' +%Y-%m-%dT%H:%M:%SZ)
	CF_MOCK_IDP_MODE=$mode
	CF_MOCK_SERVICE_TOKEN_MODE=$token_mode
	export CF_MOCK_LOG CF_MOCK_FAIL CF_MOCK_EXPIRY CF_MOCK_IDP_MODE CF_MOCK_SERVICE_TOKEN_MODE
	set +e
	(
		export CLOUDFLARE_API_TOKEN=fixture-token HELM_ADMIN_EMAIL=owner@example.com
		export HELM_REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE=0
		unset HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN
		export -f cloudflare_prepare_curl
		curl() { cloudflare_prepare_curl "$@"; }
		export -f curl
		"$CLOUDFLARE" prepare "$case_dir/tunnel.token" "$case_dir/owner.env" "$case_dir/service.env"
	) >"$output" 2>&1
	status=$?
	set -e
	if [[ "$expected_status" = success ]]; then
		if [[ "$status" != 0 ]]; then
			sed -n '1,80p' "$output" >&2
			fail "Cloudflare provider $name unexpectedly failed"
		fi
		contains 'cloudflare_prepare=ok' "$output"
	else
		[[ "$status" -ne 0 ]] || fail "Cloudflare provider $name unexpectedly succeeded"
		[[ "$(grep -Fc -- 'POST /accounts/090ae73dce25f4eca9a53ee396fdc916/access/service_tokens' "$CF_MOCK_LOG" || true)" = 0 ]] \
			|| fail "Cloudflare provider $name continued into service-token creation"
	fi
	[[ "$(grep -Fc -- '/access/identity_providers' "$CF_MOCK_LOG" || true)" = "$expected_idp_calls" ]] \
		|| fail "Cloudflare provider $name performed an unexpected number of IdP requests"
	if [[ "$token_mode" = legacy && "$expected_status" = success ]]; then
		[[ "$(grep -Fc -- 'PUT /accounts/090ae73dce25f4eca9a53ee396fdc916/access/service_tokens/service-token-fixture' "$CF_MOCK_LOG" || true)" = 1 ]] \
			|| fail 'Cloudflare legacy service token was not renamed in place'
		[[ "$(grep -Fc -- 'POST /accounts/090ae73dce25f4eca9a53ee396fdc916/access/service_tokens' "$CF_MOCK_LOG" || true)" = 0 ]] \
			|| fail 'Cloudflare legacy service token migration rotated the credential'
	fi
}

cloudflare_prepare_provider_case reuse reuse success
cloudflare_prepare_provider_case create create success
cloudflare_prepare_provider_case legacy-token reuse success legacy
cloudflare_prepare_provider_case ambiguous ambiguous failure
cloudflare_prepare_provider_case nonconforming nonconforming failure
CF_MOCK_IDP_MODE=reuse
export CF_MOCK_IDP_MODE

cloudflare_prepare_failure_case() {
	local name=$1 failure=$2 output_mode=${3:-file} case_dir="$fixture/cloudflare-prepare-$1"
	local token_output="$case_dir/tunnel.token" owner_output="$case_dir/owner.env"
	local service_output="$case_dir/service.env" output="$case_dir/output.log" status
	/usr/bin/install -d -m 0700 "$case_dir"
	if [[ "$output_mode" = directory ]]; then
		/usr/bin/install -d -m 0700 "$owner_output"
	fi
	CF_MOCK_LOG="$case_dir/api.calls"
	CF_MOCK_FAIL=$failure
	CF_MOCK_EXPIRY=$(date -u -d '+30 days' +%Y-%m-%dT%H:%M:%SZ)
	export CF_MOCK_LOG CF_MOCK_FAIL CF_MOCK_EXPIRY
	set +e
	(
		export CLOUDFLARE_API_TOKEN=fixture-token HELM_ADMIN_EMAIL=owner@example.com
		export HELM_REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE=0
		unset HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN
		export -f cloudflare_prepare_curl
		curl() { cloudflare_prepare_curl "$@"; }
		export -f curl
		"$CLOUDFLARE" prepare "$token_output" "$owner_output" "$service_output"
	) >"$output" 2>&1
	status=$?
	set -e
	[[ "$status" -ne 0 ]] || fail "Cloudflare prepare $name unexpectedly succeeded"
	[[ "$(grep -Fc -- 'DELETE /accounts/090ae73dce25f4eca9a53ee396fdc916/access/service_tokens/service-token-fixture' "$CF_MOCK_LOG" || true)" = 1 ]] \
		|| fail "Cloudflare prepare $name did not revoke its newly-created service token"
	[[ ! -e "$service_output" ]] \
		|| fail "Cloudflare prepare $name left a one-time service-token output"
}

cloudflare_prepare_failure_case app-create \
	'POST /accounts/090ae73dce25f4eca9a53ee396fdc916/access/apps'
cloudflare_prepare_failure_case api-app-read \
	'GET /accounts/090ae73dce25f4eca9a53ee396fdc916/access/apps/api-app-fixture'
cloudflare_prepare_failure_case owner-policy-update \
	'PUT /accounts/090ae73dce25f4eca9a53ee396fdc916/access/apps/ui-app-fixture/policies/ui-owner-policy'
cloudflare_prepare_failure_case tunnel-discovery \
	'GET /accounts/090ae73dce25f4eca9a53ee396fdc916/cfd_tunnel?is_deleted=false&per_page=100'
cloudflare_prepare_failure_case tunnel-config \
	'PUT /accounts/090ae73dce25f4eca9a53ee396fdc916/cfd_tunnel/tunnel-fixture/configurations'
cloudflare_prepare_failure_case tunnel-token \
	'GET /accounts/090ae73dce25f4eca9a53ee396fdc916/cfd_tunnel/tunnel-fixture/token'
cloudflare_prepare_failure_case invalid-output '' directory
printf 'cloudflare_prepare_recovery_runtime_tests=ok\n'

