# The SSH key enters one fixed root command through non-interactive sudo. The
# gateway sees SSH_ORIGINAL_COMMAND, while local argv is an explicit root-only
# test escape hatch. No deploy-key holder can request a shell or arbitrary sudo.
contains 'command=\"sudo -n /usr/local/sbin/helm-deploy-gateway\"' "$BOOTSTRAP"
contains 'command=\"sudo -n /usr/local/sbin/helm-beta-deploy-gateway\"' "$BOOTSTRAP"
contains 'env_keep += "SSH_ORIGINAL_COMMAND SSH_CONNECTION"' "$BOOTSTRAP"
contains 'ALL=(root) NOPASSWD: /usr/local/sbin/helm-deploy-gateway' "$BOOTSTRAP"
contains 'ALL=(root) NOPASSWD: /usr/local/sbin/helm-beta-deploy-gateway' "$BOOTSTRAP"
contains '/var/lib/roadmap-deploy/staging' "$BOOTSTRAP"
contains '/var/lib/helm-beta-deploy/staging' "$BOOTSTRAP"
contains 'mktemp -d "$base/bootstrap.XXXXXX"' "$BOOTSTRAP"
contains 'release-signing-public.pem' "$BOOTSTRAP"
contains '[[ ${SSH_ORIGINAL_COMMAND+x} ]]' "$GATEWAY"
contains 'REQUEST=${SSH_ORIGINAL_COMMAND:-}' "$GATEWAY"
contains 'HELM_GATEWAY_LOCAL_TEST=1' "$GATEWAY"
contains '[[ $# -eq 0 ]] || fail '\''SSH gateway does not accept command-line arguments'\''' "$GATEWAY"
contains 'flock -x 8' "$GATEWAY"
contains 'timeout --foreground "$ARCHIVE_INGEST_TIMEOUT"' "$GATEWAY"
contains 'head -c "$((ARCHIVE_MAX_BYTES + 1))" > "$ARCHIVE"' "$GATEWAY"
contains 'release provenance verification failed' "$GATEWAY"
contains '"$VERIFY_SCRIPT" "$ARCHIVE" "$SHA" "$SIGNING_PUBLIC_KEY"' "$GATEWAY"
contains 'GATEWAY_BASENAME=${BASH_SOURCE[0]##*/}' "$GATEWAY"
contains '[[ "$GATEWAY_BASENAME" = helm-beta-deploy-gateway ]]' "$GATEWAY"
contains 'CTID=106' "$GATEWAY"
contains 'CT_NAME=helm-beta' "$GATEWAY"
contains 'CT_IP=10.0.0.39' "$GATEWAY"
contains 'SIGNING_PUBLIC_KEY=/etc/helm-beta-deploy/release-signing-public.pem' "$GATEWAY"
contains "CT_TAGS='homelab;lan;roadmap;service'" "$GATEWAY"
contains "CT_TAGS='beta;service;lan'" "$GATEWAY"
contains '--tags "$CT_TAGS"' "$GATEWAY"

# The verifier must run before any deploy-branch pct state change. Status and
# rollback are intentionally payload-free actions and do not need a release
# signature.
verify_line=$(grep -n 'verified=\$("\$VERIFY_SCRIPT"' "$GATEWAY" | cut -d: -f1)
deploy_pct_line=$(grep -n 'if ! pct config "\$CTID"' "$GATEWAY" | cut -d: -f1)
[[ -n "$verify_line" && -n "$deploy_pct_line" && "$verify_line" -lt "$deploy_pct_line" ]] \
	|| fail 'gateway does not verify release provenance before deploy pct state changes'

