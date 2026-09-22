# End-to-end testing

Helm treats user-observable workflows against a real Helm server and database
as its primary verification mechanism. New behavior should be proven through
the same HTTP, persistence, and browser boundaries used in production.

Every end-to-end run must leave a repeatable evidence bundle containing:

- the Playwright HTML report and machine-readable JSON result;
- browser attachments produced by the workflow;
- the Helm server log and SQLite database used by the run;
- a small metadata file identifying the tested revision and command; and
- `SHA256SUMS`, covering every other file in the bundle.

The bundle is successful evidence only when the test command exits zero and
`sha256sum --check SHA256SUMS` passes. CI retains the bundle whether the suite
passes or fails so a successful result remains inspectable instead of
discarding all evidence.

Run the same workflow locally with `make test` or `npm test` from `web/`.
The harness uses the installed Go toolchain when available and otherwise builds
the application in the repository's pinned Go container through Docker.

## Replacement rule

Do not add a unit test after implementing behavior. Replace existing isolated
coverage feature by feature:

1. Write the externally visible failure scenarios before test implementation.
2. Add a workflow that exercises the compiled application through real
   process, HTTP, database, and browser boundaries.
3. Make that workflow emit and verify its evidence bundle.
4. Remove the isolated tests superseded by the workflow.

Small pure checks may remain temporarily where no real boundary can express
the invariant yet. They are migration debt, not the default place for new
coverage.

## Agent mutation allowance change contract

Increasing the lifetime allowance must preserve every actor's existing
`reserved_bytes` value. The deployed workflow is successful only when an agent
that exhausted the former 64 MiB ceiling can perform a new authenticated
mutation after the 256 MiB binary is live, without deleting its resource-usage
row or resetting the database. Mutations at the new ceiling must still fail
before their handler writes domain data.

## Luna run-history failure contract

The Luna history replacement workflow must prove all of these cases through a
real Helm process:

| Failure | Observable proof |
| --- | --- |
| Helm cannot start with the deterministic Codex fixture | Readiness never succeeds and the server log is retained. |
| The human account is not recognized as connected | The task-draft workflow cannot reach review and the browser report identifies the failed step. |
| A Luna turn does not cross the runtime and HTTP boundaries | No task suggestion appears and no completed run is returned by `/api/v1/codex/runs`. |
| Run metadata is not persisted | Reloading Settings does not show the completed run. |
| History leaks actor IDs, prompts, or model output | The real history response contains one of those forbidden values. |
| The UI hides essential diagnostics | Expanding the run does not show model, effort, run ID, thread ID, and turn ID. |
| The endpoint accepts an unsafe limit | `/api/v1/codex/runs?limit=101` does not return a structured `400`. |
| Successful E2E evidence cannot be independently checked | The evidence bundle is missing or its `SHA256SUMS` verification fails. |
