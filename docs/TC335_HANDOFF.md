# TC-335 implementation handoff

This is the original worker handoff. Parent review changes and current
acceptance are recorded in [TC335_PARENT_ACCEPTANCE.md](TC335_PARENT_ACCEPTANCE.md).
The original owned-file hashes below identify the worker snapshot only.

Uncommitted owned source in `/home/shane/projects/helm-bugs-beta`, branch
`feat/helm-consolidation`, base
`19dfdeadfc4706a1c322780ba02e84ccee403daa`. No repository commit, push, PR change,
Helm lifecycle call, live credential access, deployment, container/image action,
database operation or restore was performed. Root checkout and `docs/prototypes/`
were left alone. Parent owns integration, publication and rollout; TC-335 and
TC-113 remain incomplete for parent acceptance.

## Result and boundaries

- `rollback_policy.py`: bounded, strict checksum-bound receipt tree; exact
  target/candidate/recovery pairs, known capabilities, eleven runtime scenarios
  per artifact, captured response projections, freshness and monotonic floor.
- `deployment_transition.py`: stop/prove exclusion before floor advance and
  candidate startup; never restore previous legacy image; accepted recovery
  against current data; original failure remains nonzero after recovery.
- `stop_witness.py`: checksum-pinned, read-only helper pipe, fresh nonce/time,
  exact target/data/configured pair/container name and IDs, repeated observations
  under one continuing fence. Missing/untrusted helper blocks activation.
- Active Coolify deployer retains exact service/project/environment/server
  validation, private HTTPS/no redirects, immutable coordinates and exact
  health/readiness checks. It mutates only image, SHA and the narrow floor key.
  `docker_cleanup=false` avoids implicitly opting into upstream image pruning.
- Release preparation refuses missing provenance and changed acceptance on
  replay, persists exact beta authorization in `.helm-release.json`; finish
  requires exact production evidence before publication and records acceptance,
  recovery pair, floor and observer hashes in release notes.

The complete JSON contract, trust model and primary references are in
[TC335_POLICY_CONTRACT.md](TC335_POLICY_CONTRACT.md). The implementation validates
receipt binding/completeness; it does not manufacture runtime acceptance or
implement a host fence. Same healthy accepted revision is a read-only replay;
the external continuing interlock must remain effective after controller exit.

## Parent-owned protected workflow integration (not implemented)

Do not enable041 rollout simply by merging these source changes. Current
`.github/workflows/ci.yml` has none of the new input wiring, so it fails closed
when running this source. No workflow YAML was edited.

For **both** concrete protected deployment jobs (`beta_deploy` and `deploy`):

| Input | Required trusted source |
| --- | --- |
| `HELM_DEPLOY_LANE` | Literal `beta` or `production`, matching the protected environment/job. |
| `HELM_DATA_IDENTITY` | Protected lane-specific persistent database/volume identity, independently resolved by host observer. |
| `HELM_ACCEPTANCE_BUNDLE` | Runner-local path to downloaded reviewed TC-336 bundle plus its22 sibling proof JSON files. |
| `HELM_ACCEPTANCE_SHA256` | Protected environment value pinned by parent after independent acceptance; never compute and trust a root from candidate output. |
| `HELM_RECOVERY_IMAGE` | Protected exact immutable `ghcr.io/kanterlabs/helm@sha256:<64hex>` accepted by that lane's bundle. |
| `HELM_RECOVERY_SHA` | Protected full40-hex source SHA for that exact recovery artifact. |
| `HELM_STOP_OBSERVER` | Absolute path to parent-supplied reviewed read-only observer using authorized private operator transport. |
| `HELM_STOP_OBSERVER_SHA256` | Protected checksum of actual executable bytes; scripts require a parent-reviewed interpreter/runtime too. |
| `HELM_DEPLOY_CONTROLLER_SHA` (workflow integration) | Parent-pinned reviewed full source SHA containing this enforcing controller; checkout separately from selected application source and execute that copy. The Python helper does not consume this variable itself. |

Keep existing exact Coolify identifiers, private origin, API token injection,
candidate digest from container job, exact source SHA, evidence path, branch-tip
gate, dependencies and non-canceling lane concurrency groups. Do not compute
protected acceptance/recovery/observer pins from the selected app revision.
Do not let selection/replay of a historical application revision also select
its historical unguarded deploy script. Pin the controller independently and
ensure the host admission interlock rejects old workflow/API/UI actors that
could otherwise start a pre-membership binary without consulting the floor.
Downloaded evidence/helper paths must be private regular files in a controlled
runner directory; do not allow untrusted writes between verification and use.
Upload sanitized `deployment.json` with `always()` on actual attempted runs.
Retain receipt bundle/proofs and root pin as reviewable provenance; no private
database copies/full API/env responses belong in artifacts.

Parent review connected the following serialized Python checks, plus the
parent regression suite, to `deploy/test-deployment-security.sh`, which the
ordinary checks job already invokes. To retain its sanitized receipts locally:

```sh
HELM_DEPLOYMENT_POLICY_EVIDENCE_DIR="$PWD/.artifacts/tc335-security" \
  bash deploy/test-deployment-security.sh
```

CI logs retain the journey results; uploading the receipt directory still
requires protected workflow integration. Compile all changed/new deploy modules, not only the entry
script. Parent must resolve runner-tier policy for jobs it modifies; this worker
did not alter the existing hosted-runner exception or any platform configuration.

Before activation parent must:

