# Exercise the gateway cleanup predicate with a mocked pct boundary. A failed
# pct create is allowed to leave a partial configuration, but the transaction
# must not mark that CT as owned. If a CT was marked created, cleanup must
# re-read its reviewed configuration after stopping it and refuse destruction
# when the configuration has drifted.
source <(awk '/^ct_config_field\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_config_field_count\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_option_matches\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_net0_is_exact\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_rootfs_is_exact\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_tags_are_exact\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_reviewed_field\(\)/,/^}/' "$GATEWAY")
source <(awk '/^target_vmid_state\(\)/,/^}/' "$GATEWAY")
source <(awk '/^target_vmid_is_lxc_for_cleanup\(\)/,/^}/' "$GATEWAY")
source <(awk '/^ct_config_matches_helm_identity\(\)/,/^}/' "$GATEWAY")
source <(awk '/^cleanup_deploy\(\)/,/^}/' "$GATEWAY")
log() { printf '[gateway-fixture] %s\n' "$*" >&2; }
CTID=103
CT_NAME=roadmap
CT_IP=10.0.0.38
CT_ARCH=amd64
CT_CORES=1
CT_MEMORY_MB=2048
CT_SWAP_MB=512
CT_ROOTFS_STORAGE=local-lvm
CT_ROOTFS_SIZE=16G
CT_NET_NAME=eth0
CT_NET_BRIDGE=vmbr0
CT_NET_GATEWAY=10.0.0.1
CT_NET_TYPE=veth
qm() {
	printf "Configuration file 'nodes/pve/qemu-server/%s.conf' does not exist\n" "$CTID" >&2
	return 1
}
PROFILE=production
gateway_canonical_config=$'hostname: roadmap\nunprivileged: 1\nnet0: name=eth0,bridge=vmbr0,gw=10.0.0.1,hwaddr=BC:24:11:12:34:56,ip=10.0.0.38/24,type=veth\narch: amd64\nonboot: 1\nostype: debian\ncores: 1\nmemory: 2048\nswap: 512\nrootfs: local-lvm:vm-103-disk-0,size=16G\nnameserver: 10.0.0.1 1.1.1.1\nsearchdomain: lan\nstartup: order=5,up=10,down=30\ntags: homelab;lan;roadmap;service'
gateway_secret_marker='ct-config-secret-marker-must-not-appear'

# A QEMU guest occupying the reviewed VMID must fail status before pct is
# consulted; otherwise pct config would report "not found" and status would
# incorrectly return current_sha=none for the user's VM.
gateway_status_script="$fixture/roadmap-gateway-status-test.sh"
sed -e '/must run as root/d' -e '/root deployment directory owner is invalid/d' \
	-e "s|DEPLOY_ROOT=/var/lib/roadmap-deploy|DEPLOY_ROOT=$fixture/gateway-status-deploy|" \
	"$GATEWAY" > "$gateway_status_script"
chmod 0755 "$gateway_status_script"
gateway_status_calls="$fixture/gateway-status.calls"
gateway_status_output="$fixture/gateway-status.out"
: > "$gateway_status_calls"
mkdir -m 0700 "$fixture/gateway-status-deploy"
gateway_status_qm() { printf 'name: ubuntu-gaming\n'; }
gateway_status_pct() {
	printf '%s\n' "$*" >> "$GATEWAY_STATUS_CALLS"
	return 1
}
if (
	export GATEWAY_STATUS_CALLS="$gateway_status_calls"
	qm() { gateway_status_qm "$@"; }
	pct() { gateway_status_pct "$@"; }
	export -f qm pct gateway_status_qm gateway_status_pct
	HELM_GATEWAY_LOCAL_TEST=1 "$gateway_status_script" status
) >"$gateway_status_output" 2>&1; then
	fail 'gateway status accepted a QEMU VMID collision'
fi
contains 'VMID 103 is assigned to a QEMU VM' "$gateway_status_output"
not_contains 'current_sha=none' "$gateway_status_output"
[[ ! -s "$gateway_status_calls" ]] || fail 'gateway status touched pct during a QEMU VMID collision'
printf 'gateway_qemu_vmid_collision_test=ok\n'

