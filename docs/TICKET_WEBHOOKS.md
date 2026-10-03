# Ticket webhooks: connect outside apps to Helm Tickets

Any app that can send an HTTP POST with JSON can open Helm tickets: monitoring
alerts, CI failures, form submissions, cron scripts, or Zapier/n8n flows. New
tickets land in the chosen project's **Needs triage** queue on the Tickets page.

## 1. Create a webhook URL

An administrator opens **Tickets → Connect apps** and enters:

| Setting | Meaning |
| --- | --- |
| Name | Shown as the ticket's source and in Activity, e.g. `Grafana alerts`. |
| Project | Project whose Backlog receives the tickets. |
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
| `503` | `{"error":{"code":"intake_unavailable",…}}` | The target project or its Backlog column is gone; retry after an admin fixes it. |

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

- An ordinary task in the project's Backlog column, shown in Tickets → Needs
  triage.
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

## Public URL (Cloudflare Tunnel)

Self-hosted Helm often sits behind NAT, a VPN or a private network that
outside apps cannot reach. **Tickets → Connect apps → Public URL** publishes
only the webhook routes on a public HTTPS hostname through a Cloudflare
Tunnel, with no open ports, router changes or certificates.

1. In Cloudflare, create an API token with:
   - Account › Cloudflare Tunnel › Edit
   - Zone › Zone › Read and Zone › DNS › Edit, for the zone that holds your
     hostname
2. Enter an unused hostname such as `hooks.example.com` and the token.
3. Helm uses the token once to:
   - create a Cloudflare Tunnel named `helm-<hostname>`;
   - route **only** `^/api/v1/(hooks/tickets|intake/coolify)/` to Helm, with
     everything else getting 404 at Cloudflare;
   - add a proxied CNAME;
   - start `cloudflared`.

   If any step fails, Helm deletes whatever it created and nothing is left
   behind.
4. New webhook URLs use `https://<hostname>/api/v1/hooks/tickets/…`. A URL
   created earlier works too: keep its path and replace the host with the
   public hostname.

Safety:

- **API token:** never stored, logged or returned.
- **Tunnel run token:** kept in an owner-only file beside the database (not
  in it), and passed to `cloudflared` through its environment, not the
  command line.
- **Second gate:** `cloudflared` connects to a separate loopback listener
  (`HELM_PUBLIC_HOOKS_ADDR`, default `127.0.0.1:8091`) that serves nothing
  except webhook POSTs and `/healthz`. A changed tunnel configuration still
  cannot reach the dashboard or the rest of the API.

**Remove public URL** stops `cloudflared` and deletes the tunnel token
immediately. If you also enter an API token, Helm deletes the DNS record and
the tunnel in Cloudflare. Otherwise it marks them for manual cleanup and shows
their IDs.

Requirements and settings:

- **Docker image:** includes `cloudflared` (pinned, checksum-verified).
  Other installs need `cloudflared` on `PATH`, or set
  `HELM_CLOUDFLARED_BINARY`.
- **Network:** the server needs outbound access to Cloudflare on TCP/UDP
  7844 and HTTPS 443.
- **Disabling the feature:** set `HELM_PUBLIC_HOOKS_ADDR=off`.

The machine-readable contract is `/openapi.json` (operations
`postTicketWebhook`, `listTicketWebhooks`, `createTicketWebhook`,
`rotateTicketWebhook`, `disableTicketWebhook`, `getPublicEndpoints`,
`createPublicEndpoint`, `disablePublicEndpoint`).
