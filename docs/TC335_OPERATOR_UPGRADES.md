# Native accepted artifact pair upgrade

The existing native operator additionally accepts a versioned upgrade plan.
This changes CLOSED admission authority only. It never starts/stops a process,
calls deployment or floor APIs, installs an image/profile, restores data or
creates acceptance. Automatic deployments remain denied. The original bootstrap,
operator origin, floor41 target/activation and permanent lock remain immutable.

Read the [operator contract](TC335_OPERATOR_TRANSITIONS.md) for native host
binding, root-private files, exact marker CAS, nonce, supported profile, complete
data/config drain observations and interruption requirements. All apply here.
The [upgrade failure inventory](TC335_OPERATOR_FAILURES.md) predates the change.

## Plan and independent evidence

The upgrade plan uses exactly the existing plan field set, with these values:

```json
{"format":2,"operation":"native-admission-upgrade","action":"upgrade","role":"candidate"}
```

This fragment is not a complete executable plan. Supply all other fields from
independently verified current inputs. The target must exactly match the original
operator target. `bootstrap_plan` is still the exact original bootstrap plan;
do not generate a new bootstrap plan for the new candidate. `authorization_floor`
must be independently read back as minimum41 with its original target and
original activation object, byte-for-byte equivalent as JSON. It is not replaced
with a new acceptance checksum or new pair.

`artifacts.candidate` is a new immutable image/source/binary triple. Its image
and source must each differ from both members of the current accepted pair.
`artifacts.recovery` must equal the CURRENT accepted candidate's exact image,
source AND executable SHA256, including when the current marker selects recovery.
This deliberately makes the previously accepted candidate the next recovery.
An old recovery, same image with changed binary binding or changed source refuses.
Distinct images/sources may legitimately contain identical executable bytes.

The parent must independently produce fresh TC336 acceptance for BOTH members of
the new pair, at the exact target, including every required proof exchange.
Existing `rollback_policy.Acceptance` validates the private bundle/checksum,
target, pair, proofs and freshness, and checks freshness again before switch.
Minimum41's existing activation is preserved through its unchanged floor reader;
no floor schema or policy relaxation is involved. A missing, altered, public,
expired or incomplete proof refuses. Prior candidate acceptance does not stand
in for a fresh accepted recovery receipt in this new pair's bundle.

Acceptance binds image/source, not executable extraction. Independently extract
and hash `/usr/local/bin/helm` from EACH exact immutable image before installing
the reviewed root-private plan. Actual guard execution verifies the selected
executable bytes. Neither a matching source label nor an operator receipt proves
the parent performed this image-to-binary verification.

## Parent rollout sequence

1. Independently accept the new candidate and current candidate as the new pair;
   preserve their immutable image/source/binary bindings and exact target.
2. Stop and inspect the supported service; exclude queued/manual unsafe starts
   and maintain trusted root serialization. Invoke existing format1 `close`
   with the CURRENT accepted pair and fresh marker CAS/nonce. Continue only after
   successful CLOSED receipt. Verify the runtime is stopped and profile/aliases
   meet the existing installer premises.
3. Read back the existing external floor41. Invoke format2 `upgrade` with the
   independently accepted new pair, original bootstrap/floor, and fresh current
   marker CAS/nonce. This increments generation and leaves a CLOSED prepared
   marker selecting the NEW candidate. It cannot upgrade ACTIVE authority.
4. Install and read back the new immutable candidate image and guarded execution
   profile. Preserve the external floor's exact original activation. Reverify
   directory binds, executable/image binding and alias exclusion.
5. Invoke format1 `activate` with the NEW pair and fresh marker CAS/nonce. Start
   through the reserved guarded entrypoint and verify private HTTPS health,
   authorization and current data/config preservation.
6. If recovery is needed, stop/inspect/drain, format1 close with the NEW pair and
   `role=recovery`, install/read back that exact accepted recovery/profile,
   activate with fresh CAS/nonce and verify. Never restore a live database.

The operator has no CLI option to bless a container namespace, rewrite origin,
lower/reset a floor, replace a lock, prune history or bootstrap again.

## Durable history and interruption

Existing format1 origin/intents/plans remain readable. A format2 upgrade intent
adds `artifacts` and `acceptance_sha256` to the existing intent fields. It binds
the exact independently checked new pair and bundle into the same append-only
hash chain. Before/after markers, generation, nonce, native context and both
source-observation hashes retain the existing constraints. All transitions count
toward the existing256-record bound, with each maintained file at most20,000
bytes. The runtime marker keeps its existing14-field format1 schema.

History validates each pair rotation from the pair selected by the preceding
marker. Subsequent format1 close/activate must use the effective current pair;
they cannot change it. Missing, reordered, malformed or edited upgrade history
refuses. The original accepted pair stays saved in immutable origin. Its old
plans cannot reactivate it after a committed upgrade; accepted recovery then
exists only in the new pair and requires a fresh close/activate sequence.

Upgrade persists intent before atomic marker switch. Before-switch interruption
leaves the prior CLOSED marker and pair; pending upgrade replay refuses. Any
partial temp is uncertain and requires trusted root inspection. Once a trusted
operator reconciles an exclusively known temp, only a fresh explicit format1
close using the still-current PRIOR pair can cancel the pending intent. The
canceled upgrade remains in history; retry upgrade uses new CAS and nonce.
Do not infer committed upgrade from a failed response.

After-switch interruption has committed the new prepared marker and pair. Only
the exact latest plan replay returns read-only success, even after acceptance
expires; it never switches a marker or renews admission. Older plan replay
refuses after another transition. Busy lock and stale CAS refuse without writes.

## Native DEV evidence

```sh
python3 -B test/deployment-policy/operator_upgrade_e2e.py --binary /path/to/actual/guard-binary
```

The existing native-root launcher exclusively owns and removes synthetic
fixtures. Bounded sanitized receipts go to
`.artifacts/tc335/bootstrap/operator-upgrade-*/`. This test executes the actual
unchanged guard and a copy with benign ELF trailing bytes, providing different
real executable hashes without rebuilding or changing guard code. Unknown CLI
command handling proves admission without opening/migrating the synthetic DB.
New candidate activation rejects old bytes; explicit accepted recovery admits
them. Tests cover stale/tampered/missing inputs, wrong recovery, busy lock,
retained config descriptor, old-plan replay, both interruption boundaries,
history mutation and preservation of data/config, lock, origin, bootstrap/floor.
These synthetic receipts are not TC336 acceptance or permission to deploy.