# A rejected metadata preflight is read-only too: status may read pct config,
# but must not reach any lifecycle or guest-exec operation after validation
# fails. Keep the bad value secret-like to assert that diagnostics stay fixed.
gateway_status_bad_config=${gateway_canonical_config/10.0.0.1 1.1.1.1/$gateway_secret_marker}
gateway_status_bad_calls="$fixture/gateway-status-bad.calls"
gateway_status_bad_output="$fixture/gateway-status-bad.out"
: > "$gateway_status_bad_calls"
gateway_status_bad_pct() {
	printf '%s\n' "$*" >> "$GATEWAY_STATUS_BAD_CALLS"
	case "${1:-}" in
		config) printf '%s\n' "$GATEWAY_STATUS_BAD_CONFIG" ;;
		*) return 1 ;;
	esac
}
gateway_status_free_qm() {
	printf "Configuration file 'nodes/pve/qemu-server/%s.conf' does not exist\n" "$1" >&2
	return 1
}
if (
	export GATEWAY_STATUS_BAD_CALLS="$gateway_status_bad_calls"
	export GATEWAY_STATUS_BAD_CONFIG="$gateway_status_bad_config"
	qm() { gateway_status_free_qm "$@"; }
	pct() { gateway_status_bad_pct "$@"; }
	export -f qm pct gateway_status_free_qm gateway_status_bad_pct
	HELM_GATEWAY_LOCAL_TEST=1 "$gateway_status_script" status
) >"$gateway_status_bad_output" 2>&1; then
	fail 'status accepted a rejected metadata preflight'
fi
contains 'Roadmap CT has an invalid nameserver setting' "$gateway_status_bad_output"
not_contains "$gateway_secret_marker" "$gateway_status_bad_output"
! grep -Eq '^(start|stop|destroy|exec|status) ' "$gateway_status_bad_calls" \
	|| fail 'status reached a lifecycle or guest-exec operation after config rejection'
printf 'gateway_status_read_only_config_rejection_test=ok\n'

# Installing the same gateway under the exact beta basename must select only
# CT 106 and the beta host staging root. The selection is not controllable by
# SSH_ORIGINAL_COMMAND or an environment variable supplied by the key holder.
gateway_beta_script="$fixture/helm-beta-deploy-gateway"
sed -e '/must run as root/d' -e '/root deployment directory owner is invalid/d' \
	-e "s|/var/lib/helm-beta-deploy|$fixture/gateway-beta-deploy|g" \
	"$GATEWAY" > "$gateway_beta_script"
chmod 0755 "$gateway_beta_script"
gateway_beta_calls="$fixture/gateway-beta.calls"
gateway_beta_output="$fixture/gateway-beta.out"
: > "$gateway_beta_calls"
mkdir -m 0700 "$fixture/gateway-beta-deploy"
gateway_beta_qm() {
	printf "Configuration file 'nodes/pve/qemu-server/%s.conf' does not exist\n" "$1" >&2
	return 1
}
gateway_beta_pct() {
	printf '%s\n' "$*" >> "$GATEWAY_BETA_CALLS"
	return 1
}
if ! (
	export GATEWAY_BETA_CALLS="$gateway_beta_calls"
	qm() { gateway_beta_qm "$@"; }
	pct() { gateway_beta_pct "$@"; }
	export -f qm pct gateway_beta_qm gateway_beta_pct
	HELM_GATEWAY_LOCAL_TEST=1 "$gateway_beta_script" status
) >"$gateway_beta_output" 2>&1; then
	fail 'beta gateway status fixture failed'
fi
contains 'current_sha=none' "$gateway_beta_output"
contains 'config 106' "$gateway_beta_calls"
not_contains 'config 103' "$gateway_beta_calls"
contains 'if [[ "$PROFILE" = beta ]]; then' "$GATEWAY"
contains 'pct exec "$CTID" -- systemctl is-active roadmap.service' "$GATEWAY"
contains 'if [[ "$PROFILE" = production ]]; then' "$GATEWAY"
contains 'pct exec "$CTID" -- systemctl is-active --quiet cloudflared.service' "$GATEWAY"
printf 'gateway_beta_profile_test=ok\n'

