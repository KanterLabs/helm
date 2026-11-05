# Reviewed native host rebind failure inventory

Written before the host-rebind implementation and its native synthetic journey.
Rebinding is an explicit, reviewed CLOSED operation after independently verifying
native host identity and provisioning the fixed root-private host binding. It
never provisions a host binding, rewrites origin/history, or starts a runtime.

Required refusal cases:

- Root/native context unavailable; current boot or init namespaces differ from
  the independently provisioned host.json; public, linked, malformed or unsafe
  binding/plan/proof input; absent existing origin; unfinished bootstrap.
- Ordinary close/activate/upgrade on a changed context; arbitrary context jumps
  in ordinary history; malformed rebind schema; same-context rebind; unexpected
  prior/new context, history tip, marker bytes/generation, or reused nonce.
- Busy/missing/replaced permanent lock; unknown authority files, partial temp,
  missing/gapped/tampered history, changed bootstrap metadata or source roots.
- Changed accepted pair, target, original floor41 activation, data/config/lock
  identities; decreased floor; missing, altered, expired or incomplete accepted
  current-pair proof; retained data/config access or uncertain observations.
- Rebind from a pending transition must resolve the pair from the actual marker,
  including a canceled pending upgrade. It must never adopt the uncommitted pair.
- Rebind must preserve marker role and selected artifact, increment generation,
  and produce prepared authority. It cannot activate or change an artifact.

Interruption boundaries:

1. External host provisioning precedes this command. Before a durable intent,
   existing history remains unchanged and only reviewed rebind may bridge the
   mismatch. Failed operations must not clean up or bless current context.
2. After durable rebind intent and before marker rename, the old marker remains.
   The parent must already have stopped supported writers and excluded unsafe
   starts; the intent is not runtime revocation. Its destination becomes the
   effective context for subsequent history. Activation/pending replay refuses;
   a fresh close can cancel it, retaining the intent. After another reboot a
   fresh reviewed rebind may cancel it using the new expected chain tip/context.
3. Any interrupted marker temp remains unknown authority requiring explicit
   manual review. No automatic deletion or retry may erase it.
4. Rename plus directory fsync commits CLOSED authority. A lost response is
   resolved only through exact latest read-only replay. Replayed rebind cannot
   renew admission, even with expired acceptance, and cannot bypass host context.

Native validation must capture bounded receipts and actual guard rejection of
prepared authority, then independently activate with a fresh ordinary plan and
prove the selected actual binary reaches command handling. Hash every original
origin/journal/inventory/intent before rebind and compare afterward; also compare
source bytes/inodes/mtimes, permanent lock identity and original floor. A seeded
prior-boot history fixture is synthetic evidence, not evidence of a real reboot
or live host provenance. A disposable-VM real reboot can separately verify the
full external provisioning/runbook sequence without rebooting beta.
