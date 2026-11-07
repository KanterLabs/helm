# Focus agent planning changelog for main

This is the complete functional change list for the Focus candidate relative to
main `57ab91282d25dc3ef98473b35f882b2b7411b645`. It includes the fixes already
on beta, rather than describing only the new Focus commit. Main was merged into
the candidate to retain its published screenshot guidance and updater behavior.

## Focus discovery and Backlog review

- Add `focus list`, `get`, `work`, and `review` to the Helm agent CLI. Existing
  `releases list|get`, `release-work`, and `release --task` keep their meanings.
- Review active Focus areas, required work and unassigned open Backlog in one
  explicitly resolved project. Routine review excludes completed Focus history.
- Flag overdue work, blockers, cross-Focus prerequisites, foreign claims,
  inactivity, missing next actions, absent ownership and missing acceptance
  evidence with task keys and suggested next actions. These are distinct review
  signals, not a composite priority score or automatic changes to task state.
- Use 14 elapsed days as the configurable initial inactivity threshold. Human
  comments, scope/state/dependency/checklist changes and structurally changing
  progress/evidence can count as recorded activity. Heartbeats, lease churn,
  metadata changes and identical pulses do not make neglected work look fresh.
- Keep legacy ambiguous events, removed comment authors and incomplete history
  unknown. Never infer activity from `updated_at` or claim that recorded progress
  proves delivery. Compare timestamps chronologically, including fractions.
- Bound project discovery, Focus/task pages, history reads, timeline rows, logical
  requests and elapsed time. Return coverage, explicit continuation, history
  follow-ups and restart instructions when a budget cannot assemble inventory.
  Scope-bound cursors cannot silently switch projects or combine changed inventories.
- Queue references are hydrated only for the bounded inspection slice. Ordering
  uses owned work, available due/blocker fields and stable keys; conclusions
  explicitly describe inspected evidence rather than ranking an unseen project.
- Review performs GET requests only and does not move, assign, claim or close cards.

## Focus management commands

- Add `focus create`, `edit`, `assign`, `unassign`, `complete`, `reopen`, and
  `delete` through existing project-scoped APIs.
- Preserve omitted metadata; support explicit description/date clearing. Resolve
  names within one project and use opaque IDs and strong ETags for writes.
- Preserve operation UUIDs on write failures and retries. Surface stale writes
  without silently fetching a newer version and overwriting another editor.
- Membership accepts repeated task keys, deduplicates task identities, reports
  unchanged assignments, and preserves task columns and claims.
- Report non-atomic batch success/failure/pending work and a safe resume action.
  Refuse to clear another Focus's membership implicitly.
- Require explicit completion with all required work finished, a reason for
  reopening, and an empty active Focus for deletion. Frozen membership remains
  protected. Validate returned mutation identity/version before claiming success.

## Skill guidance and session hooks

- Route relevant project planning and work discovery through existing Focus
  areas even when the user never mentions the feature. Prefer reusing a coherent
  goal over creating a duplicate; preserve an explicit single-card assignment.
- Explain when to propose a new Focus, how to interpret partial/unknown review,
  and how to choose clarify/resume/defer/close recommendations within authority.
- Carry Focus identity, remaining relevant task keys, blockers and next action
  into the current task's handoff; re-read current records in the next session.
- Emit one short static Focus reminder at startup/recovery, including fresh
  sessions without a mapped task and unreadable state. The generic reminder
  needs no credentials or API requests.
- Preserve mapped notes, progress recovery, throttled heartbeats and stop rules.
  Tool-use hooks and heartbeats do not scan Backlog or manufacture review comments.
- Keep detailed workflow/command examples in focused references so the main
  skill stays bounded. Preserve main's screenshot, credential-provisioning and
  self-update guidance. Public skill updates continue to follow GitHub main;
  beta code does not replace installed production skills automatically.

## Additive activity evidence API

- New `task.updated` events include `changed_fields` for actual title,
  description and column changes. An explicit empty array distinguishes
  metadata/no-op changes from historical events that cannot establish scope.
- Add no database migration, reset, backfill, new token scope or persistence
  service. Existing task IDs, versions, event cardinality and API routes remain.
  Older compatible readers can ignore the added payload field.

## Other fixes already on beta

- Rebuild bundled cloudflared with `golang.org/x/net` v0.62.0 to address
  CVE-2026-78661; label that build `2026.9.3-helm.3` with its updated build date.
- Parse the Tailnet agent-tag comma list with whitespace trimming, empty-entry
  removal and deduplication, while retaining strict exact-tag validation.
- If token issuance returns after a session/dialog change, attempt to revoke
  the unseen credential instead of leaving it active silently. If revocation
  fails, show an inventory warning and clear it after the token is revoked.
- Make retained LXC release verification, installation and rollback resolve
  `HELM_*` and `ROADMAP_*` authentication/origin aliases consistently. Reject
  conflicting aliases, duplicate/ambiguous keys and unsafe formatting.
- Recover rollback from a broken live environment using the verified release
  envelope. Preserve beta/production profile separation and support bootstrap
  when no current release symlink exists.

## Verification and delivery boundaries

- `make focus-test` runs skill regressions and a real isolated API/CLI journey
  with synthetic data and repeatable JSON receipts under `.artifacts/focus-*`.
- Dedicated tests cover classifier clocks, CLI HTTP contracts, hook startup,
  updater/install preservation and complete lifecycle/review flows.
- Fresh native-agent scenarios verify unsolicited existing-Focus recommendation,
  explicit-card scope and handoff recovery using recorded synthetic API evidence.
  These are scenario checks, not an organic five-session usage pilot.
- The publication rehearsal installs a committed package using an old updater,
  imports Focus commands, verifies a fresh-session reminder, preserves unrelated
  hooks/credentials/state/trust, and checks a remote-only no-op second update.
- Browser Focus regression, package tests, installed publication and exact beta
  deployment evidence are recorded separately in the PR validation record.
- Keep automatic beta deployment paused while the existing native admission
  path requires fresh candidate/recovery evidence. Warm-runner routing was
  disabled because the repository has zero available self-hosted runners; the
  existing workflow's hosted fallback supplies CI without changing runner pools.
- Main merge, production deployment, production skill distribution and the
  organic pilot are follow-up gates. This candidate does not merge itself or
  auto-complete a Focus merely because its required work is ready.
