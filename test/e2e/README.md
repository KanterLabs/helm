# Browser validation

`./test/e2e/run.sh` owns frontend preparation, immutable Go builds and all
runtime fixtures. Do not run two coordinators in the same checkout: embedding
and restoring frontend assets has one owner.

```bash
HELM_E2E_GOFLAGS=-race HELM_E2E_INCLUDE_DEADLINES=true ./test/e2e/run.sh
HELM_E2E_WORKERS=6 HELM_E2E_GOFLAGS=-race ./test/e2e/run.sh
./test/e2e/run.sh e2e/board.spec.ts --grep 'card'
```

Full runs default to four isolated main shards, then run local onboarding and
GitHub integration. CI also enables the stalled-account deadline suite. The
ancillary suites share the same worker budget (1–8), each with private state,
ports and evidence. Onboarding runs once. Focused invocations preserve their
selectors and existing onboarding check; they do not add GitHub integration.
Tests within a fixture remain sequential. `HELM_E2E_SERVER_GOMAXPROCS` defaults
to two for fixture servers; `HELM_CI_BUILD_JOBS` defaults to six for compilation
only (allowed 1–6). These are independent controls.

The coordinator builds the race Helm server and GitHub harness once. Deadlines
reuse the same verified executable. Production binaries remain separate.
`prepare.py` records checkout SHA and actual source fingerprint, embedded asset
digest, toolchain/settings, workflow run/attempt, executable hashes and race
instrumentation. Reuse fails on mismatches, including source changed during a
build. An arbitrary executable supplied through `HELM_E2E_BINARY` is rejected
without its matching `HELM_E2E_PREPARED_DIR` bundle.

An explicit prepared directory can be retained for same-input local reruns:

```bash
HELM_E2E_PREPARED_DIR="$PWD/.artifacts/prepared-race" \
  HELM_E2E_GOFLAGS=-race ./test/e2e/run.sh
```

The directory must be absent for preparation; an existing directory is verified,
never silently rebuilt. Use a new directory after changing inputs. Go is native
on the prepared CI runner. Local machines without Go use the pinned Docker
fallback. `HELM_E2E_GO_COMMAND` is an optional quoted argv prefix, parsed without
a shell, and is part of provenance. The fallback's private cache paths change
between coordinator runs, so use native Go for retained cross-run bundles.

## Evidence and failure behavior

`HELM_E2E_ARTIFACT_DIR` must be fresh. Default evidence is under `.artifacts/`.
The root contains `build-provenance.json`, `evidence.metadata`, supervisor proof
results and checksums. `suites/suites.json` records every required/skipped phase,
status, timing and exit code; each suite retains its own reports and logs.
Missing, failed or canceled required phases fail the command. Normal suite
failures still collect sibling results. Killed ownership wrappers stop further
ancillary scheduling until orphan cleanup completes.

Preparation and runtime supervisors terminate only their owned children on
cancellation. Linux subreapers recover detached fixture descendants. The CI
metrics wrapper records sanitized numeric resource samples separately; metrics
capture failures never replace the workload's result.

## Optional measured sharding

Native Playwright sharding remains the default. Collect durations from complete
reports with an independently verified source revision:

```bash
python3 test/e2e/collect-timings.py --source-revision FULL_REPORT_SOURCE_SHA \
  --output .artifacts/timings.json .artifacts/RUN/suites/main/worker-*/results.json
HELM_E2E_TIMING_PROFILE="$PWD/.artifacts/timings.json" ./test/e2e/run.sh
```

Profiles contain per-file measurements, source hashes and provenance, without
individual test payloads. Playwright discovers the current selection first;
the profile never determines which tests exist. Whole files are balanced by
measured duration. Changed/new files receive an explicitly reported median
estimate. `shard-plan.json` records the actual assignment. Filters remain
opaque; overrides that could replace selection or shared evidence are rejected.

## VM128 rollout

The `warm-runner-canary.yml` workflow runs only on the explicitly admitted
`ci/helm-warm-runner` ref. Push runs a full smoke sample and a matched hosted
control. Dispatch `mode=benchmark` for one cold plus three warm repetitions at
each of 4/6/8 workers in counterbalanced order. Dispatch `mode=confirm` with the
measured winner for five warm confirmations. Do not infer the winner from CPU
count alone. The single heavy slot serializes workloads across refs.

`HELM_WARM_RUNNER_ENABLED=true` is a gated repository variable for trusted
beta/main browser routing only. Keep it unset until the capacity, admission and
performance gates in [the single-VM plan](../../docs/CI_SINGLE_VM_PLAN.md) pass.
Public PR and protected deployment jobs retain their existing hosted boundary.
No canary step publishes a production image or deploys an application.
