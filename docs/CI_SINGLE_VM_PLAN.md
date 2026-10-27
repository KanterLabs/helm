# Faster Helm CI on VM128

Status: approved; implementation and canary validation in progress.

Planning record, 27 October 2026. Owner decision: use the existing single
`helm-ci` VM. The observations below describe the planning baseline.
Implementation began after approval; current harness behavior and rollout
controls are documented in [browser validation](../test/e2e/README.md).

## Verified starting point

Live Proxmox, guest, Kubernetes and GitHub runner-group inspection established:

| Item | Observed state |
| --- | --- |
| VM128 CPU | 8 host-type vCPUs; guest sees all 8 |
| VM memory | 32,000 MiB maximum (31.25 GiB), 8,000 MiB balloon minimum |
| Guest memory | About 30.5 GiB visible; no swap configured |
| Kubernetes capacity | 7.5 allocatable CPUs; about 29 GiB allocatable RAM |
| Current heavy runner | One warm slot; request 2.5 CPUs/4 GiB, limit 3 CPUs/5 GiB |
| Current Go environment | `GOMAXPROCS=2`, `GOFLAGS=-p=2` |
| Small runner | At most one status job, limited to 0.5 CPU/1 GiB |
| Storage | 20 GiB root plus 44 GiB CI filesystem at `/var/lib/gha` |
| Free CI space | About 35 GiB available at inspection |
| Admission | Group `helm-runners`, Helm only, reviewed beta/main and explicit canary ref |

The VM upgrade is already visible inside the guest. Pod and compiler limits
still reflect the old four-vCPU pilot. Increasing browser workers without
lifting those limits would leave most of the upgrade inaccessible to the job.

Proxmox had 32 logical CPUs, approximately 46 GiB available memory and load
averages near 1.7–1.9 during inspection. This is a point-in-time observation;
verify headroom again before changing the memory guarantee.

The latest measured hosted run, `40428034328`, used source `2698208` and passed:
browser job 611 seconds; main build/test phase 488 seconds; deadline phase
65 seconds; dependency installation 16 seconds. Its four main shards took
approximately 4.4, 5.0, 5.1 and 6.6 minutes. The older warm-runner pilot used
different source and resource limits; its results are historical evidence,
not a controlled comparison with this run.

## 1. Make the existing capacity usable — INFRA-275

Keep one heavy workload across all refs, plus at most one small status job.
Parallel browser fixtures live inside the heavy workload. Do not add a VM or
increase the number of full build/browser jobs competing on this VM.

Proposed starting envelope, subject to allocatable-resource verification:

| Resource | Heavy workload | Small status job |
| --- | --- | --- |
| CPU request / limit | 6 / 7 CPUs | 0.1 / 0.5 CPU |
| Memory request / limit | 16 / 24 GiB | 256 MiB / 1 GiB |
| Maximum simultaneous jobs | 1 | 1 |

Heavy limits include any future container-build sidecar; they are not separate
allowances for each container. Preserve room for K3s, ARC, listeners and the OS.
Keep the existing 10 GiB workspace cap until measured workspace demand supports
a reviewed storage-budget change.

Prefer fixed 32,000 MiB VM memory for predictable CI. If ballooning is retained,
its floor must cover the admitted workload and system headroom; the current
8,000 MiB floor is inconsistent with a guaranteed 24 GiB heavy-job envelope.
The screenshot's minimum/maximum pair is not evidence of 32 GiB guaranteed RAM.

Update the infrastructure renderer and source manifests, not only live pod YAML.
Drain assigned jobs before replacing idle runners or changing memory settings;
preserve existing disks, caches, network rules and other guests. Reconcile and
prove the new effective cgroup limits with a deployment-free canary. Rollback
restores runner configuration and routing, never application data.

Remove the inherited global two-way Go cap. Start compilation at explicit
parallelism six and measure it. Give fixture servers their own conservative
settings; do not propagate six-way build settings blindly to every server.

## 2. Prepare once and reuse exact outputs — TC-302

Use a single owner for checkout preparation and embedded assets:

1. Fresh checkout of the reviewed mirrored commit; clean `npm ci` using the
   prepared runtime and local download cache.
2. Frontend checks/build and embedding once.
3. Build the race-instrumented Helm server and GitHub harness once. Build the
   non-race release binary separately; it cannot replace either race executable.
4. Run main browser shards with the selected worker budget.
5. Run onboarding, GitHub integration and deadline checks from the prepared
   outputs. Introduce bounded overlap only after independent fixtures and
   failure aggregation are proven; avoid duplicate onboarding.
6. Preserve all results, then run container smoke/scanning and existing release
   gates. Initially avoid heavy compilation or container builds during Chromium.

`HELM_E2E_BINARY` already permits an explicit server executable. The deadline
invocation currently skips the web build but rebuilds the server. Reuse must
verify source SHA, run/attempt, frontend digest, executable hashes, toolchain
and `-race` build settings. A revision environment variable is not provenance.

Do not run two `run.sh` coordinators in one checkout: both mutate and restore
embedded assets. Refactor into one preparation owner and independent runtime
workers. Each suite needs separate ports, database, provider/Codex state and
artifact directory; the GitHub harness's fixed default port needs explicit
assignment. A global concurrency budget applies to all suites together.

