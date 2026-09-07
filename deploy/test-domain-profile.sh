#!/usr/bin/env bash
# Focused, network-free tests for the fixed deployment domain contract.
set -Eeuo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
PROFILE="$ROOT_DIR/deploy/domain-profile.sh"

fail() {
	printf '[domain-profile] %s\n' "$*" >&2
	exit 1
}

assert_eq() {
	local expected=$1 actual=$2 message='values differ'
	[[ $# -ge 3 ]] && message=$3
	[[ "$actual" = "$expected" ]] || fail "$message: expected <$expected>, got <$actual>"
}

assert_fails() {
	local output status
	set +e
	output=$("$@" 2>&1)
	status=$?
	set -e
	[[ "$status" -ne 0 ]] || fail "unexpected success: $*"
	printf '%s' "$output"
}

tmp_root=$(printenv TMPDIR 2>/dev/null || true)
[[ -n "$tmp_root" ]] || tmp_root=/tmp
fixture=$(mktemp -d "$tmp_root/helm-domain-profile.XXXXXX")
cleanup_fixture() { rm -rf -- "$fixture"; }
trap cleanup_fixture EXIT

[[ -f "$PROFILE" && ! -L "$PROFILE" ]] || fail 'profile is missing or symlinked'
bash -n "$PROFILE" || fail 'profile has shell syntax errors'

unset HELM_DEPLOY_ENVIRONMENT HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN \
	HELM_PUBLIC_HOST ROADMAP_PUBLIC_HOST HELM_PUBLIC_URL ROADMAP_PUBLIC_URL \
	PUBLIC_HOST PUBLIC_URL
assert_eq 'https://tc.shanekanterman.dev' "$(bash "$PROFILE" production public-url)" \
	'production public URL'
assert_eq 'tc.shanekanterman.dev' "$(bash "$PROFILE" production public-host)" \
	'production public host'
assert_eq 'roadmap-homelab' "$(bash "$PROFILE" production tunnel-name)" \
	'production tunnel name'
assert_eq 'https://beta.shanekanterman.dev' "$(bash "$PROFILE" beta public-url)" \
	'beta public URL'
assert_eq 'helm-beta-homelab' "$(bash "$PROFILE" beta tunnel-name)" \
	'beta tunnel name'
assert_eq '' "$(bash "$PROFILE" beta legacy-owner-policy-name)" \
	'beta must not expose production legacy policy names'

assert_eq 'https://tc.shanekanterman.dev' "$(
	HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev \
	ROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev \
	bash "$PROFILE" production public-url
)" 'equal Helm/Roadmap origin aliases'
assert_fails env HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev \
	ROADMAP_PUBLIC_ORIGIN=https://evil.example bash "$PROFILE" production public-url \
		>"$fixture/conflicting-aliases.out"
assert_fails env HELM_PUBLIC_ORIGIN=https://evil.example \
	bash "$PROFILE" production public-url >"$fixture/wrong-origin.out"
assert_fails env HELM_PUBLIC_HOST=evil.example \
	bash "$PROFILE" production public-url >"$fixture/wrong-host.out"
assert_fails env HELM_PUBLIC_HOST=tc.shanekanterman.dev \
	ROADMAP_PUBLIC_HOST=evil.example bash "$PROFILE" production public-url \
		>"$fixture/conflicting-host-aliases.out"
assert_eq 'https://tc.shanekanterman.dev' "$(
	HELM_PUBLIC_HOST=tc.shanekanterman.dev \
	ROADMAP_PUBLIC_HOST=tc.shanekanterman.dev \
	bash "$PROFILE" production public-url
)" 'equal Helm/Roadmap host aliases'
assert_fails env HELM_DEPLOY_ENVIRONMENT=beta \
	bash "$PROFILE" production public-url >"$fixture/environment-conflict.out"
assert_fails env HELM_DEPLOY_ENVIRONMENT=staging \
	bash "$PROFILE" staging public-url >"$fixture/unknown-environment.out"
