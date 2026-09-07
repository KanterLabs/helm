#!/usr/bin/env bash
# Fixed, reviewed public-domain profiles for deployment scripts.
#
# This file is both sourceable and executable.  When sourced it exposes
# domain_profile_load, which sets the historical script variables (and the
# DOMAIN_PROFILE_* namespaced copies) from the fixed table below.  When run as
# a command it only prints an allowlisted field or validates a generated owner
# environment's origin aliases as data; it never reads or sources a profile
# path supplied by the caller.

DOMAIN_PROFILE_SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
DOMAIN_PROFILE_ROOT_DIR=$(cd -- "$DOMAIN_PROFILE_SCRIPT_DIR/.." && pwd -P)

domain_profile_error() {
	printf 'domain profile: %s\n' "$*" >&2
}

domain_profile_check_aliases() {
	local left_name=$1 right_name=$2 expected=$3 left_value right_value selected
	left_value=${!left_name:-}
	right_value=${!right_name:-}
	if [[ -n "$left_value" && -n "$right_value" && "$left_value" != "$right_value" ]]; then
		domain_profile_error "$left_name and $right_name must match when both are set"
		return 1
	fi
	selected=${left_value:-$right_value}
	if [[ -n "$selected" && "$selected" != "$expected" ]]; then
		domain_profile_error "$left_name must be exactly $expected"
		return 1
	fi
	printf '%s' "$selected"
}

domain_profile_check_single_override() {
	local name=$1 expected=$2 value=${!1:-} owned_name=${3:-} owned_value=
	[[ -n "$owned_name" ]] && owned_value=${!owned_name:-}
	# A prior domain_profile_load call owns the historical compatibility
	# variable.  Permit that exact previous value while switching profiles, but
	# reject any caller mutation of it as an unsafe host override.
	if [[ -n "$value" && -n "$owned_value" && "$value" = "$owned_value" ]]; then
		printf '%s' "$value"
		return 0
	fi
	if [[ -n "$value" && "$value" != "$expected" ]]; then
		domain_profile_error "$name must be exactly $expected"
		return 1
	fi
	printf '%s' "$value"
}

domain_profile_validate_overrides() {
	local expected_origin=$1 expected_host=$2 expected_url=$3
	local configured_origin configured_host configured_url
	if ! configured_origin=$(domain_profile_check_aliases HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN "$expected_origin"); then
		return 1
	fi
	if ! configured_host=$(domain_profile_check_aliases HELM_PUBLIC_HOST ROADMAP_PUBLIC_HOST "$expected_host"); then
		return 1
	fi
	if ! configured_url=$(domain_profile_check_aliases HELM_PUBLIC_URL ROADMAP_PUBLIC_URL "$expected_url"); then
		return 1
	fi
	# Bare names are not deployment configuration knobs, but rejecting an
	# accidental override closes the same class of host-confusion bug when a
	# caller inherited generic shell variables from another deployment tool.
	if ! domain_profile_check_single_override PUBLIC_HOST "$expected_host" DOMAIN_PROFILE_OWNED_PUBLIC_HOST >/dev/null; then
		return 1
	fi
	if ! domain_profile_check_single_override PUBLIC_URL "$expected_url" DOMAIN_PROFILE_OWNED_PUBLIC_URL >/dev/null; then
		return 1
	fi
	DOMAIN_PROFILE_CONFIGURED_PUBLIC_ORIGIN=$configured_origin
	DOMAIN_PROFILE_CONFIGURED_PUBLIC_HOST=$configured_host
	DOMAIN_PROFILE_CONFIGURED_PUBLIC_URL=$configured_url
	CONFIGURED_PUBLIC_ORIGIN=$configured_origin
}

