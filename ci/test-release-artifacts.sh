#!/usr/bin/env bash
set -Eeuo pipefail

# End-to-end checks for release artifacts. The script downloads artifacts from
# a local HTTP server before inspecting or running them, so tests exercise the
# same files a release consumer receives rather than the build directory.
#
# Usage:
#   ci/test-release-artifacts.sh DIST [VERSION]
#
# DIST must contain archives and packages produced by ci/build-release.sh.
# Set RELEASE_TEST_DOWNLOAD_URL to an existing credential-free HTTP endpoint
# when a CI job has uploaded the artifact directory elsewhere; otherwise a
# temporary local Python HTTP server serves DIST.

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DIST_DIR=${1:-"$ROOT_DIR/dist/release"}
VERSION=${2:-${HELM_VERSION:-}}
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/helm-release-e2e.XXXXXX")
HTTP_SERVER_PID=
APP_PID=
SERVER_LOG="$TEST_ROOT/http.log"
DOWNLOAD_DIR="$TEST_ROOT/downloads"
INSTALL_ROOT="$TEST_ROOT/install"
RPM_DB_PATH="$TEST_ROOT/rpmdb"
mkdir -p "$DOWNLOAD_DIR" "$INSTALL_ROOT"
mkdir -p "$RPM_DB_PATH"

cleanup() {
	stop_app || true
	if [[ -n "$HTTP_SERVER_PID" ]]; then
		kill "$HTTP_SERVER_PID" 2>/dev/null || true
		wait "$HTTP_SERVER_PID" 2>/dev/null || true
	fi
	rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
	printf 'release E2E failure: %s\n' "$*" >&2
	exit 1
}

need_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command is missing: $1"
}

for command_name in curl tar readelf python3 sha256sum; do
	need_command "$command_name"
done

[[ -d "$DIST_DIR" ]] || fail "artifact directory does not exist: $DIST_DIR"
if [[ -z "$VERSION" && -f "$DIST_DIR/release-version" ]]; then
	VERSION=$(<"$DIST_DIR/release-version")
fi
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] ||
	fail "pass VERSION or write a semantic release-version file"

download_base=
if [[ -n "${RELEASE_TEST_DOWNLOAD_URL:-}" ]]; then
	download_base=${RELEASE_TEST_DOWNLOAD_URL%/}
else
	port_file="$TEST_ROOT/http-port"
	python3 - "$DIST_DIR" "$port_file" >"$SERVER_LOG" 2>&1 <<'PY' &
import http.server
import pathlib
import socketserver
import sys

root = pathlib.Path(sys.argv[1]).resolve()
port_file = pathlib.Path(sys.argv[2])

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(root), **kwargs)

    def log_message(self, fmt, *args):
        pass

with socketserver.TCPServer(("127.0.0.1", 0), Handler) as server:
    port_file.write_text(str(server.server_address[1]), encoding="ascii")
    server.serve_forever()
PY
	HTTP_SERVER_PID=$!
	for _ in $(seq 1 50); do
		[[ -s "$port_file" ]] && break
		sleep 0.1
	done
	[[ -s "$port_file" ]] || fail "local download server did not start"
	download_base="http://127.0.0.1:$(<"$port_file")"
fi

download() {
	local name=$1
	local destination="$DOWNLOAD_DIR/$name"
	curl --fail --silent --show-error --location --output "$destination" "$download_base/$name" ||
		fail "could not download $name"
	[[ -s "$destination" ]] || fail "downloaded artifact is empty: $name"
	printf '%s\n' "$destination"
}

runner_for_arch() {
	local arch=$1 host_arch
	host_arch=$(uname -m)
	case "$arch:$host_arch" in
		amd64:x86_64|amd64:amd64|arm64:aarch64|arm64:arm64)
			printf 'direct\n'
			return 0
			;;
		arm64:*)
			for candidate in qemu-aarch64-static qemu-aarch64; do
				if command -v "$candidate" >/dev/null 2>&1; then
					command -v "$candidate"
					return 0
				fi
			done
			;;
		amd64:*)
			for candidate in qemu-x86_64-static qemu-x86_64; do
				if command -v "$candidate" >/dev/null 2>&1; then
					command -v "$candidate"
					return 0
				fi
			done
			;;
	esac
	fail "no native or QEMU runner is available for $arch on $host_arch"
}

