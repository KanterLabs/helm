# Agent and API workflows

Detailed workflows for Focus, bug tracking, live agent work, and board audits.
The full request/response contract is in [`API_CONTRACT.md`](API_CONTRACT.md).

## Focus

Focus is a bounded, project-local set of tasks that matter now. A focus can
have an optional target date, task assignments, readiness tracking, and
explicit completion or reopening. The project view is `/p/:slug/focus`;
existing `/releases` links remain compatible. Internally and in the API, this
feature retains the `release` and `release_id` compatibility names. Focus is
separate from the deployment `X-Roadmap-Revision`, the task claim action
`POST /api/v1/tasks/{task}/release` (which releases an agent claim), and a
bug's `affected_version`. The release API is:

- `GET|POST /api/v1/projects/{project}/releases`
- `GET|PATCH|DELETE /api/v1/releases/{release}`
- `POST /api/v1/releases/{release}/complete`
- `POST /api/v1/releases/{release}/reopen`
- `GET /api/v1/releases/{release}/work-queue`

Release reads and the read-only work queue use `tasks:read`; release
creation, edits, deletion, completion, and reopening use `tasks:write`. The
existing project ceiling applies, and no new bearer scope is required.
Release metadata mutations use the strong release `ETag` in `If-Match` and an
`Idempotency-Key`; released releases are frozen until reopened with a reason.

Task creation and PATCH accept nullable `release_id`; omission preserves an
existing assignment and explicit `null` clears it. A release must be planned
and belong to the task's project. Project task collections accept
`release={id|name|unassigned}`; global Issues, My work, Search, and saved
views use stable `release_id={id|unassigned}`. Filters are applied before
pagination and names are resolved only within a selected project.

The work queue is read-only: it includes direct members and transitive
same-project prerequisites, reports dependency and cross-release conflicts,
and orders owned work before claimable tasks. Agents still claim and finish
one task at a time through the existing task lifecycle. Queue cursors are
invalidated by relevant changes and return `release_queue_changed` with
`restart: true`. Portable exports are `helm.portable` v2 with releases and
task release references; v1 archives remain import-compatible with tasks
unassigned on import.

Tasks declare `kind: task` or `kind: bug`. Bug creation requires nested
`bug.actual_behavior`; triage sets `severity` (`s1`–`s4`), resolve records a
documented resolution, and reopen records a reason. These mutations use the
same `If-Match`, idempotency, claim, and bearer-scope rules as other task
actions. Agents with `tasks:read` can use `GET /api/v1/issues` to list bugs
across their permitted projects with lifecycle, board, ownership, search, and
pagination filters.

Agent issue workflow:

1. Report with `POST /api/v1/projects/{project}/tasks`, `kind: "bug"`, nested
   bug details, and an `Idempotency-Key`; the server records the reporter.
2. Discover work with `GET /api/v1/issues?severity=untriaged`, then claim the
   selected task with `POST /api/v1/tasks/{task}/claim`.
3. Triage severity, priority, assignment, and destination together with
   `POST /api/v1/tasks/{task}/triage` and the current `If-Match` value.
4. Use the ordinary task patch, comment, claim-renewal, and event-polling APIs
   while working the bug.
5. Finish through `POST .../resolve` with an explicit resolution; use
   `POST .../reopen` with a reason if the regression returns. Retry mutations
   with the same idempotency key and refresh after a stale ETag conflict.

The Issues view exposes operational counts for open, untriaged, S1/S2,
recently resolved, and recently reopened bugs. Command search opens issue keys
and titles directly. Filters use the same global issue query vocabulary, so a
filtered URL can be bookmarked as a working view without a separate issue
permission or search system.

Agent work workflow:

1. Claim a task with `POST /api/v1/tasks/{task}/claim` using a token with the
   `tasks:claim` scope, then retain the returned strong task ETag.
2. Publish a complete progress snapshot with
   `POST /api/v1/tasks/{task}/progress`, the current `If-Match` value, and an
   `Idempotency-Key`. The request requires an `operation_id`, one of the
   documented agent-work states, and a non-empty `summary`; optional phase,
   next action, checkpoint references, and paired checkpoint counts are
   described in [`docs/API_CONTRACT.md`](API_CONTRACT.md).
3. Refresh the ETag from each response before the next mutation. The server
   records the structured pulse and a readable activity comment atomically;
   ordinary `POST .../comments` remains available for notes that are not live
   progress snapshots.

The board and drawer read the same `Task.agent_work` snapshot. A pulse is
marked stale deterministically after 15 minutes without an update; stale is a
coordination signal, not a task failure or automatic claim release. Filter
task collections with `agent_state` or `action_needed=true`, and use
`/api/v1/my-work?view=live` for the Live Work view. Unscoped human identities
may see live work across their visible projects; a project-scoped bearer token
must include a permitted `project` query value.
Completed tasks retain their last snapshot as history, but its `stale` and
`action_needed` flags are inactive; completed tasks do not match live-work
filters and the browser does not render their snapshot as a live pulse.

Board-audit workflow:

1. List or start a project audit with `GET|POST
   /api/v1/projects/{project}/audits`. Audit reads require `tasks:read`; audit
   writes require `tasks:write`. Starting a run only captures audit metadata.
2. Read the bounded run summary and cursor-paginated findings with
   `GET /api/v1/audits/{audit}` and `GET
   /api/v1/audits/{audit}/findings`. The summary's `findings` array is always
   empty, and findings expose `changed_since_audit` drift.
3. Review a finding with `PATCH /api/v1/audit-findings/{finding}` using its
   `If-Match` ETag. Approval, dismissal, finalization, and all audit reads are
   task read/review operations; none moves a task.
4. After a separate read-only preview and explicit confirmation, call
   `POST /api/v1/tasks/{task}/move` with the current task `If-Match`, expected
   source column, provenance, and an `Idempotency-Key`. Only backlog and ready
   destinations are allowed, active claims reject the move, the server
   computes position atomically, and success emits `task.moved`. See
   [`docs/API_CONTRACT.md`](API_CONTRACT.md) for exact finding, review,
   and move request fields and aliases.

The UI's Run audit action creates a queued run for an agent. The bundled Helm
skill processes that same run with `submit --audit AUDIT_ID`, so a
UI request is not duplicated before its findings are finalized for review.
