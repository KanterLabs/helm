# Runner metrics wrapper: failure inventory

Recorded before implementation and executable proofs.

- The collector changes the workload result, buffers progress, or merges output
  channels. Inherit stdout/stderr and preserve normal exit codes and signals.
- Missing cgroup/proc/cache files or malformed counters break browser work.
  Treat each source independently as unavailable; collection failure must never
  replace the workload's result. Record only error types, never raw file data.
- Failed workloads or cancellation lose final measurements. Persist before and
  after snapshots, numeric counter differences, status and timing in final JSON;
  append one-second samples while the child is running.
- Cancellation or a crashed child leaves detached descendants alive. Supervise
  owned groups using the existing ownership/subreaper cleanup, allow cooperative
  cleanup, then escalate. Never select cleanup targets by process name or port.
- Receipts leak credentials, command arguments, environment values or private
  paths. Store allowlisted numeric host counters, fixed source names, relative
  times, status and sanitized error classes only. Never retain command output.
- Counter resets create misleading deltas. Report negative counter differences
  as unavailable rather than interpreting them as resource savings.
- Output directories overwrite earlier evidence. Require fresh metrics files;
  atomic final JSON replacement preserves a complete receipt.

Run `python3 test/e2e/test_runner_metrics.py`. Set
`HELM_E2E_PROOF_ARTIFACT_DIR` to a fresh directory to retain synthetic workload
and wrapper receipts, including failure and cancellation results. No compilation,
real server, browser or network service is needed.