# Deploy payloads are streamed to the gateway. status/rollback carry no
# payload, and the caller never invokes scp or puts sudo/gateway text in the
# SSH request.
contains 'ssh -o BatchMode=yes -T "$PVE_DEPLOY_USER@$PVE_HOST" "deploy $SHA" < "$ARCHIVE"' "$DEPLOY_CI"
contains 'ssh -o BatchMode=yes -T "$PVE_DEPLOY_USER@$PVE_HOST" status < /dev/null' "$DEPLOY_CI"
contains 'ssh -o BatchMode=yes -T "$PVE_DEPLOY_USER@$PVE_HOST" "rollback $SHA" < /dev/null' "$DEPLOY_CI"
not_contains 'scp -q' "$DEPLOY_CI"
not_contains '/usr/local/sbin/helm-deploy-gateway' "$DEPLOY_CI"
contains 'HELM_DEPLOY_ENVIRONMENT must be production, production-private or beta' "$DEPLOY_CI"
contains 'PVE_DEPLOY_USER must be helm-beta-deploy for beta deployments' "$DEPLOY_CI"

# Only the intended command alphabet can reach gateway action dispatch.
for meta in "*';'*" "*'&'*" "*'|'*" "*'\$'*" "*'\`'*" "*'<'*" "*'>'*" "*'('*" "*')'*" "*'\\\\'*"; do
	contains "$meta" "$GATEWAY"
done

