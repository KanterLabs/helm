#!/usr/bin/env bash
# Exercise the real builder's origin gate without downloading or signing a release.
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d "${TMPDIR:-/tmp}/helm-bundle-origin.XXXXXX")
trap 'rm -rf -- "$fixture"' EXIT
chmod 0700 "$fixture"
install -d "$fixture/deploy" "$fixture/dist" "$fixture/bin"
install -m 0755 "$ROOT_DIR/deploy/build-bundle.sh" "$fixture/deploy/build-bundle.sh"
install -m 0644 "$ROOT_DIR/deploy/domain-profile.sh" "$fixture/deploy/domain-profile.sh"
install -m 0755 /bin/true "$fixture/dist/helm"
install -m 0755 /bin/true "$fixture/dist/codex"
openssl genpkey -algorithm Ed25519 -out "$fixture/signing.pem" >/dev/null 2>&1
chmod 0600 "$fixture/signing.pem"
printf 'fixture-not-a-real-token\n' > "$fixture/dist/cloudflared.token"
printf '#!/usr/bin/env bash\nprintf "network-attempt\\n" >&2\nexit 91\n' > "$fixture/bin/curl"
chmod 0755 "$fixture/bin/curl"

run_case() {
	local label=$1 environment=$2 expected=$3 contents=$4
	printf '%s\n' "$contents" > "$fixture/dist/owner.env"
	if env -u HELM_PUBLIC_ORIGIN -u ROADMAP_PUBLIC_ORIGIN \
		-u HELM_PUBLIC_HOST -u ROADMAP_PUBLIC_HOST -u HELM_PUBLIC_URL -u ROADMAP_PUBLIC_URL \
		-u PUBLIC_HOST -u PUBLIC_URL \
		-u ROADMAP_RELEASE_SIGNING_KEY_FILE -u ROADMAP_CLOUDFLARED_TOKEN_FILE -u ROADMAP_OWNER_ENV_FILE \
		PATH="$fixture/bin:$PATH" HELM_DEPLOY_ENVIRONMENT="$environment" \
		HELM_RELEASE_SIGNING_KEY_FILE="$fixture/signing.pem" \
		HELM_CLOUDFLARED_TOKEN_FILE="$fixture/dist/cloudflared.token" \
		HELM_OWNER_ENV_FILE="$fixture/dist/owner.env" \
		bash "$fixture/deploy/build-bundle.sh" 1111111111111111111111111111111111111111 \
		> "$fixture/output" 2>&1; then
		printf 'builder unexpectedly succeeded: %s\n' "$label" >&2
		exit 1
	fi
	if [[ "$expected" = rejected ]]; then
		if grep -q 'network-attempt' "$fixture/output" || ! grep -q 'domain profile:' "$fixture/output"; then
			printf 'origin was not rejected before network: %s\n' "$label" >&2
			exit 1
		fi
	else
		if ! grep -q 'network-attempt' "$fixture/output"; then
			printf 'valid origin failed before the download boundary: %s\n' "$label" >&2
			exit 1
		fi
	fi
}

run_case missing production rejected 'HELM_AUTH_MODE=cloudflare'
run_case wrong-host production rejected 'HELM_PUBLIC_ORIGIN=https://invalid.example'
run_case beta-in-production production rejected 'HELM_PUBLIC_ORIGIN=https://beta.shanekanterman.dev'
run_case duplicate production rejected $'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev\nHELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev'
run_case whitespace-override production rejected $'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev\n HELM_PUBLIC_ORIGIN=https://invalid.example'
run_case production production download $'HELM_PUBLIC_ORIGIN=https://tc.shanekanterman.dev\nROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev'
run_case legacy-production production download 'ROADMAP_PUBLIC_ORIGIN=https://tc.shanekanterman.dev'
run_case beta beta download 'HELM_PUBLIC_ORIGIN=https://beta.shanekanterman.dev'
printf 'bundle_origin_gate_tests=ok\n'
