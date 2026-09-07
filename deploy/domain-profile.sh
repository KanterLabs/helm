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
	local left_name=$1 right_name=$2 expected=$3 owned=${4:-} left_value right_value selected
	left_value=${!left_name:-}
	right_value=${!right_name:-}
	if [[ -n "$left_value" && -n "$right_value" && "$left_value" != "$right_value" ]]; then
		domain_profile_error "$left_name and $right_name must match when both are set"
		return 1
	fi
	selected=${left_value:-$right_value}
	if [[ -n "$owned" && "$selected" = "$owned" && "$selected" != "$expected" ]]; then
		# A prior domain_profile_load call owns this compatibility value; it is
		# not an external override when switching the fixed phase in a sourced
		# test or helper process.
		selected=
	fi
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
	local expected_origin=$1 expected_host=$2 expected_url=$3 expected_legacy_origin=${4:-}
	local configured_origin configured_host configured_url configured_legacy_origin
	if ! configured_origin=$(domain_profile_check_aliases HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN "$expected_origin"); then
		return 1
	fi
	if ! configured_host=$(domain_profile_check_aliases HELM_PUBLIC_HOST ROADMAP_PUBLIC_HOST "$expected_host"); then
		return 1
	fi
	if ! configured_url=$(domain_profile_check_aliases HELM_PUBLIC_URL ROADMAP_PUBLIC_URL "$expected_url"); then
		return 1
	fi
	if ! configured_legacy_origin=$(domain_profile_check_aliases HELM_LEGACY_ORIGIN ROADMAP_LEGACY_ORIGIN "$expected_legacy_origin" "${DOMAIN_PROFILE_OWNED_LEGACY_ORIGIN:-}"); then
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
	DOMAIN_PROFILE_CONFIGURED_LEGACY_ORIGIN=$configured_legacy_origin
	CONFIGURED_PUBLIC_ORIGIN=$configured_origin
}

domain_profile_set_common() {
	DOMAIN_PROFILE_ACCOUNT_ID=090ae73dce25f4eca9a53ee396fdc916
	DOMAIN_PROFILE_ZONE_ID=1206ce4daa0fe3c4791f9df9069764f6
	DOMAIN_PROFILE_LEGACY_HOST=tc.shanekanterman.dev
	DOMAIN_PROFILE_LEGACY_URL=https://tc.shanekanterman.dev
	DOMAIN_PROFILE_CANONICAL_HOST=helm.shanekanterman.dev
	DOMAIN_PROFILE_CANONICAL_URL=https://helm.shanekanterman.dev
	DOMAIN_PROFILE_TUNNEL_NAME=roadmap-homelab
	DOMAIN_PROFILE_SERVICE_TOKEN_NAME='Helm agents'
	DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME='Roadmap agents'
	DOMAIN_PROFILE_LEGACY_UI_APP_NAME='Helm owner UI'
	DOMAIN_PROFILE_LEGACY_API_APP_NAME='Helm agents API'
	DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME='Helm owner only'
	DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME='Helm agents Service Auth'
	DOMAIN_PROFILE_CANONICAL_UI_APP_NAME='Helm canonical owner UI'
	DOMAIN_PROFILE_CANONICAL_API_APP_NAME='Helm canonical agents API'
	DOMAIN_PROFILE_CANONICAL_OWNER_POLICY_NAME='Helm canonical owner only'
	DOMAIN_PROFILE_CANONICAL_SERVICE_POLICY_NAME='Helm canonical agents Service Auth'
	DOMAIN_PROFILE_MIGRATION_OWNER_POLICY_NAME='Roadmap owner only'
	DOMAIN_PROFILE_MIGRATION_SERVICE_POLICY_NAME='Roadmap agents Service Auth'
}

