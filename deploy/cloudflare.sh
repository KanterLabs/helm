#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DOMAIN_PROFILE_SCRIPT="$ROOT_DIR/deploy/domain-profile.sh"
[[ -f "$DOMAIN_PROFILE_SCRIPT" && ! -L "$DOMAIN_PROFILE_SCRIPT" ]] || {
	printf 'deployment domain profile is missing or not regular: %s\n' "$DOMAIN_PROFILE_SCRIPT" >&2
	exit 1
}
source "$DOMAIN_PROFILE_SCRIPT"

compat_env() {
	local canonical=$1 legacy=$2 default_value=${3:-} canonical_value legacy_value
	canonical_value=${!canonical:-}
	legacy_value=${!legacy:-}
	[[ -z "$canonical_value" || -z "$legacy_value" || "$canonical_value" = "$legacy_value" ]] || {
		printf '%s and %s must match when both are set\n' "$canonical" "$legacy" >&2
		exit 1
	}
	printf '%s' "${canonical_value:-${legacy_value:-$default_value}}"
}

MODE=${1:-}
TOKEN_OUTPUT=${2:-}
OWNER_ENV_OUTPUT=${3:-}
SERVICE_TOKEN_OUTPUT=${4:-$(compat_env HELM_ACCESS_TOKEN_OUTPUT ROADMAP_ACCESS_TOKEN_OUTPUT "$ROOT_DIR/dist/helm-access-token.env")}
REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE=$(compat_env HELM_REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE ROADMAP_REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE 0)
DEPLOY_ENVIRONMENT=${HELM_DEPLOY_ENVIRONMENT:-production}
domain_profile_load "$DEPLOY_ENVIRONMENT"

[[ "$REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE" = 0 || "$REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE" = 1 ]] || {
	printf 'HELM_REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE must be 0 or 1\n' >&2
	exit 1
}