Preserve the required `checks`, `browser`, `race` and container/deployment gates.
If work is consolidated into a preparation/validation job, lightweight status
jobs must inspect every phase result and fail on failed, missing, skipped or
canceled required work. Never turn missing evidence into a green required check.
New coordination behavior needs failure scenarios and meaningful subprocess/E2E
proofs before implementation, including cancellation and owned-process cleanup.

## 3. Keep tools warm and cache storage bounded — TC-287

Continue the existing pinned runner image: matching Go, Node, Chromium and OS
libraries are already installed. Verify its manifest against the selected
commit; rebuild the image on toolchain/browser changes instead of silently
running a mismatched version.

Keep npm downloads, Go modules and normal/race compiler caches on the 44 GiB
data disk. Continue fresh `npm ci`. Preserve the existing 8 GiB aggregate
dependency/compiler budget, idle-generation eviction and 6 GiB free-space
admission floor until measurements justify a different allocation. The larger
RAM allocation does not enlarge the filesystem. Do not persist credentials,
browser profiles, mutable workspaces or unverified executables as build inputs.

Container-layer reuse is a later acceptance gate: the pilot has not yet proven
the job-private Docker/BuildKit path. Explicitly import/export trusted bounded
cache generations into the daemon that builds the image; containerd's runner
image cache is a different store. Keep image smoke checks and current security
data. No host Docker socket or general LAN access is needed for test jobs.

## 4. Benchmark the whole pipeline — TC-288

First run the existing harness at four workers with the new limits. Then
benchmark four, six and eight workers on the same commit/toolchain/cache policy:

- One cold reference; at least three warm repetitions per candidate with
  counterbalanced ordering, then five warm confirmation runs for the winner.
- Record queue time, all-required-check wall time, each preparation/test/build
  phase, per-shard timing, CPU usage/throttling/steal, peak memory, available
  memory, retries, OOM/evictions, disk latency and free space.
- Separate a raised-resource-only result from build-reuse/scheduling changes
  so the cause of improvement is measurable.
- Use recorded durations to balance shard assignments while preserving complete,
  nonduplicated test inventory and test independence.
- Select the lowest worker count within about 5% of the fastest reliable median.
  Eight vCPUs do not by themselves establish that eight browsers are optimal.
- Verify a second heavy workflow queues, and repeat under representative DCT
  host load without modifying DCT's VM, group or caches.

Acceptance: unchanged coverage and race instrumentation, every required gate
reports correctly, no increased retries/failures or memory/storage failures,
warm preparation sample p95 below 30 seconds, no median total-pipeline
regression, and a target of at least 20% lower matched-commit total latency.
These are rollout criteria, not promised timings. Report queue time separately;
one heavy slot may serialize jobs that hosted CI previously ran concurrently.

## 5. Controlled routing and promotion

The intended trusted build/test capacity is this one VM. Start with the reviewed
canary, then route trusted browser work, then checks/container work only after
whole-pipeline acceptance. Use `homelab-heavy` for sustained work and `homelab`
for small status/orchestration jobs, explicitly scoped to `helm-runners`.

Retain the existing public-PR and protected-deploy boundaries during cutover.
Current group admission intentionally rejects public PR refs, and test pods
cannot reach private deployment services. Those jobs retain their existing
hosted exceptions until an explicitly isolated path on VM128 is validated;
this plan does not claim that all CI/deployment is already on the VM. Do not
solve routing by granting ordinary test pods deployment credentials or broad
LAN access. The outstanding dedicated controller credential and denied-ref,
changed-manifest and container-canary proofs remain rollout gates.

After build reuse is accepted, promote the exact validated image through beta
and production with source/provenance checks. Reuse validation only for an
identical, trusted source tree and relevant build inputs. A changed merge base,
workflow, dependency input or policy requires validation. This release-flow
optimization is a later slice, not needed for the first capacity measurement.

## Work records and delivery

- **TC-301:** this planning record; complete when evidence and backlog scope are
  saved, with no implementation implied.
- **INFRA-275:** raise effective VM128 runner resources; unclaimed Backlog.
- **TC-302:** reusable race builds and coordinated isolated suites; unclaimed
  Backlog.
- **TC-287:** existing canary/routing work, updated for this single-VM plan.
- **TC-288:** existing benchmark/rollout work, updated for the new resources.

Respect existing INFRA-273/274 ownership and unfinished gates. Cross-project
execution gates are recorded in descriptions because Helm dependency links are
project-scoped. TC-288 depends on TC-287 and TC-302; capacity acceptance also
requires INFRA-275. Future implementation cards remain unclaimed.

Files to review during implementation include infrastructure
`homelab/ci-runners/helm/{render.py,homelab-heavy.yaml,homelab.yaml,PLAN.md}`,
Helm `.github/workflows/ci.yml`, `test/e2e/` and `test/githubbeta/`. Measure before
editing and at handoff; keep every maintained file at or below 20,000 bytes,
splitting by responsibility near 16,000. Preserve unrelated dirty checkouts.

Validation for this planning deliverable is read-only configuration inspection,
code review, backlog re-read and file-size checks. No benchmark, configuration
change, deployment or workflow cutover was performed by this planning task.