stop_app() {
	local status=0
	if [[ -n "${APP_PID:-}" ]]; then
		kill -TERM "$APP_PID" 2>/dev/null || true
		for _ in $(seq 1 50); do
			if ! kill -0 "$APP_PID" 2>/dev/null; then break; fi
			if [[ "$(ps -o stat= -p "$APP_PID" 2>/dev/null || true)" == Z* ]]; then break; fi
			sleep 0.1
		done
		if kill -0 "$APP_PID" 2>/dev/null; then
			kill -KILL "$APP_PID" 2>/dev/null || true
			status=1
		fi
		wait "$APP_PID" 2>/dev/null || true
		APP_PID=
	fi
	return "$status"
}

start_app() {
	local root=$1 arch=$2 state=$3 log=$4 port=$5 runner=$6
	local binary="$root/usr/bin/helm"
	APP_PID=
	mkdir -p "$state/codex-users"
	(
		cd "$state"
		export HELM_ADDR="127.0.0.1:$port"
		export HELM_DB="$state/roadmap.db"
		export HELM_AUTH_MODE=disabled
		export HELM_PUBLIC_ORIGIN="http://127.0.0.1:$port"
		export HELM_SECURE_COOKIES=false
		export HELM_DEMO_SEED=false
		export HELM_LUNA_ENABLED=false
		export HELM_CODEX_BINARY="$state/missing-companion"
		export HELM_CODEX_HOME_ROOT="$state/codex-users"
		if [[ "$runner" == direct ]]; then
			exec "$binary"
		else
			exec "$runner" "$binary"
		fi
	) >"$log" 2>&1 &
	APP_PID=$!
	for _ in $(seq 1 120); do
		if curl --fail --silent "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$APP_PID" 2>/dev/null; then
			cat "$log" >&2 || true
			fail "$arch binary exited before /healthz became available"
		fi
		sleep 0.1
	done
	cat "$log" >&2 || true
	fail "$arch binary did not start without the optional companion"
}

api_json() {
	local method=$1 url=$2 origin=$3 body=${4:-} idem=${5:-}
	local args=(--fail --silent --show-error --request "$method" "$url" -H "Origin: $origin" -H 'Accept: application/json')
	if [[ -n "$body" ]]; then
		args+=(-H 'Content-Type: application/json' --data-raw "$body")
	fi
	if [[ -n "$idem" ]]; then
		args+=(-H "Idempotency-Key: $idem")
	fi
	curl "${args[@]}"
}

sqlite_integrity_check() {
	local database=$1
	DB_PATH="$database" python3 - <<'PY'
import os
import sqlite3
import sys

database = os.environ["DB_PATH"]
with sqlite3.connect(database) as connection:
    integrity = [row[0] for row in connection.execute("PRAGMA integrity_check;")]
    foreign_keys = list(connection.execute("PRAGMA foreign_key_check;"))
if integrity != ["ok"] or foreign_keys:
    print(f"SQLite integrity={integrity!r} foreign_keys={foreign_keys!r}", file=sys.stderr)
    raise SystemExit(1)
PY
}

sqlite_backup() {
	local source=$1 destination=$2
	DB_SOURCE="$source" DB_BACKUP="$destination" python3 - <<'PY'
import os
import sqlite3

source = sqlite3.connect(os.environ["DB_SOURCE"])
backup = sqlite3.connect(os.environ["DB_BACKUP"])
try:
    source.backup(backup)
    backup.commit()
finally:
    backup.close()
    source.close()
PY
}

assert_api_state() {
	local port=$1 project_key=$2 project_id=$3 task_id=$4 task_title=$5
	local origin="http://127.0.0.1:$port"
	local project_response tasks_response projects_response project_count task_count
	project_response=$(api_json GET "http://127.0.0.1:$port/api/v1/projects/$project_key" "$origin") ||
		fail "could not read project $project_key after restart"
	EXPECTED_PROJECT_ID="$project_id" EXPECTED_PROJECT_KEY="$project_key" EXPECTED_PROJECT_NAME="Release artifact $project_key" \
		python3 -c 'import json, os, sys; p=json.load(sys.stdin); assert p["id"] == os.environ["EXPECTED_PROJECT_ID"]; assert p["key"] == os.environ["EXPECTED_PROJECT_KEY"]; assert p["name"] == os.environ["EXPECTED_PROJECT_NAME"]' <<<"$project_response" ||
		fail "project identity or name changed for $project_key"
	tasks_response=$(api_json GET "http://127.0.0.1:$port/api/v1/projects/$project_key/tasks?limit=200" "$origin") ||
		fail "could not read tasks for $project_key after restart"
	EXPECTED_TASK_ID="$task_id" EXPECTED_TASK_TITLE="$task_title" \
		python3 -c 'import json, os, sys; tasks=json.load(sys.stdin)["data"]; assert len(tasks) == 1; task=tasks[0]; assert task["id"] == os.environ["EXPECTED_TASK_ID"]; assert task["title"] == os.environ["EXPECTED_TASK_TITLE"]' <<<"$tasks_response" ||
		fail "task identity or title changed for $project_key"
	projects_response=$(api_json GET "http://127.0.0.1:$port/api/v1/projects?limit=200" "$origin") ||
		fail "could not list projects after restart"
	project_count=$(python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]))' <<<"$projects_response") ||
		fail "project collection response was invalid"
	[[ "$project_count" == 1 ]] || fail "project count changed to $project_count for $project_key"
	task_count=$(python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]))' <<<"$tasks_response") ||
		fail "task collection response was invalid"
	[[ "$task_count" == 1 ]] || fail "task count changed to $task_count for $project_key"
}