: "${CLOUDFLARE_API_TOKEN:?CLOUDFLARE_API_TOKEN is required}"
[[ "$CLOUDFLARE_API_TOKEN" != *$'\n'* && "$CLOUDFLARE_API_TOKEN" != *$'\r'* ]] || {
	printf 'CLOUDFLARE_API_TOKEN contains a control character\n' >&2
	exit 1
}
command -v curl >/dev/null 2>&1 || { printf 'curl is required\n' >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { printf 'jq is required\n' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf 'sha256sum is required\n' >&2; exit 1; }

CF_API=https://api.cloudflare.com/client/v4
SECURE_TMP_ROOT=${TMPDIR:-${RUNNER_TEMP:-/tmp}}
[[ -d "$SECURE_TMP_ROOT" && -w "$SECURE_TMP_ROOT" ]] || {
	printf 'secure temporary directory is unavailable\n' >&2
	exit 1
}
CF_HEADER_FILE=$(mktemp "$SECURE_TMP_ROOT/helm-cloudflare-header.XXXXXX")
CREATED_SERVICE_TOKEN_ID=
CREATED_SERVICE_TOKEN_OUTPUT=
TUNNEL_BEFORE_STATE=
TUNNEL_BEFORE_CONFIG_DIGEST=
TUNNEL_BEFORE_CONFIG_KEYS=
TUNNEL_LATEST_CONFIG=
TUNNEL_LATEST_CONFIG_DIGEST=
TUNNEL_AFTER_CONFIG_DIGEST=

revoke_created_service_token() {
	local token_id=${CREATED_SERVICE_TOKEN_ID:-} output_path=${CREATED_SERVICE_TOKEN_OUTPUT:-}
	[[ -n "$token_id" ]] || return 0
	# The one-time secret is never included in this request or its diagnostics.
	# Keep this best-effort: the prepare failure must remain the reported error.
	if ! cf_request DELETE "/accounts/$ACCOUNT_ID/access/service_tokens/$token_id" >/dev/null 2>&1; then
		printf 'could not revoke newly created Cloudflare service token; manual cleanup may be required\n' >&2
		return 1
	fi
	CREATED_SERVICE_TOKEN_ID=
	if [[ -n "$output_path" && -f "$output_path" && ! -L "$output_path" ]]; then
		rm -f -- "$output_path" || true
	fi
	CREATED_SERVICE_TOKEN_OUTPUT=
}

cleanup() {
	local exit_status=$?
	if [[ -n "${CREATED_SERVICE_TOKEN_ID:-}" ]]; then
		revoke_created_service_token || true
	fi
	rm -f -- "$CF_HEADER_FILE"
	return "$exit_status"
}
trap cleanup EXIT
chmod 0600 "$CF_HEADER_FILE"
printf 'Authorization: Bearer %s\n' "$CLOUDFLARE_API_TOKEN" > "$CF_HEADER_FILE"
cf_request() {
	local method=$1 path=$2 body=${3:-} response success
	if [[ -n "$body" ]]; then
		if ! response=$(curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
			--request "$method" \
			--header "@$CF_HEADER_FILE" \
			--header 'Content-Type: application/json' \
			--data "$body" "$CF_API$path"); then
			printf 'Cloudflare API request failed\n' >&2
			return 1
		fi
	else
		if ! response=$(curl --fail --silent --show-error --proto '=https' --tlsv1.2 \
			--request "$method" \
			--header "@$CF_HEADER_FILE" \
			"$CF_API$path"); then
			printf 'Cloudflare API request failed\n' >&2
			return 1
		fi
	fi
	if ! success=$(jq -r '.success // false' <<<"$response"); then
		printf 'Cloudflare API response was not valid JSON\n' >&2
		return 1
	fi
	if [[ "$success" != true ]]; then
		jq -r '.errors[]? | "Cloudflare error \(.code): \(.message)"' <<<"$response" >&2 || true
		return 1
	fi
	printf '%s' "$response"
}

valid_email() {
	[[ "$1" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]]
}

owner_email() {
	local configured members
	configured=$(compat_env HELM_ADMIN_EMAIL ROADMAP_ADMIN_EMAIL)
	if [[ -n "$configured" ]]; then
		valid_email "$configured" || { printf 'HELM_ADMIN_EMAIL is invalid\n' >&2; return 1; }
		printf '%s' "$configured"
		return 0
	fi
	if ! members=$(cf_request GET "/accounts/$ACCOUNT_ID/members"); then
		return 1
	fi
	if ! configured=$(jq -r '[.result[] | select(.status == "accepted") | .user.email] | if length == 1 then .[0] else empty end' <<<"$members"); then
		printf 'Cloudflare members response was invalid\n' >&2
		return 1
	fi
	valid_email "$configured" || {
		printf 'set HELM_ADMIN_EMAIL or ensure the account has exactly one accepted member\n' >&2
		return 1
	}
	printf '%s' "$configured"
}

identity_provider_id() {
	local providers candidates candidate_count id body response
	if ! providers=$(cf_request GET "/accounts/$ACCOUNT_ID/access/identity_providers"); then
		return 1
	fi
	if ! jq -e '.result | type == "array"' <<<"$providers" >/dev/null; then
		printf 'Cloudflare identity-provider response was invalid\n' >&2
		return 1
	fi
	if ! candidates=$(jq -c '[.result[] | select(.type == "cloudflare")]' <<<"$providers"); then
		printf 'Cloudflare identity-provider response was invalid\n' >&2
		return 1
	fi
	if ! candidate_count=$(jq -r 'length' <<<"$candidates"); then
		printf 'Cloudflare identity-provider response was invalid\n' >&2
		return 1
	fi
	if ! id=$(jq -r '
		[.[] | select(
			.type == "cloudflare" and
			(.config | type == "object") and
			.config.restrict_to_account_members == true
		)]
		| if length == 1 and (.[0].id | type == "string" and length > 0) then .[0].id else empty end
	' <<<"$candidates"); then
		printf 'Cloudflare identity-provider response was invalid\n' >&2
		return 1
	fi
	if [[ "$candidate_count" = 1 && -n "$id" && "$id" != null ]]; then
		printf '%s' "$id"
		return 0
	fi
	if (( candidate_count > 0 )); then
		printf 'Cloudflare identity providers are ambiguous or nonconforming\n' >&2
		return 1
	fi
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		printf 'hostname transition requires the retained identity provider\n' >&2
		return 1
	fi

	if ! body=$(jq -cn '{name:"Cloudflare",type:"cloudflare",config:{restrict_to_account_members:true}}'); then
		printf 'could not construct Cloudflare identity-provider request\n' >&2
		return 1
	fi
	if ! response=$(cf_request POST "/accounts/$ACCOUNT_ID/access/identity_providers" "$body"); then
		printf 'Cloudflare identity-provider creation failed\n' >&2
		return 1
	fi
	if ! id=$(jq -r '
		.result
		| select(type == "object")
		| select(.type == "cloudflare")
		| select((.config | type == "object") and .config.restrict_to_account_members == true)
		| select(.id | type == "string" and length > 0)
		| .id
	' <<<"$response"); then
		printf 'Cloudflare identity-provider response was invalid\n' >&2
		return 1
	fi
	[[ -n "$id" && "$id" != null ]] || {
		printf 'Cloudflare identity-provider creation returned a nonconforming provider\n' >&2
		return 1
	}
	printf '%s' "$id"
}

access_team_name() {
	local auth_domain
	if ! auth_domain=$(cf_request GET "/accounts/$ACCOUNT_ID/access/organizations" | jq -r '.result.auth_domain // empty'); then
		printf 'Cloudflare Access organization response was invalid\n' >&2
		return 1
	fi
	[[ "$auth_domain" = *.* ]] || { printf 'Cloudflare Access organization domain is missing\n' >&2; return 1; }
	printf '%s' "${auth_domain%%.*}"
}

access_team_domain() {
	local auth_domain
	if ! auth_domain=$(cf_request GET "/accounts/$ACCOUNT_ID/access/organizations" | jq -r '.result.auth_domain // empty'); then
		printf 'Cloudflare Access organization response was invalid\n' >&2
		return 1
	fi
	[[ "$auth_domain" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || {
		printf 'Cloudflare Access organization domain is invalid\n' >&2
		return 1
	}
	printf '%s' "$auth_domain"
}

find_tunnel_id() {
	local tunnels
	if ! tunnels=$(cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel?is_deleted=false&per_page=100"); then
		return 1
	fi
	if ! jq -r --arg name "$TUNNEL_NAME" '[.result[] | select(.name == $name)] | if length == 1 then .[0].id // empty elif length == 0 then empty else error("multiple Roadmap tunnels") end' <<<"$tunnels"; then
		printf 'Cloudflare tunnel response was invalid\n' >&2
		return 1
	fi
}

ensure_app() {
	local name=$1 domain=$2 idp=$3 service_401=$4 apps count body response app_id
	if ! apps=$(cf_request GET "/accounts/$ACCOUNT_ID/access/apps?per_page=100"); then
		return 1
	fi
	if ! count=$(jq -r --arg domain "$domain" '[.result[] | select(.domain == $domain)] | length' <<<"$apps"); then
		printf 'Cloudflare Access applications response was invalid\n' >&2
		return 1
	fi
	if ! body=$(jq -cn --arg name "$name" --arg domain "$domain" --arg idp "$idp" --argjson service401 "$service_401" \
		'{name:$name,domain:$domain,type:"self_hosted",session_duration:"168h",auto_redirect_to_identity:true,allowed_idps:[$idp],app_launcher_visible:false,service_auth_401_redirect:$service401}'); then
		printf 'could not construct Cloudflare Access application request\n' >&2
		return 1
	fi
	if [[ "$count" = 0 ]]; then
		if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 &&
			( "$domain" = "$DOMAIN_PROFILE_LEGACY_HOST" || "$domain" = "$DOMAIN_PROFILE_LEGACY_API_PATH" ) ]]; then
			printf 'hostname transition requires the retained legacy Access application\n' >&2
			return 1
		fi
		if ! response=$(cf_request POST "/accounts/$ACCOUNT_ID/access/apps" "$body"); then
			return 1
		fi
		if ! app_id=$(jq -r '.result.id // empty' <<<"$response"); then
			printf 'Cloudflare Access application response was invalid\n' >&2
			return 1
		fi
	else
		[[ "$count" = 1 ]] || { printf 'Cloudflare Access application domain is ambiguous: %s\n' "$domain" >&2; return 1; }
		if ! app_id=$(jq -r --arg domain "$domain" '.result[] | select(.domain == $domain) | .id' <<<"$apps"); then
			printf 'Cloudflare Access applications response was invalid\n' >&2
			return 1
		fi
		[[ -n "$app_id" && "$app_id" != null ]] || { printf 'Cloudflare Access application ID is missing for %s\n' "$domain" >&2; return 1; }
		# Existing applications are reconciled as well as newly created ones.
		# This prevents a manually changed IdP, redirect, or Service Auth mode
		# from silently weakening the reviewed definition on a later deploy.
		if ! cf_request PUT "/accounts/$ACCOUNT_ID/access/apps/$app_id" "$body" >/dev/null; then
			return 1
		fi
	fi
	[[ -n "$app_id" && "$app_id" != null ]] || { printf 'Cloudflare Access application ID is missing for %s\n' "$domain" >&2; return 1; }
	# Read back the canonical object after either POST or PUT. Besides providing
	# the stable audience, this verifies that the API accepted every reviewed
	# setting instead of trusting a partial update response.
	if ! response=$(cf_request GET "/accounts/$ACCOUNT_ID/access/apps/$app_id"); then
		return 1
	fi
	APP_ID=$app_id
	if ! APP_AUD=$(jq -r '.result.aud // empty' <<<"$response"); then
		printf 'Cloudflare Access application response was invalid\n' >&2
		return 1
	fi
	[[ -n "$APP_ID" && -n "$APP_AUD" && "$APP_AUD" != null ]] || { printf 'Access application audience is missing for %s\n' "$domain" >&2; return 1; }
	if ! jq -e --arg name "$name" --arg domain "$domain" --arg idp "$idp" --argjson service401 "$service_401" \
		'.result.name == $name and .result.domain == $domain and .result.type == "self_hosted" and
		 .result.session_duration == "168h" and .result.auto_redirect_to_identity == true and
		 .result.app_launcher_visible == false and (.result.allowed_idps // []) == [$idp] and
		 (.result.service_auth_401_redirect // false) == $service401' <<<"$response" >/dev/null
then
		printf 'Access application did not converge to the reviewed definition: %s\n' "$domain" >&2
		return 1
	fi
}

upsert_owner_policy() {
	local app_id=$1 email=$2 precedence=$3 owner_name=${4:-$OWNER_POLICY_NAME} legacy_name=${5:-$LEGACY_OWNER_POLICY_NAME}
	local policies policy_id body
	if ! policies=$(cf_request GET "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies"); then
		return 1
	fi
	if [[ -n "$legacy_name" ]]; then
		if ! policy_id=$(jq -r --arg name "$owner_name" --arg legacy "$legacy_name" '[.result[] | select(.name == $name or .name == $legacy)] | if length == 1 then .[0].id elif length == 0 then empty else error("duplicate owner policies") end' <<<"$policies"); then
			printf 'Cloudflare owner-policy response was invalid\n' >&2
			return 1
		fi
	else
		if ! policy_id=$(jq -r --arg name "$owner_name" '[.result[] | select(.name == $name)] | if length == 1 then .[0].id elif length == 0 then empty else error("duplicate owner policies") end' <<<"$policies"); then
			printf 'Cloudflare owner-policy response was invalid\n' >&2
			return 1
		fi
	fi
	if ! body=$(jq -cn --arg name "$owner_name" --arg email "$email" --argjson precedence "$precedence" \
		'{name:$name,decision:"allow",precedence:$precedence,include:[{email:{email:$email}}]}'); then
		printf 'could not construct Cloudflare owner-policy request\n' >&2
		return 1
	fi
	if [[ -n "$policy_id" ]]; then
		if ! cf_request PUT "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies/$policy_id" "$body" >/dev/null; then
			return 1
		fi
	else
		if ! cf_request POST "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies" "$body" >/dev/null; then
			return 1
		fi
	fi
}

upsert_service_policy() {
	local app_id=$1 token_id=$2 service_name=${3:-$SERVICE_POLICY_NAME} legacy_name=${4:-$LEGACY_SERVICE_POLICY_NAME}
	local policies policy_id body
	if ! policies=$(cf_request GET "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies"); then
		return 1
	fi
	if [[ -n "$legacy_name" ]]; then
		if ! policy_id=$(jq -r --arg name "$service_name" --arg legacy "$legacy_name" '[.result[] | select(.name == $name or .name == $legacy)] | if length == 1 then .[0].id elif length == 0 then empty else error("duplicate service policies") end' <<<"$policies"); then
			printf 'Cloudflare service-policy response was invalid\n' >&2
			return 1
		fi
	else
		if ! policy_id=$(jq -r --arg name "$service_name" '[.result[] | select(.name == $name)] | if length == 1 then .[0].id elif length == 0 then empty else error("duplicate service policies") end' <<<"$policies"); then
			printf 'Cloudflare service-policy response was invalid\n' >&2
			return 1
		fi
	fi
	if ! body=$(jq -cn --arg name "$service_name" --arg token "$token_id" \
		'{name:$name,decision:"non_identity",precedence:1,include:[{service_token:{token_id:$token}}]}'); then
		printf 'could not construct Cloudflare service-policy request\n' >&2
		return 1
	fi
	if [[ -n "$policy_id" ]]; then
		if ! cf_request PUT "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies/$policy_id" "$body" >/dev/null; then
			return 1
		fi
	else
		if ! cf_request POST "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies" "$body" >/dev/null; then
			return 1
		fi
	fi
}

read_service_token_output() {
	local path=$1 mode
	[[ -f "$path" && ! -L "$path" ]] || return 1
	if ! mode=$(stat -c '%a' -- "$path"); then
		return 1
	fi
	[[ "$mode" = 600 ]] || { printf 'service-token output must be mode 0600: %s\n' "$path" >&2; return 1; }
	if ! CF_ACCESS_CLIENT_ID=$(awk -F= '$1 == "CF_ACCESS_CLIENT_ID" { print substr($0, index($0, "=") + 1) }' "$path"); then
		return 1
	fi
	if ! CF_ACCESS_CLIENT_SECRET=$(awk -F= '$1 == "CF_ACCESS_CLIENT_SECRET" { print substr($0, index($0, "=") + 1) }' "$path"); then
		return 1
	fi
	[[ -n "$CF_ACCESS_CLIENT_ID" && -n "$CF_ACCESS_CLIENT_SECRET" ]] || { printf 'service-token output is incomplete\n' >&2; return 1; }
}

ensure_service_token() {
	local tokens count token_id client_id client_secret response body created=0 created_expires_at= existing_name
	local legacy_service_token_name=${LEGACY_SERVICE_TOKEN_NAME:-}
	if ! tokens=$(cf_request GET "/accounts/$ACCOUNT_ID/access/service_tokens?per_page=100"); then
		return 1
	fi
	if [[ -n "$legacy_service_token_name" ]]; then
		if ! count=$(jq -r --arg name "$SERVICE_TOKEN_NAME" --arg legacy "$legacy_service_token_name" '[.result[] | select(.name == $name or .name == $legacy)] | length' <<<"$tokens"); then
			printf 'Cloudflare service-token response was invalid\n' >&2
			return 1
		fi
	else
		if ! count=$(jq -r --arg name "$SERVICE_TOKEN_NAME" '[.result[] | select(.name == $name)] | length' <<<"$tokens"); then
			printf 'Cloudflare service-token response was invalid\n' >&2
			return 1
		fi
	fi
	if [[ "$count" != 0 && "$count" != 1 ]]; then
		printf 'Cloudflare service-token response was invalid\n' >&2
		return 1
	fi
	if [[ "$count" = 0 ]]; then
		if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
			printf 'hostname transition requires the retained service token; refusing to create a replacement\n' >&2
			return 1
		fi
		if [[ "$REQUIRE_DURABLE_SERVICE_TOKEN_CAPTURE" = 1 ]]; then
			printf 'refusing to create a Cloudflare service token without a durable secret-capture destination; run manual prepare first\n' >&2
			return 1
		fi
		# Validate the one-time capture destination before creating anything.
		# Cloudflare will not show the secret again if a pre-existing output
		# path causes the write to fail after creation.
		[[ ! -e "$SERVICE_TOKEN_OUTPUT" && ! -L "$SERVICE_TOKEN_OUTPUT" ]] || {
			printf 'refusing to create a service token over an existing output path\n' >&2
			return 1
		}
		if ! body=$(jq -cn --arg name "$SERVICE_TOKEN_NAME" '{name:$name,duration:"8760h"}'); then
			printf 'could not construct Cloudflare service-token request\n' >&2
			return 1
		fi
		if ! response=$(cf_request POST "/accounts/$ACCOUNT_ID/access/service_tokens" "$body"); then
			return 1
		fi
		if ! token_id=$(jq -r '.result.id // empty' <<<"$response"); then
			printf 'Cloudflare service-token response was invalid\n' >&2
			return 1
		fi
		if [[ -n "$token_id" && "$token_id" != null ]]; then
			created=1
			CREATED_SERVICE_TOKEN_ID=$token_id
		fi
		if ! client_id=$(jq -r '.result.client_id // empty' <<<"$response") || \
			! client_secret=$(jq -r '.result.client_secret // empty' <<<"$response") || \
			! created_expires_at=$(jq -r '.result.expires_at // .result.expiration_time // empty' <<<"$response"); then
			printf 'Cloudflare service-token response was invalid\n' >&2
			return 1
		fi
		# The list response used to select a token predates the POST. Refresh it
		# before checking expiry so a newly created token is validated by the same
		# one-year/future policy as a reused token.
		if [[ -z "$created_expires_at" ]]; then
			if ! tokens=$(cf_request GET "/accounts/$ACCOUNT_ID/access/service_tokens?per_page=100"); then
				return 1
			fi
		fi
	else
		[[ "$count" = 1 ]] || { printf 'Cloudflare service token name is ambiguous\n' >&2; return 1; }
		if [[ -n "$legacy_service_token_name" ]]; then
			if ! token_id=$(jq -r --arg name "$SERVICE_TOKEN_NAME" --arg legacy "$legacy_service_token_name" '.result[] | select(.name == $name or .name == $legacy) | .id' <<<"$tokens") || \
				! client_id=$(jq -r --arg name "$SERVICE_TOKEN_NAME" --arg legacy "$legacy_service_token_name" '.result[] | select(.name == $name or .name == $legacy) | (.client_id // empty)' <<<"$tokens") || \
				! existing_name=$(jq -r --arg name "$SERVICE_TOKEN_NAME" --arg legacy "$legacy_service_token_name" '.result[] | select(.name == $name or .name == $legacy) | .name' <<<"$tokens"); then
				printf 'Cloudflare service-token response was invalid\n' >&2
				return 1
			fi
		else
			if ! token_id=$(jq -r --arg name "$SERVICE_TOKEN_NAME" '.result[] | select(.name == $name) | .id' <<<"$tokens") || \
				! client_id=$(jq -r --arg name "$SERVICE_TOKEN_NAME" '.result[] | select(.name == $name) | (.client_id // empty)' <<<"$tokens") || \
				! existing_name=$(jq -r --arg name "$SERVICE_TOKEN_NAME" '.result[] | select(.name == $name) | .name' <<<"$tokens"); then
				printf 'Cloudflare service-token response was invalid\n' >&2
				return 1
			fi
		fi
		if [[ -n "$legacy_service_token_name" && "$existing_name" = "$legacy_service_token_name" ]]; then
			body=$(jq -cn --arg name "$SERVICE_TOKEN_NAME" '{name:$name}') || return 1
			cf_request PUT "/accounts/$ACCOUNT_ID/access/service_tokens/$token_id" "$body" >/dev/null || return 1
			tokens=$(cf_request GET "/accounts/$ACCOUNT_ID/access/service_tokens?per_page=100") || return 1
			jq -e --arg id "$token_id" --arg name "$SERVICE_TOKEN_NAME" '([.result[] | select(.id == $id and .name == $name)] | length) == 1' <<<"$tokens" >/dev/null || {
				printf 'Cloudflare service token did not converge to the Helm name\n' >&2
				return 1
			}
		fi
		client_secret=
	fi
	[[ -n "$token_id" && "$token_id" != null ]] || { printf 'Cloudflare service token ID is missing\n' >&2; return 1; }
	# A named token is not sufficient: later prepares must reject an expired or
	# unexpectedly long-lived credential before wiring it into the API policy.
	local expires_at expiry_epoch now_epoch
	if [[ -n "$created_expires_at" ]]; then
		expires_at=$created_expires_at
	else
		if ! expires_at=$(jq -r --arg id "$token_id" '.result[] | select(.id == $id) | (.expires_at // .expiration_time // empty)' <<<"$tokens"); then
			printf 'Cloudflare service-token response was invalid\n' >&2
			return 1
		fi
	fi
	[[ "$expires_at" != "" && "$expires_at" != null ]] || {
		printf 'Cloudflare service token expiry is missing\n' >&2
		return 1
	}
	if ! expiry_epoch=$(date -u -d "$expires_at" +%s 2>/dev/null); then
		printf 'Cloudflare service token expiry is invalid\n' >&2
		return 1
	fi
	if ! now_epoch=$(date -u +%s); then
		printf 'could not read current time for service-token expiry\n' >&2
		return 1
	fi
	(( expiry_epoch > now_epoch )) || {
		printf 'Cloudflare service token is expired\n' >&2
		return 1
	}
	(( expiry_epoch <= now_epoch + 366*24*60*60 )) || {
		printf 'Cloudflare service token duration exceeds the reviewed one-year limit\n' >&2
		return 1
	}

	if (( created )); then
		[[ -n "$client_id" && -n "$client_secret" && "$client_secret" != null ]] || {
			printf 'Cloudflare returned no one-time service token secret\n' >&2
			return 1
		}
		if ! install -d -m 0700 "$(dirname "$SERVICE_TOKEN_OUTPUT")"; then
			printf 'could not create service-token output directory\n' >&2
			return 1
		fi
		local temporary
		if ! temporary=$(mktemp "${SERVICE_TOKEN_OUTPUT}.XXXXXX"); then
			printf 'could not create service-token temporary output\n' >&2
			return 1
		fi
		umask 077
		if ! printf 'CF_ACCESS_CLIENT_ID=%s\nCF_ACCESS_CLIENT_SECRET=%s\nHELM_ACCESS_SERVICE_TOKEN_ID=%s\nROADMAP_ACCESS_SERVICE_TOKEN_ID=%s\n' "$client_id" "$client_secret" "$token_id" "$token_id" > "$temporary" || \
			! chmod 0600 "$temporary" || \
			! mv -T -- "$temporary" "$SERVICE_TOKEN_OUTPUT"; then
			rm -f -- "$temporary"
			printf 'could not atomically install service-token output\n' >&2
			return 1
		fi
		CREATED_SERVICE_TOKEN_OUTPUT=$SERVICE_TOKEN_OUTPUT
	elif [[ -e "$SERVICE_TOKEN_OUTPUT" ]]; then
		read_service_token_output "$SERVICE_TOKEN_OUTPUT" || return 1
		[[ -z "$client_id" || "$client_id" = "$CF_ACCESS_CLIENT_ID" ]] || {
			printf 'existing service-token output does not match Cloudflare\n' >&2
			return 1
		}
	else
		printf 'Cloudflare service token already exists; its one-time secret is not fetched again\n' >&2
	fi
	SERVICE_TOKEN_ID=$token_id
}

validate_policy_set() {
	local app_id=$1 expected_count=$2 email=$3 token_id=${4:-} owner_name=${5:-$OWNER_POLICY_NAME} service_name=${6:-$SERVICE_POLICY_NAME}
	local policies owner_precedence=1
	if ! policies=$(cf_request GET "/accounts/$ACCOUNT_ID/access/apps/$app_id/policies"); then
		return 1
	fi
	if [[ "$expected_count" = 1 ]]; then
		if ! jq -e --arg name "$owner_name" --arg email "$email" --argjson owner_precedence "$owner_precedence" \
			'([.result[]] | length == 1) and
			 ([.result[] | select(.name == $name)] | length == 1) and
			 ([.result[] | select(.name == $name)][0].decision == "allow") and
			 ([.result[] | select(.name == $name)][0].precedence == $owner_precedence) and
			 ([.result[] | select(.name == $name)][0].include == [{email:{email:$email}}])' <<<"$policies" >/dev/null; then
			printf 'UI Access policy set is not exactly the reviewed owner policy\n' >&2
			return 1
		fi
	else
		owner_precedence=2
		if ! jq -e --arg owner "$owner_name" --arg service "$service_name" --arg email "$email" --arg token "$token_id" --argjson owner_precedence "$owner_precedence" \
			'([.result[]] | length == 2) and
			 ([.result[] | select(.name == $owner or .name == $service)] | length == 2) and
			 ([.result[] | select(.name == $owner)] | length == 1) and
			 ([.result[] | select(.name == $service)] | length == 1) and
			 ([.result[] | select(.name == $owner)][0].decision == "allow") and
			 ([.result[] | select(.name == $owner)][0].precedence == $owner_precedence) and
			 ([.result[] | select(.name == $owner)][0].include == [{email:{email:$email}}]) and
			 ([.result[] | select(.name == $service)][0].decision == "non_identity") and
			 ([.result[] | select(.name == $service)][0].precedence == 1) and
			 ([.result[] | select(.name == $service)][0].include == [{service_token:{token_id:$token}}])' <<<"$policies" >/dev/null; then
				printf 'API Access policy set is not exactly the reviewed owner and Service Auth policies\n' >&2
				return 1
		fi
	fi
}

validate_owner_env() {
	local path=$1 email=$2 issuer=$3 ui_aud=$4 api_aud=$5
	local expected_public_url=${DOMAIN_PROFILE_PUBLIC_URL:-${PUBLIC_URL:-}}
	[[ -n "$expected_public_url" ]] || {
		printf 'deployment domain profile is not loaded\n' >&2
		return 1
	}
	local public_url=${6:-$expected_public_url}
	local legacy_ui_aud=${7:-$ui_aud} legacy_api_aud=${8:-$api_aud}
	local canonical_ui_aud=${9:-} canonical_api_aud=${10:-}
	local host_audience_map=${11:-} legacy_origin=${12:-${DOMAIN_PROFILE_LEGACY_ORIGIN:-}}
	local phase=${DOMAIN_PROFILE_PHASE:-legacy} dual=${DOMAIN_PROFILE_DUAL_HOST:-0}
	[[ -n "$public_url" && "$public_url" = "$expected_public_url" ]] || {
		printf 'generated owner environment public origin does not match the selected deployment environment\n' >&2
		return 1
	}
	[[ -s "$path" && ! -L "$path" ]] || {
		printf 'generated owner environment is empty or not regular: %s\n' "$path" >&2
		return 1
	}
	[[ "$(tail -c 1 "$path" | od -An -t x1 | tr -d '[:space:]')" = 0a ]] || {
		printf 'generated owner environment must end with a newline\n' >&2
		return 1
	}
	[[ "$phase" = legacy || "$phase" = staged || "$phase" = canonical ]] || {
		printf 'generated owner environment phase is invalid\n' >&2
		return 1
	}
	if [[ "$dual" = 1 ]]; then
		[[ "$phase" = staged || "$phase" = canonical ]] || {
			printf 'generated owner environment has dual-host fields in legacy phase\n' >&2
			return 1
		}
		[[ "$legacy_ui_aud" =~ ^[A-Za-z0-9_-]+$ && "$legacy_api_aud" =~ ^[A-Za-z0-9_-]+$ &&
			"$canonical_ui_aud" =~ ^[A-Za-z0-9_-]+$ && "$canonical_api_aud" =~ ^[A-Za-z0-9_-]+$ ]] || {
			printf 'generated owner environment audience is invalid\n' >&2
			return 1
		}
		[[ -n "${DOMAIN_PROFILE_LEGACY_HOST:-}" && -n "${DOMAIN_PROFILE_CANONICAL_HOST:-}" ]] || {
			printf 'deployment domain profile host pair is missing\n' >&2
			return 1
		}
		host_audience_map=${host_audience_map:-$DOMAIN_PROFILE_LEGACY_HOST=$legacy_ui_aud,$legacy_api_aud\;$DOMAIN_PROFILE_CANONICAL_HOST=$canonical_ui_aud,$canonical_api_aud}
		[[ "$host_audience_map" = "$DOMAIN_PROFILE_LEGACY_HOST=$legacy_ui_aud,$legacy_api_aud;$DOMAIN_PROFILE_CANONICAL_HOST=$canonical_ui_aud,$canonical_api_aud" ]] || {
			printf 'generated owner environment host-audience map is not exactly the reviewed pair\n' >&2
			return 1
		}
	else
		[[ "$phase" = legacy && -z "$canonical_ui_aud" && -z "$canonical_api_aud" && -z "$host_audience_map" ]] || {
			printf 'generated owner environment has unexpected dual-host fields\n' >&2
			return 1
		}
		legacy_ui_aud=$ui_aud
		legacy_api_aud=$api_aud
	fi
	if [[ "$phase" = canonical ]]; then
		[[ "$legacy_origin" = "${DOMAIN_PROFILE_LEGACY_URL:-}" && -n "$legacy_origin" ]] || {
			printf 'generated canonical environment must contain the reviewed legacy origin\n' >&2
			return 1
		}
	else
		[[ -z "$legacy_origin" ]] || {
			printf 'generated noncanonical environment must not contain a legacy origin\n' >&2
			return 1
		}
	fi
	local expected_audiences="$legacy_ui_aud,$legacy_api_aud"
	if [[ "$dual" = 1 ]]; then
		expected_audiences="$expected_audiences,$canonical_ui_aud,$canonical_api_aud"
	fi
	local expected_host_map=$host_audience_map
	local line key count expected_line
	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ "$line" != *$'\r'* ]] || {
			printf 'generated owner environment contains a carriage return\n' >&2
			return 1
		}
		case "$line" in
			''|'#'*) ;;
			HELM_ADDR=*|HELM_DB=*|HELM_LUNA_ENABLED=*|HELM_LUNA_MODEL=*|HELM_LUNA_EFFORT=*|\
			HELM_AUTH_MODE=*|HELM_PUBLIC_ORIGIN=*|\
			HELM_ADMIN_EMAIL=*|HELM_CLOUDFLARE_ISSUER=*|HELM_CF_ACCESS_AUDIENCES=*|\
			HELM_CF_ACCESS_HOST_AUDIENCES=*|HELM_LEGACY_ORIGIN=*|\
			HELM_SECURE_COOKIES=*|HELM_DEMO_SEED=*|\
			ROADMAP_ADDR=*|ROADMAP_DB=*|ROADMAP_AUTH_MODE=*|ROADMAP_PUBLIC_ORIGIN=*|\
			ROADMAP_ADMIN_EMAIL=*|ROADMAP_CLOUDFLARE_ISSUER=*|ROADMAP_CF_ACCESS_AUDIENCES=*|\
			ROADMAP_CF_ACCESS_HOST_AUDIENCES=*|ROADMAP_LEGACY_ORIGIN=*|\
			ROADMAP_SECURE_COOKIES=*|ROADMAP_DEMO_SEED=*) ;;
			*)
				printf 'generated owner environment contains an unexpected line\n' >&2
				return 1
				;;
		esac
	done < "$path"

	local expected_lines=(
		'HELM_ADDR=127.0.0.1:8080'
		'HELM_DB=/var/lib/roadmap/data/roadmap.db'
		'HELM_LUNA_ENABLED=true'
		'HELM_LUNA_MODEL=gpt-5.6-luna'
		'HELM_LUNA_EFFORT=medium'
		'HELM_AUTH_MODE=cloudflare'
		"HELM_PUBLIC_ORIGIN=$public_url"
		"HELM_ADMIN_EMAIL=$email"
		"HELM_CLOUDFLARE_ISSUER=$issuer"
		"HELM_CF_ACCESS_AUDIENCES=$expected_audiences"
		'HELM_SECURE_COOKIES=true'
		'HELM_DEMO_SEED=false'
		'ROADMAP_ADDR=127.0.0.1:8080'
		'ROADMAP_DB=/var/lib/roadmap/data/roadmap.db'
		'ROADMAP_AUTH_MODE=cloudflare'
		"ROADMAP_PUBLIC_ORIGIN=$public_url"
		"ROADMAP_ADMIN_EMAIL=$email"
		"ROADMAP_CLOUDFLARE_ISSUER=$issuer"
		"ROADMAP_CF_ACCESS_AUDIENCES=$expected_audiences"
		'ROADMAP_SECURE_COOKIES=true'
		'ROADMAP_DEMO_SEED=false'
	)
	for expected_line in "${expected_lines[@]}"; do
		key=${expected_line%%=*}
		count=$(awk -F= -v key="$key" '$1 == key { count++ } END { print count + 0 }' "$path") || {
			printf 'could not inspect generated owner environment key: %s\n' "$key" >&2
			return 1
		}
		[[ "$count" = 1 ]] || {
			printf 'generated owner environment key is missing or duplicated: %s\n' "$key" >&2
			return 1
		}
		awk -v expected="$expected_line" '$0 == expected { found++ } END { exit(found != 1) }' "$path" || {
			printf 'generated owner environment value is wrong: %s\n' "$key" >&2
			return 1
		}
		done
	local optional_key optional_value optional_count
	for optional_key in HELM_CF_ACCESS_HOST_AUDIENCES ROADMAP_CF_ACCESS_HOST_AUDIENCES HELM_LEGACY_ORIGIN ROADMAP_LEGACY_ORIGIN; do
		optional_value=
		case "$optional_key" in
			HELM_CF_ACCESS_HOST_AUDIENCES|ROADMAP_CF_ACCESS_HOST_AUDIENCES)
				[[ "$dual" = 1 ]] && optional_value=$expected_host_map
				;;
			HELM_LEGACY_ORIGIN|ROADMAP_LEGACY_ORIGIN)
				[[ "$phase" = canonical ]] && optional_value=$legacy_origin
				;;
		 esac
		optional_count=$(awk -F= -v key="$optional_key" '$1 == key { count++ } END { print count + 0 }' "$path") || {
			printf 'could not inspect generated owner environment key: %s\n' "$optional_key" >&2
			return 1
		}
		if [[ -n "$optional_value" ]]; then
			[[ "$optional_count" = 1 ]] || {
				printf 'generated owner environment key is missing or duplicated: %s\n' "$optional_key" >&2
				return 1
			}
			awk -v expected="$optional_key=$optional_value" '$0 == expected { found++ } END { exit(found != 1) }' "$path" || {
				printf 'generated owner environment value is wrong: %s\n' "$optional_key" >&2
				return 1
			}
		else
			[[ "$optional_count" = 0 ]] || {
				printf 'generated owner environment contains an unexpected key: %s\n' "$optional_key" >&2
				return 1
			}
		fi
	done
}

