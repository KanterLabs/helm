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

## Isolated browser workers

`test/e2e/run.sh` builds Helm once and runs the full browser suite in four
isolated browser processes by default. Each process runs Playwright with one
worker and executes its assigned files sequentially. Every process has its own
Helm server, database, Codex home, provider fixture, ports and artifact directory.

Set `HELM_E2E_WORKERS=1` for a serial run; worker counts from 1 through 8 are
accepted. Focused runs (any Playwright arguments passed to `run.sh`) default to
one process unless `HELM_E2E_WORKERS` explicitly requests another count. For
example:

```sh
HELM_E2E_WORKERS=1 test/e2e/run.sh
test/e2e/run.sh tickets.spec.ts
HELM_E2E_WORKERS=4 test/e2e/run.sh tickets.spec.ts
```

`HELM_E2E_BASE_PORT` defaults to `18080`. Each worker's base port advances by
100; its fixture ports are its base plus 10, 20 and 30. Choose a different base
when another run needs the same ports.

Within the run's artifact bundle, each worker writes evidence under
`disabled/worker-N/`. The supervisor writes `disabled/workers.json` with the
worker results and captures each worker's console output in
`disabled/worker-N.console.log`. Inspect these paths when a worker fails; the
bundle's checksums cover the retained evidence. Local onboarding and GitHub
integration remain separate phases after the main browser suite.

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

## Task-save concurrency failure contract

The task-save stability workflow must prove these failure cases before changing
drawer save orchestration. Each delayed browser route must call `route.fetch()`
against the disposable Helm server, so the assertions cover real persistence
and delivery races rather than mocked responses.

| Failure | Observable proof |
| --- | --- |
| A delayed PATCH response overwrites edits made after the first save started | The first submitted title and description are persisted, newer edits remain visible and dirty after the response arrives, and a second save persists those newer values. |
| A delayed label creation resumes a save against a different task | Task A persists its original submitted snapshot, task B's API state remains unchanged, task B's newer drawer draft remains visible and dirty, and no unrelated drawer error appears. |
| A delayed PATCH response overwrites a draft in a reopened drawer | After closing and reopening the same task, edits made in the reopened drawer remain visible and dirty when the old response is delivered. |
## Coolify alert intake failure contract

See the [external integration failure contract](E2E_EXTERNAL_INTEGRATIONS.md#coolify-alert-intake-failure-contract).

## Tickets workspace failure contract

The Tickets workflow must prove these failure cases through a real Helm
process, real database and browser. Fixtures use run-unique projects so a
retry against the same database starts clean.

| Failure | Observable proof |
| --- | --- |
| Every task leaks into Tickets, or a ticket loses membership | An ordinary task in the same project never appears in `/api/v1/tickets` or the UI; a ticket stays listed after its labels change. |
| Alert intake and manual creation produce different kinds of ticket | A Coolify alert and a `New ticket` both appear as `kind=task` tickets with origins `alert` and `manual`, in Backlog, assigned, unclaimed. |
| Queue counts are inferred from the loaded page | With more tickets than one page, server counts for each queue match the full filtered membership while the page returns `limit` rows plus a cursor. |
| Status comes from agent progress or claims | Each queue maps to the column semantic state only (`backlog`, `ready`, `active`, `blocked`, `completed`); `My open` uses assignee only. |
| A triage action applies partially or over a newer edit | Accept moves to Ready; a stale version returns `409`, the UI shows a conflict, reloads the ticket, and applies nothing. |
| Waiting loses its reason, or start/resume creates an agent claim | Waiting records the human reason in Activity; Start and Resume never set `claimed_by` or `agent_work`. |
| Selection, filters or queue are lost on reload or Back | The URL carries queue, project, search and ticket; reload restores them, and phone Back returns from detail to the filtered list. |
| A delayed response for ticket A renders into selected ticket B | Switching tickets while the first detail load is delayed leaves ticket B's header and notes visible. |
| Keyboard users cannot work the queue | Arrow keys move through rows, Enter opens a ticket, `/` focuses search outside text inputs and does nothing while typing. |
| Notes are lost or trigger hidden side effects | An internal note posts as an ordinary task comment and shows in the ticket immediately and after reload. |

## Ticket queue failure contract

The one-queue workflow (see `TICKET_QUEUE_PLAN.md`) must prove these failure
cases through a real Helm process, database and browser
(`web/e2e/tickets.spec.ts`, plus the webhook, email, Coolify and guided-setup
specs).

| Failure | Observable proof |
| --- | --- |
| Intake still asks for a project | The New ticket, webhook, inbox and guided-setup forms have no project field. |
| Intake lands somewhere else | A New ticket, a webhook post, an inbox email and a Coolify alert all land in the queue project reported by `GET /api/v1/tickets` (`queue`), in Needs triage, shown as **Not filed**. |
| The queue leaks into normal project UI or can be retired | The queue has `system_kind=tickets`, is absent from the sidebar project list, offers **Not filed** in the Tickets filter, and archiving it returns `400`. |
| Filing loses the ticket or its history | After **File to project** the ticket has the project's next key, keeps its status, is listed under that project, the old key still resolves to it, and the page says `<old> is now <new>`. |
| A ticket can be filed back into the queue | Filing into the queue project returns `400`. |

## Ticket webhooks failure contract

See the [external integration failure contract](E2E_EXTERNAL_INTEGRATIONS.md#ticket-webhooks-failure-contract).

## Public endpoint failure contract

See the [external integration failure contract](E2E_EXTERNAL_INTEGRATIONS.md#public-endpoint-failure-contract).

## Connect Cloudflare failure contract

See the [external integration failure contract](E2E_EXTERNAL_INTEGRATIONS.md#connect-cloudflare-failure-contract).

## Email intake failure contract

See the [external integration failure contract](E2E_EXTERNAL_INTEGRATIONS.md#email-intake-failure-contract).
