# Reviewed native host rebind

After a reboot, the externally provisioned native boot/namespace binding no
longer matches the current host. Ordinary operator transitions deliberately
refuse. Existing origin, bootstrap journal/inventory and historical intents
remain immutable. Do not edit their original context hashes or bootstrap again.

The existing `deploy/helm-admission-operator.py --plan PRIVATE_PLAN` additionally
accepts a reviewed format3 plan. It changes admission authority to CLOSED only;
it does not provision host.json, stop processes, accept images, install profiles,
change floor/target/artifacts, restore data, or start any runtime. The existing
[operator installer and operating premises](TC335_OPERATOR_TRANSITIONS.md)
remain mandatory. Namespace equality is not independent host provenance.

## Review and plan

First independently verify the intended native VM/host and its protected volume,
data/config directory and permanent lock identities, current installed immutable
image/executable, supported profile and alias exclusion. Stop the supported
service and exclude queued/manual unsafe starts under trusted root maintenance.
The old active runtime marker remains active until the rebind switch commits;
the boot mismatch and an appended intent are not runtime revocation.

Independently provision `/srv/helm/operator/host.json` for that verified current
native host, preserving its existing format1 contract and root:root0600 metadata
inside root:root0700 operator directory with trusted nonsymlinked ancestors.
The command has no writer, auto-provisioning path, native context override or
option to bless its own namespace. Because the binding is shared across lanes,
each existing lane needs its own independently reviewed rebind history edge.

Prepare a root-private plan using every field of the ordinary current-pair
operator plan, with exactly these changes/additions:

```json
{
  "format": 3,
  "operation": "native-admission-host-rebind",
  "action": "host-rebind",
  "expected_previous_host_context_sha256": "PRIOR_EFFECTIVE_CONTEXT",
  "expected_host_context_sha256": "CURRENT_EXTERNALLY_PROVISIONED_CONTEXT",
  "expected_history_sha256": "CURRENT_HISTORY_TIP"
}
```

This is a schema fragment, not an executable plan. All three added values must
be lowercase64-character SHA256 digests. The prior effective context comes from
the latest validated intent's `host_context_sha256`, or the immutable origin if
there are no intents. The current context hashes the parsed current host binding
using the existing canonical JSON encoding (sorted keys, compact separators,
trailing newline); it is not necessarily the hash of host.json's raw formatting.
The contexts must differ. The history tip hashes the exact canonical bytes of
the latest intent, or the immutable origin if there are no intents. This CAS is
required in addition to the ordinary marker byte hash and generation CAS.

Preserve the marker's current role and selected image/source/binary. Supply the
exact accepted pair belonging to the ACTUAL current marker, unchanged original
bootstrap plan and target, independently read-back floor41 with its original
activation, fresh independently accepted current-pair proof bundle and fresh
nonce. A pending upgrade's uncommitted pair cannot be adopted by rebinding.
Plans/proofs retain all ordinary private-file, metadata and size restrictions.
The command refuses absent origin rather than creating replacement authority.

Under the permanent existing nonblocking lock, the command validates complete
mixed-version history, pinned authority and private source metadata/identities,
acceptance, unchanged original floor/bootstrap/target/pair, all CAS values and
fresh clean native data/config observations. It rechecks source, binding and
acceptance before switching. A successful new operation increments generation,
preserves selected role/artifact and writes prepared authority. Start remains
forbidden until a separate fresh ordinary activation and parent profile checks.

## Append-only history and interruption

The rebind intent uses format3 with the ordinary intent fields plus
`previous_host_context_sha256`. Its `host_context_sha256` records the destination
context. Its ordinary previous-intent hash extends the same bounded256-record
chain. No extra authority file, history reset, compaction or origin migration is
introduced. Old formats1/2 remain readable and their bytes remain unchanged.

Validation folds the effective context from the origin through each intent:
ordinary close/activate/upgrade must keep it unchanged; only a well-formed
format3 rebind may move from the exact prior context to a distinct new context.
A rebind record also preserves the selected marker role and artifact. Subsequent
ordinary transitions must match the folded current context.

Before a durable rebind intent, the original authority is unchanged. The newly
provisioned host binding alone grants no transition authority; only a reviewed
rebind with matching prior/current contexts and exact chain tip can proceed.

After intent fsync but before marker rename, effective history context has moved
but the old runtime marker remains. The pending rebind cannot be replayed to
activate. A fresh explicit close with the new context and actual marker/pair may
cancel it while preserving every intent. Another reboot requires another fresh
reviewed rebind using the latest intent's context and history tip. Rebind itself
can cancel a prior pending transition, including a pending upgrade, using the
pair resolved from the actual marker. Unknown partial marker temps still refuse
and require explicit manual reconciliation; no command silently removes them.

Rename plus authority-directory fsync commits CLOSED authority. A lost response
can be resolved with exact latest read-only replay, including expired proof
inputs, because replay cannot write or renew admission. Current independently
provisioned native context must still match the latest effective history context.
An older replay after another transition refuses. No error response proves that
close succeeded; inspect durable state and never infer runtime revocation from
an intent alone.

## Validation evidence

The [failure inventory](TC335_HOST_REBIND_FAILURES.md) and native synthetic E2E
were written before production code. Run serialized on a clean native DEV host:

```sh
python3 -B test/deployment-policy/operator_host_rebind_e2e.py --binary PATH_TO_GUARDED_HELM
python3 -B test/deployment-policy/operator_native_e2e.py --binary PATH_TO_GUARDED_HELM
python3 -B test/deployment-policy/operator_upgrade_e2e.py --binary PATH_TO_GUARDED_HELM
```

The existing root fixture launcher restricts all writes/cleanup to exclusively
created synthetic fixtures and captures bounded receipts under
`.artifacts/tc335/bootstrap/operator-host-rebind-native-*/`. The test explicitly
seeds synthetic prior-boot history, then performs all new rebind operations with
unpatched verification of the real current native host binding. It checks actual
guard rejection while prepared and selected-binary admission after a separate
ordinary activation, preservation of original origin/journal/inventory/intent
bytes, lock/source identity/content/mtime and floor, refusal gates, pending
rebind cancellation and cancellation of a prior-boot pending upgrade.

This is not evidence of an actual reboot, independent production host provenance,
or TC336 acceptance. An isolated disposable-VM reboot can separately exercise
external provisioning and the whole runbook. Never reboot beta to manufacture
test evidence or replace live deployment acceptance with a synthetic receipt.

Native DEV GREEN receipts using the independently extracted static16aa guarded
binary are `operator-host-rebind-native-9961ed895461`,
`operator-native-ec3e4b586a4f`, and `operator-upgrade-fd093de7e416` under the receipt
directory above. The first contains all three rebind journeys. Earlier local
runs correctly refused empty mountinfo from orphaned task-owned Chrome previews;
the parent closed only those previews, without changing observation policy.
