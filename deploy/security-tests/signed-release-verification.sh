# Build a small, valid signed release with the same canonical member set as
# build-bundle.sh. This proves the verifier accepts the intended archive before
# the negative cases mutate one property at a time.
PAYLOAD_MEMBERS=(
	cloudflared
	cloudflared.service
	cloudflared.token
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
BUNDLE_MEMBERS=(
	cloudflared cloudflared.service cloudflared.token codex codex.sha256 compose.yaml
	install-inside-lxc.sh nftables.conf roadmap roadmap-backup.service
	roadmap-backup.sh roadmap-backup.timer roadmap.env roadmap-restore.sh
	roadmap-rollback.sh roadmap.service roadmap.sha256 release.manifest
	release.manifest.sig release.sha
)
SHA=$(printf 'a%.0s' {1..40})
source_dir="$fixture/source"
install -d -m 0700 "$source_dir"
for member in "${PAYLOAD_MEMBERS[@]}"; do
	if [[ "$member" = release.sha ]]; then
		printf '%s\n' "$SHA" > "$source_dir/$member"
	else
		printf 'fixture payload for %s\n' "$member" > "$source_dir/$member"
	fi
done
{
	printf 'roadmap-release-manifest-v1\n'
	for member in "${PAYLOAD_MEMBERS[@]}"; do
		bytes=$(stat -c '%s' -- "$source_dir/$member")
		digest=$(sha256sum -- "$source_dir/$member" | awk '{print $1}')
		printf '%s\t%s\t%s\n' "$member" "$bytes" "$digest"
	done
} > "$source_dir/release.manifest"
openssl genpkey -algorithm ED25519 -out "$fixture/private.pem" >/dev/null 2>&1
chmod 0600 "$fixture/private.pem"
openssl pkey -in "$fixture/private.pem" -pubout -out "$fixture/public.pem" >/dev/null 2>&1
openssl pkeyutl -sign -rawin -inkey "$fixture/private.pem" -in "$source_dir/release.manifest" -out "$source_dir/release.manifest.sig"

make_archive() {
	local output=$1 directory=$2
	GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
		-czf "$output" -C "$directory" "${BUNDLE_MEMBERS[@]}"
}
make_archive "$fixture/valid.tar.gz" "$source_dir"
"$VERIFY" "$fixture/valid.tar.gz" "$SHA" "$fixture/public.pem" >/dev/null \
	|| fail 'valid signed release archive was rejected'

# Beta uses the same detached-signature and manifest machinery with a distinct
# exact member set. Its profile is selected only by the root-installed beta
# verifier basename and must not accept any Cloudflare connector member.
BETA_PAYLOAD_MEMBERS=(
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
	validate-beta-private.sh
)
BETA_BUNDLE_MEMBERS=(
	codex codex.sha256 compose.yaml install-inside-lxc.sh
	nftables.conf roadmap roadmap-backup.service roadmap-backup.sh roadmap-backup.timer
	roadmap.env roadmap-restore.sh roadmap-rollback.sh roadmap.service roadmap.sha256
	release.manifest release.manifest.sig release.sha validate-beta-private.sh
)
beta_source_dir="$fixture/beta-source"
install -d -m 0700 "$beta_source_dir"
for member in "${BETA_PAYLOAD_MEMBERS[@]}"; do
	if [[ "$member" = release.sha ]]; then
		printf '%s\n' "$SHA" > "$beta_source_dir/$member"
	else
		printf 'private beta fixture payload for %s\n' "$member" > "$beta_source_dir/$member"
	fi
done
{
	printf 'roadmap-release-manifest-v1\n'
	for member in "${BETA_PAYLOAD_MEMBERS[@]}"; do
		bytes=$(stat -c '%s' -- "$beta_source_dir/$member")
		digest=$(sha256sum -- "$beta_source_dir/$member" | awk '{print $1}')
		printf '%s\t%s\t%s\n' "$member" "$bytes" "$digest"
	done
} > "$beta_source_dir/release.manifest"
openssl pkeyutl -sign -rawin -inkey "$fixture/private.pem" -in "$beta_source_dir/release.manifest" -out "$beta_source_dir/release.manifest.sig"
GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$fixture/beta-valid.tar.gz" -C "$beta_source_dir" "${BETA_BUNDLE_MEMBERS[@]}"
cp -- "$VERIFY" "$fixture/helm-beta-verify-release"
chmod 0755 "$fixture/helm-beta-verify-release"
"$fixture/helm-beta-verify-release" "$fixture/beta-valid.tar.gz" "$SHA" "$fixture/public.pem" >/dev/null \
	|| fail 'valid private beta signed release archive was rejected'
printf 'private_beta_release_profile_test=ok\n'

# The switch-controller beta envelope extends the legacy beta set with the
# controller unit/binary and a signed canonical branch ref.  Keep this fixture
# separate so the verifier's rollback compatibility remains exercised above.
BETA_SWITCH_PAYLOAD_MEMBERS=(
	codex
	codex.sha256
	compose.yaml
	helm-beta-switchd
	helm-beta-switchd.service
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
	release.ref
	release.sha
	release.subject
	validate-beta-private.sh
)
BETA_SWITCH_BUNDLE_MEMBERS=(
	codex codex.sha256 compose.yaml helm-beta-switchd helm-beta-switchd.service
	install-inside-lxc.sh nftables.conf roadmap roadmap-backup.service
	roadmap-backup.sh roadmap-backup.timer roadmap.env roadmap-restore.sh
	roadmap-rollback.sh roadmap.service roadmap.sha256 release.manifest
	release.manifest.sig release.ref release.sha release.subject validate-beta-private.sh
)
beta_switch_source_dir="$fixture/beta-switch-source"
install -d -m 0700 "$beta_switch_source_dir"
for member in "${BETA_SWITCH_PAYLOAD_MEMBERS[@]}"; do
	if [[ "$member" = release.sha ]]; then
		printf '%s\n' "$SHA" > "$beta_switch_source_dir/$member"
	elif [[ "$member" = release.ref ]]; then
		printf 'refs/heads/beta\n' > "$beta_switch_source_dir/$member"
	elif [[ "$member" = release.subject ]]; then
		printf 'Show commit subjects in beta switcher\n' > "$beta_switch_source_dir/$member"
	else
		printf 'private beta switch fixture payload for %s\n' "$member" > "$beta_switch_source_dir/$member"
	fi
done
chmod 0755 "$beta_switch_source_dir/helm-beta-switchd"
{
	printf 'roadmap-release-manifest-v1\n'
	for member in "${BETA_SWITCH_PAYLOAD_MEMBERS[@]}"; do
		bytes=$(stat -c '%s' -- "$beta_switch_source_dir/$member")
		digest=$(sha256sum -- "$beta_switch_source_dir/$member" | awk '{print $1}')
		printf '%s\t%s\t%s\n' "$member" "$bytes" "$digest"
	done
} > "$beta_switch_source_dir/release.manifest"
openssl pkeyutl -sign -rawin -inkey "$fixture/private.pem" \
	-in "$beta_switch_source_dir/release.manifest" \
	-out "$beta_switch_source_dir/release.manifest.sig"
GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$fixture/beta-switch-valid.tar.gz" -C "$beta_switch_source_dir" "${BETA_SWITCH_BUNDLE_MEMBERS[@]}"
"$fixture/helm-beta-verify-release" "$fixture/beta-switch-valid.tar.gz" "$SHA" "$fixture/public.pem" >/dev/null \
	|| fail 'valid private beta switch release archive was rejected'
printf 'private_beta_switch_release_profile_test=ok\n'

# A signed controller envelope still rejects an unsafe branch ref; signing the
# manifest does not make metadata with controls or traversal separators valid.
beta_switch_bad_ref_dir="$fixture/beta-switch-bad-ref"
cp -a "$beta_switch_source_dir" "$beta_switch_bad_ref_dir"
printf 'refs/heads/../owner\n' > "$beta_switch_bad_ref_dir/release.ref"
{
	printf 'roadmap-release-manifest-v1\n'
	for member in "${BETA_SWITCH_PAYLOAD_MEMBERS[@]}"; do
		bytes=$(stat -c '%s' -- "$beta_switch_bad_ref_dir/$member")
		digest=$(sha256sum -- "$beta_switch_bad_ref_dir/$member" | awk '{print $1}')
		printf '%s\t%s\t%s\n' "$member" "$bytes" "$digest"
	done
} > "$beta_switch_bad_ref_dir/release.manifest"
openssl pkeyutl -sign -rawin -inkey "$fixture/private.pem" \
	-in "$beta_switch_bad_ref_dir/release.manifest" \
	-out "$beta_switch_bad_ref_dir/release.manifest.sig"
GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$fixture/beta-switch-bad-ref.tar.gz" -C "$beta_switch_bad_ref_dir" "${BETA_SWITCH_BUNDLE_MEMBERS[@]}"
if "$fixture/helm-beta-verify-release" "$fixture/beta-switch-bad-ref.tar.gz" "$SHA" "$fixture/public.pem" \
	>"$fixture/beta-switch-bad-ref.out" 2>&1; then
	fail 'unsafe beta release ref was unexpectedly accepted'
fi
printf 'private_beta_release_ref_security_test=ok\n'

expect_verify_fail() {
	local label=$1 archive=$2
	if "$VERIFY" "$archive" "$SHA" "$fixture/public.pem" >"$fixture/$label.out" 2>&1; then
		fail "$label release archive was unexpectedly accepted"
	fi
}

# High-ratio bomb: a sparse member is only a few bytes on disk but declares
# more than the aggregate uncompressed cap. The bounded tar listing must stop
# on that header promptly, before tar skips/decompresses the member payload.
high_ratio_dir="$fixture/high-ratio"
cp -a "$source_dir" "$high_ratio_dir"
truncate -s 536870913 "$high_ratio_dir/roadmap"
make_archive "$fixture/high-ratio.tar.gz" "$high_ratio_dir"
if timeout --foreground 10s "$VERIFY" "$fixture/high-ratio.tar.gz" "$SHA" "$fixture/public.pem" \
	>"$fixture/high-ratio.out" 2>&1; then
	fail 'high-ratio release archive was unexpectedly accepted'
else
	high_ratio_status=$?
fi
[[ "$high_ratio_status" -ne 124 && "$high_ratio_status" -ne 137 ]] \
	|| fail 'high-ratio release archive was not rejected promptly'

# Unsigned archive: omitting the detached signature fails the exact envelope
# count before extraction.
GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$fixture/unsigned.tar.gz" -C "$source_dir" \
		cloudflared cloudflared.service cloudflared.token codex codex.sha256 compose.yaml install-inside-lxc.sh \
		nftables.conf roadmap roadmap-backup.service roadmap-backup.sh roadmap-backup.timer \
		roadmap.env roadmap-restore.sh roadmap-rollback.sh roadmap.service roadmap.sha256 \
		release.manifest release.sha
expect_verify_fail unsigned "$fixture/unsigned.tar.gz"

# Tampered payload: the signed manifest remains unchanged, so its digest check
# must reject the archive.
tampered_dir="$fixture/tampered"
cp -a "$source_dir" "$tampered_dir"
printf 'tampered payload\n' > "$tampered_dir/roadmap"
make_archive "$fixture/tampered.tar.gz" "$tampered_dir"
expect_verify_fail tampered "$fixture/tampered.tar.gz"

# Duplicate archive member: exact member count/canonical set rejects a second
# copy even though all individual payload bytes are otherwise valid.
GZIP=-n tar --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$fixture/duplicate.tar.gz" -C "$source_dir" "${BUNDLE_MEMBERS[@]}" roadmap
expect_verify_fail duplicate "$fixture/duplicate.tar.gz"

# Canonical path alias: GNU tar preserves the explicit ./ spelling, which is
# rejected before any payload extraction.
alias_args=()
for member in "${BUNDLE_MEMBERS[@]}"; do alias_args+=("./$member"); done
GZIP=-n tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$fixture/alias.tar.gz" -C "$source_dir" "${alias_args[@]}"
expect_verify_fail canonical_alias "$fixture/alias.tar.gz"

# Oversized manifest: sign it so this case specifically exercises the
# manifest-size cap rather than the detached-signature failure path.
oversized_dir="$fixture/oversized"
cp -a "$source_dir" "$oversized_dir"
dd if=/dev/zero of="$oversized_dir/release.manifest" bs=1 count=131073 status=none
openssl pkeyutl -sign -rawin -inkey "$fixture/private.pem" -in "$oversized_dir/release.manifest" -out "$oversized_dir/release.manifest.sig"
make_archive "$fixture/oversized.tar.gz" "$oversized_dir"
expect_verify_fail oversized_manifest "$fixture/oversized.tar.gz"

# Duplicate manifest record: its signature is valid, but canonical order and
# exact payload cardinality reject the repeated name.
duplicate_manifest_dir="$fixture/duplicate-manifest"
cp -a "$source_dir" "$duplicate_manifest_dir"
first_record=$(sed -n '2p' "$duplicate_manifest_dir/release.manifest")
{ head -n 1 "$duplicate_manifest_dir/release.manifest"; printf '%s\n' "$first_record"; tail -n +2 "$duplicate_manifest_dir/release.manifest"; } > "$duplicate_manifest_dir/release.manifest.new"
mv -T -- "$duplicate_manifest_dir/release.manifest.new" "$duplicate_manifest_dir/release.manifest"
openssl pkeyutl -sign -rawin -inkey "$fixture/private.pem" -in "$duplicate_manifest_dir/release.manifest" -out "$duplicate_manifest_dir/release.manifest.sig"
make_archive "$fixture/duplicate-manifest.tar.gz" "$duplicate_manifest_dir"
expect_verify_fail duplicate_manifest "$fixture/duplicate-manifest.tar.gz"

