#!/usr/bin/env bash
# Verify and complete the toolchain of a generic ActionRunners job for the
# Gitea shadow workflow. Each job runs as root in a disposable node:24-bookworm
# container with no Docker daemon and no persistent cache, so it installs what
# it needs and stops with a named cause when a version is wrong.
# Failure contract: docs/GITEA_CI_SHADOW_FAILURES.md.
set -Eeuo pipefail

readonly NODE_VERSION=v22.23.2
# Standalone Compose renders and validates compose.yaml without a Docker CLI
# or daemon. Checksums are from the release's checksums.txt.
readonly COMPOSE_VERSION=v5.6.1
readonly COMPOSE_SHA256_X86_64=47737f4236e6d3ccea7291995203f8f018058783b4c3a9469119811a0b5db8c0
readonly COMPOSE_SHA256_AARCH64=42cfa75f51ad0ccf290bad37899e0038a8c10a0069e85233ecc1d53e3ff1347d

fail() { printf '[gitea-runner] %s\n' "$*" >&2; exit 1; }

job=${1:-}
[[ "$job" = checks || "$job" = browser ]] || fail 'usage: prepare-runner.sh checks|browser'
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

# Playwright's --with-deps uses apt-get; there is no sudo on the runner.
[[ "$(id -u)" = 0 ]] || fail "the shadow jobs expect a root job container; running as uid $(id -u)"

want_go=$(awk '/^toolchain / {print $2}' go.mod)
[[ "$want_go" =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || fail 'go.mod has no toolchain line'
command -v go >/dev/null || fail 'go is not on PATH; setup-go did not run'
[[ "${GOTOOLCHAIN:-}" = local ]] || fail 'GOTOOLCHAIN must be local so Go cannot switch toolchains silently'
have_go=$(go env GOVERSION)
[[ "$have_go" = "$want_go" ]] || fail "Go is $have_go; go.mod requires $want_go"

have_node=$(node --version)
[[ "$have_node" = "$NODE_VERSION" ]] || fail "Node is $have_node on PATH; the jobs require $NODE_VERSION"

python3 -c 'import sys; sys.exit(sys.version_info < (3, 11))' \
	|| fail "Python $(python3 --version 2>&1) is older than 3.11"
command -v git >/dev/null || fail 'git is not installed'
printf 'go=%s node=%s python=%s uid=0\n' "$have_go" "$have_node" "$(python3 -c 'import platform; print(platform.python_version())')"

install_compose() {
	local arch sum target=/usr/local/bin/docker-compose download
	arch=$(uname -m)
	case "$arch" in
		x86_64) sum=$COMPOSE_SHA256_X86_64 ;;
		aarch64) sum=$COMPOSE_SHA256_AARCH64 ;;
		*) fail "no pinned Compose checksum for $arch" ;;
	esac
	download=$(mktemp "${RUNNER_TEMP:-/tmp}/docker-compose.XXXXXX")
	curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
		--retry 3 --output "$download" \
		"https://github.com/docker/compose/releases/download/$COMPOSE_VERSION/docker-compose-linux-$arch"
	printf '%s  %s\n' "$sum" "$download" | sha256sum --check --status \
		|| { rm -f -- "$download"; fail "Compose $COMPOSE_VERSION does not match its pinned SHA-256"; }
	install -m 0755 "$download" "$target"
	rm -f -- "$download"
	docker-compose version
}

install_chromium() {
	cd web
	npm ci
	npx playwright install --with-deps chromium
	node --input-type=module -e '
		import {chromium} from "@playwright/test";
		const browser = await chromium.launch();
		console.log(`Chromium ${browser.version()} launch passed`);
		await browser.close();
	' || fail 'Chromium was installed but cannot launch'
}

case "$job" in
	checks) install_compose ;;
	browser) install_chromium ;;
esac