retained_binary_for_arch() {
	local arch=$1 candidate
	if [[ -n "${RELEASE_TEST_RETAINED_BINARY:-}" ]]; then
		printf '%s\n' "$RELEASE_TEST_RETAINED_BINARY"
		return 0
	fi
	if [[ -n "${RELEASE_TEST_RETAINED_DIR:-}" ]]; then
		for candidate in \
			"$RELEASE_TEST_RETAINED_DIR/$arch/usr/bin/helm" \
			"$RELEASE_TEST_RETAINED_DIR/linux-$arch/usr/bin/helm" \
			"$RELEASE_TEST_RETAINED_DIR/$arch/helm" \
			"$RELEASE_TEST_RETAINED_DIR/helm-$arch"; do
			if [[ -f "$candidate" ]]; then
				printf '%s\n' "$candidate"
				return 0
			fi
		done
	fi
	printf '\n'
}

archive_path() {
	local arch=$1
	download "helm-${VERSION}-linux-${arch}.tar.gz"
}

assert_elf_arch() {
	local binary=$1 expected=$2 machine
	machine=$(readelf -h "$binary" | awk -F: '$1 ~ /Machine/ {gsub(/^ +| +$/, "", $2); print $2}')
	case "$expected" in
		amd64)
			[[ "$machine" == "Advanced Micro Devices X86-64" ]] ||
				fail "$binary has ELF machine $machine, expected amd64"
			;;
		arm64)
			[[ "$machine" == "AArch64" ]] ||
				fail "$binary has ELF machine $machine, expected arm64"
			;;
		*) fail "unsupported test architecture: $expected" ;;
	esac
}

assert_tar_member() {
	local archive=$1 member=$2
	tar -tzf "$archive" | awk -v wanted="$member" '$0 == wanted { found=1 } END { exit found ? 0 : 1 }' ||
		fail "$archive is missing $member"
}

assert_service_contract() {
	local root=$1
	local service="$root/usr/lib/systemd/system/helm.service"
	[[ -f "$service" ]] || fail "package is missing helm.service"
	grep -Fqx 'User=helm' "$service" || fail 'helm.service must run as User=helm'
	grep -Fqx 'Group=helm' "$service" || fail 'helm.service must run as Group=helm'
	grep -Fqx 'ReadWritePaths=/var/lib/helm' "$service" || fail 'helm.service must grant only /var/lib/helm writes'
	grep -Fqx 'NoNewPrivileges=true' "$service" || fail 'helm.service must enable NoNewPrivileges'
	grep -Fqx 'ProtectSystem=strict' "$service" || fail 'helm.service must enable ProtectSystem=strict'
	grep -Fqx 'Environment=HELM_DB=/var/lib/helm/roadmap.db' "$service" || fail 'helm.service must use the native database path'
	! grep -Eq '(^|[[:space:]])rm([[:space:]]|$)' "$service" || fail 'helm.service contains destructive state removal'
}

