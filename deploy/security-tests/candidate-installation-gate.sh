# The service ExecStart resolves through the current release link. Switch it
# before systemd verifies the unit and before starting the new application so
# a clean install cannot fail verification on a missing executable.
install_switch_line=$(grep -n '^atomic_switch "\$release_target"' "$INSTALL" | cut -d: -f1 || true)
install_verify_line=$(grep -n 'systemd-analyze verify ' "$INSTALL" | sed -n '1p' | cut -d: -f1 || true)
install_helm_start_line=$(grep -n '^systemctl start helm\.service$' "$INSTALL" | cut -d: -f1 || true)
[[ -n "$install_switch_line" && -n "$install_verify_line" && -n "$install_helm_start_line" ]] \
	|| fail 'install ordering regression checks could not find the release switch, unit verification, and Helm start'
[[ "$install_switch_line" -lt "$install_verify_line" ]] \
	|| fail 'install switches the current release after systemd unit verification'
[[ "$install_switch_line" -lt "$install_helm_start_line" ]] \
	|| fail 'install starts Helm before switching the current release'
contains 'WorkingDirectory=/var/lib/roadmap/data' "$SERVICE"
contains 'ReadWritePaths=/var/lib/roadmap/data' "$SERVICE"
not_contains 'ReadWritePaths=/var/lib/roadmap ' "$SERVICE"
contains 'ReadWritePaths=/var/lib/roadmap/data /var/lib/roadmap/backups /var/lib/roadmap/deploy.lock' "$BACKUP_SERVICE"
not_contains 'ReadWritePaths=/var/lib/roadmap ' "$BACKUP_SERVICE"
contains 'RandomizedDelaySec=1h' "$DEPLOY_DIR/helm-backup.timer"
contains 'Persistent=true' "$DEPLOY_DIR/helm-backup.timer"
contains 'helm-backup.timer' "$INSTALL"
contains 'HELM_DATA_DIR' "$BACKUP"
contains 'HELM_DATA_DIR' "$RESTORE"
contains 'HELM_DEPLOY_LOCK_HELD' "$BACKUP"
contains 'schema_version' "$BACKUP"
contains 'migration_digest' "$BACKUP"
contains 'PRAGMA foreign_key_check' "$BACKUP"
contains 'schema_version' "$RESTORE"
contains 'migration_digest' "$RESTORE"
contains 'PRAGMA foreign_key_check' "$RESTORE"
contains 'migration-apply' "$RESTORE"
contains 'migrate_staged_candidate "$candidate"' "$RESTORE"
contains 'metadata release does not match its filename' "$RESTORE"
contains 'email = NULL' "$RESTORE"
contains 'password_hash = NULL' "$RESTORE"
contains 'admin = 0' "$RESTORE"
contains 'description = (SELECT c.description' "$RESTORE"
contains 'current_auth.actors' "$RESTORE"
contains 'current_auth.auth_setup' "$RESTORE"
contains 'current_auth.actor_projects' "$RESTORE"
contains 'current_auth.actor_resource_usage' "$RESTORE"
contains 'CREATE TABLE IF NOT EXISTS actor_resource_usage' "$RESTORE"
contains 'RETENTION=$(compat_env HELM_BACKUP_RETENTION ROADMAP_BACKUP_RETENTION 14)' "$RESTORE"
contains 'prune_backups()' "$RESTORE"
contains 'prune_backups "$current_snapshot" "$BACKUP_PATH"' "$RESTORE"
contains 'protected_recovery' "$RESTORE"
contains 'protected_selected' "$RESTORE"
contains '[[ $# -eq 1 ]] || usage' "$RESTORE"
contains 'backup must be directly inside the retained backup directory' "$RESTORE"
contains 'backup filename is not a retained Roadmap backup' "$RESTORE"
contains 'DELETE FROM sessions' "$RESTORE"
contains 'DELETE FROM tokens' "$RESTORE"
contains 'DELETE FROM idempotency_keys' "$RESTORE"
contains 'Never swap a raw' "$DOCS"
not_contains 'atomically move it to /var/lib/roadmap/roadmap.db' "$DOCS"

# The candidate gate must complete while the application is still online and
# before the installer stops either unit or switches the current release link.
install_backup_line=$(grep -n 'roadmap-backup.sh" "\$SHA"' "$INSTALL" | cut -d: -f1 || true)
install_preflight_line=$(grep -n 'schema-preflight' "$INSTALL" | sed -n '1p' | cut -d: -f1 || true)
install_proof_line=$(grep -n "^printf 'pre_upgrade_backup=" "$INSTALL" | cut -d: -f1 || true)
install_stop_line=$(grep -n 'stop_unit cloudflared\.service' "$INSTALL" | tail -n 1 | cut -d: -f1 || true)
install_switch_line=$(grep -n '^atomic_switch "\$release_target"' "$INSTALL" | cut -d: -f1 || true)
legacy_refusal_line=$(grep -n 'legacy database layout requires an explicit offline maintenance migration' "$INSTALL" | cut -d: -f1 || true)
[[ -n "$install_backup_line" && -n "$install_preflight_line" && -n "$install_proof_line" && -n "$install_stop_line" && -n "$install_switch_line" && -n "$legacy_refusal_line" ]] \
	|| fail 'install migration-gate ordering checks could not find backup/preflight/proof/stop/switch'
[[ "$install_backup_line" -lt "$install_preflight_line" && "$install_preflight_line" -lt "$install_proof_line" && "$install_proof_line" -lt "$install_stop_line" && "$install_stop_line" -lt "$install_switch_line" ]] \
	|| fail 'install does not gate the proof and atomic switch on a verified preflight'
[[ "$legacy_refusal_line" -lt "$install_stop_line" ]] \
	|| fail 'legacy database refusal must occur before any normal deployment downtime'

# The proof is deliberately one line, basename-only, and machine-parseable.
# Keep this assertion in the deployment-security suite so a future change
# cannot accidentally put credentials or broad guest paths on the CI stream.
assert_proof_format() {
	local proof=$1
	[[ "$proof" =~ ^pre_upgrade_backup=(none|roadmap-[0-9]{8}T[0-9]{6}Z-(manual|daily|pre-restore|[0-9a-f]{40})\.db)[[:space:]]source_schema=[0-9]+[[:space:]]candidate_schema=[0-9]+[[:space:]]latest_schema=[0-9]+[[:space:]]migration_digest=[0-9a-f]{64}[[:space:]]checksum=valid[[:space:]]integrity=ok[[:space:]]fk=ok[[:space:]]preflight=ok$ ]] \
		|| fail "invalid sanitized deployment proof: $proof"
}
assert_proof_format 'pre_upgrade_backup=roadmap-20260828T173000Z-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.db source_schema=9 candidate_schema=10 latest_schema=10 migration_digest=0000000000000000000000000000000000000000000000000000000000000000 checksum=valid integrity=ok fk=ok preflight=ok'
assert_proof_format 'pre_upgrade_backup=none source_schema=0 candidate_schema=10 latest_schema=10 migration_digest=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff checksum=valid integrity=ok fk=ok preflight=ok'
not_contains 'sqlite3' "$ROLLBACK"
not_contains 'cp --' "$ROLLBACK"

