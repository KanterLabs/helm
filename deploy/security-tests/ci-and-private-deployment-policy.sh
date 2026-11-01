# Coolify lane mutation uses a fixed non-canceling lock, immutable image,
# protected environment, and all existing validation dependencies. Legacy
# credentials and LXC mutation must not return to the deployment workflow.
contains 'group: helm-${{ github.workflow }}-${{ github.ref }}' "$WORKFLOW"
contains "cancel-in-progress: \${{ github.ref != 'refs/heads/main' && github.ref != 'refs/heads/beta' }}" "$WORKFLOW"
count_contains 1 'group: helm-production' "$WORKFLOW"
count_contains 1 'group: helm-beta' "$WORKFLOW"
count_contains 2 'cancel-in-progress: false' "$WORKFLOW"
contains "github.ref == 'refs/heads/main'" "$WORKFLOW"
contains "github.ref == 'refs/heads/beta'" "$WORKFLOW"
contains 'branches: [main, beta]' "$WORKFLOW"
contains 'name: beta' "$WORKFLOW"
contains 'name: production' "$WORKFLOW"
contains 'url: https://beta-helm.home.shanekanterman.dev' "$WORKFLOW"
contains 'url: https://helm.home.shanekanterman.dev' "$WORKFLOW"
count_contains 1 "vars.HELM_BETA_DEPLOY_PAUSED != 'true'" "$WORKFLOW"
count_contains 1 "vars.HELM_PRODUCTION_DEPLOY_PAUSED != 'true'" "$WORKFLOW"
count_contains 2 'needs: [checks, browser, race, container]' "$WORKFLOW"
count_contains 2 'run: python3 deploy/coolify-deploy.py' "$WORKFLOW"
count_contains 2 'COOLIFY_API_TOKEN: ${{ secrets.COOLIFY_API_TOKEN }}' "$WORKFLOW"
for target in COOLIFY_API_URL COOLIFY_PROJECT_UUID COOLIFY_ENVIRONMENT_UUID COOLIFY_SERVICE_UUID COOLIFY_SERVICE_NAME COOLIFY_SERVER_UUID HELM_SITE_URL; do
	count_contains 2 "$target: \${{ vars.$target }}" "$WORKFLOW"
done
count_contains 2 'HELM_IMAGE: ${{ needs.container.outputs.image }}' "$WORKFLOW"
count_contains 2 'HELM_RELEASE_SHA: ${{ github.sha }}' "$WORKFLOW"
contains 'image=ghcr.io/kanterlabs/helm' "$WORKFLOW"
contains '[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]' "$WORKFLOW"
contains 'docker push "$image:${GITHUB_SHA}"' "$WORKFLOW"
contains 'current=false' "$WORKFLOW"
contains 'ls-remote --exit-code --refs origin "$EXPECTED_REF"' "$WORKFLOW"
# Check the exact runner selector and trust gate instead of legacy runner comments.
python3 "$DEPLOY_DIR/test-ci-runner-policy.py" "$WORKFLOW"
python3 "$DEPLOY_DIR/security-tests/test_ci_runner_policy.py" --artifact "$fixture/runner-policy-proofs.json"
count_contains 2 'uses: tailscale/github-action@d1b6cd204f8dceda5b3eaad7f1f767be390056cd' "$WORKFLOW"
count_contains 2 'oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}' "$WORKFLOW"
count_contains 2 'oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}' "$WORKFLOW"
count_contains 2 'tags: tag:gha-helm-deploy' "$WORKFLOW"
not_contains 'args: --accept-routes' "$WORKFLOW"
contains 'ref: ${{ github.sha }}' "$WORKFLOW"
contains 'actions/upload-artifact@' "$WORKFLOW"
contains '# v7.0.1' "$WORKFLOW"
contains 'actions/checkout@' "$WORKFLOW"
contains 'actions/setup-go@' "$WORKFLOW"
contains '# v7.0.0' "$WORKFLOW"
contains 'actions/setup-node@' "$WORKFLOW"
for retired in BETA_ADMIN_EMAIL BETA_DEPLOY_SSH_KEY BETA_DEPLOY_KNOWN_HOSTS BETA_RELEASE_SIGNING_KEY ROADMAP_RELEASE_SIGNING_KEY ROADMAP_CF_ACCESS_CLIENT_ID ROADMAP_CF_ACCESS_CLIENT_SECRET HELM_OWNER_ENV_FILE HELM_CLOUDFLARED_TOKEN_FILE deploy-ci.sh rollback_sha; do
	not_contains "$retired" "$WORKFLOW"