restore_prepare_output() {
	local output=$1 backup=$2 existed=$3
	if [[ "$existed" = 1 ]]; then
		[[ -n "$backup" && -f "$backup" && ! -L "$backup" ]] || return 1
		mv -T -- "$backup" "$output"
	else
		[[ ! -L "$output" ]] || return 1
		rm -f -- "$output"
	fi
}

write_prepare_outputs() {
	local tunnel_id=$1 email=$2 issuer=$3 ui_aud=$4 api_aud=$5
	local expected_public_url=${DOMAIN_PROFILE_PUBLIC_URL:-${PUBLIC_URL:-}}
	[[ -n "$expected_public_url" ]] || {
		printf 'deployment domain profile is not loaded\n' >&2
		return 1
	}
	local public_url=${6:-$expected_public_url}
	local legacy_ui_aud=${7:-$ui_aud} legacy_api_aud=${8:-$api_aud}
	local canonical_ui_aud=${9:-} canonical_api_aud=${10:-}
	local host_audience_map=${11:-} legacy_origin=${12:-${DOMAIN_PROFILE_LEGACY_ORIGIN:-}} output_path
	[[ -n "$TOKEN_OUTPUT" && -n "$OWNER_ENV_OUTPUT" ]] || {
		printf 'prepare requires token and owner-environment output paths\n' >&2
		return 1
	}
	[[ "$TOKEN_OUTPUT" != "$OWNER_ENV_OUTPUT" ]] || {
		printf 'prepare token and owner-environment outputs must differ\n' >&2
		return 1
	}
	[[ -n "$public_url" && "$public_url" = "$expected_public_url" ]] || {
		printf 'owner-environment public origin does not match the selected deployment environment\n' >&2
		return 1
	}
	valid_email "$email" || {
		printf 'Cloudflare owner email is invalid\n' >&2
		return 1
	}
	[[ "$issuer" =~ ^https://[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || {
		printf 'Cloudflare Access issuer is invalid\n' >&2
		return 1
	}
	[[ "$ui_aud" =~ ^[A-Za-z0-9_-]+$ && "$api_aud" =~ ^[A-Za-z0-9_-]+$ ]] || {
		printf 'Cloudflare Access audience is invalid\n' >&2
		return 1
	}
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		[[ "$legacy_ui_aud" =~ ^[A-Za-z0-9_-]+$ && "$legacy_api_aud" =~ ^[A-Za-z0-9_-]+$ &&
			"$canonical_ui_aud" =~ ^[A-Za-z0-9_-]+$ && "$canonical_api_aud" =~ ^[A-Za-z0-9_-]+$ ]] || {
			printf 'Cloudflare Access transition audiences are invalid\n' >&2
			return 1
		}
		[[ -n "$host_audience_map" ]] || host_audience_map="$DOMAIN_PROFILE_LEGACY_HOST=$legacy_ui_aud,$legacy_api_aud;$DOMAIN_PROFILE_CANONICAL_HOST=$canonical_ui_aud,$canonical_api_aud"
	else
		legacy_ui_aud=$ui_aud
		legacy_api_aud=$api_aud
		canonical_ui_aud=
		canonical_api_aud=
		host_audience_map=
	fi
	if [[ "${DOMAIN_PROFILE_PHASE:-legacy}" = canonical ]]; then
		legacy_origin=${legacy_origin:-${DOMAIN_PROFILE_LEGACY_URL:-}}
	else
		legacy_origin=
	fi
	if ! install -d -m 0700 "$(dirname "$TOKEN_OUTPUT")" "$(dirname "$OWNER_ENV_OUTPUT")"; then
		printf 'could not create prepare output directories\n' >&2
		return 1
	fi
	[[ ! -L "$TOKEN_OUTPUT" && ! -L "$OWNER_ENV_OUTPUT" ]] || {
		printf 'prepare output paths must not be symlinks\n' >&2
		return 1
	}
	for output_path in "$TOKEN_OUTPUT" "$OWNER_ENV_OUTPUT"; do
		if [[ -e "$output_path" && ( ! -f "$output_path" || -L "$output_path" ) ]]; then
			printf 'prepare output paths must be regular files when present\n' >&2
			return 1
		fi
	done
	umask 077
	local token_tmp="" owner_tmp="" token_value template
	if ! token_tmp=$(mktemp "${TOKEN_OUTPUT}.XXXXXX"); then
		printf 'could not create tunnel-token temporary output\n' >&2
		return 1
	fi
	if ! owner_tmp=$(mktemp "${OWNER_ENV_OUTPUT}.XXXXXX"); then
		rm -f -- "$token_tmp"
		printf 'could not create owner-environment temporary output\n' >&2
		return 1
	fi
	if ! cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/token" | \
		jq -er '.result | select(type == "string" and length > 0)' > "$token_tmp"; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'Cloudflare tunnel token request failed\n' >&2
		return 1
	fi
	if [[ ! -s "$token_tmp" ]] || ! token_value=$(<"$token_tmp") || \
		[[ -z "$token_value" || "$token_value" = *$'\n'* || "$token_value" = *$'\r'* ]]; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'Cloudflare tunnel token is empty\n' >&2
		return 1
	fi
	if ! chmod 0600 "$token_tmp"; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'could not secure tunnel-token temporary output\n' >&2
		return 1
	fi
	template="$ROOT_DIR/deploy/helm.env.template"
	if [[ ! -f "$template" || -L "$template" ]]; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'owner-environment template is missing or not regular\n' >&2
		return 1
	fi
	# Bash/awk replacement values are already restricted above. Unlike sed's
	# slash-delimited expression, awk treats the HTTPS issuer literally.
	if ! awk -v owner_email="$email" -v issuer="$issuer" -v public_origin="$public_url" \
		-v ui_aud="$legacy_ui_aud" -v api_aud="$legacy_api_aud" \
		-v canonical_ui_aud="$canonical_ui_aud" -v canonical_api_aud="$canonical_api_aud" \
		-v host_audience_map="$host_audience_map" -v legacy_origin="$legacy_origin" '
		{
			line = $0
			gsub(/@OWNER_EMAIL@/, owner_email, line)
			gsub(/@CLOUDFLARE_ISSUER@/, issuer, line)
			gsub(/@PUBLIC_ORIGIN@/, public_origin, line)
			gsub(/@UI_AUDIENCE@/, ui_aud, line)
			gsub(/@API_AUDIENCE@/, api_aud, line)
			gsub(/@LEGACY_UI_AUDIENCE@/, ui_aud, line)
			gsub(/@LEGACY_API_AUDIENCE@/, api_aud, line)
			gsub(/@CANONICAL_UI_AUDIENCE@/, canonical_ui_aud, line)
			gsub(/@CANONICAL_API_AUDIENCE@/, canonical_api_aud, line)
			gsub(/@HOST_AUDIENCE_MAP@/, host_audience_map, line)
			gsub(/@HELM_CF_ACCESS_HOST_AUDIENCES@/, host_audience_map, line)
			gsub(/@LEGACY_ORIGIN@/, legacy_origin, line)
			gsub(/@HELM_LEGACY_ORIGIN@/, legacy_origin, line)
			if (host_audience_map != "" && line ~ /^HELM_CF_ACCESS_AUDIENCES=/) line = "HELM_CF_ACCESS_AUDIENCES=" ui_aud "," api_aud "," canonical_ui_aud "," canonical_api_aud
			if (host_audience_map != "" && line ~ /^ROADMAP_CF_ACCESS_AUDIENCES=/) line = "ROADMAP_CF_ACCESS_AUDIENCES=" ui_aud "," api_aud "," canonical_ui_aud "," canonical_api_aud
			if (legacy_origin == "" && (line ~ /^HELM_LEGACY_ORIGIN=/ || line ~ /^ROADMAP_LEGACY_ORIGIN=/)) next
			if (host_audience_map == "" && (line ~ /^HELM_CF_ACCESS_HOST_AUDIENCES=/ || line ~ /^ROADMAP_CF_ACCESS_HOST_AUDIENCES=/)) next
			print line
		}
	' "$template" > "$owner_tmp"; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'could not render owner environment\n' >&2
		return 1
	fi
	if ! validate_owner_env "$owner_tmp" "$email" "$issuer" "$legacy_ui_aud" "$legacy_api_aud" "$public_url" \
		"$legacy_ui_aud" "$legacy_api_aud" "$canonical_ui_aud" "$canonical_api_aud" \
		"$host_audience_map" "$legacy_origin"; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'generated owner environment failed validation\n' >&2
		return 1
	fi
	if ! chmod 0600 "$owner_tmp"; then
		rm -f -- "$token_tmp" "$owner_tmp"
		printf 'could not secure owner-environment temporary output\n' >&2
		return 1
	fi
	local token_existed=0 owner_existed=0 token_backup="" owner_backup="" token_old_staged=0 owner_old_staged=0
	[[ -e "$TOKEN_OUTPUT" ]] && token_existed=1
	[[ -e "$OWNER_ENV_OUTPUT" ]] && owner_existed=1
	if (( token_existed )); then
		if ! token_backup=$(mktemp "${TOKEN_OUTPUT}.backup.XXXXXX") || \
			! mv -T -- "$TOKEN_OUTPUT" "$token_backup"; then
			rm -f -- "$token_backup" "$token_tmp" "$owner_tmp"
			printf 'could not stage existing tunnel-token output\n' >&2
			return 1
		fi
		token_old_staged=1
	fi
	if (( owner_existed )); then
		if ! owner_backup=$(mktemp "${OWNER_ENV_OUTPUT}.backup.XXXXXX") || \
			! mv -T -- "$OWNER_ENV_OUTPUT" "$owner_backup"; then
			if (( token_old_staged )); then
				restore_prepare_output "$TOKEN_OUTPUT" "$token_backup" "$token_existed" || true
			fi
			rm -f -- "$token_backup" "$owner_backup" "$token_tmp" "$owner_tmp"
			printf 'could not stage existing owner-environment output\n' >&2
			return 1
		fi
		owner_old_staged=1
	fi

	# Commit each independently atomic rename only after both old outputs are
	# staged. If the second rename fails, restore both old files byte-for-byte
	# (or remove newly-created paths when no old file existed).
	if ! mv -T -- "$token_tmp" "$TOKEN_OUTPUT"; then
		if (( owner_old_staged )); then
			restore_prepare_output "$OWNER_ENV_OUTPUT" "$owner_backup" "$owner_existed" || true
		fi
		if (( token_old_staged )); then
			restore_prepare_output "$TOKEN_OUTPUT" "$token_backup" "$token_existed" || true
		fi
		rm -f -- "$token_tmp" "$owner_tmp" "$token_backup" "$owner_backup"
		printf 'could not atomically install tunnel-token output\n' >&2
		return 1
	fi
	token_tmp=
	if ! mv -T -- "$owner_tmp" "$OWNER_ENV_OUTPUT"; then
		restore_prepare_output "$OWNER_ENV_OUTPUT" "$owner_backup" "$owner_existed" || true
		restore_prepare_output "$TOKEN_OUTPUT" "$token_backup" "$token_existed" || true
		rm -f -- "$owner_tmp" "$token_backup" "$owner_backup"
		printf 'could not atomically install owner-environment output; prior outputs restored\n' >&2
		return 1
	fi
	owner_tmp=
	rm -f -- "$token_backup" "$owner_backup" || true
	return 0
}

validate_tunnel_config() {
	local team=$1 ui_aud=$2 api_aud=$3 tunnel_id=$4
	local legacy_host=${5:-} legacy_ui_aud=${6:-} legacy_api_aud=${7:-}
	local canonical_host=${8:-} canonical_ui_aud=${9:-} canonical_api_aud=${10:-}
	local single_host=${11:-${PUBLIC_HOST:-}} tunnel_config=${12:-}
	if [[ -z "$tunnel_config" ]]; then
		if ! tunnel_config=$(cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations"); then
			return 1
		fi
	fi
	if [[ -z "$legacy_host" ]]; then
		if ! jq -e --arg host "$single_host" --arg team "$team" --arg ui "$ui_aud" --arg api "$api_aud" '
			(.result.config // null) as $config |
			($config.ingress // []) as $ingress |
			($config | type == "object" and (keys | sort) == ["ingress"]) and
			($ingress | type == "array" and length == 2) and
			([$ingress[] | select(.hostname == $host)] | length == 1) and
			([$ingress[] | select(.service == "http_status:404" and ((.hostname // null) == null))] | length == 1) and
			(($ingress[0] | keys | sort) == ["hostname", "originRequest", "service"]) and
			(($ingress[0].originRequest | keys | sort) == ["access"]) and
			(($ingress[0].originRequest.access | keys | sort) == ["audTag", "required", "teamName"]) and
			($ingress[0].hostname == $host) and
			($ingress[0].service == "http://127.0.0.1:8080") and
			($ingress[0].originRequest.access.required == true) and
			($ingress[0].originRequest.access.teamName == $team) and
			((($ingress[0].originRequest.access.audTag // []) | sort) == ([$ui, $api] | sort)) and
			(($ingress[1] | keys | sort) == ["service"]) and
			($ingress[1].service == "http_status:404")
		' <<<"$tunnel_config" >/dev/null; then
			printf 'Cloudflare tunnel configuration is not exactly the reviewed ingress set\n' >&2
			return 1
		fi
	else
		if ! jq -e --arg legacy_host "$legacy_host" --arg canonical_host "$canonical_host" \
			--arg team "$team" --arg legacy_ui "$legacy_ui_aud" --arg legacy_api "$legacy_api_aud" \
			--arg canonical_ui "$canonical_ui_aud" --arg canonical_api "$canonical_api_aud" '
			(.result.config // null) as $config |
			($config.ingress // []) as $ingress |
			($config | type == "object" and (keys | sort) == ["ingress"]) and
			($ingress | type == "array" and length == 3) and
			(($ingress[0] | keys | sort) == ["hostname", "originRequest", "service"]) and
			(($ingress[1] | keys | sort) == ["hostname", "originRequest", "service"]) and
			(($ingress[2] | keys | sort) == ["service"]) and
			($ingress[0].hostname == $legacy_host) and ($ingress[1].hostname == $canonical_host) and
			($ingress[0].service == "http://127.0.0.1:8080") and ($ingress[1].service == "http://127.0.0.1:8080") and
			($ingress[2].service == "http_status:404") and
			(($ingress[0].originRequest | keys | sort) == ["access"]) and
			(($ingress[1].originRequest | keys | sort) == ["access"]) and
			(($ingress[0].originRequest.access | keys | sort) == ["audTag", "required", "teamName"]) and
			(($ingress[1].originRequest.access | keys | sort) == ["audTag", "required", "teamName"]) and
			($ingress[0].originRequest.access.required == true) and ($ingress[1].originRequest.access.required == true) and
			($ingress[0].originRequest.access.teamName == $team) and ($ingress[1].originRequest.access.teamName == $team) and
			((($ingress[0].originRequest.access.audTag // []) | sort) == ([$legacy_ui, $legacy_api] | sort)) and
			((($ingress[1].originRequest.access.audTag // []) | sort) == ([$canonical_ui, $canonical_api] | sort))
		' <<<"$tunnel_config" >/dev/null; then
			printf 'Cloudflare dual-host tunnel configuration is not exactly the reviewed ingress set\n' >&2
			return 1
		fi
	fi
}

configure_tunnel() {
	local team=$1 ui_aud=$2 api_aud=$3 tunnel_id=$4
	local legacy_host=${5:-} legacy_ui_aud=${6:-} legacy_api_aud=${7:-}
	local canonical_host=${8:-} canonical_ui_aud=${9:-} canonical_api_aud=${10:-} body
	if [[ -z "$legacy_host" ]]; then
		if ! body=$(jq -cn --arg host "$PUBLIC_HOST" --arg team "$team" --arg ui "$ui_aud" --arg aud "$api_aud" \
			'{config:{ingress:[
				{hostname:$host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$ui,$aud]}}},
				{service:"http_status:404"}
			]}}'); then
			printf 'could not construct Cloudflare tunnel configuration request\n' >&2
			return 1
		fi
	else
		if ! body=$(jq -cn --arg legacy_host "$legacy_host" --arg canonical_host "$canonical_host" \
			--arg team "$team" --arg legacy_ui "$legacy_ui_aud" --arg legacy_api "$legacy_api_aud" \
			--arg canonical_ui "$canonical_ui_aud" --arg canonical_api "$canonical_api_aud" \
			'{config:{ingress:[
				{hostname:$legacy_host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$legacy_ui,$legacy_api]}}},
				{hostname:$canonical_host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$canonical_ui,$canonical_api]}}},
				{service:"http_status:404"}
			]}}'); then
			printf 'could not construct Cloudflare dual-host tunnel configuration request\n' >&2
			return 1
		fi
	fi
	if ! cf_request PUT "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations" "$body" >/dev/null; then
		return 1
	fi
	if ! validate_tunnel_config "$team" "$ui_aud" "$api_aud" "$tunnel_id" \
		"$legacy_host" "$legacy_ui_aud" "$legacy_api_aud" "$canonical_host" "$canonical_ui_aud" "$canonical_api_aud"; then
		printf 'Cloudflare tunnel configuration did not converge to the reviewed ingress set\n' >&2
		return 1
	fi
}

capture_tunnel_before_state() {
	local tunnel_id=$1 phase=$2 config summary canonical_config digest config_keys
	if ! config=$(cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations"); then
		printf 'could not capture the existing Cloudflare tunnel configuration before reconciliation\n' >&2
		return 1
	fi
	if ! canonical_config=$(jq -cS '.result.config // error("missing tunnel config") | select(type == "object")' <<<"$config"); then
		printf 'existing Cloudflare tunnel configuration was not a complete object\n' >&2
		return 1
	fi
	if ! digest=$(printf '%s\n' "$canonical_config" | sha256sum | awk '{print $1}'); then
		printf 'could not digest the existing Cloudflare tunnel configuration\n' >&2
		return 1
	fi
	if ! config_keys=$(jq -cS '.result.config | keys' <<<"$config"); then
		printf 'could not inspect the existing Cloudflare tunnel configuration\n' >&2
		return 1
	fi
	if ! summary=$(jq -c --arg phase "$phase" --arg tunnel_id "$tunnel_id" --arg digest "$digest" --argjson config_keys "$config_keys" \
		'{phase:$phase,tunnel_id:$tunnel_id,config_sha256:$digest,config_keys:$config_keys,rollback_capture:"digest-only; provider restore requires a separate retained config",ingress:(.result.config.ingress // []) | map({hostname:(.hostname // null),service:(.service // null),access:((.originRequest.access // {}) | {required:(.required // null),teamName:(.teamName // null),audTag:(.audTag // [])})})}' <<<"$config"); then
		printf 'existing Cloudflare tunnel configuration was not valid JSON\n' >&2
		return 1
	fi
	TUNNEL_BEFORE_STATE=$summary
	TUNNEL_BEFORE_CONFIG_DIGEST=$digest
	TUNNEL_BEFORE_CONFIG_KEYS=$config_keys
}

tunnel_config_digest() {
	local config=$1 canonical_config
	canonical_config=$(jq -cS '.result.config // error("missing tunnel config") | select(type == "object")' <<<"$config") || return 1
	printf '%s\n' "$canonical_config" | sha256sum | awk '{print $1}'
}

assert_tunnel_config_unchanged() {
	local tunnel_id=$1 latest latest_digest
	[[ -n "${TUNNEL_BEFORE_CONFIG_DIGEST:-}" ]] || {
		printf 'Cloudflare tunnel before-state digest is missing\n' >&2
		return 1
	}
	if ! latest=$(cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations"); then
		printf 'could not re-read the Cloudflare tunnel configuration before overwrite\n' >&2
		return 1
	fi
	if ! latest_digest=$(tunnel_config_digest "$latest"); then
		printf 'latest Cloudflare tunnel configuration was not a complete object\n' >&2
		return 1
	fi
	if [[ "$latest_digest" != "$TUNNEL_BEFORE_CONFIG_DIGEST" ]]; then
		printf 'Cloudflare tunnel configuration changed during prepare; refusing to overwrite provider state\n' >&2
		return 1
	fi
	TUNNEL_LATEST_CONFIG=$latest
	TUNNEL_LATEST_CONFIG_DIGEST=$latest_digest
}

capture_tunnel_after_state() {
	local tunnel_id=$1 config digest
	if ! config=$(cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations"); then
		printf 'could not read back the Cloudflare tunnel configuration after reconciliation\n' >&2
		return 1
	fi
	if ! digest=$(tunnel_config_digest "$config"); then
		printf 'Cloudflare tunnel readback was not a complete configuration object\n' >&2
		return 1
	fi
	TUNNEL_AFTER_CONFIG_DIGEST=$digest
}

validate_tunnel_shape() {
	local team=$1 tunnel_id=$2 legacy_host=$3 canonical_host=${4:-} config
	if ! config=$(cf_request GET "/accounts/$ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations"); then
		return 1
	fi
	if [[ -z "$canonical_host" ]]; then
		jq -e --arg host "$legacy_host" --arg team "$team" '
			(.result.config // null) as $config |
			($config.ingress // []) as $ingress |
			($config | type == "object" and (keys | sort) == ["ingress"]) and
			($ingress | type == "array" and length == 2) and
			(($ingress[0] | keys | sort) == ["hostname", "originRequest", "service"]) and
			(($ingress[1] | keys | sort) == ["service"]) and
			($ingress[0].hostname == $host) and ($ingress[0].service == "http://127.0.0.1:8080") and
			($ingress[1].service == "http_status:404") and
			(($ingress[0].originRequest | keys | sort) == ["access"]) and
			(($ingress[0].originRequest.access | keys | sort) == ["audTag", "required", "teamName"]) and
			($ingress[0].originRequest.access.required == true) and ($ingress[0].originRequest.access.teamName == $team) and
			(($ingress[0].originRequest.access.audTag | type == "array" and length == 2) and
			 all($ingress[0].originRequest.access.audTag[]; type == "string" and length > 0))
		' <<<"$config" >/dev/null
		return
	fi
	jq -e --arg legacy_host "$legacy_host" --arg canonical_host "$canonical_host" --arg team "$team" '
		(.result.config // null) as $config |
		($config.ingress // []) as $ingress |
		($config | type == "object" and (keys | sort) == ["ingress"]) and
		($ingress | type == "array" and length == 3) and
		(($ingress[0] | keys | sort) == ["hostname", "originRequest", "service"]) and
		(($ingress[1] | keys | sort) == ["hostname", "originRequest", "service"]) and
		(($ingress[2] | keys | sort) == ["service"]) and
		($ingress[0].hostname == $legacy_host) and ($ingress[1].hostname == $canonical_host) and
		($ingress[0].service == "http://127.0.0.1:8080") and ($ingress[1].service == "http://127.0.0.1:8080") and
		($ingress[2].service == "http_status:404") and
		(($ingress[0].originRequest | keys | sort) == ["access"]) and (($ingress[1].originRequest | keys | sort) == ["access"]) and
		(($ingress[0].originRequest.access | keys | sort) == ["audTag", "required", "teamName"]) and
		(($ingress[1].originRequest.access | keys | sort) == ["audTag", "required", "teamName"]) and
		($ingress[0].originRequest.access.required == true) and ($ingress[1].originRequest.access.required == true) and
		($ingress[0].originRequest.access.teamName == $team) and ($ingress[1].originRequest.access.teamName == $team) and
		(($ingress[0].originRequest.access.audTag | type == "array" and length == 2) and
		 all($ingress[0].originRequest.access.audTag[]; type == "string" and length > 0)) and
		(($ingress[1].originRequest.access.audTag | type == "array" and length == 2) and
		 all($ingress[1].originRequest.access.audTag[]; type == "string" and length > 0))
	' <<<"$config" >/dev/null
}

reconcile_tunnel_config() {
	local team=$1 ui_aud=$2 api_aud=$3 tunnel_id=$4 created=${5:-0}
	local legacy_host=${6:-} legacy_ui_aud=${7:-} legacy_api_aud=${8:-}
	local canonical_host=${9:-} canonical_ui_aud=${10:-} canonical_api_aud=${11:-}
	local phase=${DOMAIN_PROFILE_PHASE:-legacy}
	TUNNEL_BEFORE_STATE=
	if [[ "$created" = 1 ]]; then
		TUNNEL_BEFORE_STATE=$(jq -cn --arg phase "$phase" --arg tunnel_id "$tunnel_id" \
			'{phase:$phase,tunnel_id:$tunnel_id,ingress:[],state:"new-tunnel"}') || {
			printf 'could not capture new Cloudflare tunnel before-state\n' >&2
			return 1
		}
		configure_tunnel "$team" "$ui_aud" "$api_aud" "$tunnel_id" \
			"$legacy_host" "$legacy_ui_aud" "$legacy_api_aud" "$canonical_host" "$canonical_ui_aud" "$canonical_api_aud" || return
		capture_tunnel_after_state "$tunnel_id"
		return
	fi

	if ! capture_tunnel_before_state "$tunnel_id" "$phase"; then
		return 1
	fi
	if ! assert_tunnel_config_unchanged "$tunnel_id"; then
		return 1
	fi
	if [[ -n "$legacy_host" ]]; then
		if validate_tunnel_config "$team" "$ui_aud" "$api_aud" "$tunnel_id" \
			"$legacy_host" "$legacy_ui_aud" "$legacy_api_aud" "$canonical_host" "$canonical_ui_aud" "$canonical_api_aud" \
			"$PUBLIC_HOST" "$TUNNEL_LATEST_CONFIG" >/dev/null 2>&1; then
			capture_tunnel_after_state "$tunnel_id" || return
			return 0
		fi
		# A dual-host transition is allowed to start only from the exact
		# reviewed single-host topology. Any other drift is fail-closed.
		if ! validate_tunnel_config "$team" "$legacy_ui_aud" "$legacy_api_aud" "$tunnel_id" \
			'' '' '' '' '' '' "$legacy_host" "$TUNNEL_LATEST_CONFIG" >/dev/null 2>&1; then
			printf 'existing Cloudflare tunnel topology is unexpected; refusing to overwrite provider state\n' >&2
			return 1
		fi
	else
		if ! validate_tunnel_config "$team" "$ui_aud" "$api_aud" "$tunnel_id" \
			'' '' '' '' '' '' "$PUBLIC_HOST" "$TUNNEL_LATEST_CONFIG" >/dev/null 2>&1; then
			printf 'existing Cloudflare tunnel topology is unexpected; refusing to overwrite provider state\n' >&2
			return 1
		fi
		capture_tunnel_after_state "$tunnel_id" || return
		return 0
	fi

	configure_tunnel "$team" "$ui_aud" "$api_aud" "$tunnel_id" \
		"$legacy_host" "$legacy_ui_aud" "$legacy_api_aud" "$canonical_host" "$canonical_ui_aud" "$canonical_api_aud" || return
	capture_tunnel_after_state "$tunnel_id"
}

validate_dns_record() {
	local hostname=$1 tunnel_id=$2 records
	if ! records=$(cf_request GET "/zones/$ZONE_ID/dns_records?name=$hostname"); then
		return 1
	fi
	if ! jq -e --arg host "$hostname" --arg target "$tunnel_id.cfargotunnel.com" '
		(.result | type == "array" and length == 1) and
		(.result[0].name == $host) and
		(.result[0].type == "CNAME") and
		(.result[0].content == $target) and
		(.result[0].proxied == true)
	' <<<"$records" >/dev/null; then
		printf 'Cloudflare DNS record is not exactly the reviewed proxied tunnel CNAME: %s\n' "$hostname" >&2
		return 1
	fi
}

upsert_dns() {
	local hostname=$1 tunnel_id=$2 records record_id record_type body count
	if ! records=$(cf_request GET "/zones/$ZONE_ID/dns_records?name=$hostname"); then
		return 1
	fi
	if ! count=$(jq -r '.result | length' <<<"$records"); then
		printf 'Cloudflare DNS response was invalid\n' >&2
		return 1
	fi
	[[ "$count" -le 1 ]] || { printf 'DNS name has multiple records: %s\n' "$hostname" >&2; return 1; }
	if ! record_id=$(jq -r '.result[0].id // empty' <<<"$records") || \
		! record_type=$(jq -r '.result[0].type // empty' <<<"$records"); then
		printf 'Cloudflare DNS response was invalid\n' >&2
		return 1
	fi
	if [[ -n "$record_type" && "$record_type" != CNAME ]]; then
		printf 'DNS name %s already exists with type %s\n' "$hostname" "$record_type" >&2
		return 1
	fi
	if ! body=$(jq -cn --arg hostname "$hostname" --arg content "$tunnel_id.cfargotunnel.com" \
		'{type:"CNAME",name:$hostname,content:$content,proxied:true,ttl:1}'); then
		printf 'could not construct Cloudflare DNS request\n' >&2
		return 1
	fi
	if [[ -n "$record_id" ]]; then
		if ! cf_request PUT "/zones/$ZONE_ID/dns_records/$record_id" "$body" >/dev/null; then
			return 1
		fi
	else
		if ! cf_request POST "/zones/$ZONE_ID/dns_records" "$body" >/dev/null; then
			return 1
		fi
	fi
	validate_dns_record "$hostname" "$tunnel_id"
}

prepare() {
	local email idp team team_domain tunnel_id body created=0
	if ! email=$(owner_email); then
		return 1
	fi
	if ! idp=$(identity_provider_id); then
		return 1
	fi
	if ! team_domain=$(access_team_domain); then
		return 1
	fi
	team=${team_domain%%.*}
	if ! tunnel_id=$(find_tunnel_id); then
		return 1
	fi
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 && -z "$tunnel_id" ]]; then
		printf 'hostname transition requires the retained tunnel; refusing to create a replacement\n' >&2
		return 1
	fi
	# Read and validate the existing topology before any service-token, Access
	# application, or tunnel mutation. A dual-host transition may begin only
	# from the exact reviewed single-host topology (or already be idempotent).
	if [[ -n "$tunnel_id" ]]; then
		if ! capture_tunnel_before_state "$tunnel_id" "${DOMAIN_PROFILE_PHASE:-legacy}"; then
			return 1
		fi
		if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
			if ! validate_tunnel_shape "$team" "$tunnel_id" "$DOMAIN_PROFILE_LEGACY_HOST" "$DOMAIN_PROFILE_CANONICAL_HOST" >/dev/null 2>&1 && \
				! validate_tunnel_shape "$team" "$tunnel_id" "$DOMAIN_PROFILE_LEGACY_HOST" >/dev/null 2>&1; then
				printf 'existing Cloudflare tunnel topology is unexpected; refusing to prepare transition\n' >&2
				return 1
			fi
		else
			if ! validate_tunnel_shape "$team" "$tunnel_id" "$PUBLIC_HOST" >/dev/null 2>&1; then
				printf 'existing Cloudflare tunnel topology is unexpected; refusing to prepare provider state\n' >&2
				return 1
			fi
		fi
	fi
	if ! ensure_service_token; then
		return 1
	fi

	local legacy_ui_id legacy_api_id legacy_ui_aud legacy_api_aud
	local canonical_ui_id canonical_api_id canonical_ui_aud canonical_api_aud
	local legacy_owner_name=$DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME
	local legacy_service_name=$DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME
	local canonical_owner_name=$DOMAIN_PROFILE_CANONICAL_OWNER_POLICY_NAME
	local canonical_service_name=$DOMAIN_PROFILE_CANONICAL_SERVICE_POLICY_NAME
	# The API path is deliberately a distinct Access application. Cloudflare
	# path applications do not inherit the parent UI application's policy.
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		if ! ensure_app "$DOMAIN_PROFILE_LEGACY_UI_APP_NAME" "$DOMAIN_PROFILE_LEGACY_HOST" "$idp" false; then
			return 1
		fi
		legacy_ui_id=$APP_ID
		legacy_ui_aud=$APP_AUD
		if ! ensure_app "$DOMAIN_PROFILE_LEGACY_API_APP_NAME" "$DOMAIN_PROFILE_LEGACY_API_PATH" "$idp" true; then
			return 1
		fi
		legacy_api_id=$APP_ID
		legacy_api_aud=$APP_AUD
		if ! ensure_app "$DOMAIN_PROFILE_CANONICAL_UI_APP_NAME" "$DOMAIN_PROFILE_CANONICAL_HOST" "$idp" false; then
			return 1
		fi
		canonical_ui_id=$APP_ID
		canonical_ui_aud=$APP_AUD
		if ! ensure_app "$DOMAIN_PROFILE_CANONICAL_API_APP_NAME" "$DOMAIN_PROFILE_CANONICAL_API_PATH" "$idp" true; then
			return 1
		fi
		canonical_api_id=$APP_ID
		canonical_api_aud=$APP_AUD
		if ! upsert_owner_policy "$legacy_ui_id" "$email" 1 "$legacy_owner_name" "$DOMAIN_PROFILE_MIGRATION_OWNER_POLICY_NAME" || \
			! upsert_owner_policy "$legacy_api_id" "$email" 2 "$legacy_owner_name" "$DOMAIN_PROFILE_MIGRATION_OWNER_POLICY_NAME" || \
			! upsert_service_policy "$legacy_api_id" "$SERVICE_TOKEN_ID" "$legacy_service_name" "$DOMAIN_PROFILE_MIGRATION_SERVICE_POLICY_NAME" || \
			! validate_policy_set "$legacy_ui_id" 1 "$email" '' "$legacy_owner_name" || \
			! validate_policy_set "$legacy_api_id" 2 "$email" "$SERVICE_TOKEN_ID" "$legacy_owner_name" "$legacy_service_name" || \
			! upsert_owner_policy "$canonical_ui_id" "$email" 1 "$canonical_owner_name" '' || \
			! upsert_owner_policy "$canonical_api_id" "$email" 2 "$canonical_owner_name" '' || \
			! upsert_service_policy "$canonical_api_id" "$SERVICE_TOKEN_ID" "$canonical_service_name" '' || \
			! validate_policy_set "$canonical_ui_id" 1 "$email" '' "$canonical_owner_name" || \
			! validate_policy_set "$canonical_api_id" 2 "$email" "$SERVICE_TOKEN_ID" "$canonical_owner_name" "$canonical_service_name"; then
			return 1
		fi
	else
		if ! ensure_app "$UI_APP_NAME" "$PUBLIC_HOST" "$idp" false; then
			return 1
		fi
		legacy_ui_id=$APP_ID
		legacy_ui_aud=$APP_AUD
		if ! ensure_app "$API_APP_NAME" "$API_PATH" "$idp" true; then
			return 1
		fi
		legacy_api_id=$APP_ID
		legacy_api_aud=$APP_AUD
		if ! upsert_owner_policy "$legacy_ui_id" "$email" 1 "$OWNER_POLICY_NAME" "$LEGACY_OWNER_POLICY_NAME" || \
			! upsert_owner_policy "$legacy_api_id" "$email" 2 "$OWNER_POLICY_NAME" "$LEGACY_OWNER_POLICY_NAME" || \
			! upsert_service_policy "$legacy_api_id" "$SERVICE_TOKEN_ID" "$SERVICE_POLICY_NAME" "$LEGACY_SERVICE_POLICY_NAME" || \
			! validate_policy_set "$legacy_ui_id" 1 "$email" '' "$OWNER_POLICY_NAME" || \
			! validate_policy_set "$legacy_api_id" 2 "$email" "$SERVICE_TOKEN_ID" "$OWNER_POLICY_NAME" "$SERVICE_POLICY_NAME"; then
			return 1
		fi
	fi

	if [[ -z "$tunnel_id" ]]; then
		if ! body=$(jq -cn --arg name "$TUNNEL_NAME" '{name:$name,config_src:"cloudflare"}'); then
			printf 'could not construct Cloudflare tunnel request\n' >&2
			return 1
		fi
		if ! tunnel_id=$(cf_request POST "/accounts/$ACCOUNT_ID/cfd_tunnel" "$body" | jq -r '.result.id // empty'); then
			printf 'Cloudflare tunnel creation failed\n' >&2
			return 1
		fi
		created=1
	fi
	[[ -n "$tunnel_id" ]] || { printf 'Cloudflare tunnel ID is missing\n' >&2; return 1; }
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		if ! reconcile_tunnel_config "$team" "$legacy_ui_aud" "$legacy_api_aud" "$tunnel_id" "$created" \
			"$DOMAIN_PROFILE_LEGACY_HOST" "$legacy_ui_aud" "$legacy_api_aud" \
			"$DOMAIN_PROFILE_CANONICAL_HOST" "$canonical_ui_aud" "$canonical_api_aud"; then
			return 1
		fi
	else
		if ! reconcile_tunnel_config "$team" "$legacy_ui_aud" "$legacy_api_aud" "$tunnel_id" "$created"; then
			return 1
		fi
	fi
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		local host_audience_map="$DOMAIN_PROFILE_LEGACY_HOST=$legacy_ui_aud,$legacy_api_aud;$DOMAIN_PROFILE_CANONICAL_HOST=$canonical_ui_aud,$canonical_api_aud"
		if ! write_prepare_outputs "$tunnel_id" "$email" "https://$team_domain" "$legacy_ui_aud" "$legacy_api_aud" "$PUBLIC_URL" \
			"$legacy_ui_aud" "$legacy_api_aud" "$canonical_ui_aud" "$canonical_api_aud" "$host_audience_map" "${DOMAIN_PROFILE_LEGACY_ORIGIN:-}"; then
			return 1
		fi
	else
		if ! write_prepare_outputs "$tunnel_id" "$email" "https://$team_domain" "$legacy_ui_aud" "$legacy_api_aud" "$PUBLIC_URL"; then
			return 1
		fi
	fi
	local before_state=${TUNNEL_BEFORE_STATE:-}
	[[ -n "$before_state" ]] || before_state='{}'
	printf 'cloudflare_before_state=%s\n' "$before_state"
	printf 'cloudflare_after_config_sha256=%s\n' "${TUNNEL_AFTER_CONFIG_DIGEST:-unknown}"
	printf 'cloudflare_tunnel_guard=read-compare-before-write\n'
	printf 'cloudflare_prepare_phase=%s\n' "${DOMAIN_PROFILE_PHASE:-legacy}"
	printf 'cloudflare_prepare=ok\n'
	CREATED_SERVICE_TOKEN_ID=
	CREATED_SERVICE_TOKEN_OUTPUT=
	return 0
}

publish() {
	local tunnel_id
	# TC-149 must add live pre-publish Access/TLS verification and phase-aware
	# provider rollback before the new hostname is made discoverable. The
	# preparatory release must not bypass the matching CI gate via manual CLI.
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		printf 'transition DNS publication is gated on TC-149 preflight and rollback rehearsal\n' >&2
		return 1
	fi
	if ! tunnel_id=$(find_tunnel_id); then
		return 1
	fi
	[[ -n "$tunnel_id" ]] || { printf 'Roadmap tunnel does not exist\n' >&2; return 1; }
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		if ! upsert_dns "$DOMAIN_PROFILE_LEGACY_HOST" "$tunnel_id" || \
			! upsert_dns "$DOMAIN_PROFILE_CANONICAL_HOST" "$tunnel_id"; then
			return 1
		fi
	else
		if ! upsert_dns "$PUBLIC_HOST" "$tunnel_id"; then
			return 1
		fi
	fi
	printf 'cloudflare_publish=ok\n'
}

case "$MODE" in
	prepare) prepare ;;
	publish) publish ;;
	*)
		printf 'usage: %s prepare <tunnel-token-output> <owner-env-output> [service-token-output] | publish\n' "$0" >&2
		exit 64
		;;
esac
