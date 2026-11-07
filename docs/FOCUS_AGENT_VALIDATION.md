# Focus agent validation and beta handoff

Implementation cards: TC-369, TC-370, TC-371 and TC-372. Integrated beta/PR
verification is tracked in TC-373. The full main delta is documented in
[the changelog](FOCUS_AGENT_CHANGELOG.md); the original scope and measurable
acceptance are in [the plan](FOCUS_AGENT_PLAN.md).

## Repeatable checks

```sh
make focus-test
python3 test/skill-updates/run.py --receipt .artifacts/focus-skill-update.json
./test/e2e/run.sh e2e/releases.spec.ts
go test ./internal/store ./internal/httpapi ./internal/db
check-file-sizes
git diff --check
```

Use a fresh evidence path for explicit API/browser receipts. The API harness
owns its temporary SQLite database, process, synthetic credentials and shutdown;
it never targets a running lane. It records source/binary/runner identities,
selected synthetic responses, negative results and read-only event-count checks.
Credentials remain in memory and a temporary mode-0600 config, then are removed
with the owned fixture directory. Captures contain no real task content.

Real API/CLI scenarios cover project ceilings, bounded continuation and too-small
budgets, old creation/activity timestamps, new scope-versus-metadata event
evidence, identical durable progress plus heartbeat, changing progress,
truncated histories, operation replay, stale writes, omitted/cleared metadata,
membership deduplication without claims/column moves, explicit completion,
frozen scope, reopening with reason, unassigning and empty deletion.

Hook/classifier isolation failures were recorded and reproduced before their
implementations. HTTP/user flows exercise actual commands and a real server;
they supplement the existing Focus browser lifecycle flow. The source change
adds no schema migration; existing populated migration tests remain applicable.

## Fresh agent scenario evidence

Three independent fresh agents read the candidate skill and recorded synthetic
API facts. Their user requests did not name Focus:

| Scenario | Observed behavior |
| --- | --- |
| Recommend next onboarding work without implementing | Reused existing Onboarding launch, identified concrete member tasks, and called out missing acceptance/dependency evidence. |
| Clarify only SYN-4 acceptance | Stayed on SYN-4, proposed observable criteria and kept onboarding cards outside scope. |
| Recover after SYN-1 finished, without claiming | Identified remaining SYN-2/SYN-3, recommended claimable SYN-2, and provided a compact existing-Focus handoff. |

The sanitized scenario receipt is `.artifacts/focus-adoption/result.json`.
This validates behavior against recorded synthetic facts. The five-session
organic pilot in the plan remains post-publication observation; no fake usage
counts or outcome improvements are claimed.

## Publication and rollout

Main's updater/bootstrap and credential guidance were merged into the candidate.
An old installed updater must install the entire committed Focus package, its
split modules and references, then reconcile hooks and report a no-op on the
next update. The local-Git publication rehearsal verifies this without changing
the actual installed skill or its GitHub-main distribution revision.

Beta retains its private hostname, exact existing Coolify service, persistent
data/config, authorization floor and native admission lock. Deployment needs a
fresh verified pre-upgrade attachment-aware backup, exact CI-backed image
provenance, accepted candidate/recovery pairs, native close/upgrade/activate,
and exact private health/readiness afterward. Recovery retains current writes;
database restore is never automatic. Production is unchanged by beta rollout.

The PR must record actual CI, backup, acceptance, source/image, readiness and
retained-data results before calling beta deployed. The existing workflow's
warm-runner flag was turned off after GitHub reported zero available runners;
its supported hosted fallback is used. `HELM_BETA_DEPLOY_PAUSED` stays true.
No runner pools, deployment admission gates or production settings are changed.

This request authorizes beta delivery and a reviewable PR to main. It does not
merge main or publish the skill through production distribution. TC-373 retains
those later publication/pilot gates as a handoff rather than reporting them done.
