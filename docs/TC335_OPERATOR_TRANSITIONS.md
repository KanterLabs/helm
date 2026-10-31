# Native operator admission transitions

`deploy/helm-admission-operator.py --plan PRIVATE_PLAN` is a separate native-root
operator command. It changes admission authority only: no Coolify requests,
process stopping, image deployment, profile installation, floor API writes,
backup restoration, signing service or CI host-write channel. Existing CLOSED
bootstrap commands and runtime marker v1 stay unchanged. The installed observer
still returns unknown and is not a fence. Automatic deployment remains denied.

This is a bounded operator path for a parent-controlled rollout. Source journeys
are synthetic and do not accept a real TC336 artifact. The parent still owns
reviewed installation, independently protected acceptance, live stop/profile
inspection, external floor readback, immutable image provenance, HTTPS readiness
and current-data recovery verification.

## Installer premises

Provision `/srv/helm/operator/host.json` independently in VERIFIED native host
context, using the existing host-binding format from
[the CLOSED bootstrap contract](TC335_CLOSED_BOOTSTRAP.md). This command does not
create that file or expose a flag that blesses its own namespace. Both native
init namespaces and the bound boot must match. Reboot requires independently
reviewed rebinding; a changed binding cannot rewrite existing operator history.

Before deployment, the parent must verify directory data/config binds, legacy
UID65532 without ownership capabilities, UID70541 reserved for guarded runtimes,
NoNewPrivs, no supplementary groups or capabilities, a read-only authority mount,
the reserved executable entrypoint and no file-only/alternate-path aliases that
bypass directory exclusion. Stop the supported service and exclude queued unsafe
starts and manual deployment while maintaining trusted root serialization under
the permanent lane lock. These are installer and operating premises; clean proc
observations do not establish them. Deliberate privileged bypass is outside the
supported boundary.

Independently extract and hash the executable from EACH exact immutable accepted
image. Acceptance authenticates image/source receipts; its schema does not bind
an executable SHA256. The reviewed root-private plan supplies that separately
verified image-to-binary binding. No generated example or copied image label can
replace this installer check. Candidate/recovery images AND source revisions must
be distinct. Their executable hashes may legitimately match.

## Root-private plan

Every plan, acceptance bundle and referenced proof must be a single-link regular
root:root0600 file in a root-owned private directory, with trusted nonsymlinked
ancestors. Each is at most20,000 bytes. Unknown/duplicate JSON keys, unsupported
formats and unsafe paths refuse. Lane authority paths are fixed under `/srv/helm`; there is
no environment override. Only the library's explicit `base` argument exists for
exclusively created native DEV fixtures.

The plan has exactly these fields. This complete example is SYNTHETIC and cannot
be used for deployment. All coordinates, target values, paths, hashes and nonce
must be supplied from independently verified live inputs:

```json
{
  "format": 1,
  "operation": "native-admission-transition",
  "action": "close",
  "lane": "beta",
  "role": "candidate",
  "target": {
    "lane": "beta",
    "service_uuid": "synthetic-service",
    "service_name": "synthetic-beta",
    "project_uuid": "synthetic-project",
    "environment_uuid": "synthetic-environment",
    "server_uuid": "synthetic-server",
    "site_url": "https://synthetic.invalid",
    "api_url": "https://synthetic.invalid/api/v1",
    "data_identity": "synthetic-volume/roadmap.db"
  },
  "artifacts": {
    "candidate": {
      "image": "ghcr.io/kanterlabs/helm@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "binary_sha256": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
    },
    "recovery": {
      "image": "ghcr.io/kanterlabs/helm@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
      "sha": "cccccccccccccccccccccccccccccccccccccccc",
      "binary_sha256": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
    }
  },
  "authorization_floor": {
    "format": 1,
    "minimum": 0,
    "target": {
      "lane": "beta",
      "service_uuid": "synthetic-service",
      "service_name": "synthetic-beta",
      "project_uuid": "synthetic-project",
      "environment_uuid": "synthetic-environment",
      "server_uuid": "synthetic-server",
      "site_url": "https://synthetic.invalid",
      "api_url": "https://synthetic.invalid/api/v1",
      "data_identity": "synthetic-volume/roadmap.db"
    }
  },
  "bootstrap_plan": "/srv/helm/operator/synthetic-bootstrap.json",
  "acceptance_bundle": "/srv/helm/operator/synthetic-tc336/bundle.json",
  "acceptance_sha256": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
  "expected_marker_sha256": "1111111111111111111111111111111111111111111111111111111111111111",
  "expected_generation": 1,
  "operation_nonce": "2222222222222222222222222222222222222222222222222222222222222222"
}
```