domain_profile_load() {
	local environment
	if [[ $# -gt 1 ]]; then
		domain_profile_error 'usage: domain_profile_load [production|beta]'
		return 64
	fi
	if [[ $# = 1 ]]; then
		environment=$1
	else
		environment=${HELM_DEPLOY_ENVIRONMENT:-production}
	fi
	case "$environment" in
	production)
		DOMAIN_PROFILE_ACCOUNT_ID=090ae73dce25f4eca9a53ee396fdc916
		DOMAIN_PROFILE_ZONE_ID=1206ce4daa0fe3c4791f9df9069764f6
		DOMAIN_PROFILE_PUBLIC_HOST=tc.shanekanterman.dev
		DOMAIN_PROFILE_PUBLIC_URL=https://tc.shanekanterman.dev
		DOMAIN_PROFILE_API_PATH="$DOMAIN_PROFILE_PUBLIC_HOST/api/v1/*"
		DOMAIN_PROFILE_TUNNEL_NAME=roadmap-homelab
		DOMAIN_PROFILE_UI_APP_NAME='Helm owner UI'
		DOMAIN_PROFILE_API_APP_NAME='Helm agents API'
		DOMAIN_PROFILE_OWNER_POLICY_NAME='Helm owner only'
		DOMAIN_PROFILE_SERVICE_TOKEN_NAME='Helm agents'
		DOMAIN_PROFILE_SERVICE_POLICY_NAME='Helm agents Service Auth'
		DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME='Roadmap owner only'
		DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME='Roadmap agents'
		DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME='Roadmap agents Service Auth'
		;;
	beta)
		DOMAIN_PROFILE_ACCOUNT_ID=090ae73dce25f4eca9a53ee396fdc916
		DOMAIN_PROFILE_ZONE_ID=1206ce4daa0fe3c4791f9df9069764f6
		DOMAIN_PROFILE_PUBLIC_HOST=beta.shanekanterman.dev
		DOMAIN_PROFILE_PUBLIC_URL=https://beta.shanekanterman.dev
		DOMAIN_PROFILE_API_PATH="$DOMAIN_PROFILE_PUBLIC_HOST/api/v1/*"
		DOMAIN_PROFILE_TUNNEL_NAME=helm-beta-homelab
		DOMAIN_PROFILE_UI_APP_NAME='Helm beta owner UI'
		DOMAIN_PROFILE_API_APP_NAME='Helm beta agents API'
		DOMAIN_PROFILE_OWNER_POLICY_NAME='Helm beta owner only'
		DOMAIN_PROFILE_SERVICE_TOKEN_NAME='Helm beta agents'
		DOMAIN_PROFILE_SERVICE_POLICY_NAME='Helm beta agents Service Auth'
		# Beta is a separate trust boundary.  It must never discover, rename, or
		# reuse a production-era Roadmap/Helm object by legacy name.
		DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME=
		DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME=
		DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME=
		;;
	*)
		domain_profile_error 'HELM_DEPLOY_ENVIRONMENT must be exactly production or beta'
		return 1
		;;
	esac

	if ! domain_profile_validate_overrides \
		"$DOMAIN_PROFILE_PUBLIC_URL" "$DOMAIN_PROFILE_PUBLIC_HOST" "$DOMAIN_PROFILE_PUBLIC_URL"; then
		return 1
	fi

	# Keep the existing variable names as a compatibility surface for the
	# deployment scripts.  Every value comes from the fixed profile above.
	DOMAIN_PROFILE_ENVIRONMENT=$environment
	DOMAIN_PROFILE_OWNED_PUBLIC_HOST=$DOMAIN_PROFILE_PUBLIC_HOST
	DOMAIN_PROFILE_OWNED_PUBLIC_URL=$DOMAIN_PROFILE_PUBLIC_URL
	ACCOUNT_ID=$DOMAIN_PROFILE_ACCOUNT_ID
	ZONE_ID=$DOMAIN_PROFILE_ZONE_ID
	PUBLIC_HOST=$DOMAIN_PROFILE_PUBLIC_HOST
	PUBLIC_URL=$DOMAIN_PROFILE_PUBLIC_URL
	API_PATH=$DOMAIN_PROFILE_API_PATH
	TUNNEL_NAME=$DOMAIN_PROFILE_TUNNEL_NAME
	UI_APP_NAME=$DOMAIN_PROFILE_UI_APP_NAME
	API_APP_NAME=$DOMAIN_PROFILE_API_APP_NAME
	OWNER_POLICY_NAME=$DOMAIN_PROFILE_OWNER_POLICY_NAME
	SERVICE_TOKEN_NAME=$DOMAIN_PROFILE_SERVICE_TOKEN_NAME
	SERVICE_POLICY_NAME=$DOMAIN_PROFILE_SERVICE_POLICY_NAME
	LEGACY_OWNER_POLICY_NAME=$DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME
	LEGACY_SERVICE_TOKEN_NAME=$DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME
	LEGACY_SERVICE_POLICY_NAME=$DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME
}

# Descriptive alias for callers that prefer a verb-first name.  Keep one
# implementation so sourced scripts and the CLI share exactly the same table.
load_domain_profile() {
	domain_profile_load "$@"
}

domain_profile_field() {
	local field=$1
	case "$field" in
		environment) printf '%s\n' "$DOMAIN_PROFILE_ENVIRONMENT" ;;
		account-id) printf '%s\n' "$DOMAIN_PROFILE_ACCOUNT_ID" ;;
		zone-id) printf '%s\n' "$DOMAIN_PROFILE_ZONE_ID" ;;
		public-host) printf '%s\n' "$DOMAIN_PROFILE_PUBLIC_HOST" ;;
		public-url) printf '%s\n' "$DOMAIN_PROFILE_PUBLIC_URL" ;;
		api-path) printf '%s\n' "$DOMAIN_PROFILE_API_PATH" ;;
		tunnel-name) printf '%s\n' "$DOMAIN_PROFILE_TUNNEL_NAME" ;;
		ui-app-name) printf '%s\n' "$DOMAIN_PROFILE_UI_APP_NAME" ;;
		api-app-name) printf '%s\n' "$DOMAIN_PROFILE_API_APP_NAME" ;;
		owner-policy-name) printf '%s\n' "$DOMAIN_PROFILE_OWNER_POLICY_NAME" ;;
		service-token-name) printf '%s\n' "$DOMAIN_PROFILE_SERVICE_TOKEN_NAME" ;;
		service-policy-name) printf '%s\n' "$DOMAIN_PROFILE_SERVICE_POLICY_NAME" ;;
		legacy-owner-policy-name) printf '%s\n' "$DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME" ;;
		legacy-service-token-name) printf '%s\n' "$DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME" ;;
		legacy-service-policy-name) printf '%s\n' "$DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME" ;;
		*)
			domain_profile_error "unknown fixed field: $field"
			return 64
			;;
	esac
}

