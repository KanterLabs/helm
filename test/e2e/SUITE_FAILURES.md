# Runtime suite coordination: failure inventory

Recorded before implementing the suite coordinator or its proofs.

- Independent coordinators rebuild or replace embedded frontend assets while
  fixtures are running. Preparation remains a single owner; runtime suites
  consume executable paths and never build, restore or modify embedded assets.
- Main shards compete with ancillary fixtures beyond the worker budget. Main
  finishes before ancillary suites start; every ancillary fixture costs one
  worker and the same global budget bounds their overlap.
- Suite state, providers, Codex homes, ports or evidence overlap. Allocate fresh
  private state and artifact paths plus nonoverlapping complete port blocks.
- The inherited stalled-account flag poisons ordinary main tests. Strip it from
  every ordinary fixture and enable it only for the dedicated deadline suite.
- Full CI repeats local onboarding during its deadline invocation, or ordinary
  local runs unexpectedly start deadline tests. Schedule onboarding once and
  make the deadline phase opt-in for full runs.
- Focused selectors silently become full runs or lose shell argument boundaries.
  Preserve exact selectors and onboarding compatibility, excluding optional
  suites from focused invocations.
- A failed/spawn-failed suite hides successful siblings or required phases never
  started. Persist every phase's requirement, status, exit result and timing;
  failed, canceled or missing required evidence fails the coordinator.
- Cancellation or an exception kills only the outer supervisor, leaving nested
  browser process groups and occupied fixture ports. Give cooperative cleanup
  time, then escalate only owned process groups, including reparented nested
  descendants. Reap owned descendants and keep unrelated processes alive.
- A crashed supervisor leaves an uncooperative descendant holding its log open.
  Log directly to regular files and continue bounded owned-descendant cleanup
  even after a supervisor exits.
- GitHub startup reaches an unrelated listener or loses evidence on TERM. Probe
  its assigned port before startup, retain phase/server metadata and bound
  requests; use a fresh state directory and one Playwright worker.
- Build parallelism leaks into each Go server. Apply the optional fixture
  GOMAXPROCS setting at server launch only, preserving the parent build env.

Proofs use executable synthetic harnesses and retain JSON phase receipts and
logs in a disposable directory. They exercise process trees and argument/env
boundaries without compiling, starting real servers or launching browsers.

Run `python3 test/e2e/test_run_suites.py` and
`python3 test/e2e/test_github_runtime.py`. Set
`HELM_E2E_PROOF_ARTIFACT_DIR` to a fresh directory to retain each proof's
synthetic receipts, inputs and logs after the run. Per-suite subprocess
wrappers keep slots occupied through nested descendant cleanup, so an exited
supervisor cannot silently exceed the worker budget. On Linux, child subreapers
also recover descendants after a supervisor crashes. Cooperative fixture
cleanup runs before bounded group escalation; no process names or ports are
used to choose cleanup targets.