domain_profile_read_production_phase() {
	local path="$DOMAIN_PROFILE_SCRIPT_DIR/production-domain-phase" line count=0
	[[ -f "$path" && ! -L "$path" ]] || {
		domain_profile_error 'production phase file is missing or symlinked'
		return 1
	}
	[[ "$(stat -c '%s' -- "$path" 2>/dev/null)" -le 32 ]] || {
		domain_profile_error 'production phase file is too large'
		return 1
	}
	while IFS= read -r line || [[ -n "$line" ]]; do
		((++count))
		[[ "$count" = 1 ]] || {
			domain_profile_error 'production phase file must contain exactly one phase'
			return 1
		}
		[[ "$line" = legacy || "$line" = staged || "$line" = canonical ]] || {
			domain_profile_error 'production phase file must be exactly legacy, staged, or canonical'
			return 1
		}
		DOMAIN_PROFILE_PHASE=$line
	done < "$path"
	[[ "$count" = 1 ]] || {
		domain_profile_error 'production phase file must contain exactly one phase'
		return 1
	}
	[[ "$(tail -c 1 -- "$path" | od -An -t x1 | tr -d '[:space:]')" = 0a ]] || {
		domain_profile_error 'production phase file must end with a newline'
		return 1
	}
	printf '%s' "$DOMAIN_PROFILE_PHASE"
}