domain_profile_validate_origin_file() {
	local path=$1 line key value helm_count=0 roadmap_count=0
	local helm_origin= roadmap_origin=
	[[ -n "$path" && -f "$path" && ! -L "$path" ]] || {
		domain_profile_error "origin file must be a regular non-symlink file: $path"
		return 1
	}
	while IFS= read -r line || [[ -n "$line" ]]; do
		case "$line" in
			''|'#'*) continue ;;
		esac
		# The generated owner environment uses simple KEY=value records.  Do
		# not accept shell/systemd quoting, whitespace trimming, or continuation
		# syntax: parsing this file must never acquire execution semantics.
		if ! [[ "$line" =~ ^[A-Za-z_][A-Za-z0-9_]*= ]] ||
			[[ "$line" = *[[:space:]]* || "$line" = *\\* || "$line" = *\"* || "$line" = *\'* ]]; then
			domain_profile_error 'origin file contains a malformed environment assignment'
			return 1
		fi
		key=${line%%=*}
		value=${line#*=}
		case "$key" in
			HELM_PUBLIC_ORIGIN)
				((++helm_count))
				[[ "$helm_count" = 1 ]] || {
					domain_profile_error 'origin file contains duplicate HELM_PUBLIC_ORIGIN'
					return 1
				}
				[[ -n "$value" && "$value" != *$'\r'* ]] || {
					domain_profile_error 'origin file HELM_PUBLIC_ORIGIN is empty or contains a carriage return'
					return 1
				}
				helm_origin=$value
				;;
			ROADMAP_PUBLIC_ORIGIN)
				((++roadmap_count))
				[[ "$roadmap_count" = 1 ]] || {
					domain_profile_error 'origin file contains duplicate ROADMAP_PUBLIC_ORIGIN'
					return 1
				}
				[[ -n "$value" && "$value" != *$'\r'* ]] || {
					domain_profile_error 'origin file ROADMAP_PUBLIC_ORIGIN is empty or contains a carriage return'
					return 1
				}
				roadmap_origin=$value
				;;
		esac
	done < "$path"

	[[ -n "$helm_origin" || -n "$roadmap_origin" ]] || {
		domain_profile_error 'origin file must contain HELM_PUBLIC_ORIGIN or ROADMAP_PUBLIC_ORIGIN'
		return 1
	}
	if [[ -n "$helm_origin" && -n "$roadmap_origin" && "$helm_origin" != "$roadmap_origin" ]]; then
		domain_profile_error 'origin file HELM_PUBLIC_ORIGIN and ROADMAP_PUBLIC_ORIGIN must match'
		return 1
	fi
	local selected_origin=${helm_origin:-$roadmap_origin}
	[[ "$selected_origin" = "$DOMAIN_PROFILE_PUBLIC_URL" ]] || {
		domain_profile_error "origin file public origin must be exactly $DOMAIN_PROFILE_PUBLIC_URL"
		return 1
	}
}

domain_profile_usage() {
	printf 'usage: %s production|beta FIELD\n' "$0" >&2
	printf '       %s production|beta validate-origin-file PATH\n' "$0" >&2
}

domain_profile_main() {
	local environment=${1:-} field=${2:-}
	if [[ $# -lt 2 || $# -gt 3 || -z "$environment" || -z "$field" ]]; then
		domain_profile_usage
		return 64
	fi
	if [[ -n "${HELM_DEPLOY_ENVIRONMENT:-}" && "$HELM_DEPLOY_ENVIRONMENT" != "$environment" ]]; then
		domain_profile_error 'explicit environment does not match HELM_DEPLOY_ENVIRONMENT'
		return 1
	fi
	domain_profile_load "$environment" || return
	if [[ "$field" = validate-origin-file ]]; then
		[[ $# = 3 ]] || {
			domain_profile_error 'validate-origin-file requires a file path'
			return 64
		}
		domain_profile_validate_origin_file "$3"
		return
	fi
	[[ $# = 2 ]] || {
		domain_profile_error 'fixed fields do not accept extra arguments'
		return 64
	}
	domain_profile_field "$field"
}

if [[ "${BASH_SOURCE[0]}" = "$0" ]]; then
	set -Eeuo pipefail
	domain_profile_main "$@"
fi
