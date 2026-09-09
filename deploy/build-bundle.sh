#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist"
SHA=${1:-}

resolve_compat_var() {
	local canonical=$1 legacy=$2 default_value=${3:-} canonical_value legacy_value
	canonical_value=${!canonical:-}
	legacy_value=${!legacy:-}
	if [[ -n "$canonical_value" && -n "$legacy_value" && "$canonical_value" != "$legacy_value" ]]; then
		printf '%s and %s must match when both are set\n' "$canonical" "$legacy" >&2
		exit 1
	fi
	printf '%s' "${canonical_value:-${legacy_value:-$default_value}}"
}

SIGNING_KEY_FILE=$(resolve_compat_var HELM_RELEASE_SIGNING_KEY_FILE ROADMAP_RELEASE_SIGNING_KEY_FILE)
OWNER_ENV_FILE=$(resolve_compat_var HELM_OWNER_ENV_FILE ROADMAP_OWNER_ENV_FILE "$DIST_DIR/owner.env")

DEPLOY_ENVIRONMENT=${HELM_DEPLOY_ENVIRONMENT:-production}
case "$DEPLOY_ENVIRONMENT" in
	production) PRIVATE_TAILNET_BETA=0 ;;
	beta) PRIVATE_TAILNET_BETA=1 ;;
	*)
		printf 'HELM_DEPLOY_ENVIRONMENT must be production or beta\n' >&2
		exit 1
		;;
esac

if (( PRIVATE_TAILNET_BETA == 0 )); then
	CLOUDFLARED_TOKEN_FILE=$(resolve_compat_var HELM_CLOUDFLARED_TOKEN_FILE ROADMAP_CLOUDFLARED_TOKEN_FILE "$DIST_DIR/cloudflared.token")
fi

COMMON_PAYLOAD_MEMBERS=(
	codex
	codex.sha256
	compose.yaml
	install-inside-lxc.sh
	nftables.conf
	roadmap
	roadmap-backup.service
	roadmap-backup.sh
	roadmap-backup.timer
	roadmap.env
	roadmap-restore.sh
	roadmap-rollback.sh
	roadmap.service
	roadmap.sha256
	release.sha
)
ENVELOPE_MEMBERS=(release.manifest release.manifest.sig)
COMMON_BUNDLE_MEMBERS=(
	codex
	codex.sha256
	compose.yaml
	install-inside-lxc.sh
	nftables.conf
	roadmap
	roadmap-backup.service
	roadmap-backup.sh
	roadmap-backup.timer
	roadmap.env
	roadmap-restore.sh
	roadmap-rollback.sh
	roadmap.service
	roadmap.sha256
	release.manifest
	release.manifest.sig
	release.sha
)
if (( PRIVATE_TAILNET_BETA == 1 )); then
	PAYLOAD_MEMBERS=("${COMMON_PAYLOAD_MEMBERS[@]}" validate-beta-private.sh)
	BUNDLE_MEMBERS=("${COMMON_BUNDLE_MEMBERS[@]}" validate-beta-private.sh)
else
	PAYLOAD_MEMBERS=(cloudflared cloudflared.service cloudflared.token "${COMMON_PAYLOAD_MEMBERS[@]}")
	BUNDLE_MEMBERS=(cloudflared cloudflared.service cloudflared.token "${COMMON_BUNDLE_MEMBERS[@]}")
fi

if [[ ! "$SHA" =~ ^[0-9a-f]{40}$ ]]; then
	printf 'usage: %s <40-character git sha>\n' "$0" >&2
	exit 64
fi

safe_file() {
	local path=$1 label=$2 mode
	[[ -f "$path" && ! -L "$path" ]] || {
		printf '%s is missing or not a regular file: %s\n' "$label" "$path" >&2
		exit 1
	}
	mode=$(stat -c '%a' -- "$path")
	[[ "$mode" =~ ^[0-7]+$ ]] || {
		printf 'could not inspect permissions for %s\n' "$label" >&2
		exit 1
	}
}

