# GitHub artifact failure inventory

Written before implementation. Synthetic store/HTTP fixtures cover these boundaries;
provider-specific transport cases belong to the provider isolation inventory.

- A stale or missing task version, another actor's live claim, or wrong project
  must reject links/detaches without changing links, version, activity or columns.
- Duplicate URL spellings and verified immutable repository identities must
  converge on one project artifact; one PR may independently link many tasks.
- Detaching one task must retain other links and retained evidence identities.
- Deselecting a repository immediately hides cached metadata; a refresh captured
  under an earlier selection version must fail to publish. Reselecting cannot
  expose the old snapshot until a new successful verification.
- Provider permission loss must hide private cached titles/checks. Transient
  timeout/rate limits retain stale metadata with explicit error and unknown
  summary, never a passing signal. Allowed valid URLs remain unverified on failure.
- A force-push must replace the entire snapshot atomically; checks cannot be
  copied from a previous head. Provider snapshots carry exact checked SHA.
- Board summaries are a bounded local batch: forbidden task rejects the whole
  request; no upstream calls occur on list/summary. Failing beats pending,
  unknown beats passing. Merged/closed do not move Helm tasks.
- Configuration/credential generation changes fence outstanding refreshes.
  Concurrent explicit refreshes join one bounded provider request.
- Additive migration preserves populated task identities, claims, columns,
  events, and all existing audit records. Restart preserves explicit links.
- Background refresh starts explicitly, stops on server shutdown, has a fixed
  work budget, timeout, jitter and retry delay; it never blocks board reads.
- API response artifacts contain synthetic values only, never token material.

Acceptance evidence: focused tests write sanitized response JSON beneath the
`HELM_TEST_ARTIFACT_DIR` (or a retained temporary evidence directory) and print
its location. Integration owns retained
browser screenshots/traces and populated retained-binary upgrade verification.

Response replay privacy gate: an idempotent link response must not replay a
private snapshot after revocation or deselection. Persist only the explicit
link identity in mutation replay storage; hydrate the current authorized
snapshot immediately before writing each response. A repeated request after
credential removal is exercised against a synthetic HTTP provider.

Storage cache budget: publishing a snapshot retains at most 512 disposable
snapshots across the installation, preferring the refreshed object and recent
observations. Eviction preserves durable artifact identities and every link.
The corresponding test populates 513 synthetic rows before the pruning code.

Link response scope gate: creating a PR link can return private provider
metadata, including on idempotent replay. A write-only task token must receive
`insufficient_scope` before any link mutation; linking requires both
`tasks:write` and `tasks:read`. Detaching requires only write permission. The
HTTP regression exercises a real scoped bearer and verifies unchanged task
version and links on rejection, then accepts a reader/writer token.