# Beta keeps its own exact identity tags. In particular, production's homelab
# marker must not make a beta CT appear owned by the beta gateway.
PROFILE=beta
ct_tags_are_exact 'beta;lan;service' \
	|| fail 'beta CT tag identity was rejected'
if ct_tags_are_exact 'homelab;beta;lan;service'; then
	fail 'beta CT tag identity accepted the production homelab marker'
fi
PROFILE=production
printf 'gateway_beta_tag_identity_test=ok\n'

gateway_drift_config=${gateway_canonical_config/hostname: roadmap/hostname: unrelated}
gateway_extra_option_config="$gateway_canonical_config"$'\nfeatures: nesting=1'
gateway_extra_net_config=${gateway_canonical_config/type=veth/type=veth,rate=100}
gateway_permuted_tags_config=${gateway_canonical_config/tags: homelab;lan;roadmap;service/tags: roadmap;service;homelab;lan}
ct_config_matches_helm_identity "$gateway_canonical_config" \
	|| fail 'canonical Helm CT configuration was rejected'
ct_config_matches_helm_identity "$gateway_permuted_tags_config" \
	|| fail 'canonical Helm CT configuration with reordered tags was rejected'
if ct_config_matches_helm_identity "$gateway_extra_option_config"; then
	fail 'Helm CT configuration accepted an unreviewed top-level option'
fi
if ct_config_matches_helm_identity "$gateway_extra_net_config"; then
	fail 'Helm CT configuration accepted an unreviewed network option'
fi

gateway_expect_config_error() {
	local name=$1 expected=$2 config=$3
	CT_CONFIG_ERROR='stale diagnostic'
	if ct_config_matches_helm_identity "$config"; then
		fail "gateway config fixture unexpectedly passed: $name"
	fi
	[[ "$CT_CONFIG_ERROR" = "$expected" ]] \
		|| fail "gateway config fixture $name returned unexpected diagnostic: $CT_CONFIG_ERROR"
	[[ "$CT_CONFIG_ERROR" != *"$gateway_secret_marker"* ]] \
		|| fail "gateway config fixture $name leaked uncontrolled config content"
	printf 'gateway_config_%s=ok\n' "$name"
}

gateway_expect_config_error \
	nameserver_invalid \
	'Roadmap CT has an invalid nameserver setting' \
	"${gateway_canonical_config/10.0.0.1 1.1.1.1/$gateway_secret_marker}"
gateway_expect_config_error \
	nameserver_missing \
	'Roadmap CT is missing the reviewed nameserver setting' \
	"$(sed '/^nameserver: /d' <<<"$gateway_canonical_config")"
gateway_expect_config_error \
	nameserver_duplicate \
	'Roadmap CT has duplicate nameserver settings' \
	"$gateway_canonical_config"$'\nnameserver: 10.0.0.1 1.1.1.1'
gateway_expect_config_error \
	nameserver_malformed \
	'Roadmap CT has a malformed nameserver setting' \
	"${gateway_canonical_config/nameserver: 10.0.0.1 1.1.1.1/nameserver:10.0.0.1 1.1.1.1}"

gateway_expect_config_error \
	searchdomain_invalid \
	'Roadmap CT has an invalid searchdomain setting' \
	"${gateway_canonical_config/searchdomain: lan/searchdomain: invalid.example}"
gateway_expect_config_error \
	searchdomain_missing \
	'Roadmap CT is missing the reviewed searchdomain setting' \
	"$(sed '/^searchdomain: /d' <<<"$gateway_canonical_config")"
gateway_expect_config_error \
	searchdomain_duplicate \
	'Roadmap CT has duplicate searchdomain settings' \
	"$gateway_canonical_config"$'\nsearchdomain: lan'
gateway_expect_config_error \
	searchdomain_malformed \
	'Roadmap CT has a malformed searchdomain setting' \
	"${gateway_canonical_config/searchdomain: lan/searchdomain:lan}"

gateway_expect_config_error \
	startup_invalid \
	'Roadmap CT has an invalid startup setting' \
	"${gateway_canonical_config/startup: order=5,up=10,down=30/startup: order=5,up=10,down=20}"