safe_file "$DIST_DIR/helm" helm-binary
[[ -x "$DIST_DIR/helm" ]] || {
	printf 'helm binary must be executable\n' >&2
	exit 1
}
safe_file "$DIST_DIR/codex" codex-binary
[[ -x "$DIST_DIR/codex" ]] || {
	printf 'codex binary must be executable\n' >&2
	exit 1
}
[[ -n "$SIGNING_KEY_FILE" ]] || {
	printf 'HELM_RELEASE_SIGNING_KEY_FILE is required to sign a release\n' >&2
	exit 1
}
safe_file "$SIGNING_KEY_FILE" release-signing-private-key
signing_key_mode=$(stat -c '%a' -- "$SIGNING_KEY_FILE")
(( (8#$signing_key_mode & 077) == 0 )) || {
	printf 'release-signing private key must not be group/world accessible\n' >&2
	exit 1
}
command -v openssl >/dev/null 2>&1 || {
	printf 'openssl is required to sign a release\n' >&2
	exit 1
}
key_description=$(openssl pkey -in "$SIGNING_KEY_FILE" -text -noout 2>/dev/null | sed -n '1p') || {
	printf 'release-signing private key is invalid\n' >&2
	exit 1
}
[[ "$key_description" = ED25519\ Private-Key:* ]] || {
	printf 'release-signing private key must be Ed25519\n' >&2
	exit 1
}
safe_file "$OWNER_ENV_FILE" owner-environment
chmod 0600 "$OWNER_ENV_FILE"

single_owner_value() {
	local key=$1
	awk -F= -v key="$key" '
		$1 == key { count++; value = substr($0, index($0, "=") + 1) }
		END {
			if (count != 1) exit 1
			print value
		}
	' "$OWNER_ENV_FILE"
}

valid_ipv4() {
	local value=$1 octet
	local -a octets
	IFS=. read -r -a octets <<<"$value"
	[[ ${#octets[@]} -eq 4 ]] || return 1
	for octet in "${octets[@]}"; do
		[[ "$octet" =~ ^[0-9]{1,3}$ ]] || return 1
		(( 10#$octet <= 255 )) || return 1
		[[ "$octet" != 0* || "$octet" = 0 ]] || return 1
	done
}

validate_private_owner_env() {
	local key value
	local -a peers
	for key in \
		HELM_AUTH_MODE ROADMAP_AUTH_MODE \
		HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN \
		HELM_TAILNET_AUDIENCE ROADMAP_TAILNET_AUDIENCE \
		HELM_TAILNET_ASSERTION_KEY_FILE ROADMAP_TAILNET_ASSERTION_KEY_FILE \
		HELM_TAILNET_TLS_ADDR ROADMAP_TAILNET_TLS_ADDR \
		HELM_TAILNET_TLS_CERT_FILE ROADMAP_TAILNET_TLS_CERT_FILE \
		HELM_TAILNET_TLS_KEY_FILE ROADMAP_TAILNET_TLS_KEY_FILE \
		HELM_TAILNET_ALLOWED_PEER_IPS ROADMAP_TAILNET_ALLOWED_PEER_IPS \
		HELM_ADMIN_EMAIL ROADMAP_ADMIN_EMAIL \
		HELM_TAILNET_OWNER_LOGIN ROADMAP_TAILNET_OWNER_LOGIN; do
		value=$(single_owner_value "$key") || {
			printf 'private beta owner environment is missing or duplicates %s\n' "$key" >&2
			exit 1
		}
		[[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || {
			printf 'private beta owner environment contains a control character\n' >&2
			exit 1
		}
	done
	[[ "$(single_owner_value HELM_AUTH_MODE)" = tailnet && "$(single_owner_value ROADMAP_AUTH_MODE)" = tailnet ]] || {
		printf 'private beta owner environment must select tailnet authentication\n' >&2
		exit 1
	}
	for key in HELM_PUBLIC_ORIGIN ROADMAP_PUBLIC_ORIGIN HELM_TAILNET_AUDIENCE ROADMAP_TAILNET_AUDIENCE; do
		[[ "$(single_owner_value "$key")" = https://beta-helm.home.shanekanterman.dev ]] || {
			printf 'private beta owner environment has an unexpected private origin\n' >&2
			exit 1
		}
	done
	[[ "$(single_owner_value HELM_TAILNET_ASSERTION_KEY_FILE)" = /etc/roadmap/tailnet.key ]] || {
		printf 'private beta owner environment has an unexpected assertion-key path\n' >&2
		exit 1
	}
	[[ "$(single_owner_value ROADMAP_TAILNET_ASSERTION_KEY_FILE)" = /etc/roadmap/tailnet.key ]] || {
		printf 'private beta owner environment has an unexpected assertion-key path\n' >&2
		exit 1
	}
	[[ "$(single_owner_value HELM_TAILNET_TLS_ADDR)" = 10.0.0.39:8443 && "$(single_owner_value ROADMAP_TAILNET_TLS_ADDR)" = 10.0.0.39:8443 ]] || {
		printf 'private beta owner environment has an unexpected Tailnet listener address\n' >&2
		exit 1
	}
	[[ "$(single_owner_value HELM_TAILNET_TLS_CERT_FILE)" = /etc/roadmap/tailnet-origin.crt && "$(single_owner_value ROADMAP_TAILNET_TLS_CERT_FILE)" = /etc/roadmap/tailnet-origin.crt ]] || {
		printf 'private beta owner environment has an unexpected Tailnet certificate path\n' >&2
		exit 1
	}
	[[ "$(single_owner_value HELM_TAILNET_TLS_KEY_FILE)" = /etc/roadmap/tailnet-origin.key && "$(single_owner_value ROADMAP_TAILNET_TLS_KEY_FILE)" = /etc/roadmap/tailnet-origin.key ]] || {
		printf 'private beta owner environment has an unexpected Tailnet key path\n' >&2
		exit 1
	}
	peers=$(single_owner_value HELM_TAILNET_ALLOWED_PEER_IPS)
	[[ "$peers" = "$(single_owner_value ROADMAP_TAILNET_ALLOWED_PEER_IPS)" ]] || {
		printf 'private beta owner environment has conflicting Tailnet peer lists\n' >&2
		exit 1
	}
	IFS=, read -r -a peers <<<"$peers"
	[[ ${#peers[@]} -gt 0 ]] || {
		printf 'private beta owner environment must allow at least one Tailnet peer\n' >&2
		exit 1
	}
	for value in "${peers[@]}"; do
		valid_ipv4 "$value" || {
			printf 'private beta owner environment contains an invalid Tailnet peer address\n' >&2
			exit 1
		}
	done
}

if (( PRIVATE_TAILNET_BETA == 1 )); then
	validate_private_owner_env
else
	safe_file "$CLOUDFLARED_TOKEN_FILE" cloudflared-token
	chmod 0600 "$CLOUDFLARED_TOKEN_FILE"
fi

# Keep this pin in lockstep with the reviewed Stashlet deployment. Existing
# downloads are checked as well; a stale or tampered cache is never bundled.
CLOUDFLARED_VERSION=2026.8.2
CLOUDFLARED_SHA256=fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2
CLOUDFLARED_PATH="$DIST_DIR/cloudflared"

if (( PRIVATE_TAILNET_BETA == 0 )) && [[ -e "$CLOUDFLARED_PATH" && ( ! -f "$CLOUDFLARED_PATH" || -L "$CLOUDFLARED_PATH" ) ]]; then
	printf 'cloudflared cache is not a regular file\n' >&2
	exit 1
fi

if (( PRIVATE_TAILNET_BETA == 0 )) && [[ ! -f "$CLOUDFLARED_PATH" ]]; then
	tmp=$(mktemp "$DIST_DIR/.cloudflared.XXXXXX")
	trap 'rm -f "$tmp"' EXIT
	curl --fail --location --show-error --proto '=https' --tlsv1.2 \
		"https://github.com/cloudflare/cloudflared/releases/download/${CLOUDFLARED_VERSION}/cloudflared-linux-amd64" \
		--output "$tmp"
	echo "$CLOUDFLARED_SHA256  $tmp" | sha256sum --check --strict >/dev/null
	chmod 0755 "$tmp"
	mv -T -- "$tmp" "$CLOUDFLARED_PATH"
	trap - EXIT
fi

if (( PRIVATE_TAILNET_BETA == 0 )); then
	echo "$CLOUDFLARED_SHA256  $CLOUDFLARED_PATH" | sha256sum --check --strict >/dev/null
	chmod 0755 "$CLOUDFLARED_PATH"
fi

BUNDLE_DIR=$(mktemp -d)
cleanup() { rm -rf -- "$BUNDLE_DIR"; }
trap cleanup EXIT
chmod 0700 "$BUNDLE_DIR"

# The Proxmox verifier is a separately managed trust boundary. Preserve its
# exact Roadmap v1 member names and manifest header while the payload itself
# transitions to Helm. The guest installer converts these envelope members to
# canonical Helm runtime names and retains compatibility aliases for rollback.
install -m 0755 "$DIST_DIR/helm" "$BUNDLE_DIR/roadmap"
install -m 0755 "$DIST_DIR/codex" "$BUNDLE_DIR/codex"
if (( PRIVATE_TAILNET_BETA == 0 )); then
	install -m 0755 "$CLOUDFLARED_PATH" "$BUNDLE_DIR/cloudflared"
	install -m 0600 "$CLOUDFLARED_TOKEN_FILE" "$BUNDLE_DIR/cloudflared.token"
fi
install -m 0640 "$OWNER_ENV_FILE" "$BUNDLE_DIR/roadmap.env"
install -m 0755 "$ROOT_DIR/deploy/install-inside-lxc.sh" "$BUNDLE_DIR/install-inside-lxc.sh"
if (( PRIVATE_TAILNET_BETA == 1 )); then
	install -m 0755 "$ROOT_DIR/deploy/validate-beta-private.sh" "$BUNDLE_DIR/validate-beta-private.sh"
fi
install -m 0755 "$ROOT_DIR/deploy/helm-backup.sh" "$BUNDLE_DIR/roadmap-backup.sh"
install -m 0755 "$ROOT_DIR/deploy/helm-restore.sh" "$BUNDLE_DIR/roadmap-restore.sh"
install -m 0755 "$ROOT_DIR/deploy/helm-rollback.sh" "$BUNDLE_DIR/roadmap-rollback.sh"
install -m 0644 "$ROOT_DIR/deploy/helm-backup.service" "$BUNDLE_DIR/roadmap-backup.service"
install -m 0644 "$ROOT_DIR/deploy/helm-backup.timer" "$BUNDLE_DIR/roadmap-backup.timer"
install -m 0644 "$ROOT_DIR/deploy/helm.service" "$BUNDLE_DIR/roadmap.service"
if (( PRIVATE_TAILNET_BETA == 0 )); then
	install -m 0644 "$ROOT_DIR/deploy/cloudflared.service" "$BUNDLE_DIR/cloudflared.service"
fi
install -m 0644 "$ROOT_DIR/deploy/nftables.conf" "$BUNDLE_DIR/nftables.conf"
install -m 0644 "$ROOT_DIR/compose.yaml" "$BUNDLE_DIR/compose.yaml"
if grep -Eq '^(HELM|ROADMAP)_RELEASE_SHA=' "$BUNDLE_DIR/roadmap.env"; then
	printf 'owner environment already contains a release SHA\n' >&2
	exit 1
fi
printf 'HELM_RELEASE_SHA=%s\nROADMAP_RELEASE_SHA=%s\n' "$SHA" "$SHA" >> "$BUNDLE_DIR/roadmap.env"
chmod 0640 "$BUNDLE_DIR/roadmap.env"
printf '%s\n' "$SHA" > "$BUNDLE_DIR/release.sha"
chmod 0644 "$BUNDLE_DIR/release.sha"
(cd "$BUNDLE_DIR" && sha256sum roadmap > roadmap.sha256)
chmod 0644 "$BUNDLE_DIR/roadmap.sha256"
(cd "$BUNDLE_DIR" && sha256sum codex > codex.sha256)
chmod 0644 "$BUNDLE_DIR/codex.sha256"

# The manifest is the canonical signed description of every payload member.
# The two envelope members are intentionally excluded to avoid a circular
# self-hash; they are validated by exact name, size, and signature checks on
# the PVE host.
{
	printf 'roadmap-release-manifest-v1\n'
	for member in "${PAYLOAD_MEMBERS[@]}"; do
		bytes=$(stat -c '%s' -- "$BUNDLE_DIR/$member")
		digest=$(sha256sum -- "$BUNDLE_DIR/$member" | awk '{print $1}')
		printf '%s\t%s\t%s\n' "$member" "$bytes" "$digest"
	done
} > "$BUNDLE_DIR/release.manifest"
chmod 0644 "$BUNDLE_DIR/release.manifest"
openssl pkeyutl -sign -rawin -inkey "$SIGNING_KEY_FILE" \
	-in "$BUNDLE_DIR/release.manifest" -out "$BUNDLE_DIR/release.manifest.sig" \
	|| {
		printf 'could not sign release manifest\n' >&2
		exit 1
	}
chmod 0644 "$BUNDLE_DIR/release.manifest.sig"

ARCHIVE="$DIST_DIR/helm-$SHA.tar.gz"
rm -f -- "$ARCHIVE"
# Normalize tar metadata so the archive is reproducible for a given source
# tree, while retaining strict, non-writable member modes from above.
GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$ARCHIVE" -C "$BUNDLE_DIR" "${BUNDLE_MEMBERS[@]}"
chmod 0600 "$ARCHIVE"

# A local sanity check catches accidental aliases or omitted members before
# the archive reaches the Proxmox gateway. The host verifier repeats this
# check after receiving the stream.
mapfile -t archive_members < <(tar -tzf "$ARCHIVE")
[[ ${#archive_members[@]} -eq ${#BUNDLE_MEMBERS[@]} ]] || {
	printf 'bundle member count is not canonical\n' >&2
	exit 1
}
for index in "${!BUNDLE_MEMBERS[@]}"; do
	[[ "${archive_members[$index]}" = "${BUNDLE_MEMBERS[$index]}" ]] || {
		printf 'bundle member list is not canonical\n' >&2
		exit 1
	}
done

printf '%s\n' "$ARCHIVE"
