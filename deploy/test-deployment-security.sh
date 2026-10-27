#!/usr/bin/env bash
# Deployment protocol and release-provenance regressions. These checks avoid
# any Proxmox or Cloudflare connection; the signed-archive cases exercise the
# same verifier that the root gateway invokes.
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DEPLOY_DIR="$ROOT_DIR/deploy"

# Source each responsibility in the original regression order. These modules
# share this shell, including fixture state, helper functions, and the EXIT trap.
source "$DEPLOY_DIR/security-tests/setup.sh"
source "$DEPLOY_DIR/security-tests/gateway-command-and-release-contract.sh"
source "$DEPLOY_DIR/security-tests/guest-installation-lifecycle.sh"
source "$DEPLOY_DIR/security-tests/candidate-installation-gate.sh"
source "$DEPLOY_DIR/security-tests/gateway-ownership-and-status.sh"
source "$DEPLOY_DIR/security-tests/rollback-transaction.sh"
source "$DEPLOY_DIR/security-tests/cloudflare-access-probes.sh"
source "$DEPLOY_DIR/security-tests/cloudflare-private-boundary-and-topology.sh"
source "$DEPLOY_DIR/security-tests/cloudflare-prepare-recovery.sh"
source "$DEPLOY_DIR/security-tests/ci-and-private-deployment-policy.sh"
source "$DEPLOY_DIR/security-tests/cloudflare-prepare-outputs.sh"
source "$DEPLOY_DIR/security-tests/signed-release-verification.sh"
source "$DEPLOY_DIR/security-tests/restore-authorization-overlay.sh"
source "$DEPLOY_DIR/security-tests/backup-publication-and-retention.sh"

printf 'deployment_security_tests=ok\n'