gateway_expect_config_error \
	startup_missing \
	'Roadmap CT is missing the reviewed startup setting' \
	"$(sed '/^startup: /d' <<<"$gateway_canonical_config")"
gateway_expect_config_error \
	startup_duplicate \
	'Roadmap CT has duplicate startup settings' \
	"$gateway_canonical_config"$'\nstartup: order=5,up=10,down=30'
gateway_expect_config_error \
	startup_malformed \
	'Roadmap CT has a malformed startup setting' \
	"${gateway_canonical_config/startup: order=5,up=10,down=30/startup:order=5,up=10,down=30}"

gateway_expect_config_error \
	tags_invalid \
	'Roadmap CT has an invalid tags setting' \
	"${gateway_canonical_config/homelab;lan;roadmap;service/homelab;lan;roadmap;service;other}"
gateway_expect_config_error \
	tags_missing \
	'Roadmap CT is missing a required deployment tag' \
	"${gateway_canonical_config/homelab;lan;roadmap;service/lan;roadmap;service}"
gateway_expect_config_error \
	tags_duplicate \
	'Roadmap CT has duplicate deployment tags' \
	"${gateway_canonical_config/homelab;lan;roadmap;service/homelab;lan;roadmap;service;service}"
gateway_expect_config_error \
	tags_malformed \
	'Roadmap CT has an invalid tags setting' \
	"${gateway_canonical_config/homelab;lan;roadmap;service/homelab;lan;roadmap;}"
printf 'gateway_config_diagnostics_test=ok\n'

gateway_pct_mode=
gateway_pct_config_calls=
gateway_pct_calls=
pct() {
	local action=${1:-} count
	case "$action" in
		config)
			count=$(wc -l < "$gateway_pct_config_calls")
			count=$((count + 1))
			printf '%s\n' "$count" >> "$gateway_pct_config_calls"
			if [[ "$gateway_pct_mode" = drift-after-stop && "$count" -ge 3 ]]; then
				printf '%s\n' "$gateway_drift_config"
			else
				printf '%s\n' "$gateway_canonical_config"
			fi
			;;
		create|exec|stop|destroy)
			printf '%s\n' "$*" >> "$gateway_pct_calls"
			if [[ "$action" = create && "$gateway_pct_mode" = partial-create ]]; then
				return 1
			fi
			;;
		*) return 1 ;;
	esac
}

gateway_pct_mode=partial-create
gateway_pct_config_calls="$fixture/gateway-partial-config.calls"
gateway_pct_calls="$fixture/gateway-partial.calls"
: > "$gateway_pct_config_calls"
: > "$gateway_pct_calls"
ARCHIVE="$fixture/gateway-partial.archive"
printf 'partial archive\n' > "$ARCHIVE"
created=0
healthy=0
if pct create "$CTID" reviewed-template; then
	created=1
fi
[[ "$created" = 0 ]] || fail 'failed pct create incorrectly marked the CT as created'
if (set +e; false; cleanup_deploy) >"$fixture/gateway-partial.out" 2>&1; then
	fail 'partial pct create cleanup unexpectedly succeeded'
fi
! grep -Eq '^destroy ' "$gateway_pct_calls" \
	|| fail 'partial pct create cleanup attempted to destroy a CT'

gateway_pct_mode=drift-after-stop
gateway_pct_config_calls="$fixture/gateway-drift-config.calls"
gateway_pct_calls="$fixture/gateway-drift.calls"
: > "$gateway_pct_config_calls"
: > "$gateway_pct_calls"
ARCHIVE="$fixture/gateway-drift.archive"
printf 'drift archive\n' > "$ARCHIVE"
created=1
healthy=0
if (set +e; false; cleanup_deploy) >"$fixture/gateway-drift.out" 2>&1; then
	fail 'configuration-drift cleanup unexpectedly succeeded'
fi
grep -Eq '^stop ' "$gateway_pct_calls" \
	|| fail 'configuration-drift cleanup did not stop the CT before rechecking it'
! grep -Eq '^destroy ' "$gateway_pct_calls" \
	|| fail 'configuration-drift cleanup destroyed a changed CT'
contains 'refusing to destroy CT 103 after its configuration changed' "$fixture/gateway-drift.out"

