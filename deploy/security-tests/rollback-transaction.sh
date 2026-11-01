# Rollback is transactional across both units: an app health failure and a
# connector start/active failure restore the previous link, restart the prior
# app and tunnel, and report the requested rollback as failed.
contains 'start_and_verify_previous()' "$ROLLBACK"
contains 'install_release_env "$TARGET" "$SHA"' "$ROLLBACK"
contains 'healthy_revision "$SHA"' "$ROLLBACK"
contains 'X-Roadmap-Revision' "$ROLLBACK"
contains 'validate_ct_config()' "$GATEWAY"
contains 'pct config "$CTID"' "$GATEWAY"
contains 'current_sha=none' "$GATEWAY"
contains 'created=1' "$GATEWAY"
not_contains 'unexpected deployment metadata or resolver settings' "$GATEWAY"
not_contains 'systemctl stop cloudflared.service 2>/dev/null || true' "$ROLLBACK"
not_contains 'systemctl stop helm.service 2>/dev/null || true' "$ROLLBACK"
not_contains 'systemctl stop cloudflared.service >/dev/null 2>&1 || true' "$RESTORE"
not_contains 'systemctl stop helm.service >/dev/null 2>&1 || true' "$RESTORE"
contains "start_and_verify_previous 'requested release failed app health'" "$ROLLBACK"
contains "start_and_verify_previous 'requested release failed cloudflared validation'" "$ROLLBACK"
count_contains 2 'if start_and_verify_previous' "$ROLLBACK"
contains 'atomic_switch "$previous_target"' "$ROLLBACK"
contains 'systemctl start helm.service || return 1' "$ROLLBACK"
contains 'systemctl start cloudflared.service || return 1' "$ROLLBACK"
contains 'systemctl is-active --quiet helm.service || return 1' "$ROLLBACK"
contains 'systemctl is-active --quiet roadmap.service || return 1' "$ROLLBACK"
contains 'systemctl is-active --quiet cloudflared.service || return 1' "$ROLLBACK"
contains 'exit 1' "$ROLLBACK"

# Legacy rollback targets may omit Codex entirely, while new targets must
# carry a complete executable/checksum pair and pass verification.
source <(awk '/^validate_optional_codex\(\)/,/^}/' "$ROLLBACK")
codex_rollback_fixture="$fixture/codex-rollback"
install -d -m 0755 "$codex_rollback_fixture"
validate_optional_codex "$codex_rollback_fixture" || fail 'legacy rollback target without Codex was rejected'
printf 'codex fixture\n' > "$codex_rollback_fixture/codex"
chmod 0755 "$codex_rollback_fixture/codex"
if validate_optional_codex "$codex_rollback_fixture"; then
	fail 'partial rollback target with Codex but no checksum was accepted'
fi
printf '%064d  codex\n' 0 > "$codex_rollback_fixture/codex.sha256"
if validate_optional_codex "$codex_rollback_fixture" 2>/dev/null; then
	fail 'rollback target with a bad Codex checksum was accepted'
fi
(cd "$codex_rollback_fixture" && sha256sum codex > codex.sha256)
validate_optional_codex "$codex_rollback_fixture" || fail 'valid rollback Codex runtime was rejected'
printf 'rollback_codex_validation_test=ok\n'

# Run the real rollback transaction against a fixture state root. The service,
# health, install, and rename boundaries are mocked, while release checks,
# link switching, EXIT recovery, and exact environment restoration remain
# production code. Non-root developer runs use a temporary copy with only the
# root-owner preflight removed; no production file is changed.
rollback_test_script=$ROLLBACK
if [[ "$(id -u)" -ne 0 ]]; then
	rollback_test_script="$fixture/roadmap-rollback-test.sh"
	sed -e '/must run as root/d' -e '/state directory owner is invalid/d' \
		"$ROLLBACK" > "$rollback_test_script"
	chmod 0755 "$rollback_test_script"
fi