assert_fails env HELM_PUBLIC_URL=https://evil.example \
	bash "$PROFILE" production public-url >"$fixture/wrong-url-override.out"

# Invalid profile inputs must not reach a network client, even if one is
# placed first in PATH.  The marker is written only if the fake curl runs.
fake_bin="$fixture/bin"
mkdir -m 0700 -- "$fake_bin"
cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
printf 'curl-called\n' > "$DOMAIN_PROFILE_CURL_MARKER"
exit 99
EOF
chmod 0700 "$fake_bin/curl"
DOMAIN_PROFILE_CURL_MARKER="$fixture/curl-called" \
	PATH="$fake_bin:/usr/bin:/bin" \
	HELM_PUBLIC_ORIGIN=https://evil.example \
	bash "$PROFILE" production public-url >"$fixture/no-network.out" 2>&1 || true
[[ ! -e "$fixture/curl-called" ]] || fail 'invalid profile input invoked curl'

source "$PROFILE"
unset HELM_DEPLOY_ENVIRONMENT HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN \
	HELM_PUBLIC_HOST ROADMAP_PUBLIC_HOST HELM_PUBLIC_URL ROADMAP_PUBLIC_URL \
	PUBLIC_HOST PUBLIC_URL
domain_profile_load production
assert_eq 'tc.shanekanterman.dev' "$PUBLIC_HOST" 'sourceable production host'
domain_profile_load beta
assert_eq 'beta.shanekanterman.dev' "$PUBLIC_HOST" 'sourceable beta host'
assert_eq '' "$LEGACY_OWNER_POLICY_NAME" 'sourceable beta legacy policy isolation'

origin_file="$fixture/owner.env"
printf '%s\n' \
	'HELM_ADDR=127.0.0.1:8080' \
	'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' \
	'ROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' \
	'HELM_ADMIN_EMAIL=owner@example.com' >"$origin_file"
assert_eq '' "$(env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$origin_file")" \
	'valid origin file'

printf '%s\n' 'ROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' >"$fixture/legacy-only.env"
assert_eq '' "$(env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/legacy-only.env")" \
	'legacy-only origin file'

printf '%s\n' \
	'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' \
	'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' >"$fixture/duplicate.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/duplicate.env" \
	>"$fixture/duplicate.out"

printf '%s\n' \
	'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' \
	' ROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' >"$fixture/whitespace-duplicate.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/whitespace-duplicate.env" \
	>"$fixture/whitespace-duplicate.out"

printf '%s\n' 'HELM_PUBLIC_ORIGIN =https://tc.shanekanterman.dev' >"$fixture/space-before-equals.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/space-before-equals.env" \
	>"$fixture/space-before-equals.out"

printf '%s\n' 'HELM_PUBLIC_ORIGIN="https://tc.shanekanterman.dev"' >"$fixture/quoted.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/quoted.env" \
	>"$fixture/quoted.out"

printf '%s\n' 'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev\\' \
	'ROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev' >"$fixture/continued.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/continued.env" \
	>"$fixture/continued.out"

printf '%s\n' 'HELM_PUBLIC_ORIGIN=' >"$fixture/empty.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/empty.env" \
	>"$fixture/empty.out"

printf '%s\n' 'HELM_ADMIN_EMAIL=$(touch '"$fixture"'/executed)' >"$fixture/no-exec.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/no-exec.env" \
	>"$fixture/no-exec.out"
[[ ! -e "$fixture/executed" ]] || fail 'origin file parser executed shell content'

printf '%s\n' 'HELM_PUBLIC_ORIGIN=https://evil.example' >"$fixture/wrong-file-origin.env"
assert_fails env -u HELM_DEPLOY_ENVIRONMENT -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
	bash "$PROFILE" production validate-origin-file "$fixture/wrong-file-origin.env" \
	>"$fixture/wrong-file-origin.out"

printf 'domain_profile_tests=ok\n'
