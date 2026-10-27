GATEWAY="$DEPLOY_DIR/helm-deploy-gateway"
BOOTSTRAP="$DEPLOY_DIR/bootstrap-proxmox.sh"
DEPLOY_CI="$DEPLOY_DIR/deploy-ci.sh"
VERIFY="$DEPLOY_DIR/verify-release.sh"
BUILD_BUNDLE="$DEPLOY_DIR/build-bundle.sh"
BACKUP="$DEPLOY_DIR/helm-backup.sh"
RESTORE="$DEPLOY_DIR/helm-restore.sh"
ROLLBACK="$DEPLOY_DIR/helm-rollback.sh"
INSTALL="$DEPLOY_DIR/install-inside-lxc.sh"
PRIVATE_VALIDATE="$DEPLOY_DIR/validate-beta-private.sh"
PRIVATE_OWNER_TEMPLATE="$DEPLOY_DIR/tailnet-owner.env.example"
SERVICE="$DEPLOY_DIR/helm.service"
BETA_SWITCH_SERVICE="$DEPLOY_DIR/helm-beta-switchd.service"
BACKUP_SERVICE="$DEPLOY_DIR/helm-backup.service"
CLOUDFLARE="$DEPLOY_DIR/cloudflare.sh"
VALIDATE="$DEPLOY_DIR/validate-live.sh"
WORKFLOW="$ROOT_DIR/.github/workflows/ci.yml"
BETA_CANDIDATE_WORKFLOW="$ROOT_DIR/.github/workflows/beta-candidate.yml"
DOCS="$ROOT_DIR/docs/OPERATIONS.md"
DOCKERIGNORE="$ROOT_DIR/.dockerignore"

fail() { printf '[deployment-security] %s\n' "$*" >&2; exit 1; }
contains() {
	local needle=$1 file=$2
	grep -F -- "$needle" "$file" >/dev/null || fail "missing reviewed text in $file: $needle"
}
not_contains() {
	local needle=$1 file=$2
	if grep -F -- "$needle" "$file" >/dev/null; then
		fail "unexpected reviewed text in $file: $needle"
	fi
}
count_contains() {
	local expected=$1 needle=$2 file=$3 actual
	actual=$(grep -Fc -- "$needle" "$file" || true)
	[[ "$actual" = "$expected" ]] || fail "expected $expected occurrences of $needle in $file, got $actual"
}

for file in "$GATEWAY" "$BOOTSTRAP" "$DEPLOY_CI" "$VERIFY" "$BUILD_BUNDLE" \
	"$BACKUP" "$RESTORE" "$ROLLBACK" "$INSTALL" "$PRIVATE_VALIDATE" "$PRIVATE_OWNER_TEMPLATE" "$SERVICE" "$BETA_SWITCH_SERVICE" "$BACKUP_SERVICE" "$CLOUDFLARE" "$VALIDATE" "$WORKFLOW" "$DOCS"; do
	[[ -f "$file" && ! -L "$file" ]] || fail "deployment file is missing: $file"
done

fixture=$(mktemp -d "${TMPDIR:-/tmp}/helm-deploy-security.XXXXXX")
cleanup_fixture() { rm -rf -- "$fixture"; }
trap cleanup_fixture EXIT
sed -n '/^  beta_deploy:/,/^  deploy:/p' "$WORKFLOW" > "$fixture/beta-workflow.yml"
# Mocked backup publication tests do not have a release binary from which to
# query migration-info; use a deterministic non-secret fixture digest.
export HELM_MIGRATION_DIGEST=0000000000000000000000000000000000000000000000000000000000000000

