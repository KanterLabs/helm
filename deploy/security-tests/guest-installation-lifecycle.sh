# Guest state ownership and lifecycle safeguards.
contains 'DATA_DIR="$STATE_DIR/data"' "$INSTALL"
contains 'install -d -m 0755 -o root -g root "$STATE_DIR"' "$INSTALL"
contains 'install -d -m 0750 -o roadmap -g roadmap "$DATA_DIR"' "$INSTALL"
contains 'HELM_DEPLOY_LOCK_HELD=1' "$INSTALL"
contains 'healthy_revision()' "$INSTALL"
contains 'validate_release_env()' "$INSTALL"
contains 'sha256sum --check --strict "${checksum##*/}"' "$INSTALL"
contains 'X-Roadmap-Revision' "$INSTALL"
contains 'loopback_listener()' "$INSTALL"
contains '127\.0\.0\.1:8080|\[::1\]:8080' "$INSTALL"
contains 'install -m 0640 -o root -g root "$RELEASE_DIR/roadmap.env" "$new_target/roadmap.env"' "$INSTALL"
contains 'stop_unit cloudflared.service' "$INSTALL"
contains 'stop_unit helm.service' "$INSTALL"
contains 'HELM_MIGRATION_INFO_BINARY' "$INSTALL"
contains 'schema-preflight' "$INSTALL"
contains 'verified pre-upgrade backup' "$INSTALL"
contains "pre_upgrade_backup=%s source_schema=%s candidate_schema=%s latest_schema=%s migration_digest=%s checksum=%s integrity=%s fk=%s preflight=%s" "$INSTALL"
contains 'proof_backup=$(basename -- "$verified_backup")' "$INSTALL"
contains 'proof_migration_digest=$preflight_digest' "$INSTALL"
not_contains 'preflight_digest" = "$proof_migration_digest' "$INSTALL"
contains 'sha256sum --check --strict "$(basename -- "$checksum_sidecar")"' "$INSTALL"
contains 'PRAGMA integrity_check;' "$INSTALL"
contains 'PRAGMA foreign_key_check;' "$INSTALL"
contains 'fresh-install candidate schema preflight' "$INSTALL"
contains 'legacy database layout requires an explicit offline maintenance migration; no services were stopped' "$INSTALL"
contains 'migration-info' "$BACKUP"
contains 'migration-info' "$ROOT_DIR/cmd/helm/main.go"
not_contains 'systemctl stop cloudflared.service 2>/dev/null || true' "$INSTALL"
not_contains 'systemctl stop helm.service 2>/dev/null || true' "$INSTALL"
contains 'Keep guest installer stdout/stderr attached' "$GATEWAY"
contains 'pre_upgrade_backup=...' "$GATEWAY"

# Exercise the first-upgrade compatibility aliases under the installer's
# nounset contract. Keep filesystem commands mocked so this regression test
# executes the production helper bodies without touching the host.
source <(awk '/^install_compat_symlink\(\)/,/^}/' "$INSTALL")
source <(awk '/^install_unit_alias\(\)/,/^}/' "$INSTALL")
(
	ln() { return 0; }
	mv() { return 0; }
	rm() { return 0; }
	install_compat_symlink helm-backup roadmap-backup
	install_unit_alias helm.service roadmap.service
)
printf 'first_upgrade_alias_runtime_test=ok\n'

# If a first Helm install fails after stopping Roadmap but before installing
# helm.service, recovery must restart the retained unit instead of leaving the
# guest offline.
source <(awk '/^restore_previous\(\)/,/^}/' "$INSTALL")
(
	previous_target=/var/lib/roadmap/releases/0000000000000000000000000000000000000000
	previous_sha=0000000000000000000000000000000000000000
	recovery_calls="$fixture/first-upgrade-recovery.calls"
	: > "$recovery_calls"
	stop_unit() { return 0; }
	install_release_env() { return 0; }
	atomic_switch() { return 0; }
	healthy_revision() { return 0; }
	systemctl() {
		printf '%s\n' "$*" >> "$recovery_calls"
		[[ "${1:-} ${2:-}" != 'start helm.service' ]]
	}
	restore_previous
	contains 'start helm.service' "$recovery_calls"
	contains 'start roadmap.service' "$recovery_calls"
	contains 'is-active --quiet roadmap.service' "$recovery_calls"
	contains 'start cloudflared.service' "$recovery_calls"
)
printf 'first_upgrade_recovery_runtime_test=ok\n'