1. Complete TC-336 actual immutable candidate and membership-backend/previous-UI
   recovery builds and isolated runtime/storage acceptance. `cc07c75` remains
   only a possible source; no digest is accepted by these synthetic tests.
2. Refresh and verify pre-upgrade lane backups and actual current-data recovery
   against populated copies. Keep every historical image/backup. Rejection of
   schema35/40 authorization widening is an eligibility gate, not deletion.
3. Independently implement/verify a durable least-privilege host interlock and
   actual read-only observer via existing authorized operator transport. It must
   prevent surviving/restart-eligible legacy containers and known queued or
   concurrent operations in supported deployment paths from starting across
   controller interruption and recovery. Privileged operators deliberately
   bypassing the deployment procedure are outside this contract. A
   single stale snapshot or an expiring CI lock cannot certify exclusion.
4. Confirm the installed Coolify version's stop/start/PATCH behavior, including
   `docker_cleanup=false`, exact service/env identities and observer queue proof.
   Verify the independent floor entry survives compose parsing/start/recovery
   unchanged; it must not be pruned as an unused application environment key.
   Protect floor mutation and start admission from other API/UI actors; verified
   upstream routes provide no atomic CAS or durable admission fence by themselves.
5. Provision one unique readable service `HELM_AUTHORIZATION_FLOOR` entry after
   inspecting lane state, with exact target and initial zero only if041 has
   never activated. Never initialize zero on an already governed database.
   Wire both lanes and review all protected pins before any rollout decision.

## Red and green evidence

Failure inventory was written before product implementation:
[TC335_FAILURES.md](TC335_FAILURES.md). All retained outputs are synthetic under
`.artifacts/tc335/`; none are real runtime acceptance.

| Journey command | Red evidence | Green evidence |
| --- | --- | --- |
| `python3 test/deployment-policy/journeys.py OUT` | `red.log`, `red/*.json`:38 failures against original deployer | `green.log`, `green/*.json`:38 passed |
| `python3 test/deployment-policy/contract_journeys.py OUT` | `contract-red.log`:missing exchange, changed container IDs and already-stopped replay failed; `uncertain-floor-red.log`:metadata loss left candidate serving | `contract-green.log`, `contract-green/*.json`:11 passed |
| `python3 test/deployment-policy/release_journeys.py OUT` | `release-red.log`, `red/release-summary.json`:6 failures before release gates | `release-green.log`, `green/release-summary.json`:6 passed |
| `python3 test/release-pipeline/authorization.py OUT` | `release-commands-red.log`:3 failures; `replay-red.log`:changed-acceptance replay failed before its fix | `release-commands-green.log`, `green/release-commands.json`:4 passed |
| `python3 test/release-pipeline/run.py OUT` | Existing regression suite, no new red claim | `final-existing-release-pipeline.log`, `final-existing-release-pipeline/`:original command flow and11 failure/recovery scenarios passed |

The first release-provenance reader test had an assertion bug: it inferred
rejection from absent retained provenance even when the command succeeded.
That test was corrected and rerun before release implementation; the retained
`release-red.log` has all six meaningful failures. No false pass is acceptance.

The controller journeys exercise the actual entrypoint class/orchestration
with synthetic request I/O and capture the API ledger, exact starts, bounded
evidence and persistent synthetic write/schema identity. The contract suite
also executes the real subprocess observer pipe with a temporary checksum-pinned
fake and verifies no API token was inherited. Release commands run against
disposable local Git/HTTP fixtures only; temporary fixture commits/refs are not
repository publication. Every fixture server was closed.

First/second coordinate PATCH failure and lost-response cases recover only the
accepted pair; floor PATCH failure/loss starts nothing. Candidate migration and
write survive recovery at schema41. Recovery failure attempts stop and never
selects the historical pair. Receipt/target/capability/checksum/staleness errors
reject before mutation. Repeated healthy accepted state makes no mutations.
Container-ID changes within a stop phase and a changed fence reject startup.
Floor metadata loss after startup fails immediately and takes the exact-target
stop-only path; an unverified stop request is never reported as verified recovery.
The first new metadata-loss run was terminated because the old readiness loop
retried for its normal600-second bound; its retained red rerun uses a short
synthetic polling deadline. The product now treats policy/configuration loss
as immediately fatal, while still retrying transient route-readiness failures.

`synthetic-examples/` contains complete checksum-consistent bundle/proofs,
floor-before/after, stop request/response and deployment evidence, marked by
`SYNTHETIC.json`. Regenerate into a new directory with:

```sh
python3 test/deployment-policy/export_examples.py OUTPUT_DIRECTORY
```

The sample observer always exits1 when actually executed; the fixture alone
supplies its fake transport. The sample cannot be installed as a real helper.
An earlier example export used a relative helper path and correctly failed
closed; its incomplete `synthetic-contract-example/` is not the accepted sample.

## Exact source handoff

`owned-files.json` under `.artifacts/tc335/` records exact paths, byte counts and
SHA-256 for every owned source/test/doc at handoff. `verification.log` records
compile, diff and size checks. Each maintained owned file is below20,000 bytes;
no extraction threshold is crossed. No files were staged. Parent should inspect
the diff and rerun focused checks before acceptance; synthetic results cannot
close actual host integration, preserved-data or rollout gates.

Live beta `a414c69`/schema40 and production `c0f119cf`/schema35 were not contacted
or changed by this worker. Retired LXC switcher/broker/controller, backend,
UI, migrations, E2E runner, generated assets and package files are outside this
handoff. No membership browser/runner acceptance was rerun.
