# Exercise backup publication and retention with a mocked sqlite3 API. The
# helper still performs its real descriptor identity, checksum, metadata, and
# publication ordering checks; only SQLite and ownership capabilities are
# mocked so this remains deterministic on ordinary developer hosts.
backup_mock_id() {
	if [[ "${1:-}" = -u ]]; then
		printf '0\n'
	else
		command id "$@"
	fi
}

backup_mock_install() {
	local -a args=()
	while [[ $# -gt 0 ]]; do
		case "$1" in
			-o|-g) shift 2 ;;
			*) args+=("$1"); shift ;;
		esac
	done
	command install "${args[@]}"
}

backup_mock_chown() { :; }

backup_mock_stat() {
	local format= path= arg stat_option=-c
	while [[ $# -gt 0 ]]; do
		arg=$1
		case "$arg" in
			-c|-Lc)
				format=$2
				stat_option=$arg
				shift 2
				;;
			*)
				path=$arg
				shift
				;;
		esac
	done
	if [[ "$format" = '%U:%G' ]]; then
		if [[ "$path" = "$BACKUP_MOCK_DB_PATH" || "$path" = "${BACKUP_MOCK_DB_PATH%/*}" || "$path" = /proc/*/fd/* ]]; then
			printf 'roadmap:roadmap\n'
		else
			printf 'root:root\n'
		fi
		return 0
	fi
	command stat "$stat_option" "$format" -- "$path"
}

backup_mock_sqlite3() {
	local arg backup_path= sql
	for arg in "$@"; do
		if [[ "$arg" = .backup\ * ]]; then
			backup_path=${arg#".backup '"}
			backup_path=${backup_path%\'}
		fi
	done
	if [[ -n "$backup_path" ]]; then
		printf 'fixture sqlite backup\n' > "$backup_path"
		if [[ "${BACKUP_MOCK_SOURCE_DRIFT:-0}" = 1 ]]; then
			/bin/mv -- "$BACKUP_MOCK_DB_PATH" "$BACKUP_MOCK_DB_PATH.original"
			printf 'replacement database\n' > "$BACKUP_MOCK_DB_PATH"
		fi
		return 0
	fi
	for sql in "$@"; do
		if [[ "$sql" = 'SELECT COALESCE(MAX(version), 0) FROM schema_migrations;' ]]; then
			printf '8\n'
			return 0
		fi
		if [[ "$sql" = 'PRAGMA integrity_check;' ]]; then
			printf 'ok\n'
			return 0
		fi
		if [[ "$sql" = 'PRAGMA foreign_key_check;' ]]; then
			return 0
		fi
	done
	return 1
}

backup_mock_mv() {
	local -a args=("$@") source destination
	source=${args[$(( ${#args[@]} - 2 ))]}
	destination=${args[$(( ${#args[@]} - 1 ))]}
	if [[ "${BACKUP_MOCK_MV_FAILURE:-}" = metadata &&
		"$destination" = "$BACKUP_MOCK_DIR"/roadmap-*.db.metadata &&
		! -e "$BACKUP_MOCK_FAILURE_MARKER" ]]; then
		: > "$BACKUP_MOCK_FAILURE_MARKER"
		return 1
	fi
	if [[ "${BACKUP_MOCK_MV_FAILURE:-}" = database &&
		"$destination" = "$BACKUP_MOCK_DIR"/roadmap-*.db &&
		! -e "$BACKUP_MOCK_FAILURE_MARKER" ]]; then
		: > "$BACKUP_MOCK_FAILURE_MARKER"
		return 1
	fi
	command mv "$@"
}

backup_mock_setup() {
	local case_dir=$1
	/usr/bin/install -d -m 0755 "$case_dir/state/data" "$case_dir/state/backups"
	chmod 0755 "$case_dir/state"
	chmod 0750 "$case_dir/state/data"
	chmod 0700 "$case_dir/state/backups"
	printf 'fixture source database\n' > "$case_dir/state/data/roadmap.db"
}

backup_mock_run() {
	local name=$1 mode=$2 status case_dir
	local state_dir db_path backup_dir output
	case_dir="$fixture/backup-$name"
	state_dir="$case_dir/state"
	db_path="$state_dir/data/roadmap.db"
	backup_dir="$state_dir/backups"
	output="$case_dir/output.log"
	/usr/bin/install -d -m 0755 "$case_dir"
	backup_mock_setup "$case_dir"
	BACKUP_MOCK_DB_PATH=$db_path
	BACKUP_MOCK_DIR=$backup_dir
	BACKUP_MOCK_MV_FAILURE=$mode
	BACKUP_MOCK_SOURCE_DRIFT=0
	BACKUP_MOCK_FAILURE_MARKER="$case_dir/failure.marker"
	export BACKUP_MOCK_DB_PATH BACKUP_MOCK_DIR BACKUP_MOCK_MV_FAILURE BACKUP_MOCK_SOURCE_DRIFT BACKUP_MOCK_FAILURE_MARKER
	set +e
	(
		export ROADMAP_STATE_DIR="$state_dir" ROADMAP_DATA_DIR="$state_dir/data" \
			ROADMAP_DB_PATH="$db_path" ROADMAP_BACKUP_DIR="$backup_dir" ROADMAP_BACKUP_RETENTION=2
		export -f backup_mock_id backup_mock_install backup_mock_chown backup_mock_stat \
			backup_mock_sqlite3 backup_mock_mv
		id() { backup_mock_id "$@"; }
		install() { backup_mock_install "$@"; }
		chown() { backup_mock_chown "$@"; }
		stat() { backup_mock_stat "$@"; }
		sqlite3() { backup_mock_sqlite3 "$@"; }
		mv() { backup_mock_mv "$@"; }
		export -f id install chown stat sqlite3 mv
		"$BACKUP" manual
	) >"$output" 2>&1
	status=$?
	set -e
	[[ "$status" -ne 0 ]] || fail "backup $name unexpectedly succeeded"
	if compgen -G "$backup_dir/roadmap-*.db*" >/dev/null; then
		fail "backup $name left a partially published artifact"
	fi
}

# The helper must remain usable during a binary-only rollback when current
# points at an older executable that does not implement migration-info.
backup_old_binary_case="$fixture/backup-old-binary"
backup_mock_setup "$backup_old_binary_case"
backup_old_binary_state="$backup_old_binary_case/state"
backup_old_binary_dir="$backup_old_binary_state/backups"
backup_old_binary_path="$backup_old_binary_state/data/roadmap.db"
backup_old_binary_failure="$backup_old_binary_case/failure.marker"
set +e
(
	export ROADMAP_STATE_DIR="$backup_old_binary_state" ROADMAP_DATA_DIR="$backup_old_binary_state/data" \
		ROADMAP_DB_PATH="$backup_old_binary_path" ROADMAP_BACKUP_DIR="$backup_old_binary_dir" ROADMAP_BACKUP_RETENTION=2 \
		ROADMAP_MIGRATION_INFO_BINARY="$old_binary"
	unset ROADMAP_MIGRATION_DIGEST
	BACKUP_MOCK_DB_PATH="$backup_old_binary_path" BACKUP_MOCK_DIR="$backup_old_binary_dir" \
		BACKUP_MOCK_MV_FAILURE= BACKUP_MOCK_SOURCE_DRIFT=0 BACKUP_MOCK_FAILURE_MARKER="$backup_old_binary_failure"
	export BACKUP_MOCK_DB_PATH BACKUP_MOCK_DIR BACKUP_MOCK_MV_FAILURE BACKUP_MOCK_SOURCE_DRIFT BACKUP_MOCK_FAILURE_MARKER
	export -f backup_mock_id backup_mock_install backup_mock_chown backup_mock_stat backup_mock_sqlite3 backup_mock_mv
	id() { backup_mock_id "$@"; }
	install() { backup_mock_install "$@"; }
	chown() { backup_mock_chown "$@"; }
	stat() { backup_mock_stat "$@"; }
	sqlite3() { backup_mock_sqlite3 "$@"; }
	mv() { backup_mock_mv "$@"; }
	export -f id install chown stat sqlite3 mv
	"$BACKUP" manual
) >"$backup_old_binary_case/output.log" 2>&1
backup_old_binary_status=$?
set -e
[[ "$backup_old_binary_status" -eq 0 ]] || fail 'backup failed with an old current binary lacking migration-info'
backup_old_binary_metadata=$(find "$backup_old_binary_dir" -maxdepth 1 -type f -name '*.db.metadata' -print -quit)
[[ -n "$backup_old_binary_metadata" ]] || fail 'old-binary backup did not publish metadata'
grep -Eq '^migration_digest=[0-9a-f]{64}$' "$backup_old_binary_metadata" \
	|| fail 'old-binary backup did not publish deterministic migration digest'
printf 'backup_old_binary_compatibility_test=ok\n'

backup_mock_run publication-metadata-failure metadata
backup_mock_run publication-database-failure database

backup_retention_case="$fixture/backup-retention"
/usr/bin/install -d -m 0755 "$backup_retention_case"
backup_mock_setup "$backup_retention_case"
backup_retention_state="$backup_retention_case/state"
backup_retention_dir="$backup_retention_state/backups"
backup_old="$backup_retention_dir/roadmap-20000101T000000Z-manual.db"
backup_incomplete="$backup_retention_dir/roadmap-20000101T000001Z-manual.db"
backup_corrupt="$backup_retention_dir/roadmap-20000101T000002Z-manual.db"
printf 'old valid backup\n' > "$backup_old"
printf 'incomplete backup\n' > "$backup_incomplete"
printf 'corrupt backup\n' > "$backup_corrupt"
(
	cd "$backup_retention_dir"
	sha256sum "$(basename -- "$backup_old")" > "$(basename -- "$backup_old").sha256"
)
printf 'release_sha=manual\ncreated_at=2000-01-01T00:00:00Z\n' > "$backup_old.metadata"
printf '%064d  %s\n' 0 "$(basename -- "$backup_corrupt")" > "$backup_corrupt.sha256"
printf 'release_sha=manual\ncreated_at=2000-01-01T00:00:00Z\n' > "$backup_corrupt.metadata"
chmod 0600 "$backup_old" "$backup_old.sha256" "$backup_old.metadata" \
	"$backup_incomplete" "$backup_corrupt" "$backup_corrupt.sha256" "$backup_corrupt.metadata"
touch -d '2000-01-01T00:00:00Z' "$backup_old" "$backup_old.sha256" "$backup_old.metadata"
BACKUP_MOCK_DB_PATH="$backup_retention_state/data/roadmap.db"
BACKUP_MOCK_DIR=$backup_retention_dir
BACKUP_MOCK_MV_FAILURE=
BACKUP_MOCK_SOURCE_DRIFT=0
BACKUP_MOCK_FAILURE_MARKER="$backup_retention_case/failure.marker"
export BACKUP_MOCK_DB_PATH BACKUP_MOCK_DIR BACKUP_MOCK_MV_FAILURE BACKUP_MOCK_SOURCE_DRIFT BACKUP_MOCK_FAILURE_MARKER
set +e
(
	export ROADMAP_STATE_DIR="$backup_retention_state" ROADMAP_DATA_DIR="$backup_retention_state/data" \
		ROADMAP_DB_PATH="$BACKUP_MOCK_DB_PATH" ROADMAP_BACKUP_DIR="$backup_retention_dir" ROADMAP_BACKUP_RETENTION=1
	export -f backup_mock_id backup_mock_install backup_mock_chown backup_mock_stat \
		backup_mock_sqlite3 backup_mock_mv
	id() { backup_mock_id "$@"; }
	install() { backup_mock_install "$@"; }
	chown() { backup_mock_chown "$@"; }
	stat() { backup_mock_stat "$@"; }
	sqlite3() { backup_mock_sqlite3 "$@"; }
	mv() { backup_mock_mv "$@"; }
	export -f id install chown stat sqlite3 mv
	"$BACKUP" manual
) >"$backup_retention_case/output.log" 2>&1
backup_retention_status=$?
set -e
[[ "$backup_retention_status" -eq 0 ]] || fail 'backup retention fixture failed unexpectedly'
[[ -f "$backup_old" && -f "$backup_old.sha256" && -f "$backup_old.metadata" ]] \
	|| fail 'retention removed a valid historical backup without schema metadata'
[[ -f "$backup_incomplete" && -f "$backup_corrupt" && -f "$backup_corrupt.sha256" && -f "$backup_corrupt.metadata" ]] \
	|| fail 'retention removed or counted an invalid/incomplete backup set'

backup_drift_case="$fixture/backup-source-drift"
/usr/bin/install -d -m 0755 "$backup_drift_case"
backup_mock_setup "$backup_drift_case"
backup_drift_state="$backup_drift_case/state"
backup_drift_dir="$backup_drift_state/backups"
BACKUP_MOCK_DB_PATH="$backup_drift_state/data/roadmap.db"
BACKUP_MOCK_DIR=$backup_drift_dir
BACKUP_MOCK_MV_FAILURE=
BACKUP_MOCK_SOURCE_DRIFT=1
BACKUP_MOCK_FAILURE_MARKER="$backup_drift_case/failure.marker"
export BACKUP_MOCK_DB_PATH BACKUP_MOCK_DIR BACKUP_MOCK_MV_FAILURE BACKUP_MOCK_SOURCE_DRIFT BACKUP_MOCK_FAILURE_MARKER
set +e
(
	export ROADMAP_STATE_DIR="$backup_drift_state" ROADMAP_DATA_DIR="$backup_drift_state/data" \
		ROADMAP_DB_PATH="$BACKUP_MOCK_DB_PATH" ROADMAP_BACKUP_DIR="$backup_drift_dir" ROADMAP_BACKUP_RETENTION=2
	export -f backup_mock_id backup_mock_install backup_mock_chown backup_mock_stat \
		backup_mock_sqlite3 backup_mock_mv
	id() { backup_mock_id "$@"; }
	install() { backup_mock_install "$@"; }
	chown() { backup_mock_chown "$@"; }
	stat() { backup_mock_stat "$@"; }
	sqlite3() { backup_mock_sqlite3 "$@"; }
	mv() { backup_mock_mv "$@"; }
	export -f id install chown stat sqlite3 mv
	"$BACKUP" manual
) >"$backup_drift_case/output.log" 2>&1
backup_drift_status=$?
set -e
[[ "$backup_drift_status" -ne 0 ]] || fail 'backup accepted a source identity replacement race'
contains 'database path changed during backup' "$backup_drift_case/output.log"
if compgen -G "$backup_drift_dir/roadmap-*.db*" >/dev/null; then
	fail 'source identity drift left a published backup artifact'
fi
printf 'backup_publication_runtime_tests=ok\n'

python3 "$DEPLOY_DIR/test-helm-restore-drill.py"
printf 'restore_drill_runtime_tests=ok\n'

