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

## Agent lifetime allowance removal contract

The E2E workflow must prove these failure cases before changing admission code:

| Failure | Observable proof |
| --- | --- |
| An existing agent has a legacy usage row at the former 256 MiB ceiling | A bearer-authenticated mutation succeeds without deleting or changing that row. |
| Removing the lifetime ceiling accidentally removes burst protection | The same actor's eleventh immediate mutation returns 429 with `Retry-After`; a different token cannot evade it. |
| The migration discards historical usage | The legacy row remains unchanged after the successful mutation. |

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

## Coolify alert intake failure contract

The Coolify webhook workflow must prove these failure cases through a real
Helm process configured with a disposable intake secret file. Payloads are
synthetic and use run-unique server identities so a Playwright retry against
the same database starts from a fresh condition.

| Failure | Observable proof |
| --- | --- |
| A wrong or missing secret can create work or reveal that intake exists | The request returns the same `404` as an unknown route and the project task count is unchanged. |
| A Traefik notice creates work in the wrong place or for an agent | The task is `kind=task` in the configured project's Backlog column, assigned to the configured human, with no `claimed_by` and no `agent_work`. |
| Concurrent deliveries of one notice create duplicate tasks | Parallel identical deliveries return one `created` and the rest `repeated`; exactly one task exists and its occurrence count equals the delivery count. |
| A repeat overwrites human triage or notifies again | After a human edits title, priority and description, a repeat leaves those fields and the task version unchanged and adds no assignee notification. |
| A repeat reopens or recreates completed work | After completion, an identical notice reports `retained`; the task stays completed and no new task appears. |
| A new offered version is merged into old work | A different `latest_version` creates a second task whose alert source links the earlier task key. |
| A multi-server notice partially commits | A payload with one valid and one invalid server returns `400` and creates no task; a fully valid two-server payload creates two tasks. |
| Unsupported or test events create work | `test` reports `ignored` and `server_unreachable` reports `unsupported`, both with no task. |
| Oversized or malformed bodies are processed | A body over the intake limit returns `413`; invalid JSON returns `400`; neither creates a task. |
| Misconfigured routing silently drops alerts | A missing target project returns `503` with no task. |
| The secret leaks into logs or responses | The retained server log and every intake response omit the secret. |
| Evidence is invisible to the human | The task drawer shows the Alert source panel with server, installed and offered versions and repeat count, and Activity shows the repeat alert. |