# A retained binary may predate migration-info. The installer must ask the
# candidate binary for metadata rather than selecting the retained executable
# and failing before the first metadata-bearing backup is published.
old_binary="$fixture/retained-old-roadmap"
printf '#!/usr/bin/env bash\nexit 64\n' > "$old_binary"
chmod 0755 "$old_binary"
if "$old_binary" migration-info >/dev/null 2>&1; then
	fail 'old retained binary fixture unexpectedly implements migration-info'
fi
contains 'migration_info_binary="$RELEASE_DIR/roadmap"' "$INSTALL"

# Postfix is not used by Roadmap. Keep the aggregate, template, and generated
# default instance names explicit so the active postfix@-.service unit is
# stopped and future package actions cannot reactivate any of them.
contains 'disable_unused_postfix()' "$INSTALL"
contains 'for unit in postfix.service postfix@-.service; do' "$INSTALL"
contains 'for unit in postfix.service postfix@.service postfix@-.service; do' "$INSTALL"
contains 'systemctl disable "$unit"' "$INSTALL"
contains 'systemctl mask "$unit"' "$INSTALL"
contains 'systemctl is-enabled "$unit"' "$INSTALL"
not_contains 'systemctl stop postfix*' "$INSTALL"
not_contains 'systemctl disable postfix*' "$INSTALL"
not_contains 'systemctl mask postfix*' "$INSTALL"
not_contains 'apt-get purge postfix' "$INSTALL"

# Exercise the exact-unit handling with an active default instance, an
# indirect template, and a second idempotent invocation. The template does
# not have an instantiated state to inspect, so this mock fails if the
# implementation tries to query it with `systemctl show`.
source <(awk '/^unit_state\(\)/,/^}/' "$INSTALL")
SERVICE_STOP_TIMEOUT=30
source <(awk '/^stop_unit\(\)/,/^}/' "$INSTALL")
source <(awk '/^disable_unused_postfix\(\)/,/^}/' "$INSTALL")
postfix_call_log="$fixture/postfix.calls"
postfix_service_state=active
postfix_instance_state=active
postfix_mock_absent=0
postfix_template_show_attempt=0
declare -A postfix_masked=()
: > "$postfix_call_log"
postfix_mock_systemctl() {
	local action=${1:-} unit=${2:-} state
	case "$action" in
		show)
			case "$unit" in
				postfix.service|postfix@-.service) printf 'loaded\n' ;;
				postfix@.service) postfix_template_show_attempt=1; return 1 ;;
				*) return 1 ;;
			esac
			;;
		is-active)
			case "$unit" in
				postfix.service) state=$postfix_service_state ;;
				postfix@-.service) state=$postfix_instance_state ;;
				*) state=unknown ;;
			esac
			printf '%s\n' "$state"
			;;
		stop)
			printf 'stop %s\n' "$unit" >> "$postfix_call_log"
			case "$unit" in
				postfix.service) postfix_service_state=inactive ;;
				postfix@-.service) postfix_instance_state=inactive ;;
				*) return 1 ;;
			esac
			;;
		is-enabled)
			if [[ "${postfix_masked[$unit]:-0}" = 1 ]]; then
				printf 'masked\n'
				return 1
			fi
			if (( postfix_mock_absent )); then
				printf 'not-found\n'
				return 1
			fi
			case "$unit" in
				postfix.service) printf 'enabled\n' ;;
				postfix@.service) printf 'indirect\n' ;;
				postfix@-.service) printf 'enabled-runtime\n' ;;
				*) return 1 ;;
			esac
			;;
		disable)
			printf 'disable %s\n' "$unit" >> "$postfix_call_log"
			;;
		mask)
			printf 'mask %s\n' "$unit" >> "$postfix_call_log"
			postfix_masked[$unit]=1
			;;
		*) return 1 ;;
	esac
}
systemctl() { postfix_mock_systemctl "$@"; }
postfix_expected_first=$'stop postfix.service\nstop postfix@-.service\ndisable postfix.service\nmask postfix.service\nmask postfix@.service\ndisable postfix@-.service\nmask postfix@-.service'
disable_unused_postfix || fail 'postfix hardening rejected an active default instance'
[[ "$postfix_template_show_attempt" = 0 ]] || fail 'postfix hardening queried the uninstantiated template state'
[[ "$(sed -n '1,7p' "$postfix_call_log")" = "$postfix_expected_first" ]] \
	|| fail 'postfix hardening did not stop, disable, and mask the exact units'
