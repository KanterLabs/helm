# Ticket webhooks: connect outside apps to Helm Tickets

Any app that can send an HTTP POST with JSON can open Helm tickets: monitoring
alerts, CI failures, form submissions, cron scripts, or Zapier/n8n flows. New
tickets land in **Needs triage** on the Tickets page; you decide which project
a ticket belongs to when you triage it ([Where tickets land](#where-tickets-land)).
Apps that send alerts by email use an
[email inbox](PUBLIC_ACCESS.md#email-inboxes) instead.

## Where tickets land

Every new ticket, whether from a webhook, an email inbox, Coolify or **＋ New
ticket**, lands in one queue: **Tickets → Needs triage**. Nothing asks for a
project up front, because the app sending an alert rarely knows where the
work belongs.

- Queue tickets have keys like `TKT-12` and show **Not filed**.
- **File to project** (the **Project** menu on a ticket) moves it into a
  project when you know where it belongs. It gets that project's next key
  (`TKT-12` becomes `OPS-41`); links to `TKT-12` keep working. Status,
  notes, evidence, labels, watchers and the repeat count come along, and
  the next repeat of the same alert counts on the filed ticket.
- Filing is optional: you can accept, start and complete tickets without
  ever filing them.
- The project filter on Tickets has **Not filed** for the queue.
- A ticket with subtasks, dependencies or a shipped release, or one an
  agent is working on, can't be filed until that changes; Helm says which.

Under the hood the queue is a built-in project named **Tickets**. Helm
creates it the first time a ticket needs it, hides it from project lists,
and doesn't let it be archived. Agent tokens see queue tickets only if
they're granted that project. Design: [TICKET_QUEUE_PLAN.md](TICKET_QUEUE_PLAN.md).

## 1. Create a webhook URL

An administrator opens **Tickets → Connect apps** and enters:

| Setting | Meaning |
| --- | --- |
| Name | Shown as the ticket's source and in Activity, e.g. `Grafana alerts`. |
| Format | **Generic JSON** (below) or **Coolify notifications** (Coolify's own webhook payload). |
| Assign new tickets to me | Otherwise tickets start unassigned. |

Helm shows the URL **once**:

```text
https://<your-helm>/api/v1/hooks/tickets/hk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

The URL is the credential; no headers or tokens are needed. Keep it private.
**Rotate** issues a new URL and stops the old one immediately; **Disable**
stops it permanently. Existing tickets are kept either way. The table shows
each webhook's last four characters, delivery count and last use.

The same actions are available over the API to human administrators:
`GET/POST /api/v1/ticket-webhooks`, `POST /api/v1/ticket-webhooks/{id}/rotate`
and `DELETE /api/v1/ticket-webhooks/{id}`.

## 2. Send a ticket

```sh
curl -X POST "$HELM_WEBHOOK_URL" \
  -H 'Content-Type: application/json' \
  -d '{
        "title": "Disk almost full on db-1",
        "description": "`/var/lib/postgresql` is at 93%.",
        "priority": "high",
        "dedupe_key": "db-1:disk",
        "source": "grafana",
        "url": "https://grafana.example.com/d/disk",
        "fields": {"host": "db-1", "usage": "93%"}
      }'
```

| Field | Required | Meaning |
| --- | --- | --- |
| `title` | yes | Ticket title (max 300 characters). |
| `description` | no | Markdown details shown under "What needs attention" (max 20,000). |
| `priority` | no | `low`, `normal` (default), `high` or `urgent`. |
| `dedupe_key` | no | Stable ID for the problem. See **Repeats** below. |
| `source` | no | Name of the sending app (letters, digits, space, `.`, `_`, `-`; max 60). |
| `url` | no | `http(s)` link back to the sending app (max 1000). |
| `fields` | no | Up to 20 facts, e.g. `{"host": "db-1"}`. Values are text (max 300), numbers or booleans. |

Unknown fields are rejected, so a typo such as `tittle` fails loudly instead
of being silently dropped. Bodies are limited to 64 KiB.

## 3. Read the response

| Status | Body | Meaning |
| --- | --- | --- |
| `201` | `{"disposition":"created","occurrence_count":1,"ticket":{"id":"…","key":"OPS-61","url":"https://…/p/ops/tasks/OPS-61"}}` | A new ticket was opened. |
| `200` | `{"disposition":"repeated","occurrence_count":4,"ticket":{…}}` | Same `dedupe_key` as an open ticket; only its repeat count went up. |
| `400` | `{"error":{"code":"invalid_ticket","message":"title is required","details":{"field":"title","allowed_fields":[…]}}}` | Fix the named field; nothing was created. |
| `404` | `{"error":{"code":"not_found",…}}` | Unknown, rotated or disabled URL. |
| `413` | | Body over 64 KiB. |
| `503` | `{"error":{"code":"intake_unavailable",…}}` | The ticket queue's Needs triage column is gone; retry after an admin fixes it. |

## Repeats

- **Without `dedupe_key`:** every post opens a new ticket.
- **With `dedupe_key`, while its ticket is open:** posts only increase the
  ticket's repeat count (shown as `×N` in the queue and in Alert source).
  Nothing about the ticket a person edited changes, and the assignee is not
  notified again.
- **With `dedupe_key`, after its ticket is completed (or deleted):** the next
  post opens a **new** ticket that links to the previous one.

Concurrent posts with the same key never create duplicate tickets.

## What the ticket looks like

- An ordinary task in the ticket queue's Needs triage column, shown in
  Tickets → Needs triage as **Not filed**.
- `ticket.origin` is `alert`, and the ticket is never claimed for an agent.
- The **Alert source** panel shows the webhook name, `source`, `url` (as
  *Link*), each `fields` entry, the repeat count and first/last receipt.
- Activity shows the webhook name as the actor for creation and repeats.

## Coolify

Create a webhook with format **Coolify notifications**, then paste the URL into
Coolify → Notifications → Webhook. Coolify's own SafeWebhookUrl check blocks
private addresses unless allowlisted under Settings → Advanced. The
operator-configured `/api/v1/intake/coolify/{secret}` route (environment
variables, see `COOLIFY_EMAIL_INTAKE_PLAN.md`) keeps working.

## Public URL and email addresses

To let apps outside your network reach these webhooks, use **Reach Helm
from outside → Sign in with Cloudflare**; see
[PUBLIC_ACCESS.md](PUBLIC_ACCESS.md#set-up-with-cloudflare). Apps that only
send email use [email inboxes](PUBLIC_ACCESS.md#email-inboxes).

Each webhook row then has **Send test**, a real ticket through the public
URL ([Test it](PUBLIC_ACCESS.md#test-it)). **Rotate** and **Disable** are
under **More**.

## API reference

The machine-readable contract is `/openapi.json` (operations
`postTicketWebhook`, `listTicketWebhooks`, `createTicketWebhook`,
`rotateTicketWebhook`, `disableTicketWebhook`,
`sendTestTicket`, `createQueueTicket`, `fileTicket`) and, for the Coolify path, `receiveCoolifyAlert`. Public
access operations are listed in
[PUBLIC_ACCESS.md](PUBLIC_ACCESS.md#api-reference).