`expected_marker_sha256` hashes the exact current marker FILE BYTES, including
its newline. `expected_generation` is its exact generation. Generate a fresh
64-character lowercase hexadecimal nonce for each attempt; never reuse one.
The original bootstrap plan remains mandatory and immutable. Its candidate must
match the accepted candidate image, source and separately checked executable.

`action` is `close` or `activate`; `role` is explicitly `candidate` or `recovery`.
`close` writes a new prepared marker pinned to that accepted role. It also works
on already prepared authority, allowing fresh close/rebind after interruption.
`activate` is allowed only from prepared authority, with an independently
read-back external floor41. Every committed operation increments generation.

Both commands invoke the EXISTING `rollback_policy.Acceptance` with the exact
target, protected bundle hash, distinct candidate/recovery pairs and all eleven
proof exchanges. Inputs must be fresh before intent and before marker switch.
A malformed, tampered, stale or incomplete bundle refuses. These readers validate
trusted receipts; they do not produce TC336 runtime acceptance.

The first close may receive the actual external floor0. It persists the existing
`Acceptance.advanced` result locally as floor41 and returns that exact floor for
the parent to PATCH/read back separately AFTER close succeeds. Initial activation
with an already independently read-back exact floor41 is also supported. First
operator authority must use that bundle's exact original accepted activation.
After origin exists, ALL plans require floor41 exactly equal to its saved original
activation, even for close. Receipt issuance does not attest the remote Coolify
value; the parent supplies its independently inspected value. A renewed receipt
bundle may have a new checksum with the same target/pair, while original floor
activation and original pair/binary bindings remain unchanged.

## Durable transition and interruption

The command opens existing authority without creating a lock, obtains the
permanent exclusive nonblocking lease, and checks root:70541 directory0750,
marker/lock0640, directory/lock identity and marker hash/generation CAS. Busy
lease refuses before any write. First use independently authenticates complete
bootstrap journal3, its paged inventory and prepared marker; unfinished bootstrap
cannot be activated. Source inventory pins both trees, metadata and identities.
Nonlink source objects must be70541:70541 and private; directories are0700 and
roadmap.db is0600. Opaque symlinks are never followed or modified.

While holding that lock, obtain complete fresh native data AND config process
observations, require both clean, rehash source and recheck binding/authority.
The bounded existing process-set-churn retry policy is unchanged; unknown,
retained descriptors/mappings/namespaces or native unsafe actors refuse.

`operator-origin.json` is an immutable root-private origin binding original
bootstrap metadata, native context, initial prepared marker, config identity,
exact target, accepted pair/binary coordinates and original floor41 activation.
Up to256 sequential canonical `operator-intent-NNNNNN.json` files form a hash
chain. Each includes the exact previous and desired marker, plan digest, native
context and BOTH observation digests. Each file is at most20,000 bytes. Both
origin and intent are fsynced before creating and fsyncing the0640 marker temp;
rename atomically changes marker.json and the directory is fsynced before success.
The permanent lock inode is never replaced or recreated. Original bootstrap
journal/pages remain immutable and authenticated on subsequent transitions.
There is no history compaction/reset command when this bounded history is full.

An interrupted activation before marker rename leaves prepared authority. Its
pending intent cannot be replayed to activate. A fresh explicit close, with
current hash/generation and fresh acceptance/observations, may supersede it and
bind a new role/nonce; canceled intent remains in history. A complete latest
transition replay is read-only, even after acceptance expires, because it cannot
create or renew admission. Older replay after a subsequent transition refuses.
A lost response after successful rename is resolved through this read-only replay.