done
not_contains 'BETA_CLOUDFLARE_API_TOKEN' "$WORKFLOW"
not_contains 'BETA_CF_ACCESS_CLIENT_ID' "$WORKFLOW"
not_contains 'BETA_CF_ACCESS_CLIENT_SECRET' "$WORKFLOW"
contains 'HELM_TAILNET_ALLOWED_PEER_IPS=10.0.0.101' "$PRIVATE_OWNER_TEMPLATE"
contains 'ROADMAP_TAILNET_ALLOWED_PEER_IPS=10.0.0.101' "$PRIVATE_OWNER_TEMPLATE"
contains 'REPLACE_WITH_EXISTING_ADMIN_EMAIL' "$PRIVATE_OWNER_TEMPLATE"
contains 'beta_private_ready=ok' "$PRIVATE_VALIDATE"
contains 'status" = 401' "$PRIVATE_VALIDATE"
contains 'cloudflared.service is active in the private beta profile' "$PRIVATE_VALIDATE"
contains 'http://127.0.0.1:8080/healthz' "$PRIVATE_VALIDATE"
not_contains 'cloudflare.sh' "$PRIVATE_VALIDATE"
contains 'ip saddr %s tcp dport 8443 accept' "$INSTALL"
contains 'PRIVATE_TAILNET_ALLOWED_PEER=10.0.0.101' "$INSTALL"
contains 'PRIVATE_TAILNET_ALLOWED_PEER=10.0.0.101' "$BUILD_BUNDLE"
contains 'PRIVATE_TAILNET_ALLOWED_PEER=10.0.0.101' "$PRIVATE_VALIDATE"
contains 'nft -c -f /etc/nftables.conf' "$INSTALL"
not_contains '/etc/nftables.conf' "$ROLLBACK"
not_contains 'tailscale0' "$INSTALL"
not_contains 'tailscale0' "$DEPLOY_DIR/nftables.conf"
contains 'homelab-edge' "$ROOT_DIR/docs/BETA_DEPLOYMENT_PLAN.md"
contains '10.0.0.101' "$DOCS"
contains '10.0.0.101' "$ROOT_DIR/docs/HOMELAB_DEPLOYMENT.md"

