# TC-335 failure inventory (before implementation)

All journeys use synthetic immutable pairs, a simulated Coolify request ledger,
and a persistent in-memory database identity/write ledger. They exercise the
real deploy orchestration with only its I/O replaced. They do not accept real
images, run applications, touch live hosts, or certify runtime authorization.

## Acceptance and provenance

Missing first-activation proof must reject before any mutation. Reject wrong
candidate/recovery digest or SHA, unknown/missing capability, wrong lane/service,
expired/future/malformed JSON, duplicate keys, traversal/symlink/oversized proof,
checksum mismatch and failed runtime observations. A protected checksum anchors
the complete bundle; a self-declared migration label/boolean is insufficient.
Release preparation and publication must reject absent/mismatched policy
provenance and preserve the exact beta/production acceptance identifiers.

## Durable floor and replacement

Missing/duplicate/hidden/malformed floor is an error, including first activation.
Unknown higher floors cannot decrease. Stop the old primary before advancing
the floor and before starting anything. Neither cached `exited` nor an offline
HTTP route establishes this. A protected checksum-pinned observation helper
must bind the fresh nonce, lane/service, persistent database/volume, old exact
pair/container identity, observation interval and continuing exclusion fence.
Reject surviving or restart-eligible writers, queued/concurrent operations,
missing/expired fence and ambiguous host results. Reobserve the same fence
immediately before each start; a single snapshot is insufficient. Parent must
provide the real observer and fence integration before rollout.

The floor is written and read back before coordinate PATCHes/start, never
reverted with image/revision. Model floor PATCH failure/lost response, first
and second coordinate PATCH failure/lost response, and interrupted activation.
Unknown state fails closed. Replays require fresh accepted evidence, an intact
floor and exact readiness. Direct legacy targets remain ineligible.

## Recovery and preservation

Candidate may commit migration/write then fail readiness. Stop it and prove
continuing exclusion before selecting only the explicitly accepted recovery
pair. Keep database identity and all current writes. Verify exact recovery
revision and readiness. Failure remains a failure even after verified recovery.
If recovery fails, stop it where possible and never select the previous legacy
pair. Keep every historical image/backup. No data restore or deletion exists in
this controller. HTTP/TLS redirects, target drift, malformed API responses,
private origin, persistent mounts/secrets, branch-tip checks and lane locks
remain existing safeguards; workflow integration is parent-owned.

## Parent review regressions

- Floor loss during an already-current replay must stop the writer. An
  unhealthy service must not defer metadata validation until route success.
- A different valid floor after startup/replay must fail rather than publish
  successful evidence containing the earlier floor checksum.
- Candidate and recovery must be distinct immutable images and source
  revisions; a second label for the same failing artifact is not recovery.
  Retained release evidence must enforce this distinction too, rather than
  accepting a receipt the deployment controller would reject.