Closing active authority linearizes at marker rename plus directory fsync.
Interruption BEFORE rename leaves the previously accepted active marker: pending
intent is NOT itself runtime revocation. The current14-field runtime reader does
not inspect operator intent. Parent MUST stop the supported service first and
MUST NOT patch image/profile/floor or start until close returns success. On error,
state is uncertain and requires read-only reconciliation. Root-held lease excludes
concurrent supported runtimes during the operation; the old accepted executable
is the only binary the old active marker could admit after lease release.

Unknown files (including interrupted operator-marker.next), partial/malformed
history, lost origin/marker/lock, foreign metadata or unexpected source changes
refuse without cleanup or repair. An activation error can leave committed active
authority if the atomic switch already occurred; only exact latest replay is
read-only success. No error path restores old source, lowers a floor, truncates
history, replaces a lock or resets generation. Trusted root must inspect any
partial temp explicitly; this command does not silently remove it. Never infer
successful close from a failed response.

## Runtime interface and parent sequence

Marker format remains1 with exactly the existing14 fields, at most4096 bytes.
`phase=prepared` is CLOSED and actual Helm rejects it; `phase=active` is admitted
only for the selected executable bytes, exact data/lock identities and supported
profile. Floor stays exactly41; unknown higher formats/floors refuse. Operator
history lives in separate private files that runtime need not read. This is
compatible with the existing Go marker reader. Bootstrap replay after operator
history creation intentionally refuses its exact original file-set check: the
operator owns subsequent close/recovery, and bootstrap cannot reset activation.

The parent-controlled initial sequence is: stop and inspect supported service;
complete CLOSED preparation; privately install exact independently accepted
artifact/profile inputs; close with the fresh current marker CAS; PATCH/read back
the returned external floor41 and accepted image/profile coordinates; reverify
profile/aliases; activate from prepared with new CAS and actual read-back floor41;
start through the reserved guarded entrypoint; verify exact private HTTPS health
and authorization/data preservation. Keep automatic deployment paused/denied.
For failure: stop the candidate, inspect/drain, close with `role=recovery` and
fresh CAS, PATCH/read back recovery coordinates/profile while preserving floor,
activate the accepted recovery with fresh CAS, start and verify. Never restore a
live database or reuse historical unsafe coordinates.

## Native DEV receipts

Failure inventory was written before production code:
[TC335_OPERATOR_FAILURES.md](TC335_OPERATOR_FAILURES.md).
Run explicitly as an operator-capable DEV user:

```sh
python3 -B test/deployment-policy/operator_native_e2e.py --binary .artifacts/tc335/runtime-journey/helm-green
```

The existing `bootstrap_native` launcher uses sudo only for exclusively created
fixtures under `/var/lib/helm-admission-tests`; owned fixture children and test
containers are removed. Captured bounded receipts live under
`.artifacts/tc335/bootstrap/operator-native-*/`. The actual guard's unknown CLI
command proves admission reaches command handling without opening/migrating the
synthetic DB; prepared/wrong-binary cases stop at admission. This is an actual
runtime-marker compatibility check, not an authorization scenario producer.
The recovery marker is deliberately pinned to different synthetic binary bytes,
so the candidate executable must reject it. Actual accepted recovery execution
and current-data preservation remain the parent's TC336 acceptance gate.

The first missing-module RED is `operator-native-9d9a305e8e03`. The initial23-event
GREEN is `operator-native-7e074bc4d15d`. An expanded run
`operator-native-313e72db33f6` correctly refused bootstrap observations while
concurrent immutable-image HTTP tests churned the native process set. No retry
policy was weakened. Serialized expanded GREEN `operator-native-65e1d882231d`
passes30 captured events, including private/tampered/expired proof refusal,
initial floor0 close, malformed existing origin refusal, read-only expired
completed replay, pending cancellation and post-rename lost-response replay.
Synthetic data/config bytes, inodes and mtimes, permanent lock and original
activation survive. DEV kernel7.0 evidence does not replace native verification
on the actual beta host's kernel6.12. No live preparation, installation or
deployment is claimed here.
