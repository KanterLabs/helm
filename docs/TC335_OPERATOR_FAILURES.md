# Native operator transition failure inventory

Written before the operator implementation. All fixtures and proof exchanges in
operator tests are synthetic; they do not accept any TC336 artifact.

1. Missing, foreign or container-generated host binding must refuse before write.
2. Nonprivate, linked, oversized, duplicate/unknown-field plans, bundle or proofs
   must refuse. Acceptance must independently check exact target, distinct image
   and source pairs, all eleven proof exchanges, checksum and freshness.
3. Binary coordinates are an independently installed operator input: Acceptance
   does not authenticate binary extraction. Equal binary bytes are permitted for
   distinct images/sources; actual runtime checks its own bytes.
4. Wrong marker hash/generation, metadata, lane, data/config roots or permanent
   lock identity refuses. Busy lock refuses before intent or marker writes.
5. Complete native observations of BOTH data and config under the permanent lock
   must be fresh and clean. Descriptors, mappings, namespaces, native actors and
   uncertainty refuse. Observations are snapshots, never a continuing fence.
6. Activation is possible only from prepared authority. Every close/rebind and
   activation increments generation, selects an explicit accepted role and
   preserves floor41, original activation, paired coordinates and lock inode.
7. Intent is durable before marker rename. Interruption before rename leaves the
   old marker; an activation retry cannot revive a pending/canceled activation.
   A fresh close can cancel the pending intent without resetting history.
8. Interruption after rename has committed the exact desired marker. Same-plan
   replay is read-only, including after acceptance expires; no renewed admission
   or new writes are implied. Stale replay after later transitions refuses.
9. Interrupted temp writes, missing authority, unknown files, mutated history or
   lost lock refuse. No automatic cleanup, restore, reset or lock replacement.
10. Closing an active marker linearizes only at atomic rename plus directory
    fsync. A crash before rename cannot revoke the previous accepted executable
    with the existing runtime reader. The parent must stop the supported service,
    serialize root maintenance and treat failed close as uncertain; it must not
    patch coordinates/profile/floor or start until a successful close receipt.
11. An actual guard binary must reject prepared authority, accept active authority
    for its exact bytes, reject a different pinned binary and reject again after
    reclose. Synthetic DB bytes/inodes/mtime and lock inode remain unchanged.

Installer acceptance must independently prove native execution, supported UID /
capability / NoNewPrivs profiles, directory binds, no file-only aliases, no unsafe
queued starts and trusted root serialization. This source cannot infer them from
an observer or turn a user-supplied boolean into that proof.

## Accepted pair upgrade failure inventory

Written before upgrade implementation and its native E2E:

1. Upgrade without prior operator origin, from ACTIVE, with old format/action,
   a stale CAS, reused nonce, busy permanent lock or retained data/config refuses.
2. Independently accepted new pair must have the exact existing target. Missing,
   expired, tampered or wrong-checksum proofs refuse before authority changes.
3. Recovery must equal the current candidate's image, source AND binary binding;
   a changed recovery binary or old recovery refuses. New candidate image and
   source must differ. Original floor41 activation and bootstrap remain untouched.
4. Durable pair changes must be hash chained and select the prepared new candidate.
   Tampered/missing upgrade records refuse; ordinary format1 transitions cannot
   introduce a pair change. Old pair plans cannot reactivate after upgrade.
5. Interrupted upgrade before rename remains CLOSED on the prior pair and needs
   explicit close with that prior pair after trusted temp reconciliation. Pending
   upgrades cannot be replayed to renew authority. Post-switch lost response has
   only exact latest read-only replay, including after proof expiry.
6. Actual unchanged runtime guard must admit the selected new executable bytes,
   reject the old executable, then admit old executable only when explicitly
   activated as accepted recovery. Data/config bytes/inodes/mtimes, lock inode,
   bootstrap metadata, operator origin and original floor remain unchanged.