# The private profile's generated rules must replace the release marker in the
# file nftables.service actually loads. Mock install so this exercises the
# helper body without changing the host firewall or requiring root.
private_nft_dir="$fixture/private-nft"
install -d -m 0700 "$private_nft_dir/release"
cat > "$private_nft_dir/release/nftables.conf" <<'EOF'
table inet roadmap_filter {
    # HELM_BETA_TAILNET_RULES
    ct state established,related accept
}
EOF
printf 'HELM_TAILNET_ALLOWED_PEER_IPS=10.0.0.101\n' > "$private_nft_dir/owner.env"
private_nft_output="$private_nft_dir/loaded-nftables.conf"
private_nft_destination="$private_nft_dir/destination"
source <(awk '/^private_owner_value\(\)/,/^}/' "$INSTALL")
source <(awk '/^install_private_nftables\(\)/,/^}/' "$INSTALL")
(
	CONFIG_DIR="$private_nft_dir/config"
	RELEASE_DIR="$private_nft_dir/release"
	install -d -m 0700 "$CONFIG_DIR"
	install() {
		local -a positional=() source_path destination
		while [[ $# -gt 0 ]]; do
			case "$1" in
				-m|-o|-g) shift 2 ;;
				*) positional+=("$1"); shift ;;
			esac
		done
		[[ ${#positional[@]} -eq 2 ]] || return 1
		source_path=${positional[0]}
		destination=${positional[1]}
		printf '%s\n' "$destination" > "$private_nft_destination"
		[[ "$destination" = /etc/nftables.conf ]] || return 1
		cp -- "$source_path" "$private_nft_output"
	}
	install_private_nftables "$private_nft_dir/owner.env"
)
grep -Fxq /etc/nftables.conf "$private_nft_destination" || fail 'private nftables policy was not installed at the service load path'
contains 'ip saddr 10.0.0.101 tcp dport 8443 accept' "$private_nft_output"
contains 'ct state established,related accept' "$private_nft_output"
printf 'private_nftables_runtime_test=ok\n'

not_contains 'cloudflare.sh publish' "$fixture/beta-workflow.yml"
not_contains 'validate-live.sh' "$fixture/beta-workflow.yml"
contains 'HELM_CLOUDFLARED_TOKEN_FILE' "$ROOT_DIR/deploy/build-bundle.sh"
contains 'HELM_OWNER_ENV_FILE' "$ROOT_DIR/deploy/build-bundle.sh"
contains 'Upload sanitized deployment and rollback evidence' "$WORKFLOW"
contains 'python3 -m py_compile deploy/coolify-deploy.py' "$WORKFLOW"
contains 'container_smoke=ok' "$WORKFLOW"
contains 'ready=false' "$WORKFLOW"
contains 'docker exec "$container_id" /usr/local/bin/helm healthcheck' "$WORKFLOW"
contains '/usr/local/bin/helm healthcheck' "$WORKFLOW"
not_contains 'persist-credentials: true' "$WORKFLOW"
checkout_count=$(grep -Fc -- 'uses: actions/checkout@' "$WORKFLOW" || true)
persisted_checkout_count=$(grep -Fc -- 'persist-credentials: false' "$WORKFLOW" || true)
[[ "$checkout_count" = "$persisted_checkout_count" ]] \
	|| fail 'every workflow checkout must disable credential persistence'
awk '
  /^[[:space:]]*-[[:space:]]+uses:[[:space:]]+actions\/checkout@/ {
    if (seen && !persisted) exit 1
    seen = 1
    persisted = 0
    next
  }
  /^[[:space:]]*-[[:space:]]+(uses:|name:)/ {
    if (seen && !persisted) exit 1
  }
  /persist-credentials:[[:space:]]+false/ { if (seen) persisted = 1 }
  END { if (!seen || !persisted) exit 1 }
' "$WORKFLOW" || fail 'a workflow checkout block lacks persist-credentials: false'

contains 'HEALTHCHECK' "$ROOT_DIR/Dockerfile"
contains 'test: ["CMD", "/usr/local/bin/helm", "healthcheck"]' "$ROOT_DIR/compose.yaml"

contains '/var/lib/roadmap/data/roadmap.db' "$ROOT_DIR/docs/HOMELAB_DEPLOYMENT.md"
contains '`ROADMAP_RELEASE_SIGNING_KEY`' "$ROOT_DIR/docs/HOMELAB_DEPLOYMENT.md"

# Local key material must never enter a Docker build context. .gitignore is
# intentionally owned by the repository integration agent; test its Docker
# counterpart here and report missing Git exclusions to the parent.
for secret_pattern in helm-deploy-key helm-deploy-key.pub helm-release-signing-private.pem helm-release-signing-public.pem roadmap-deploy-key roadmap-deploy-key.pub roadmap-release-signing-private.pem roadmap-release-signing-public.pem '*.pem' '*.key' '*.pub'; do
	contains "$secret_pattern" "$DOCKERIGNORE"
done