install_archive() {
	local archive=$1 arch=$2
	local root="$INSTALL_ROOT/$arch"
	mkdir -p "$root"
	tar -xzf "$archive" -C "$root" --strip-components=1
	[[ -x "$root/usr/bin/helm" ]] || fail "$archive did not install usr/bin/helm"
	assert_elf_arch "$root/usr/bin/helm" "$arch"
	assert_service_contract "$root"
	[[ -d "$root/var/lib/helm" ]] || fail "$archive omitted /var/lib/helm"
	[[ -d "$root/var/lib/helm/codex-users" ]] || fail "$archive omitted codex user state directory"
	[[ ! -e "$root/usr/lib/helm/codex" ]] || fail 'base archive must omit the optional Codex helper'
	[[ ! -e "$root/usr/lib/helm/helm-beta-switchd" ]] || fail 'base archive must omit the optional beta helper'
	[[ ! -e "$root/usr/lib/systemd/system/helm-beta-switchd.service" ]] ||
		fail 'optional beta helper service must not be installed in the base archive'
	grep -Fqx 'HELM_LUNA_ENABLED=false' "$root/etc/helm/helm.env" ||
		fail 'base archive must disable Luna by default'
}

run_server_persistence() {
	local root=$1 arch=$2 port=$((18080 + (RANDOM % 1000)))
	local state="$TEST_ROOT/state-$arch"
	local log="$TEST_ROOT/helm-$arch.log"
	local db="$state/roadmap.db" backup="$state/roadmap.db.backup"
	local retained="$root/usr/bin/helm.retained" candidate="$root/usr/bin/helm.candidate" release_binary="$root/usr/bin/helm.release"
	local runner project_key project_name task_title project_response task_response
	local project_id task_id backup_sha retained_source
	runner=$(runner_for_arch "$arch")
	retained_source=$(retained_binary_for_arch "$arch")
	cp --preserve=mode,timestamps "$root/usr/bin/helm" "$release_binary"
	if [[ -n "$retained_source" ]]; then
		[[ -x "$retained_source" ]] || fail "retained $arch binary is not executable: $retained_source"
		assert_elf_arch "$retained_source" "$arch"
		cp --preserve=mode,timestamps "$retained_source" "$retained"
		cp --preserve=mode,timestamps "$retained_source" "$root/usr/bin/helm"
	else
		cp --preserve=mode,timestamps "$root/usr/bin/helm" "$retained"
	fi
	project_key="REL${arch^^}$(date +%s%N | tail -c 8)"
	project_key=${project_key:0:16}
	project_name="Release artifact $project_key"
	task_title="Persisted task $project_key"
	start_app "$root" "$arch" "$state" "$log" "$port" "$runner"
	local origin="http://127.0.0.1:$port"
	if ! curl --fail --silent "http://127.0.0.1:$port/readyz" >/dev/null; then
		cat "$log" >&2 || true
		fail "$arch binary did not become ready without the optional companion"
	fi
	if [[ -z "$retained_source" ]]; then
		local ui
		ui=$(curl --fail --silent "http://127.0.0.1:$port/") || fail "$arch binary did not serve the embedded UI"
		grep -Fq '<div id="app">' <<<"$ui" || fail "$arch binary served the fallback UI instead of the production bundle"
	fi
	project_response=$(api_json POST "http://127.0.0.1:$port/api/v1/projects" "$origin" \
		"{\"key\":\"$project_key\",\"name\":\"$project_name\"}" "release-project-$arch-$project_key") ||
		fail "$arch binary could not create a project through its downloaded API"
	project_id=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$project_response") ||
		fail "$arch project response did not contain an id"
	task_response=$(api_json POST "http://127.0.0.1:$port/api/v1/projects/$project_key/tasks" "$origin" \
		"{\"title\":\"$task_title\"}" "release-task-$arch-$project_key") ||
		fail "$arch binary could not create a task through its downloaded API"
	task_id=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$task_response") ||
		fail "$arch task response did not contain an id"
	assert_api_state "$port" "$project_key" "$project_id" "$task_id" "$task_title"
	stop_app || fail "$arch binary did not stop cleanly before backup"
	[[ -s "$db" ]] || fail "$arch binary did not create a populated SQLite database"
	sqlite_integrity_check "$db" || fail "$arch live SQLite database failed integrity_check or foreign_key_check"
	# SQLite is configured for WAL. The online backup API folds any sidecar
	# frames into one standalone database before the executable is replaced.
	sqlite_backup "$db" "$backup" || fail "$arch SQLite backup command failed"
	[[ -s "$backup" ]] || fail "$arch SQLite backup is empty"
	sqlite_integrity_check "$backup" || fail "$arch SQLite backup failed integrity_check or foreign_key_check"
	backup_sha=$(sha256sum "$backup" | awk '{print $1}')
	[[ "$backup_sha" =~ ^[0-9a-f]{64}$ ]] || fail "$arch SQLite backup checksum is invalid"

	# Replace the retained executable with the downloaded release payload to
	# model a package upgrade with the populated state directory untouched.
	cp --preserve=mode,timestamps "$release_binary" "$candidate"
	mv -f -- "$candidate" "$root/usr/bin/helm"
	start_app "$root" "$arch" "$state" "$log.upgrade" "$port" "$runner"
	assert_api_state "$port" "$project_key" "$project_id" "$task_id" "$task_title"
	local ui
	ui=$(curl --fail --silent "http://127.0.0.1:$port/") || fail "$arch upgraded binary did not serve the embedded UI"
	grep -Fq '<div id="app">' <<<"$ui" || fail "$arch upgraded binary served the fallback UI instead of the production bundle"
	stop_app || fail "$arch upgraded binary did not stop cleanly"
	[[ "$(sha256sum "$backup" | awk '{print $1}')" == "$backup_sha" ]] || fail "$arch upgrade changed the verified SQLite backup"
	sqlite_integrity_check "$db" || fail "$arch upgraded SQLite database failed integrity_check or foreign_key_check"

	# Roll back the immutable payload to the retained executable. This is a
	# binary rollback: the populated database and its verified backup stay put.
	cp --preserve=mode,timestamps "$retained" "$root/usr/bin/helm"
	start_app "$root" "$arch" "$state" "$log.rollback" "$port" "$runner"
	assert_api_state "$port" "$project_key" "$project_id" "$task_id" "$task_title"
	stop_app || fail "$arch retained binary did not stop cleanly"
	[[ "$(sha256sum "$backup" | awk '{print $1}')" == "$backup_sha" ]] || fail "$arch rollback changed the verified SQLite backup"
	sqlite_integrity_check "$db" || fail "$arch rolled-back SQLite database failed integrity_check or foreign_key_check"
	printf 'release_arch=%s project_id=%s task_id=%s backup_sha=%s upgrade=passed rollback=passed\n' \
		"$arch" "$project_id" "$task_id" "$backup_sha"
}