domain_profile_apply_phase() {
	local phase=$1 environment=${DOMAIN_PROFILE_ENVIRONMENT:-production}
	case "$environment:$phase" in
	production:legacy|production:staged|production:canonical)
		DOMAIN_PROFILE_ENVIRONMENT=production
		DOMAIN_PROFILE_PHASE=$phase
		DOMAIN_PROFILE_HOST_COUNT=1
		DOMAIN_PROFILE_HOST_1=$DOMAIN_PROFILE_LEGACY_HOST
		DOMAIN_PROFILE_HOST_2=
		DOMAIN_PROFILE_LEGACY_API_PATH="$DOMAIN_PROFILE_LEGACY_HOST/api/v1/*"
		DOMAIN_PROFILE_CANONICAL_API_PATH="$DOMAIN_PROFILE_CANONICAL_HOST/api/v1/*"
		DOMAIN_PROFILE_LEGACY_UI_APP_NAME='Helm owner UI'
		DOMAIN_PROFILE_LEGACY_API_APP_NAME='Helm agents API'
		DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME='Helm owner only'
		DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME='Helm agents Service Auth'
		DOMAIN_PROFILE_CANONICAL_UI_APP_NAME='Helm canonical owner UI'
		DOMAIN_PROFILE_CANONICAL_API_APP_NAME='Helm canonical agents API'
		DOMAIN_PROFILE_CANONICAL_OWNER_POLICY_NAME='Helm canonical owner only'
		DOMAIN_PROFILE_CANONICAL_SERVICE_POLICY_NAME='Helm canonical agents Service Auth'
		DOMAIN_PROFILE_MIGRATION_OWNER_POLICY_NAME='Roadmap owner only'
		DOMAIN_PROFILE_MIGRATION_SERVICE_POLICY_NAME='Roadmap agents Service Auth'
		case "$phase" in
		legacy)
			DOMAIN_PROFILE_PUBLIC_HOST=$DOMAIN_PROFILE_LEGACY_HOST
			DOMAIN_PROFILE_PUBLIC_URL=$DOMAIN_PROFILE_LEGACY_URL
			DOMAIN_PROFILE_HOST_COUNT=1
			DOMAIN_PROFILE_DUAL_HOST=0
			DOMAIN_PROFILE_LEGACY_ORIGIN=
			DOMAIN_PROFILE_UI_APP_NAME=$DOMAIN_PROFILE_LEGACY_UI_APP_NAME
			DOMAIN_PROFILE_API_APP_NAME=$DOMAIN_PROFILE_LEGACY_API_APP_NAME
			DOMAIN_PROFILE_OWNER_POLICY_NAME=$DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME
			DOMAIN_PROFILE_SERVICE_POLICY_NAME=$DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME
			DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME='Roadmap agents'
			DOMAIN_PROFILE_HOST_1=$DOMAIN_PROFILE_LEGACY_HOST
			;;
		staged)
			DOMAIN_PROFILE_PUBLIC_HOST=$DOMAIN_PROFILE_LEGACY_HOST
			DOMAIN_PROFILE_PUBLIC_URL=$DOMAIN_PROFILE_LEGACY_URL
			DOMAIN_PROFILE_HOST_COUNT=2
			DOMAIN_PROFILE_DUAL_HOST=1
			DOMAIN_PROFILE_LEGACY_ORIGIN=
			DOMAIN_PROFILE_UI_APP_NAME=$DOMAIN_PROFILE_LEGACY_UI_APP_NAME
			DOMAIN_PROFILE_API_APP_NAME=$DOMAIN_PROFILE_LEGACY_API_APP_NAME
			DOMAIN_PROFILE_OWNER_POLICY_NAME=$DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME
			DOMAIN_PROFILE_SERVICE_POLICY_NAME=$DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME
			DOMAIN_PROFILE_HOST_1=$DOMAIN_PROFILE_LEGACY_HOST
			DOMAIN_PROFILE_HOST_2=$DOMAIN_PROFILE_CANONICAL_HOST
			;;
		canonical)
			DOMAIN_PROFILE_PUBLIC_HOST=$DOMAIN_PROFILE_CANONICAL_HOST
			DOMAIN_PROFILE_PUBLIC_URL=$DOMAIN_PROFILE_CANONICAL_URL
			DOMAIN_PROFILE_HOST_COUNT=2
			DOMAIN_PROFILE_DUAL_HOST=1
			DOMAIN_PROFILE_LEGACY_ORIGIN=$DOMAIN_PROFILE_LEGACY_URL
			DOMAIN_PROFILE_UI_APP_NAME=$DOMAIN_PROFILE_CANONICAL_UI_APP_NAME
			DOMAIN_PROFILE_API_APP_NAME=$DOMAIN_PROFILE_CANONICAL_API_APP_NAME
			DOMAIN_PROFILE_OWNER_POLICY_NAME=$DOMAIN_PROFILE_CANONICAL_OWNER_POLICY_NAME
			DOMAIN_PROFILE_SERVICE_POLICY_NAME=$DOMAIN_PROFILE_CANONICAL_SERVICE_POLICY_NAME
			DOMAIN_PROFILE_HOST_1=$DOMAIN_PROFILE_LEGACY_HOST
			DOMAIN_PROFILE_HOST_2=$DOMAIN_PROFILE_CANONICAL_HOST
			;;
		esac
		DOMAIN_PROFILE_API_PATH="$DOMAIN_PROFILE_PUBLIC_HOST/api/v1/*"
		DOMAIN_PROFILE_PUBLIC_ORIGINS=$DOMAIN_PROFILE_PUBLIC_URL
		DOMAIN_PROFILE_AUDIENCE_COUNT=2
		if [[ "$DOMAIN_PROFILE_DUAL_HOST" = 1 ]]; then
			DOMAIN_PROFILE_AUDIENCE_COUNT=4
		fi
		;;
	beta:legacy)
		DOMAIN_PROFILE_ENVIRONMENT=beta
		DOMAIN_PROFILE_PHASE=legacy
		DOMAIN_PROFILE_PUBLIC_HOST=beta.shanekanterman.dev
		DOMAIN_PROFILE_PUBLIC_URL=https://beta.shanekanterman.dev
		DOMAIN_PROFILE_LEGACY_HOST=$DOMAIN_PROFILE_PUBLIC_HOST
		DOMAIN_PROFILE_LEGACY_URL=$DOMAIN_PROFILE_PUBLIC_URL
		DOMAIN_PROFILE_CANONICAL_HOST=
		DOMAIN_PROFILE_CANONICAL_URL=
		DOMAIN_PROFILE_API_PATH="$DOMAIN_PROFILE_PUBLIC_HOST/api/v1/*"
		DOMAIN_PROFILE_LEGACY_API_PATH=$DOMAIN_PROFILE_API_PATH
		DOMAIN_PROFILE_CANONICAL_API_PATH=
		DOMAIN_PROFILE_TUNNEL_NAME=helm-beta-homelab
		DOMAIN_PROFILE_UI_APP_NAME='Helm beta owner UI'
		DOMAIN_PROFILE_API_APP_NAME='Helm beta agents API'
		DOMAIN_PROFILE_OWNER_POLICY_NAME='Helm beta owner only'
		DOMAIN_PROFILE_SERVICE_TOKEN_NAME='Helm beta agents'
		DOMAIN_PROFILE_SERVICE_POLICY_NAME='Helm beta agents Service Auth'
		DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME=
		DOMAIN_PROFILE_LEGACY_OWNER_POLICY_NAME=
		DOMAIN_PROFILE_LEGACY_SERVICE_POLICY_NAME=
		DOMAIN_PROFILE_CANONICAL_UI_APP_NAME=
		DOMAIN_PROFILE_CANONICAL_API_APP_NAME=
		DOMAIN_PROFILE_CANONICAL_OWNER_POLICY_NAME=
		DOMAIN_PROFILE_CANONICAL_SERVICE_POLICY_NAME=
		DOMAIN_PROFILE_MIGRATION_OWNER_POLICY_NAME=
		DOMAIN_PROFILE_MIGRATION_SERVICE_POLICY_NAME=
		DOMAIN_PROFILE_HOST_COUNT=1
		DOMAIN_PROFILE_HOST_1=$DOMAIN_PROFILE_PUBLIC_HOST
		DOMAIN_PROFILE_HOST_2=
		DOMAIN_PROFILE_DUAL_HOST=0
		DOMAIN_PROFILE_LEGACY_ORIGIN=
		DOMAIN_PROFILE_AUDIENCE_COUNT=2
		DOMAIN_PROFILE_PUBLIC_ORIGINS=$DOMAIN_PROFILE_PUBLIC_URL
		;;
	*)
		domain_profile_error 'unsupported environment/phase combination'
		return 1
		;;
	esac

	# Keep the existing variable names as a compatibility surface for the
	# deployment scripts. Every value comes from this fixed phase table.
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
	LEGACY_OWNER_POLICY_NAME=$DOMAIN_PROFILE_MIGRATION_OWNER_POLICY_NAME
	LEGACY_SERVICE_TOKEN_NAME=$DOMAIN_PROFILE_LEGACY_SERVICE_TOKEN_NAME
	LEGACY_SERVICE_POLICY_NAME=$DOMAIN_PROFILE_MIGRATION_SERVICE_POLICY_NAME
	if ! domain_profile_validate_overrides \
		"$DOMAIN_PROFILE_PUBLIC_URL" "$DOMAIN_PROFILE_PUBLIC_HOST" "$DOMAIN_PROFILE_PUBLIC_URL" \
		"$DOMAIN_PROFILE_LEGACY_ORIGIN"; then
		return 1
	fi
	HELM_LEGACY_ORIGIN=$DOMAIN_PROFILE_LEGACY_ORIGIN
	ROADMAP_LEGACY_ORIGIN=$DOMAIN_PROFILE_LEGACY_ORIGIN
	DOMAIN_PROFILE_OWNED_LEGACY_ORIGIN=$DOMAIN_PROFILE_LEGACY_ORIGIN
}

