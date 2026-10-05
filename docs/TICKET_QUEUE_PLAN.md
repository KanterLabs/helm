# One ticket queue: intake without choosing a project

Status: implemented on `beta` (2026-10-04), both phases. User guide:
[TICKET_WEBHOOKS.md § Where tickets land](TICKET_WEBHOOKS.md#where-tickets-land).
Differences from the proposal are listed under [As built](#as-built).

## Problem

Today every way into Tickets asks **which project** first:

| Entry point | Where the project is chosen |
| --- | --- |
| Webhook | **Project** on create (`ticket_webhooks.project_id`) |
| Email inbox | **Tickets go to** on create (`email_inboxes.project_id`) |
| Guided setup | **Tickets go to** for the first inbox |
| Coolify | `HELM_COOLIFY_PROJECT` |
| New ticket button | **Project** select |

That is the wrong moment to decide. An app that sends alerts (a NAS,
Proxmox, a backup job) doesn't belong to one project, and whoever sets up
the webhook or inbox can't know where future tickets will belong. Deciding
where a ticket belongs is a **triage** job, done when the ticket is read.

## Decision

**All new tickets land in one built-in Tickets queue. Triage optionally
files a ticket into a project.**

Technically the queue is a **system project** that Helm creates and owns:

- Key `TKT` (or the first free one of `TKT`, `TICKET`, `TICKETS`), name
  **Tickets**, with the standard columns (Backlog = *Needs triage*, Ready,
  In progress, Waiting, Done). Helm creates it the first time a ticket
  needs it, so installs that never use Tickets see no change.
- Marked `projects.system_kind = 'tickets'`. It can't be archived, deleted
  or re-keyed. It's hidden from the project list, board navigation and
  every "pick a project" menu. The Tickets page is its only view.

### Why a system project, not project-less tickets

A ticket is a task plus a `tickets` row (migration 030), and
`tasks.project_id` is `NOT NULL`. Projects carry:

- task numbers (`TKT-12`) and the columns that ticket status comes from
- labels and releases
- dependencies and watches
- agent tokens' per-project access limits

Making `project_id` nullable would mean revisiting every query, trigger and
permission check that assumes it. A system project keeps all of those
invariants. In particular, **agent access stays explicit**: a scoped token
sees intake tickets only if it's granted the Tickets project.

Rejected alternatives:

- **Nullable `tasks.project_id`.** It touches the whole store, the release
  triggers and access control, for no user-visible gain.
- **A separate ticket entity that becomes a task on acceptance.** It
  duplicates comments, assignees, status and repeat counting, and alert
  repeats would need to follow the converted task.

## User experience

**Connect apps:**

- Webhook create: **Name**, **Format**, **Assign new tickets to me**.
  There is no Project field.
- Inbox create: **Inbox name**, **Assign to me**. There is no Tickets go
  to field.
- Guided setup: the email step only asks for the inbox name.
- Copy changes from "Every email becomes a ticket in the inbox's project"
  to "Every email becomes a ticket in **Needs triage**".

**Tickets page:**

- **New ticket** has no Project select; tickets start in Needs triage.
- The project filter becomes **All tickets · Not filed · <project>…**.
- Each ticket shows `Not filed` or the project it was filed to.

**Triage (ticket drawer):** a new **File to project…** action.

- Pick a project. The ticket becomes `OPS-41` (in that project's Backlog
  or a chosen column), and the old key `TKT-12` redirects to it
  permanently.
- It **stays a ticket**: it still appears on the Tickets page, now
  showing its project.
- Alert repeats (same family) keep incrementing the filed ticket, wherever
  it now lives.
- Filing is optional. A ticket can be worked and completed in the queue
  without ever going to a project.

## Implementation phases

### Phase 1: one queue (makes the user's request true)

1. **Migration 035:**
   - Add `projects.system_kind` (NULL or `'tickets'`, unique when set).
   - Create the Tickets project with the default columns.
   - `ticket_webhooks.project_id` and `email_inboxes.project_id` are
     no longer read (SQLite keeps the columns). Delivery always uses
     the Tickets project.
2. **Store:**
   - Add a `TicketsProject(ctx)` helper.
   - `IngestAlerts` routes default to it.
   - `CreateEmailInbox` and `CreateTicketWebhook` drop `ProjectRef`.
   - `CreateTicket` without a project uses it.
3. **Guards:** project archive, delete and rename-key refuse the system
   project. Project list and picker endpoints omit it by default, with
   `?include=system` for the Tickets page.
4. **API:**
   - The `project` field becomes optional and is ignored on
     `POST /ticket-webhooks` and `/email-inboxes`.
   - Add `POST /api/v1/tickets` (no project).
   - `POST /projects/{p}/tickets` stays for API compatibility.
   - Update OpenAPI to match.
5. **Coolify:**
   - Without `HELM_COOLIFY_PROJECT`, Coolify alerts go to the queue.
   - If it's set, Helm logs a one-time deprecation warning and still
     uses the queue. The setting is removed next release.
   - `CONFIGURATION.md` says so.
6. **UI:** remove the project fields listed above. The ticket list
   shows `Not filed`.
7. **Existing data:** tickets already in projects stay there, counting as
   *filed*. Existing webhooks and inboxes keep their URLs and addresses
   and simply start delivering to the queue. Release notes say so.

### Phase 2: File to project

1. A store operation `FileTicket(ticketID, projectID, columnID?)` that
   runs in one transaction and moves the ticket's task across projects:
   - New number from the destination counter; the old key is recorded
     in a new `task_key_aliases(old_key, task_id)` table.
   - Column: the destination's Backlog unless one is given.
   - Labels: matched by name in the destination (created if missing).
   - Release and parent links cleared, with the reason in Activity.
   - Dependencies kept, since they are task-to-task links.
   - Watches, comments, checklist, evidence and the `tickets` row kept.
   - `alert_conditions.project_id` updated, so repeats follow the
     ticket.
   - Event: `ticket.filed {from, to, old_key, new_key}`.
2. Key lookups (`/tasks/{KEY}`, search, links in comments) try aliases
   after a miss and redirect.
3. Access: filing needs write access to both projects. Agents can file
   only into projects their token covers.
4. UI: **File to project…** in the drawer and as a bulk action on the
   Tickets page.

Phase 1 ships on its own. Phase 2 can follow once the queue feels right.

## Documentation

- [TICKET_WEBHOOKS.md](TICKET_WEBHOOKS.md): drop the Project setting and
  describe Needs triage and filing.
- [PUBLIC_ACCESS.md § Email inboxes](PUBLIC_ACCESS.md#email-inboxes):
  remove Tickets go to.
- [EMAIL_ALERT_INTAKE_PLAN.md](EMAIL_ALERT_INTAKE_PLAN.md) and
  [CLOUDFLARE_CONNECT_PLAN.md](CLOUDFLARE_CONNECT_PLAN.md): as-built notes.
- [CONFIGURATION.md](CONFIGURATION.md): deprecate `HELM_COOLIFY_PROJECT`.
- [releases/unreleased.md](releases/unreleased.md): an entry per phase.
- [E2E_TESTING.md](E2E_TESTING.md): the contract below.

## Failure contract (E2E)

| Failure | Observable proof |
| --- | --- |
| Intake still asks for a project | The webhook, inbox, guided-setup and New ticket forms have no project field; API calls without `project` succeed. |
| Intake lands somewhere else | A webhook POST, an inbox email, a Coolify alert and a manual ticket all appear in Needs triage as `TKT-n`, marked `Not filed`. |
| The system project leaks into normal project UI | It is absent from the project list, board navigation and project pickers. Archive, delete and key changes are refused. |
| Agents gain access through intake | A scoped token without the Tickets project cannot list or read `TKT` tickets. |
| Existing integrations break | A webhook and an inbox created before the migration keep working unchanged and now deliver to the queue. |
| Filing loses history (phase 2) | After filing `TKT-3` to OPS, it is `OPS-n` with its comments, evidence and repeat count; `TKT-3` redirects; the next alert repeat increments it. |
| Filing bypasses access (phase 2) | Filing into a project the caller cannot write is `403`, and nothing changes. |

## Decisions

- **Queue key:** `TKT`, renameable later like any project key.
- **Filing moves the ticket** (one record, history stays together; Linear
  works the same way) rather than creating a linked task.

## As built

- **Created on first use, not by migration 035.** Migration 035 adds
  `projects.system_kind` (unique when set) and `task_number_aliases`;
  `Store.TicketQueue` creates the project the first time a webhook, inbox,
  intake delivery or New ticket needs it, and in the same transaction points
  existing webhooks and inboxes at it.
- **Hidden in the web app, not the API.** `GET /api/v1/projects` still
  returns the queue (with `system_kind: "tickets"`) so task lookups, agent
  token scopes and deep links keep working. The web app leaves it out of
  the sidebar, project switcher, task and bug pickers and the issue filter;
  command search leaves it out on the server. Agent-token project choices
  still list it, so a token can be granted ticket access.
- **Queue tickets open in place.** "Open full task" on a queue ticket opens
  the drawer over Tickets instead of switching to a board.
- `GET /api/v1/tickets` reports the queue as `queue: {project_id, key}` once
  it exists; the page uses it for **Not filed** and the filter.
- `POST /api/v1/tickets` (`createQueueTicket`) creates a ticket in the
  queue; `POST /api/v1/projects/{p}/tickets` still works for API clients.
- `POST /api/v1/tickets/{ticket}/file` (`fileTicket`) files into any active
  non-queue project, keeping the ticket's status (same column state, else
  Backlog). It's refused for subtasks, tickets with subtasks or
  dependencies, tickets in a shipped release, and while an agent's claim is
  active. Labels are matched by name and created when missing; the release
  link is cleared.
- The `project` field is still accepted (and ignored) on webhook, inbox and
  guided-setup requests, so older scripts keep working.