amd64_archive=$(archive_path amd64)
arm64_archive=$(archive_path arm64)
assert_tar_member "$amd64_archive" "helm-${VERSION}-linux-amd64/usr/bin/helm"
assert_tar_member "$arm64_archive" "helm-${VERSION}-linux-arm64/usr/bin/helm"

install_archive "$amd64_archive" amd64
install_archive "$arm64_archive" arm64
TEST_ARCHES=${RELEASE_TEST_ARCHES:-${RELEASE_TEST_ARCH:-amd64 arm64}}
for arch in $TEST_ARCHES; do
	case "$arch" in
		amd64|arm64) ;;
		*) fail "unsupported RELEASE_TEST_ARCHES value: $arch" ;;
	esac
	run_server_persistence "$INSTALL_ROOT/$arch" "$arch"
done

for package in \
	"helm_${VERSION}_amd64.deb" \
	"helm_${VERSION}_arm64.deb" \
	"helm-${VERSION}-1.x86_64.rpm" \
	"helm-${VERSION}-1.aarch64.rpm"; do
	if [[ ! -e "$DIST_DIR/$package" ]]; then
		[[ "${RELEASE_TEST_ALLOW_PARTIAL:-0}" == 1 ]] || fail "release output is missing $package"
		continue
	fi
	download "$package" >/dev/null
done

if [[ -f "$DOWNLOAD_DIR/helm_${VERSION}_amd64.deb" ]]; then
	command -v dpkg-deb >/dev/null 2>&1 || fail 'dpkg-deb is required to inspect Debian artifacts'
	deb="$DOWNLOAD_DIR/helm_${VERSION}_amd64.deb"
	[[ "$(dpkg-deb -f "$deb" Architecture)" == amd64 ]] ||
		fail 'amd64 Debian package declares the wrong architecture'
	dpkg-deb -c "$deb" | grep -F './usr/bin/helm' >/dev/null || fail 'amd64 Debian package omitted usr/bin/helm'
	dpkg-deb -c "$deb" | grep -F './usr/lib/systemd/system/helm.service' >/dev/null || fail 'amd64 Debian package omitted helm.service'
	dpkg-deb -c "$deb" | grep -F './var/lib/helm/' >/dev/null ||
		fail 'amd64 Debian package omitted the persistent native state path'
	! dpkg-deb -c "$deb" | grep -F './usr/lib/helm/codex' >/dev/null ||
		fail 'amd64 Debian package must omit the optional Codex helper by default'
	dpkg-deb --fsys-tarfile "$deb" | tar -xO ./etc/helm/helm.env | grep -F 'HELM_LUNA_ENABLED=false' >/dev/null ||
		fail 'amd64 Debian package must disable Luna by default'
	! dpkg-deb --ctrl-tarfile "$deb" | tar -xO ./postinst 2>/dev/null | grep -Eq 'rm[[:space:]]+(-rf[[:space:]]+)?(/var/lib/helm|/etc/helm)' ||
		fail 'amd64 Debian postinst contains destructive state removal'
	! dpkg-deb --ctrl-tarfile "$deb" | tar -xO ./postrm 2>/dev/null | grep -Eq 'rm[[:space:]]+(-rf[[:space:]]+)?(/var/lib/helm|/etc/helm)' ||
		fail 'amd64 Debian postrm contains destructive state removal'
	fi
