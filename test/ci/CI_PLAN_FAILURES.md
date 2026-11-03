# CI plan failure inventory and CLI contract

Written before the classifier and its subprocess journeys.

- Partial, missing, invalid or empty diffs must never select narrow validation.
- Compare captured PR merge-base/base to head, and push before to after. Include
  every commit, not only the last one. Missing history and zero before select full.
- Renames, copies, deletions, unknown paths, shared CSS/shell, auth, API/schema,
  dependency, workflow, test helper and runtime configuration changes select full.
- Embedded guides and runtime/configuration examples are not human-only docs.
- Draft status cannot survive readiness or an advancing base as an old fast claim.
- Ready main PRs always require full validation. Drafts may propose fast even for
  high risk changes, but never claim merge readiness.
- Runtime beta/main pushes require full and may publish/deploy only through the
  existing later admission gates. Docs pushes and manual validation never publish.
- Unknown events/targets, wrong checked-out push source and malformed input fail.
- Shadow mode preserves the existing full contexts; proposed coverage stays explicit.
- Missing, cancelled, skipped and failed selected jobs fail the gate.
- Browser success without execution evidence, wrong source metadata, missing
  reports/phases, failed reports or absent selected test coverage fails the gate.
- Duplicate JSON keys, tampered scope/job inventory, and unsupported plans fail.
- Promotion provenance, evidence age, exact build inputs, superseded validation,
  deployment cancellation isolation and pause remain separate workflow/receipt gates.

`plan.py --event FILE --event-name NAME --repository DIR --output FILE
[--github-output FILE] [--mode shadow|enforced]` reads only local Git objects.
Schema 1 records `event`, `source`, complete `diff.paths` status records,
`policy_hash`, `proposed_scope`, `effective_scope`, `reasons`, `selected_suites`,
`expected_jobs`, `publish`, `deploy`, and `merge_ready`. Output flags are fixed
keys with booleans (`checks`, `browser`, `race`, `container`, `publish`, `deploy`),
scope names and compact `suites_json`; no executable commands are emitted.

`verify-plan.py --plan FILE --results FILE --output FILE
[--browser-evidence DIR]` accepts GitHub `needs` JSON, e.g.
`{"checks":{"result":"success"}}`. Browser evidence is mandatory when selected:
existing `evidence.metadata`, `suites/suites.json`, and nested Playwright
`results.json` files. Every selected spec needs actual passed coverage; conditional
skips from other fixtures are allowed. Full validation additionally needs all five
coordinator phases. No self-reported success or revision alone is accepted.

The receipt uses `schema_version:1`, `format:1`, `passed`/`accepted`, actual
`scope`/`effective_scope`, `proposed_scope`, `source` plus `source_sha`/`source_tree`,
`policy_hash`, `plan_sha256`, `selected_suites`, `executed_suites`, job `results`,
`evidence_sha256`, `browser` digests/actual inventory and `errors`. `jobs` aliases
`results` for workflow consumers. It proves validation, not release authorization.

The initial focused allowlist contains only `NotificationItem.svelte`, mapped to
`e2e/notifications.spec.ts` plus `e2e/helm-shell.spec.ts`. Session-aware components,
shared helpers, styles and application shell remain full. Manual focused runs
execute the explicit core-smoke inventory. Full plans enumerate every tracked
Playwright spec plus the `github` harness marker. `deploy:true` means eligible
for later deployment gates, never permission to bypass pause/admission/readiness.

In phase 1, shadow mode retains full execution for every supported event while
reporting proposed coverage separately. Docs pushes remain publication-ineligible
even while shadow validation runs full. `--mode enforced` activates the proposed
coverage; external branch-protection migration is intentionally separate.

## Promotion scope failures (written before implementation)

- Only an enforced main push may consume promotion proof. PR/manual/beta and
  shadow plans cannot skip current full validation using a supplied proof.
- Rejected, malformed or absent proofs, different source, mutable/untrusted image,
  invalid receipt digest and nonpositive producer identities select full fallback.
- Promotion must bind both promotion and artifact source SHA to the actual HEAD.
- The gate requires a fresh independently resolved proof identical to the bounded
  planned proof. Changed digest/image/source/producer identity must fail.
- Missing, skipped, cancelled and failed selected checks/container jobs fail.
- Promotion receipts record reused full validation with no current browser suites;
  they must never claim that the current run executed a full validation plan.

`plan.py --promotion FILE` optionally consumes a proof resolved independently by
the workflow. Only `--mode enforced`, event `push`, ref `refs/heads/main` may
select `promotion`: `selected_suites:[]`, `expected_jobs:["checks","container"]`.
Other events/shadow ignore this input and retain their ordinary validation.
Invalid supplied main proof selects full with `promotion_fallback:true`; absent
proof preserves the ordinary policy, including docs-only classification.

The bounded `promotion` object contains strict booleans `accepted:true`,
`full_required:false`, `runtime_identity_match:true`; `promotion_sha` and
`artifact_source_sha` both equal the actual checked-out SHA; immutable trusted
`image`; hex64 `receipt_sha256`; and positive integer `producer_run_id`,
`producer_run_attempt`, `artifact_id`. Unknown proof fields are discarded.

`verify-plan.py --promotion-proof FILE` is mandatory for promotion scope and
compares the same normalized identities exactly to the planned proof. The caller
must freshly resolve GitHub origin/expiry/build-input proof immediately before
verification; this CLI does no networking and does not establish artifact origin
itself. A passing receipt records `scope`/`effective_scope:"promotion"`, empty
executed suites/browser, and `reused_full_validation` containing that identity plus
the canonical normalized `proof_sha256` (sorted keys, compact JSON, SHA256). It
cannot satisfy the separate strict-full receipt
producer or imply that current full/browser validation executed.

## Gate job integration failures (written before implementation)

- Missing/malformed/duplicate-key/nonobject NEEDS_JSON fails with a sanitized
  receipt; raw environment values and child output must never reach logs/artifacts.
- A different actual HEAD, dirty tracked checkout, wrong source tree or conflicting
  GITHUB_SHA cannot verify another checkout's plan.
- Promotion rechecks require actual push/main event and matching GITHUB_SHA before
  invoking a candidate resolver. Manual/PR events must never invoke that resolver.
- Missing, failed, skipped and cancelled selected jobs remain the strict verifier's
  decision. A wrapper cannot manufacture successful receipts for missing evidence.
- Fresh candidate and verifier execute as argv subprocesses, with private temporary
  needs/proof/receipt files cleaned after completion; no shell expansion.

## Configured-suite compatibility

CI must run independently of unmerged membership feature work. A revision with
no membership spec/coordinator phase must retain its complete four-phase suite;
a revision containing the membership spec must still require its fifth phase.
Subprocess compatibility journeys remove only that configured spec and prove
four-phase acceptance, then retain it and prove missing/failed membership fails.
