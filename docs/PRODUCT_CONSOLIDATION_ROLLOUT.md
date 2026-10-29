# Helm consolidation — audit, retirement and release gates

Companion to [the product plan](PRODUCT_CONSOLIDATION_PLAN.md) and
[implementation queue](PRODUCT_CONSOLIDATION_WORK.md). These execution gates
do not certify an upgrade or authorize irreversible resource deletion.

## Phase 7 — Reintroduce audits only when they earn trust

Reuse TC-311, currently 2/5 in handoff. Do not create a second competing audit
rewrite or merge its draft merely because CI passes. The separate Sol profile
and validated evidence boundary are already implemented on the audit branch;
the real assessment still failed strict basis validation.

First align output schema/prompt constraints with validation without weakening
citation, unknown or lifecycle rules. Improve retrieval of the relevant
operating context and invocation path instead of chasing unrelated code. Use a
fixed, reviewed reference packet with complete, partial, contradictory,
unverifiable and PR-head-only cases. Keep the original ordinary-language case
unchanged. Matching destinations with irrelevant citations do not pass.

Actual model acceptance requires correct relevant evidence, a supported
completion, a supported reopening and honest unknowns; no unsupported Done or
reopen is allowed. Synthetic integration and actual model results remain
separate evidence. Use bounded attempts with a written hypothesis; retain each
failure rather than resetting history or retrying until a green sample appears.

Then verify legitimate owner-human preview, completion, reopening and stale
selection behavior. A tagged service client's intended authentication denial
does not justify copied owner assertions, injected sessions or a password.

**Exit gate:** TC-311's actual judgment, human lifecycle, preservation, CI and
exact beta promotion checkpoints all pass. Only then restore the normal audit
entry point. The reduced core can release earlier with an honest parked state.

## Phase 8 — Remove dead weight and release in controlled slices

Once consumers are gone, remove obsolete code, dependencies, config and tests.
Preserve required aliases and history readers during the compatibility window.
Do not drop old tables for tidiness. Rewrite README, help, OpenAPI and config
examples around the smaller product and actual capability/retirement states.

Suggested beta slices: trust/transfer safeguards; task-entry and board changes;
Intake/Work/Progress navigation; AI retirement; email retirement after drain;
online PWA transition. Each slice has a coherent gate and an image-only rollback
path. Do not combine every phase into one large merge or stack all later work
onto the unfinished audit PR.

Run selective local checks for each change and the required CI for each PR.
Reuse the meaningful existing browser journeys; run the final representative
integration suite on the accepted candidate rather than repeatedly rerunning a
full local matrix. Keep one runner VM and the established concurrency limits;
new/modified jobs use homelab or homelab-heavy per workload.

Before beta or production changes, take and verify a fresh online backup,
check populated upgrades, integrity/FKs and retained rollback-binary read/write
compatibility. Preserve unrelated data and all late user writes. A rollback
changes the image; restoring an older database is a separate destructive action.

Use the existing release flow: exact tested/ready remote beta revision →
`make prepare-release VERSION=vX.Y.Z` → reviewed production PR → protected
deployment → exact live revision/readiness verification → `make finish-release`.
Publish canonical Gitea before the GitHub mirror wherever the existing workflow
requires it. Never copy beta demonstration data into production.

**Exit gate:** owner acceptance of the main desktop/phone journeys, trustworthy
transfer, unchanged retained data/permissions/history, verified exact revision,
release notes listing cuts and remaining adapters, and a named owner for any
remaining deprecation observation. Health and green CI alone are insufficient.

## Execution rules

- Future implementation cards stay unclaimed Backlog until execution starts.
  Existing TC-8 and TC-311 are reused without claiming them again for planning.
- Use Opus 5.5 for UI/design decisions when executing the relevant cards;
  confirm provider availability then and do not silently substitute it.
- Serialize shared routing/state refactors, migrations, lockfiles, generated
  assets and repository-wide builds. Independent modules can be reviewed in
  parallel when specifically delegated with explicit ownership.
- Write failure inventories and failing meaningful user-flow regressions before
  implementation. Capture sanitized screenshots/traces/responses. Keep every
  maintained file at or below 20,000 bytes; measure before edits and handoff and
  split by responsibility near 16,000.
- Update the decision/consumer map if inspection reveals a shared dependency.
  Preserve the agreed smaller scope rather than adding a replacement platform.
- Evaluate usage and feedback during beta to confirm whether an optional retained
  feature deserves space. No fabricated time estimates or usage statistics are
  part of this plan.

## Verification packets

1. **Ordinary work:** create by title, choose an assignee by name, add criteria,
   edit safely, find active work, and complete through normal lifecycle guards.
2. **Intake:** report/manual/webhook arrival, triage/classify, file to a project,
   follow an old key, preserve repeats/history, and resolve/reopen a bug.
3. **Agent coordination:** scoped discovery, claim, progress, waiting/handoff,
   permission conflicts and stale reporting; no implication of process health.
4. **Navigation/evidence:** old and new links, saved filters, browser history,
   drawers, PR refresh, merged/task/deployed distinction and Explore return.
5. **Preservation:** populated transfer and upgrades preserve checklist items,
   hierarchy, required completion policy, Focus, dependencies, identities and
   histories. Preview/retry is safe and retained binaries can read/write.
6. **Installed client:** real old-PWA upgrade, explicit update consent, unsaved
   drafts, offline/reconnect, session expiry and physical iPhone acceptance.
7. **Actual audit acceptance, separate:** fixed positive/negative/unknown source
   cases with relevant pinned citations, owner-human completion/reopening,
   stale rejection, durable receipts and temporary-access cleanup.

Each implementation starts with failure cases and the meaningful failing
regression. Reuse existing journeys and capture synthetic screenshots/traces/
responses. Run selective local checks and each PR’s required CI; do not repeat
full local suites without a changed failure hypothesis. Keep every maintained
file <=20,000 bytes and split by responsibility near 16,000.

