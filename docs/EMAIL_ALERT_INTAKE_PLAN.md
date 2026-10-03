# Email addresses for ticket webhooks

Status: implemented on `beta` (2026-10-03). User guide:
[TICKET_WEBHOOKS.md § Email addresses](TICKET_WEBHOOKS.md#email-addresses).
Failure contract: [E2E_TESTING.md § Email intake failure contract](E2E_TESTING.md#email-intake-failure-contract).
This supersedes the mailbox-connector part of
[COOLIFY_EMAIL_INTAKE_PLAN.md](COOLIFY_EMAIL_INTAKE_PLAN.md).

## Goal

Many alert sources can only send email (backup tools, NAS boxes, cron,
older monitoring). Every ticket webhook should therefore also have an email
address. Mail sent to it opens or repeats a ticket exactly like a `curl` POST
to the webhook URL, and admins can see what arrived and what was refused.

Constraints that shape the design:

- **Self-hosted:** Helm usually runs behind NAT. Receiving SMTP needs port
  25 and MX records, and a Cloudflare Tunnel does not carry SMTP.
- **No stored mail credentials:** Helm already avoids storing Cloudflare
  tokens. Polling a mailbox over IMAP would mean storing a mailbox password
  plus UID state and reset recovery.
- **Same ticket semantics:** repeats, completion and the Alert source panel
  must behave as they do for webhooks.

## Decision: Cloudflare Email Routing → Email Worker → Helm

```
sender ──SMTP──▶ Cloudflare MX (Email Routing: DMARC, SPF-or-DKIM enforced)
                   │ rule: helm-alerts@example.com → Worker (plus addressing on)
                   ▼
          Email Worker "helm-email-<id>" (deployed by Helm, ~100 lines)
                   │ POST message/rfc822 + envelope headers
                   ▼
   https://<Public URL>/api/v1/hooks/tickets/email/<intake secret>
                   │ (existing tunnel, existing webhook-only ingress)
                   ▼
   Helm hooks listener → MIME parse → webhook lookup by +tag → ticket
```

Chosen over IMAP polling because it reuses the Public URL tunnel and token
model, stores no mail credentials, and pushes immediately. IMAP remains a
possible later option for installs without Cloudflare; nothing here
prevents it.

### Verified Cloudflare facts (2026-10-03)

| Fact | Consequence | Source |
| --- | --- | --- |
| Email Routing supports RFC 5233 plus addressing; `user+detail@` matches the `user@` rule and `message.to` keeps `+detail`. A more specific `user+detail@` rule wins. | One rule serves every webhook; the tag after `+` selects the webhook. | [Email routing addresses](https://developers.cloudflare.com/email-service/configuration/email-routing-addresses/) |
| Plus addressing is a per-zone setting, `support_subaddress`, read with `GET` and changed with `PATCH /zones/{zone}/email/routing`. | Turning it on changes delivery for every address in the zone, so Helm asks first and never enables it silently. | [Email Routing settings](https://developers.cloudflare.com/api/resources/email_routing/methods/get/) |
| Rules: `POST /zones/{zone}/email/routing/rules` with `matchers:[{type:"literal",field:"to",value}]` and `actions:[{type:"worker",value:[script]}]`; 200 rules per domain. | Helm creates exactly one rule. | [Create routing rule](https://developers.cloudflare.com/api/resources/email_routing/subresources/rules/methods/create/), [limits](https://developers.cloudflare.com/email-service/platform/limits/) |
| Workers upload: `PUT /accounts/{account}/workers/scripts/{name}` multipart with a `metadata` part (`main_module`, `compatibility_date`, `bindings` incl. `secret_text`). | The Worker's Helm URL (which contains the intake secret) is a secret binding, never plain text. | [Upload Worker](https://developers.cloudflare.com/api/resources/workers/subresources/scripts/methods/update/) |
| Email Worker API: `from`, `to`, `headers`, `raw` (stream), `rawSize`, `setReject(reason)` (permanent), `forward(rcpt, headers)`. | No way to ask the sender to retry later. | [Runtime API](https://developers.cloudflare.com/email-routing/email-workers/runtime-api/) |
| Behaviour when the handler throws is undocumented. | The Worker retries Helm itself and can forward to a fallback address so mail is never silently lost. | same |
| Email Routing rejects mail that fails the sender's DMARC policy, and mail that fails both SPF and DKIM. Inbound limit 25 MiB. | The sender is authenticated before the Worker runs. | [Postmaster](https://developers.cloudflare.com/email-routing/postmaster/) |
| Open bug (May 2026): Worker deliveries can lack `Authentication-Results`. | Helm records SPF/DKIM/DMARC verdicts when present but never depends on them. | [workerd#6740](https://github.com/cloudflare/workerd/issues/6740) |

## User experience

**Tickets → Connect apps → Email** (admins only, below Public URL):

1. Requires an active Public URL (the Worker posts through it). Without one
   the card explains why and links up to the Public URL card.
2. Fields: domain (defaults to the Public URL's zone), address name
   (default `helm-alerts`), optional fallback address, Cloudflare token.
   If plus addressing is off for the zone, a required checkbox explains
   the zone-wide effect before Helm turns it on.
3. Helm deploys the Worker and rule, then shows the base address, Worker
   name, rule, plus-addressing state and fallback.

**Per webhook:** creating a webhook while email is set up reveals both the
URL and an address such as `helm-alerts+k3j9x2m4q7ab5cde@example.com`, once,
with copy buttons. Existing webhooks get an **Email address…** action that
creates (or replaces) the address and reveals it once. Rows show a masked
hint (`helm-alerts+…5cde@example.com`). The address is a bearer credential
just like the URL: anyone who knows it can open tickets.

**Recent emails:** the card lists the last 20 receipts: time, sender,
subject, outcome (`created`, `repeated`, `retained`, or a refusal reason)
and the ticket link. Refusals the sender also sees as a bounce are listed
too, so "my alert never arrived" is answerable.

**Removing:** with a token Helm deletes the rule and Worker; without one it
marks them *Needs cleanup* with IDs and a **Finish cleanup…** action, as for
Public URLs. Helm never turns plus addressing back off (other addresses may
now rely on it); the card says so. A Public URL cannot be removed while
email intake is active.

Admin page: the Public access line also states whether email intake is on.

## How an email becomes a ticket

| Input | Rule |
| --- | --- |
| Recipient | Envelope `to` must be `<name>+<tag>@<domain>` of the active intake (case-insensitive). The tag's SHA-256 selects an enabled webhook. Otherwise: refused `unknown_recipient` (Worker bounces "No Helm address"). |
| Title | Decoded `Subject` (RFC 2047), one line, max 300 chars; empty → `Email from <sender>`. |
| Description | `text/plain` part, else HTML converted to text, else the decoded body; quoted-printable/base64 and UTF-8/US-ASCII/ISO-8859-1/Windows-1252 decoded; max 20,000 chars with a truncation note. Attachments are ignored (named in evidence). |
| Priority | `X-Priority: 1`/`2`, `Importance: high` or `Priority: urgent` → high; otherwise normal. |
| Repeats | Family = header From address + normalized subject (lower-cased, whitespace collapsed, leading `Re:`/`Fwd:` removed). Same family while the ticket is open → repeat count; after completion → new linked ticket. The envelope sender is not used because bulk senders vary it per message (VERP). |
| Duplicates | Receipt key = normalized `Message-ID`, or SHA-256 of the raw message when absent. A receipt already recorded for this intake returns `duplicate` and changes nothing. The receipt, ticket and webhook statistics commit in one transaction. |
| Evidence | From, Date, Message-ID, received time, attachment names, and SPF/DKIM/DMARC verdicts when Cloudflare supplied them. Never the recipient tag. |
| Size | The Worker refuses messages over 1 MiB (or forwards them to the fallback) and reports the refusal to Helm without the body. |

## Security model

- **Two secrets, both stored only as SHA-256:** the intake secret in the
  Worker's URL (`em_…`, 256 bits) proves a request came from Helm's Worker;
  the per-webhook tag (16 base32 characters, 80 bits) selects the webhook.
  Neither appears in responses after creation, logs, metrics or evidence.
- **Sender authentication** is Cloudflare's: DMARC policy plus SPF-or-DKIM.
  Helm does not trust `Authentication-Results` from message content; it only
  records the verdict header the Worker copies from Cloudflare's own
  `mx.cloudflare.net` result, when present.
- **Public surface:** the email route lives under the existing
  `/api/v1/hooks/tickets/` ingress, so no tunnel change is needed and the
  hooks-only listener still serves nothing else. Wrong intake secrets get the
  same `404` as unknown webhooks.
- **Content is data:** MIME is parsed with bounded sizes; HTML is converted
  to text, never rendered; links are not followed; attachments are not
  stored.
- **Token** used once for setup/removal, never stored, logged or returned.
  Required permissions: Account › Workers Scripts › Edit; Zone › Zone ›
  Read; Zone › Email Routing Rules › Edit; Zone › Zone Settings › Read
  (Edit to turn on plus addressing); Account › Email Routing Addresses ›
  Read (only with a fallback address).

## Data model (migration 033)

- `email_intakes`: provider, domain, local part, account/zone IDs, Worker
  name, rule ID, `secret_sha256`, fallback address, whether Helm enabled plus
  addressing, the Public URL it posts through, status, cleanup flag,
  creator and timestamps. At most one active.
- `ticket_webhooks` gains `email_tag_sha256` (unique), `email_tag_hint`,
  `email_tag_created_at`.
- `email_receipts`: intake, receipt key digest (unique per intake), webhook,
  sender, subject, outcome, ticket, repeat count, reason, time. Pruned to the
  newest 500 per intake.

## API

| Route | Purpose |
| --- | --- |
| `GET /api/v1/email-intake` | Active intake, history, recent receipts, required permissions, prerequisites. Admins. |
| `POST /api/v1/email-intake` | `{domain, local_part, fallback_address?, enable_subaddressing, api_token}` provisions. Admins. |
| `DELETE /api/v1/email-intake/{id}` | Optional `{api_token}`; same cleanup model as Public URLs. Admins. |
| `POST /api/v1/ticket-webhooks/{id}/email` | Creates or replaces the webhook's address; returns it once. Admins. |
| `POST /api/v1/hooks/tickets/email/{secret}` | Worker → Helm. `message/rfc822` body ≤ 1 MiB with `X-Helm-Envelope-To`/`-From`. `201` created, `200` repeated/retained/duplicate, `404` unknown address, `400` unreadable, `413` too large. |

## Worker contract

Helm embeds the Worker source in its binary, so each release deploys a
reviewed version. The Worker:

1. Rejects (or forwards to the fallback) messages over 1 MiB and tells Helm
   with `X-Helm-Oversize` and no body.
2. POSTs the raw message with envelope headers to `env.HELM_INTAKE_URL`.
3. On `2xx` stops. On `400`/`404`/`413` calls `setReject` with Helm's
   message (the sender gets a bounce). On network errors or `5xx`
   (including Cloudflare `530` when the tunnel is down) retries twice with
   2 s and 4 s waits.
4. After the last failure forwards the original to the fallback address
   with `X-Helm-Intake-Failed`; without a fallback it throws, leaving the
   outcome to Cloudflare's undocumented behaviour (documented to users).

## Failure modes and handling

| Failure | Handling |
| --- | --- |
| Token lacks a permission | Setup fails naming the operation; every resource created so far is deleted and plus addressing is restored. |
| Email Routing not enabled on the zone | Refused before anything is created, with the dashboard step. |
| Address already routed in Cloudflare | Refused before anything is created. |
| Helm down / tunnel down | Worker retries, then fallback forward or throw. |
| Message re-delivered | Receipt dedup → `duplicate`, no ticket or count change. |
| Webhook disabled or address replaced | Old address bounces as unknown; refusal listed in Recent emails. |
| Public URL removal attempted while active | `409`; remove email intake first. |
| Unreadable MIME / missing From | `400` bounce, refusal listed. |

## Tests

Before code: the failure contract in `E2E_TESTING.md`. Proof:

- **Go unit tests:** MIME parsing (plain, multipart/alternative, HTML-only,
  base64, quoted-printable, RFC 2047 subjects, Latin-1, missing subject,
  attachments), recipient parsing, family normalization, priority.
- **Store tests:** receipt idempotency inside the ingest transaction.
- **E2E** (`web/e2e/email-intake.spec.ts`) against a real Helm process and
  the fake Cloudflare API, which accepts Worker uploads, settings and rules.
  The test runs the **uploaded Worker source itself** in Node with a
  synthetic `ForwardableEmailMessage` against Helm's hooks listener, so the
  Worker contract is tested, not mocked.
- **Real Cloudflare** on beta with a throwaway address before calling it
  done: deploy, send a real email, see the ticket, repeat, bounce for an
  unknown tag, remove, and confirm in Cloudflare.

## Out of scope (later)

IMAP intake for non-Cloudflare installs; per-webhook sender allowlists;
storing attachments; HTML rendering; per-address mutes; replying by email.