# Regression for the old decimal-prefix bug: group/world write bits are
# rejected even when a mode begins with an otherwise permitted 06 prefix.
archive_mode_allowed() {
	local mode=$1
	(( (8#$mode & 022) == 0 ))
}
archive_mode_allowed 600 || fail '0600 archive mode should be accepted'
archive_mode_allowed 640 || fail '0640 archive mode should be accepted'
archive_mode_allowed 620 && fail '0620 archive mode must be rejected'
archive_mode_allowed 602 && fail '0602 archive mode must be rejected'
archive_mode_allowed 666 && fail '0666 archive mode must be rejected'

# Signed release envelope and exact caps are part of the trusted boundary.
contains 'ARCHIVE_MAX_BYTES=536870912' "$VERIFY"
contains 'ARCHIVE_MAX_MEMBERS=${#ALL_MEMBERS[@]}' "$VERIFY"
contains 'ARCHIVE_MAX_UNCOMPRESSED_BYTES=536870912' "$VERIFY"
contains 'MANIFEST_MAX_BYTES=131072' "$VERIFY"
contains 'release.manifest.sig' "$VERIFY"
contains 'openssl pkeyutl -verify' "$VERIFY"
contains 'bounded_tar()' "$VERIFY"
contains 'timeout --foreground --kill-after=2s' "$VERIFY"
contains 'ulimit -t "$1"' "$VERIFY"
contains 'ulimit -v "$2"' "$VERIFY"
contains 'bounded_tar_listing()' "$VERIFY"
contains 'bounded_tar_listing --numeric-owner --full-time -tvzf "$ARCHIVE"' "$VERIFY"
contains 'mkfifo -m 0600 "$tar_listing_fifo"' "$VERIFY"
contains 'kill -TERM -- "-$listing_pid"' "$VERIFY"
contains 'max_listing_bytes="$TAR_LISTING_LIMIT_BYTES"' "$VERIFY"
contains 'awk -v max_members="$ARCHIVE_MAX_MEMBERS"' "$VERIFY"
archive_cap_line=$(grep -n 'archive_size=.*stat' "$VERIFY" | cut -d: -f1)
bounded_tar_line=$(grep -n '^bounded_tar()' "$VERIFY" | cut -d: -f1)
[[ -n "$archive_cap_line" && -n "$bounded_tar_line" && "$archive_cap_line" -lt "$bounded_tar_line" ]] \
	|| fail 'compressed archive cap is not checked before tar processing'
contains 'ARCHIVE_INGEST_TIMEOUT=120' "$GATEWAY"
contains 'HELM_RELEASE_SIGNING_KEY_FILE' "$BUILD_BUNDLE"
contains 'HELM_RELEASE_REF' "$BUILD_BUNDLE"
contains 'RELEASE_REF_MAX_BYTES=256' "$BUILD_BUNDLE"
contains 'RELEASE_SUBJECT_MAX_BYTES=160' "$BUILD_BUNDLE"
contains 'helm-beta-switchd' "$BUILD_BUNDLE"
contains 'release.ref' "$BUILD_BUNDLE"
contains 'release.subject' "$BUILD_BUNDLE"
contains 'Candidate admission is disabled' "$BETA_CANDIDATE_WORKFLOW"
contains 'exit 1' "$BETA_CANDIDATE_WORKFLOW"
not_contains 'deploy-ci.sh' "$BETA_CANDIDATE_WORKFLOW"
not_contains 'helm-beta-switchd' "$BETA_CANDIDATE_WORKFLOW"
not_contains 'secrets.' "$BETA_CANDIDATE_WORKFLOW"
contains 'HELM_RELEASE_SHA=%s' "$BUILD_BUNDLE"
contains 'owner environment already contains a release SHA' "$BUILD_BUNDLE"
# Private beta retains its separately pinned optional connector download.
contains 'CLOUDFLARED_VERSION=2026.8.2' "$INSTALL"
contains 'CLOUDFLARED_SHA256=fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2' "$INSTALL"
contains 'echo "$CLOUDFLARED_SHA256  $temporary" | sha256sum --check --strict --status' "$INSTALL"
contains 'safe_file "$DIST_DIR/codex" codex-binary' "$BUILD_BUNDLE"
contains '(cd "$BUNDLE_DIR" && sha256sum codex > codex.sha256)' "$BUILD_BUNDLE"
contains 'verify_codex_binary()' "$INSTALL"
contains 'codex codex.sha256 roadmap.env' "$INSTALL"
contains 'sha256sum --check --strict codex.sha256' "$INSTALL"
contains 'install -m 0755 -o root -g root "$RELEASE_DIR/codex" "$new_target/codex"' "$INSTALL"
contains 'apt-get install --yes --no-install-recommends bubblewrap ca-certificates' "$INSTALL"
contains 'validate_optional_codex()' "$ROLLBACK"
contains 'validate_optional_codex "$TARGET"' "$ROLLBACK"
contains 'validate_optional_codex "$previous_target"' "$ROLLBACK"
contains 'Environment=HELM_CODEX_BINARY=/var/lib/roadmap/current/codex' "$SERVICE"
contains 'User=root' "$BETA_SWITCH_SERVICE"
contains 'Group=root' "$BETA_SWITCH_SERVICE"
contains '--socket /run/helm-beta-switcher/helm-beta-switchd.sock' "$BETA_SWITCH_SERVICE"
contains '--releases-dir /var/lib/roadmap/releases' "$BETA_SWITCH_SERVICE"
contains '--current-link /var/lib/roadmap/current' "$BETA_SWITCH_SERVICE"
contains '--jobs-dir /var/lib/roadmap/beta-switch-jobs' "$BETA_SWITCH_SERVICE"
contains 'install -d -m 0700 -o root -g root /var/lib/roadmap/beta-switch-jobs' "$INSTALL"
contains '--rollback /usr/local/sbin/helm-rollback' "$BETA_SWITCH_SERVICE"
contains '--allowed-user roadmap' "$BETA_SWITCH_SERVICE"
contains '--socket-group roadmap' "$BETA_SWITCH_SERVICE"
contains 'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6' "$BETA_SWITCH_SERVICE"
contains 'CapabilityBoundingSet=CAP_CHOWN' "$BETA_SWITCH_SERVICE"
contains 'AmbientCapabilities=' "$BETA_SWITCH_SERVICE"
contains 'RuntimeDirectory=helm-beta-switcher' "$BETA_SWITCH_SERVICE"
contains 'RuntimeDirectoryMode=0711' "$BETA_SWITCH_SERVICE"
not_contains 'ReadWritePaths=/run ' "$BETA_SWITCH_SERVICE"
not_contains 'PartOf=helm.service' "$BETA_SWITCH_SERVICE"
not_contains 'BindsTo=helm.service' "$BETA_SWITCH_SERVICE"
contains 'systemctl enable --now helm-beta-switchd.service' "$INSTALL"
contains '--unix-socket /run/helm-beta-switcher/helm-beta-switchd.sock http://localhost/v1/releases' "$INSTALL"
contains "'root:roadmap 660'" "$INSTALL"
contains 'docker cp "$container_id:/usr/local/bin/codex" "$image_dir/codex"' "$WORKFLOW"
contains 'name: helm-image-${{ github.sha }}' "$WORKFLOW"
contains 'test -x "$image_dir/codex"' "$WORKFLOW"