rollback_systemctl() {
	local action=${1:-} unit=${2:-} state_file state current
	case "$action" in
		daemon-reload|mask|unmask)
			return 0
			;;
		show)
			printf 'loaded\n'
			;;
		is-active)
			unit=${!#}
			[[ "$unit" = roadmap.service ]] && unit=helm.service
			state_file="$ROLLBACK_SERVICE_STATE_DIR/${unit%.service}"
			state=$(<"$state_file")
			if [[ "${1:-}" = is-active && "${2:-}" = --quiet ]]; then
				[[ "$state" = active ]]
			else
				printf '%s\n' "$state"
			fi
			;;
		stop)
			unit=$2
			[[ "$unit" = roadmap.service ]] && unit=helm.service
			state_file="$ROLLBACK_SERVICE_STATE_DIR/${unit%.service}"
			printf 'stop %s\n' "$unit" >> "$ROLLBACK_CALL_LOG"
			printf 'inactive\n' > "$state_file"
			;;
		start)
			unit=$2
			[[ "$unit" = roadmap.service ]] && unit=helm.service
			state_file="$ROLLBACK_SERVICE_STATE_DIR/${unit%.service}"
			current=$(readlink -- "$ROLLBACK_CURRENT_LINK" 2>/dev/null || true)
			if [[ "$current" = "$ROLLBACK_TARGET_DIR" &&
				( "$ROLLBACK_MODE" = app-restart-failure && "$unit" = helm.service ||
				  "$ROLLBACK_MODE" = connector-restart-failure && "$unit" = cloudflared.service ) &&
				! -e "$ROLLBACK_START_FAILURE_MARKER" ]]; then
				: > "$ROLLBACK_START_FAILURE_MARKER"
				printf 'start-failure %s\n' "$unit" >> "$ROLLBACK_CALL_LOG"
				printf 'failed\n' > "$state_file"
				return 1
			fi
			printf 'start %s\n' "$unit" >> "$ROLLBACK_CALL_LOG"
			printf 'active\n' > "$state_file"
			;;
		*) return 1 ;;
	esac
}