domain_profile_load() {
	local environment phase
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
		domain_profile_set_common
		if ! phase=$(domain_profile_read_production_phase); then
			return 1
		fi
		DOMAIN_PROFILE_ENVIRONMENT=production
		domain_profile_apply_phase "$phase"
		;;
	beta)
		domain_profile_set_common
		DOMAIN_PROFILE_ENVIRONMENT=beta
		domain_profile_apply_phase legacy
		;;
	*)
		domain_profile_error 'HELM_DEPLOY_ENVIRONMENT must be exactly production or beta'
		return 1
		;;
	esac
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
		phase) printf '%s\n' "$DOMAIN_PROFILE_PHASE" ;;
		account-id) printf '%s\n' "$DOMAIN_PROFILE_ACCOUNT_ID" ;;
		zone-id) printf '%s\n' "$DOMAIN_PROFILE_ZONE_ID" ;;
		public-host) printf '%s\n' "$DOMAIN_PROFILE_PUBLIC_HOST" ;;
		public-url) printf '%s\n' "$DOMAIN_PROFILE_PUBLIC_URL" ;;
		legacy-origin) printf '%s\n' "$DOMAIN_PROFILE_LEGACY_ORIGIN" ;;
		legacy-host) printf '%s\n' "$DOMAIN_PROFILE_LEGACY_HOST" ;;
		canonical-host) printf '%s\n' "$DOMAIN_PROFILE_CANONICAL_HOST" ;;
		host-count) printf '%s\n' "$DOMAIN_PROFILE_HOST_COUNT" ;;
		audience-count) printf '%s\n' "$DOMAIN_PROFILE_AUDIENCE_COUNT" ;;
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
	local -A transition_values=()
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
			HELM_LEGACY_ORIGIN|ROADMAP_LEGACY_ORIGIN|HELM_CF_ACCESS_AUDIENCES|ROADMAP_CF_ACCESS_AUDIENCES|HELM_CF_ACCESS_HOST_AUDIENCES|ROADMAP_CF_ACCESS_HOST_AUDIENCES)
				[[ ! -v "transition_values[$key]" ]] || {
					domain_profile_error 'origin file contains a duplicate transition setting'
					return 1
				}
				transition_values[$key]=$value
				;;
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
	local suffix canonical_key legacy_key selected_legacy= selected_audiences= selected_map=
	for suffix in LEGACY_ORIGIN CF_ACCESS_AUDIENCES CF_ACCESS_HOST_AUDIENCES; do
		canonical_key=HELM_$suffix
		legacy_key=ROADMAP_$suffix
		if [[ -v "transition_values[$canonical_key]" && -v "transition_values[$legacy_key]" &&
			"${transition_values[$canonical_key]}" != "${transition_values[$legacy_key]}" ]]; then
			domain_profile_error 'origin file transition aliases must match'
			return 1
		fi
		value=${transition_values[$canonical_key]:-${transition_values[$legacy_key]:-}}
		case "$suffix" in
			LEGACY_ORIGIN) selected_legacy=$value ;;
			CF_ACCESS_AUDIENCES) selected_audiences=$value ;;
			CF_ACCESS_HOST_AUDIENCES) selected_map=$value ;;
		esac
	done
	[[ "$selected_legacy" = "${DOMAIN_PROFILE_LEGACY_ORIGIN:-}" ]] || {
		domain_profile_error 'origin file legacy origin does not match the reviewed phase'
		return 1
	}
	if [[ "${DOMAIN_PROFILE_DUAL_HOST:-0}" = 1 ]]; then
		local -a audiences=()
		local audience expected_map
		local -A seen_audiences=()
		IFS=, read -r -a audiences <<< "$selected_audiences"
		[[ ${#audiences[@]} = 4 && "$selected_audiences" != *, ]] || {
			domain_profile_error 'dual-host origin file requires four distinct audiences'
			return 1
		}
		for audience in "${audiences[@]}"; do
			[[ "$audience" =~ ^[A-Za-z0-9_-]+$ && ! -v "seen_audiences[$audience]" ]] || {
				domain_profile_error 'dual-host origin file has invalid or duplicate audiences'
				return 1
			}
			seen_audiences[$audience]=1
		done
		expected_map="$DOMAIN_PROFILE_LEGACY_HOST=${audiences[0]},${audiences[1]};$DOMAIN_PROFILE_CANONICAL_HOST=${audiences[2]},${audiences[3]}"
		[[ "$selected_map" = "$expected_map" ]] || {
			domain_profile_error 'origin file host audience bindings do not match the reviewed hosts'
			return 1
		}
	elif [[ -n "$selected_map" ]]; then
		domain_profile_error 'single-host origin file must not enable transition host bindings'
		return 1
	fi
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
