# Event-specific Helm CI

The `Helm CI and deploy` workflow records its proposed and effective plan before
running work. All existing required job names (`checks`, `browser`, `race`,
`container`) remain. The additional `pr-gate` checks actual selected job results,
source identities and browser artifacts; a successful job status alone is not
full-suite evidence.

## Rollout state

`HELM_CI_PLAN_MODE` defaults to `shadow`. Shadow runs the complete current suite
and records which smaller plan would be selected. Set the variable to `enforced`
only after the shadow gate has passed on the branch and required-check migration
has been accepted. The setting is repository-wide, so audit every receiving
branch before changing it. Never remove the old requirements before a working
replacement exists.

No protection rules, runner admission, deployment pause variables or live
services are changed by this implementation. GitHub main currently requires the
four historical contexts; GitHub beta and canonical Gitea need an explicit gate
policy review. GitHub-only required-PR settings must not break canonical mirror
publication. Direct or mirrored runtime pushes still require complete trusted
branch validation before publication/deployment.

## Plans

| Event/change | Enforced scope | Publication/deployment |
| --- | --- | --- |
| Feature-branch push | No workflow | None |
| Draft PR | Fast checks | None |
| Human documentation-only PR/push | Docs checks | None |
| Ready bounded UI PR into beta | Focused E2E plus core smoke | None |
| Backend/auth/schema/API/shared styles/dependencies/CI/unknown paths | Full | None for PRs |
| Ordinary ready PR into main | Full | None |
| Runtime push to beta/main | Full | Trusted output only after the declared gate |
| Main push with accepted exact-source beta proof | Promotion: fresh checks/image smoke/security scan; prior full validation explicitly recorded | Reuse the exact tested digest |
| Manual `fast`, `focused`, or `full` | Explicit validation scope (shadow remains full) | Never publishes or deploys |

The initial UI allowlist is deliberately small: `NotificationItem.svelte` maps
to notifications and shell smoke. Additional components require a reviewed map
and failure-first selection proof. Unknown, renamed, deleted, ambiguous or
unavailable/incomplete changes fall back to full validation. Runtime-embedded
help/config/OpenAPI files are not automatically documentation-only.

PR diffs cover the captured merge base through the entire head, not only the
latest commit. Actual checkout SHA/tree and the captured base/head/merge are
recorded. Missing shallow history falls back to full; a wrong checkout cannot
substitute for the recorded source. PR readiness/base edits are re-evaluated;
`ready_for_review` and `converted_to_draft` explicitly trigger validation.
Edited events currently revalidate rather than allowing a metadata-only edit to
replace a failed code check with skipped green required contexts.

## Required gate and execution artifacts

`test/ci/plan.py` emits bounded enum outputs and `ci-plan.json`. The workflow
passes spec names as arguments, never shell commands. `run-browser.py` invokes
the existing isolated coordinator, retaining race instrumentation, worker cap
and cleanup ownership. A canary with no plan still runs the complete suite.

`gate-job.py` verifies a clean actual checkout, protects the needs document in a
private temporary directory, then calls `verify-plan.py`. Browser validation
requires matching build provenance, race binaries, actual non-failing report
inventory and every configured coordinator phase. Membership is mandatory when
its spec exists at that source revision; older beta revisions retain their
four-phase inventory. Missing/failed/cancelled/unexpectedly skipped selected
work cannot satisfy the gate. Planner failure also leaves historical `checks`
red, preventing an all-skipped legacy gate from passing.

Artifacts:

- `helm-ci-plan-<sha>-<attempt>`: captured plan.
- `helm-e2e-<sha>`: existing sanitized browser and resource evidence.
- `helm-ci-gate-<sha>-<attempt>`: verified current scope and selected results.
- `helm-validation-<sha>-<attempt>`: one `validation.json`, either full trusted
  validation or a separately typed promotion acceptance.

## Trusted image and promotion

PR container jobs have no registry write permission. `publish` is a separate
push-only job, after the selected gate and image checks. It verifies the image
archive checksum and source label before isolated-credential publication.
Credentials are sent through stdin and removed on successful or failed exits.

Full receipts bind actual source/tree, workflow/run/attempt, dependency/policy
hashes, toolchain/settings, complete selected/executed inventory and immutable
binary/frontend/image identities. Creation uses a clean checkout and the actual
matching build outputs. It does not manufacture a receipt from a revision string.

A main candidate is resolved through GitHub APIs: successful canonical beta
push, exact workflow/run/attempt, complete successful validation jobs, immutable
nonexpired artifact ID/archive digest, and matching receipt/input bindings.
Gate resolution repeats that provenance check before accepting reuse. PR,
manual, fork, stale, expired or malformed proof cannot authorize reuse.

Current runtime readiness and admission bind an image to its source SHA. Reuse
therefore requires the **same source SHA**, not merely an equal tree. Different
merge SHAs, VERSION/build/policy/toolchain changes or missing proof run the full
path. This preserves existing identity controls; broad cross-SHA promotion is
not silently enabled. Runtime identity migration would be separate work.

A reused run records scope `promotion`, empty current browser inventory and a
bound prior full-validation proof. Its artifact kind is
`helm-promotion-acceptance`, never a newly claimed full run. The image is pulled,
smoke-tested and freshly scanned; it is not rebuilt just to change a tag.

Existing beta/production pause variables, protected environments, branch-tip
checks, non-canceling deployment locks, immutable-image identities and
admission/readiness/backup/recovery controls remain. Manual validation cannot
start a deployment. No application schema or data format changes are included.

## One VM and runner compatibility

The reviewed single-VM browser selector remains unchanged: trusted main/beta
refs plus explicit opt-in may use `helm-runners` / `homelab-heavy`; public PRs
cannot acquire that private execution boundary. Four isolated fixtures remain
the default. No additional VM, nightly suite or benchmark matrix is added.

Control and PR jobs retain the existing `ubuntu-24.04` compatibility exception:
the current VM admission contract admits browser/canary work, not arbitrary
control/publish/deployment jobs. Moving those jobs to the normal `homelab` or
`homelab-heavy` tier requires the separately tracked runner-policy acceptance.
The strict runner checker covers every new job and rejects unknown selectors.

## Activation checklist

1. Run the CLI/event/workflow/publisher journeys and pinned Actionlint. Verify a
   real shadow Actions run, including the full selected inventory and gate.
2. Review required contexts in both the canonical merge path and GitHub mirror.
   Add `pr-gate` before enabling reduced PR coverage, keeping historical contexts
   while compatible branches transition. Do not enable enforcement on a branch
   whose workflow cannot produce its required gate.
3. Set `HELM_CI_PLAN_MODE=enforced` only after that review. Exercise a draft,
   ready UI, high-risk, docs-only, failed selected job and manual-validation
   path. Retain the explicit full override.
4. Confirm a trusted beta push produces the full receipt without bypassing any
   deployment pause. Validate a same-source main reuse proof and a mismatching
   candidate's full fallback before relying on promotion.
5. Compare source-matched validation latency and failure discovery. Full beta
   validation still precedes deployment; reduced pre-merge coverage means some
   broad regressions may first be discovered after a low-risk merge.

Failure inventories and repeatable CLI journeys live in `test/ci/`. Parent
acceptance evidence is retained under `.artifacts/ci-workflows/`; evidence is
synthetic or sanitized and contains no credentials/private user content.
