# Release v0.2.0: promote Tickets and public access to production

Status: in progress. Decisions made 2026-10-04 (all recommendations
accepted); see the [progress log](#progress-log).

## Goal

Ship everything on `beta` to production as **v0.2.0**:
- Tickets, alert intake, ticket webhooks and the ticket queue
- Public access with Connect Cloudflare
- Email inboxes

Do it with no lost work and no surprise downtime, with a rollback that is
tested before it is needed, and with `beta` and `main` left in sync so the
next release is easy.

## Where things stand (checked 2026-10-04)

| Fact | Consequence |
| --- | --- |
| `beta` is 126 commits ahead of `main`. `main` has 8 commits `beta` lacks: v0.1.1 (#40) and #35–#37, all squash-merged. | The branches have diverged. `main` must be merged into `beta` before promotion. |
| A trial merge of `origin/main` into `origin/beta` conflicts in 16 files (37 blocks). The biggest are `web/src/App.svelte` (7), `test/e2e/run.sh` (6), `AdminMetrics.svelte` (5), `openapi.json` (3, generated) and `project_intelligence.go` (3). | The merge is a focused job with per-file rules (below), not a click. |
| No migration-number collision: 029–035 exist only on `beta`, and `main` adds none. | Production applies seven additive migrations (029–035) in one upgrade. |
| Production has never run Tickets, intake, webhooks, public access or email. | This is the largest release so far. It needs a rehearsal against real production data and a soak on beta. |
| The production deploy takes an online SQLite backup, then runs `schema-preflight` on a private copy before switching (OPERATIONS.md § Release, backup, and rollback). | Data safety is built in, but the rehearsal should still catch migration surprises before the real deploy. |
| Older binaries deliberately open a database migrated by a newer release (`internal/db/db.go:255`). | Binary rollback to v0.1.1 works without a database restore; v0.1.1 just ignores the new tables. |
| This checkout has uncommitted files. Most are byte-identical to `main` (an uncommitted main→beta merge). `ci.yml`, `App.svelte`, `E2E_TESTING.md`, `package.json` and four tests differ from both branches. Three plan docs exist only here. | Preserve them before anything else, then rebuild the merge cleanly on its own branch. |
| CT 103 (production) and CT 106 (beta) both have `cloudflared`. Beta owns `hooks.shanekanterman.dev` and the `helm-alerts@shanekanterman.dev` routing rule. | Production public access must use a different hostname and address name. |

## Principles

1. **Never lose work.** Snapshot first (Phase 0). Every destructive step
   (discarding local files, deleting data) comes after a verified copy
   exists.
2. **Branches and PRs, never direct pushes** for the sync and the release.
   Pushing to `beta` deploys, and merging to `main` deploys production. CI
   must be green on the PR first.
3. **The release PR carries no new behaviour.** Version, notes, docs and the
   deprecated-config removal only. Refactors and dead-code trims wait for
   the next cycle.
4. **Promote with a merge commit, not a squash.** Squashing is why `main`
   and `beta` diverged. A merge commit keeps one history, so after release
   `main` fast-forwards into `beta` with no conflicts.
5. **Rehearse the upgrade on a copy of production data** before the real
   deploy. Test the rollback the same way.
6. **One environment owns each Cloudflare name.** Beta and production never
   share a hostname, tunnel or email routing rule.
7. **Every phase has an exit check.** Don't start the next phase until it
   passes.

## Phase 0: freeze and snapshot (15 min)

1. Freeze `beta`: no feature pushes until v0.2.0 is out. Fixes found
   during the soak go through the PR flow (Phase 2).
2. Mark the current tip:
   `git tag -a beta-pre-v0.2.0 origin/beta -m "beta before v0.2.0 sync"`,
   then push the tag.
3. Save the local leftovers outside the repo, without touching them:
   - `git diff > ~/backups/helm-local-wip-2026-10-04.patch`
   - copy the untracked files into the same folder
   - record `git status --short` next to them
4. Decide where the three local-only plan docs go:
   `AGENT_CREDENTIAL_LIFECYCLE_PLAN.md`, `RELEASE_BOUND_TASKS_PLAN.md`,
   `VISUAL_UX_ANIMATION_PLAN.md`. **Recommendation:** commit them on a
   separate `docs/upcoming-plans` branch. They describe future work and
   don't belong in v0.2.0.

**Exit:** the tag exists on GitHub, and the patch and copies open and
diff cleanly.

## Phase 1: sync `main` into `beta` (half a day)

On a fresh worktree:
`git worktree add ../helm-sync -b sync/main-into-beta-v0.2.0 origin/beta`,
then `git merge origin/main`.

Resolve each file by rule, not by eye:

| File | Rule |
| --- | --- |
| `openapi.json` | Never hand-merge. Resolve `openapi.yaml` first, then regenerate with `node web/scripts/generate-openapi.mjs`. |
| `openapi.yaml` | Keep both sides: v0.1.1's Luna changes and beta's Tickets, public-access and inbox operations. Run `npm run docs:check` and `openapi:check`. |
| `.github/workflows/ci.yml`, `beta-candidate.yml` | Start from `main`'s runner/gating structure (v0.1.1), then re-apply beta-only steps. Run actionlint. (`HELM_COOLIFY_PROJECT` is dropped in Phase 3, so the sync stays a pure merge.) |
| `test/e2e/run.sh`, `test/e2e/fake-codex` | Keep both: main's stalled-account fault scenarios and beta's fake Cloudflare, relay, hooks listener and Coolify secret. |
| `web/src/App.svelte` | Keep v0.1.1's task-save and bulk-selection fixes and beta's Tickets, ticket-queue and fan-out changes. Afterwards, check the save and bulk-selection race fixes are still present. |
| `AdminMetrics.svelte`, `LunaRunHistory.svelte`, `project_intelligence.go`, `task_draft.go` | Prefer `main` (newer Luna and metrics fixes). Re-apply any beta-only lines the diff shows. |
| `observability.go` | Keep the union of route templates. `observability_test.go` must still pass. |
| `docs/E2E_TESTING.md`, `README.md`, `LUNA_PROJECT_INTELLIGENCE_PLAN.md` | Keep the union of sections. Run the docs check. |
| `web/package.json` (+ lock) | Take `main`'s `0.1.1` for now; Phase 3 bumps it. Regenerate the lock with `npm install`; never hand-merge it. |

Then run the full local gate (`make lint` with SSH variables unset,
`go vet`, `go test ./...`, `go test -race ./...`, `npm run check`,
vitest, full `test/e2e/run.sh`). Open a PR from
`sync/main-into-beta-v0.2.0` into `beta`.

**Exit:**
- CI is green on the PR, and it's merged with a merge commit.
- The beta deploy succeeds.
- `git log origin/beta..origin/main` is empty.
- The local leftovers are now redundant: compare them with the merged
  tree, then discard them.

## Phase 2: harden the release candidate (2–3 days, mostly waiting)

1. **Soak beta for at least 48 hours** with real use, and every
   integration exercised:

   | Flow | Proof |
   | --- | --- |
   | Email from a personal mailbox to an inbox | Ticket in Needs triage, inbox count +1 |
   | Reply to that email | Repeat ×2, no new ticket |
   | Webhook post via the public URL (curl) | `201`, ticket `TKT-n` |
   | Real Coolify alert on the edge listener | Ticket in the queue, assigned |
   | File a ticket into a project | New key, old key redirects, repeats follow |
   | Send test and Test public URL | Both green |
   | Existing boards, task saves, bulk select, Luna | No regressions |

2. **Upgrade rehearsal on production data**, on the operator machine, not
   the server:
   - Take a fresh production backup with `helm-backup`.
   - Copy it locally and run
     `helm migration-preflight <copy>` with the candidate binary. It must
     report integrity ok, foreign keys ok and schema 35.
   - Start the candidate binary on the copy (auth disabled, loopback only):
     the board, issues, existing tasks and agents all load.
   - Check how long the migration takes on real data.
3. **Rollback rehearsal:** start the v0.1.1 binary on the migrated copy. It
   must open the newer schema (it's designed to) and serve boards. Write
   down what v0.1.1 shows for v0.2.0 data: the queue appears as a normal
   "Tickets" project, which is acceptable.
4. **API compatibility check** for existing clients:
   - Agent tokens and scripts still work: `createTicket` on
     `/projects/{p}/tickets` is still served, and `project` is still
     accepted on webhook and inbox creation.
5. Fix anything found through small PRs into `beta`, then restart the
   soak clock for anything risky.

**Exit:**
- The soak checklist is all green, and the preflight and both rehearsals
  passed.
- There are no open bugs tagged for v0.2.0.

## Phase 3: release PR (1–2 hours)

Branch `release/v0.2.0` from `beta`. Same naming as `release/v0.1.1-stability`.

- **Version:** `web/package.json` (and lock) → `0.2.0`. It's a minor bump:
  new features, no breaking API removals. Note that `HELM_COOLIFY_PROJECT`
  is deprecated, not removed.
- **Notes:**
  - Create `docs/releases/v0.2.0.md` in v0.1.1's shape, built from
    `unreleased.md`: Features, Upgrade notes, Database compatibility
    (additive migrations 029–035, rollback-safe), Verification (suite
    counts, rehearsal results).
  - Reset `unreleased.md` to empty.
- **Upgrade notes must say:**
  - Production needs a public hostname and email address name distinct
    from beta's.
  - Inboxes and webhooks are per environment; apps must be re-pointed.
  - `HELM_COOLIFY_PROJECT` is ignored.
  - The ticket queue is created on first use.
- **Plan statuses:** "implemented on `beta`" becomes "released in v0.2.0"
  in TICKET_QUEUE_PLAN, EMAIL_ALERT_INTAKE_PLAN, CLOUDFLARE_CONNECT_PLAN
  and the related plans. Mark this plan as in progress.
- **Not in this PR:** dead-code trims (`AlertIntakeRoute.ProjectRef`,
  033's unused `email_tag_*` columns, always-queue `project_*` response
  fields). Track them as a v0.2.1 cleanup ticket.

Open a PR from `release/v0.2.0` to `main`. The description has:
- a summary
- the migration list
- rehearsal results
- rollback steps
- links to the soak evidence

**Exit:** CI is green, you've reviewed the diff (docs and version only on
top of the soaked beta), and the PR is approved.

## Phase 4: promote and deploy (1 hour, watched)

1. Merge the PR **with a merge commit**, at a quiet time when you can
   watch for an hour. The merge triggers the production deploy: online
   backup, preflight, release switch, live validation, automatic rollback
   if validation fails.
2. Watch the run to the end. Then confirm:
   - `/api/v1` reports the merge SHA.
   - The board, task save and Luna work.
   - The Cloudflare Access boundary is unchanged: the dashboard still
     needs sign-in.
3. Tag `v0.2.0` on the merge commit and publish a GitHub release from
   `docs/releases/v0.2.0.md`.

**Exit:** production is on v0.2.0, live validation is green, and the tag
and release are published.

## Phase 5: turn on production public access (30 min)

1. In production, go to Tickets → Connect apps → **Sign in with Cloudflare**.
2. **Hostname:** use one distinct from beta, for example
   `hooks.helm.shanekanterman.dev`. The name is your choice; it just
   mustn't be `hooks.shanekanterman.dev`. Make sure no Cloudflare Access
   application (including a wildcard) covers it, or webhooks get a login
   redirect.
3. **Email:** use a distinct address name, for example `helm` (so
   addresses look like `helm+homelab-alerts-x7k2qm@shanekanterman.dev`).
   Add a fallback address so alerts are never silently lost.
4. Create production inboxes and webhooks. Re-point each homelab app one at
   a time:
   - change the address in the app
   - send a test
   - see the ticket arrive
   - move on to the next app
5. Coolify: point its webhook at a production webhook URL, or keep the
   edge-listener route if production gets one. Check one real alert.

**Exit:** each production integration has delivered one real ticket.

## Phase 6: close out (30 min)

- Bring `beta` up to date with `main` (it should fast-forward after a
  merge-commit promotion). Confirm `git log origin/beta..origin/main` is
  empty.
- Beta hygiene:
  - Turn off the "Email test (safe to delete)" webhook.
  - Complete or delete the test tickets (TKT-1, TEST-55/56/57).
  - Decide whether beta keeps its own inbox for testing.
- Delete the merged `sync/…` and `release/…` branches and the temporary
  worktrees.
- Open the v0.2.1 cleanup ticket (dead code above) and a ticket to switch
  the repo's promotion setting to merge commits, if GitHub is set to
  squash.
- Mark this plan as released, with dates and links.

## Rollback

| Situation | Action | Data |
| --- | --- | --- |
| Live validation fails during deploy | Automatic: CI switches back to the previous release | No loss: the old binary opens the newer schema |
| A problem found after deploy | Dispatch the workflow from `main` with `rollback_sha=<v0.1.1 sha>` | No loss. Tickets and inboxes stay in the database, unused until roll-forward. |
| Data corruption (not expected; rehearsal guards it) | Stop the service, restore the pre-upgrade backup the deploy recorded (`pre_upgrade_backup=…`) | Loses writes since the upgrade. Last resort, decided by you. |

Fixes after rollback go to `beta` first, then through Phases 2–4 again
(a short soak is fine for a small fix).

## Risks

| Risk | Mitigation |
| --- | --- |
| A bad conflict resolution silently drops a v0.1.1 fix | Per-file rules; the full E2E suite includes v0.1.1's race specs; review the merge diff against `main` before the PR |
| Migrations 029–035 are slow or fail on real data | Phase 2 preflight and timing on a production copy |
| Cloudflare name clash between beta and production | Distinct hostname and address name; Helm also refuses names already routed |
| Access policy intercepts the hooks hostname | Check Access apps before Phase 5; the self-test shows a redirect immediately |
| Beta keeps diverging after release | Merge-commit promotion, plus the Phase 6 sync check |
| Large surface area (first Tickets release) | 48-hour soak with a written checklist; additive migrations; rehearsed rollback |

## Decisions needed from you

1. **Version:** `v0.2.0` (recommended) or something else.
2. **Promotion strategy:** merge commit (recommended) vs squash, as before.
3. **Production names:** the public hooks hostname and the email address
   name (examples above).
4. **Soak length:** 48 hours (recommended) or shorter.
5. **The three local plan docs:** separate branch (recommended), include
   in the release, or drop.

## Progress log

- **Decisions (2026-10-04):** v0.2.0; promote with a merge commit;
  production uses `hooks.helm.shanekanterman.dev` and the address name
  `helm`; 48-hour soak; the three local plan docs go on their own branch.
- **Phase 0 done (2026-10-04):**
  - Tag `beta-pre-v0.2.0` pushed.
  - Local changes saved to `~/backups/helm-local-wip-2026-10-04/` (the
    patch reverse-applies cleanly; untracked copies verified).
  - The plan docs are on branch `docs/upcoming-plans`, with the required
    Status lines added.
  - `data/` (local Codex runtime state) was left in place.
- **Phase 1 (2026-10-04), branch `sync/main-into-beta-v0.2.0`:**
  - All 16 conflicts were resolved by the rules above.
  - A check that every line `main` added still exists in the merge flagged
    only the intentionally replaced lines.
  - Two problems git did not flag were found and fixed:
    - **Deploy jobs:** beta's lines for both deploy jobs had dropped
      `main`'s new `race` gate from `needs`. Restored, so production and
      beta deploys again wait for the race-instrumented browser run.
    - **Duplicated Luna types:** both branches had added the same
      88-line block of Luna and project-intelligence types in
      `web/src/lib/types.ts`, and the merge kept both copies. TypeScript
      merges duplicate interfaces silently, so only one type alias
      errored. One copy removed; a scan of every merged file found no
      other duplicate interfaces or doc headings.
  - **Gate:** `make lint`, `go vet`, `go test -race ./...`,
    `npm run check` and 233 unit tests all passed.
  - **Local note:** `/tmp` is a nearly full 16 GB tmpfs, so run gates with
    `GOTMPDIR`/`TMPDIR` on the home disk.

