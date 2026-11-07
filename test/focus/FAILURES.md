# Focus agent workflow failure cases

Recorded before implementation, 7 November 2026.

- Fresh sessions omit guidance, recovery duplicates it, or hooks add API calls.
- Explicit single-card work gets replaced by unrelated Focus/Backlog work.
- Discovery guesses a project/Focus, leaks foreign cards, or includes completed work.
- Heartbeats, leases, metadata churn, identical progress, and agent notes conceal
  inactivity; ambiguous legacy events are mistaken for substantive changes.
- Changed phase/checkpoints/evidence/next action or human comments go unnoticed.
- A truncated timeline invents an inactivity date; pagination skips work or loops.
- A request/time budget expires without reporting coverage and continuation.
- Invalidated queues combine snapshots or retry without a bound.
- Review mutates records, claims work, or produces a misleading urgency score.
- CLI mutations lose operation IDs, overwrite concurrent writes, clear omitted
  metadata, silently change columns/claims, or hide partial membership failures.
- Completed Focus accepts membership, incomplete work completes, or reopening
  lacks a reason. Legacy release reads and claim-release commands regress.
- Installed skill differs from tested source; beta-only publication is called a
  production skill update; unrelated hook entries or task data are replaced.

Verification uses dedicated classifier/hook isolation tests written before code,
then a real isolated Helm HTTP server/CLI journey with synthetic data and JSON
receipts. Fresh agent validation uses a synthetic fixture and sanitized behavior
transcripts. Production publication and an organic five-session pilot remain
separate from the requested beta deployment and PR preparation.