if [[ -f "$DOWNLOAD_DIR/helm_${VERSION}_arm64.deb" ]]; then
	deb="$DOWNLOAD_DIR/helm_${VERSION}_arm64.deb"
	command -v dpkg-deb >/dev/null 2>&1 || fail 'dpkg-deb is required to inspect Debian artifacts'
	[[ "$(dpkg-deb -f "$deb" Architecture)" == arm64 ]] ||
		fail 'arm64 Debian package declares the wrong architecture'
	dpkg-deb -c "$deb" | grep -F './usr/bin/helm' >/dev/null || fail 'arm64 Debian package omitted usr/bin/helm'
	! dpkg-deb -c "$deb" | grep -F './usr/lib/helm/codex' >/dev/null ||
		fail 'arm64 Debian package must omit the optional Codex helper by default'
fi
if [[ -f "$DOWNLOAD_DIR/helm-${VERSION}-1.x86_64.rpm" ]]; then
	command -v rpm >/dev/null 2>&1 || fail 'rpm is required to inspect Fedora artifacts'
	rpm_package="$DOWNLOAD_DIR/helm-${VERSION}-1.x86_64.rpm"
	[[ "$(rpm --dbpath "$RPM_DB_PATH" -qp --qf '%{ARCH}' "$rpm_package")" == x86_64 ]] ||
		fail 'x86_64 RPM declares the wrong architecture'
	rpm --dbpath "$RPM_DB_PATH" -qp --list "$rpm_package" | grep -Fx '/usr/bin/helm' >/dev/null || fail 'x86_64 RPM omitted usr/bin/helm'
	rpm --dbpath "$RPM_DB_PATH" -qp --list "$rpm_package" | grep -Fx '/usr/lib/systemd/system/helm.service' >/dev/null || fail 'x86_64 RPM omitted helm.service'
	rpm --dbpath "$RPM_DB_PATH" -qp --list "$rpm_package" | grep -Fx '/var/lib/helm' >/dev/null ||
		fail 'x86_64 RPM omitted the persistent native state path'
	! rpm --dbpath "$RPM_DB_PATH" -qp --list "$rpm_package" | grep -Fx '/usr/lib/helm/codex' >/dev/null ||
		fail 'x86_64 RPM must omit the optional Codex helper by default'
	! rpm --dbpath "$RPM_DB_PATH" -qp --scripts "$rpm_package" | grep -Eq 'rm[[:space:]]+(-rf[[:space:]]+)?(/var/lib/helm|/etc/helm)' ||
		fail 'x86_64 RPM scriptlet contains destructive state removal'
fi
if [[ -f "$DOWNLOAD_DIR/helm-${VERSION}-1.aarch64.rpm" ]]; then
	command -v rpm >/dev/null 2>&1 || fail 'rpm is required to inspect Fedora artifacts'
	rpm_package="$DOWNLOAD_DIR/helm-${VERSION}-1.aarch64.rpm"
	[[ "$(rpm --dbpath "$RPM_DB_PATH" -qp --qf '%{ARCH}' "$rpm_package")" == aarch64 ]] ||
		fail 'aarch64 RPM declares the wrong architecture'
	rpm --dbpath "$RPM_DB_PATH" -qp --list "$rpm_package" | grep -Fx '/usr/bin/helm' >/dev/null || fail 'aarch64 RPM omitted usr/bin/helm'
	! rpm --dbpath "$RPM_DB_PATH" -qp --list "$rpm_package" | grep -Fx '/usr/lib/helm/codex' >/dev/null ||
		fail 'aarch64 RPM must omit the optional Codex helper by default'
fi

printf 'release_artifacts_e2e=passed\n'