rollback_curl() {
	local current revision
	current=$(readlink -- "$ROLLBACK_CURRENT_LINK" 2>/dev/null || true)
	revision=${current##*/}
	printf 'HTTP/1.1 200 OK\nX-Roadmap-Revision: %s\n\n{"revision":"%s"}\n' \
		"$revision" "$revision"
}

rollback_install() {
	local -a args=()
	while [[ $# -gt 0 ]]; do
		case "$1" in
			-o|-g) shift 2 ;;
			*) args+=("$1"); shift ;;
		esac
	done
	local source=${args[$(( ${#args[@]} - 2 ))]} destination=${args[$(( ${#args[@]} - 1 ))]}
	if [[ "$ROLLBACK_MODE" = install-before-switch-failure &&
		"$source" = "$ROLLBACK_TARGET_DIR/roadmap.env" &&
		! -e "$ROLLBACK_INSTALL_FAILURE_MARKER" ]]; then
		: > "$ROLLBACK_INSTALL_FAILURE_MARKER"
		printf 'install-failure\n' >> "$ROLLBACK_CALL_LOG"
		return 1
	fi
	command install "${args[@]}"
	chmod 0640 -- "$destination"
}

rollback_mv() {
	local -a args=("$@") source destination
	source=${args[$(( ${#args[@]} - 2 ))]}
	destination=${args[$(( ${#args[@]} - 1 ))]}
	if [[ "$ROLLBACK_MODE" = symlink-switch-failure &&
		"$destination" = "$ROLLBACK_CURRENT_LINK" && -L "$source" &&
		"$(readlink -- "$source")" = "$ROLLBACK_TARGET_DIR" &&
		! -e "$ROLLBACK_SWITCH_FAILURE_MARKER" ]]; then
		: > "$ROLLBACK_SWITCH_FAILURE_MARKER"
		printf 'switch-failure\n' >> "$ROLLBACK_CALL_LOG"
		return 1
	fi
	command mv "$@"
}

rollback_sleep() { :; }

rollback_run_case() {
	local mode=$1 case_dir="$fixture/rollback-$1" status
	local previous_sha=1111111111111111111111111111111111111111
	local target_sha=2222222222222222222222222222222222222222
	local state_dir="$case_dir/state" releases_dir="$case_dir/state/releases"
	local config_dir="$case_dir/config" service_state_dir="$case_dir/services"
	local previous_dir="$releases_dir/$previous_sha" target_dir="$releases_dir/$target_sha"
	local call_log="$case_dir/calls.log" output="$case_dir/output.log"
	/usr/bin/install -d -m 0755 "$releases_dir" "$config_dir" "$service_state_dir"
	chmod 0755 "$state_dir"
	/usr/bin/install -d -m 0755 "$previous_dir" "$target_dir"
	printf 'previous release binary\n' > "$previous_dir/roadmap"
	printf 'target release binary\n' > "$target_dir/helm"
	chmod 0755 "$previous_dir/roadmap" "$target_dir/helm"
	(
		cd "$previous_dir" && sha256sum roadmap > roadmap.sha256
	)
	(
		cd "$target_dir" && sha256sum helm > helm.sha256
	)
	printf 'ROADMAP_RELEASE_SHA=%s\nROADMAP_ENV_MARKER=previous\n' "$previous_sha" > "$previous_dir/roadmap.env"
	printf 'HELM_RELEASE_SHA=%s\nROADMAP_RELEASE_SHA=%s\nHELM_ENV_MARKER=target\n' "$target_sha" "$target_sha" > "$target_dir/roadmap.env"
	cp -- "$previous_dir/roadmap.env" "$case_dir/previous.env.ref"
	ln -s -- "$previous_dir" "$state_dir/current"
	printf 'ROADMAP_RELEASE_SHA=%s\nROADMAP_ENV_MARKER=live-before\n' "$previous_sha" > "$config_dir/roadmap.env"
	printf 'active\n' > "$service_state_dir/helm"
	printf 'active\n' > "$service_state_dir/cloudflared"
	: > "$call_log"

	ROLLBACK_MODE=$mode
	ROLLBACK_STATE_DIR="$state_dir"
	ROLLBACK_CURRENT_LINK="$state_dir/current"
	ROLLBACK_TARGET_DIR="$target_dir"
	ROLLBACK_CALL_LOG="$call_log"
	ROLLBACK_SERVICE_STATE_DIR="$service_state_dir"
	ROLLBACK_START_FAILURE_MARKER="$case_dir/start-failure"
	ROLLBACK_INSTALL_FAILURE_MARKER="$case_dir/install-failure"
	ROLLBACK_SWITCH_FAILURE_MARKER="$case_dir/switch-failure"
	ROLLBACK_TARGET_SHA="$target_sha"
	ROLLBACK_CONFIG_DIR="$config_dir"
	export ROLLBACK_MODE ROLLBACK_CURRENT_LINK ROLLBACK_TARGET_DIR ROLLBACK_CALL_LOG \
		ROLLBACK_SERVICE_STATE_DIR ROLLBACK_START_FAILURE_MARKER ROLLBACK_INSTALL_FAILURE_MARKER \
		ROLLBACK_SWITCH_FAILURE_MARKER ROLLBACK_TARGET_SHA
	set +e
	(
		export ROADMAP_STATE_DIR="$state_dir" ROADMAP_CONFIG_DIR="$config_dir"
		export -f rollback_systemctl rollback_curl rollback_install rollback_mv rollback_sleep
		systemctl() { rollback_systemctl "$@"; }
		curl() { rollback_curl "$@"; }
		install() { rollback_install "$@"; }
		mv() { rollback_mv "$@"; }
		sleep() { rollback_sleep "$@"; }
		export -f systemctl curl install mv sleep
		"$rollback_test_script" "$target_sha"
	) >"$output" 2>&1
	status=$?
	set -e
	[[ "$status" -ne 0 ]] || fail "rollback $mode unexpectedly succeeded"
	cmp -s "$case_dir/previous.env.ref" "$config_dir/roadmap.env" \
		|| fail "rollback $mode did not restore the exact previous environment"
	[[ "$(readlink -- "$state_dir/current")" = "$previous_dir" ]] \
		|| fail "rollback $mode did not restore the previous current link"
	[[ "$(<"$service_state_dir/helm")" = active && "$(<"$service_state_dir/cloudflared")" = active ]] \
		|| fail "rollback $mode did not restore both prior services"
	grep -Eq '^start helm\.service$' "$call_log" \
		|| fail "rollback $mode did not restart helm.service"
	grep -Eq '^start cloudflared\.service$' "$call_log" \
		|| fail "rollback $mode did not restart cloudflared.service"
}

rollback_run_case install-before-switch-failure
rollback_run_case symlink-switch-failure
rollback_run_case app-restart-failure
rollback_run_case connector-restart-failure
printf 'rollback_recovery_runtime_tests=ok\n'