[[ "$postfix_service_state" = inactive && "$postfix_instance_state" = inactive ]] \
	|| fail 'postfix hardening left an active service or default instance'
disable_unused_postfix || fail 'postfix hardening was not idempotent'
[[ "$(sed -n '8,10p' "$postfix_call_log")" = $'mask postfix.service\nmask postfix@.service\nmask postfix@-.service' ]] \
	|| fail 'postfix hardening repeated more than the exact mask operations'

# A postfix-free image has no service/default instance state; all three masks
# must still be created and verified so a later package installation cannot
# enable the service.
postfix_mock_absent=1
postfix_service_state=unknown
postfix_instance_state=unknown
postfix_masked=()
: > "$postfix_call_log"
disable_unused_postfix || fail 'postfix-free image was not handled idempotently'
[[ "$(<"$postfix_call_log")" = $'mask postfix.service\nmask postfix@.service\nmask postfix@-.service' ]] \
	|| fail 'postfix-free image did not receive all three exact masks'
unset -f systemctl postfix_mock_systemctl
unset postfix_service_state postfix_instance_state postfix_mock_absent postfix_template_show_attempt
unset postfix_call_log postfix_masked
printf 'postfix_hardening_runtime_tests=ok\n'

# The listener check must recognize only an exact loopback local-address field
# in ss output. It must reject non-loopback listeners, port-prefix matches,
# loopback peers, and an ss command failure.
source <(awk '/^loopback_listener\(\)/,/^}/' "$INSTALL")
loopback_ss_output=$'State  Recv-Q Send-Q Local Address:Port  Peer Address:Port\nLISTEN 0 128 127.0.0.1:8080 0.0.0.0:*\nLISTEN 0 128 [::1]:8080 [::]:*'
ss() { [[ "$1" = -ltn ]] && printf '%s\n' "$loopback_ss_output"; }
loopback_listener || fail 'listener check rejected canonical IPv4/IPv6 loopback output'
for loopback_bad_ss_output in \
	$'State  Recv-Q Send-Q Local Address:Port  Peer Address:Port\nLISTEN 0 128 10.0.0.38:8080 0.0.0.0:*' \
	$'State  Recv-Q Send-Q Local Address:Port  Peer Address:Port\nLISTEN 0 128 127.0.0.1:80801 0.0.0.0:*' \
	$'State  Recv-Q Send-Q Local Address:Port  Peer Address:Port\nLISTEN 0 128 0.0.0.0:1234 127.0.0.1:8080'; do
	loopback_ss_output=$loopback_bad_ss_output
	if loopback_listener; then
		fail "listener check accepted invalid ss output: $loopback_bad_ss_output"
	fi
done
ss() { return 1; }
if loopback_listener; then
	fail 'listener check accepted an ss command failure'
fi
unset -f ss
printf 'loopback_listener_runtime_tests=ok\n'

