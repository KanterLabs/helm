# Public access: let outside apps reach Helm

Self-hosted Helm usually sits behind NAT, a VPN or a private network that
outside apps cannot reach. Public access fixes that with Cloudflare, without
opening ports: outside apps get a **public URL** for
[ticket webhooks](TICKET_WEBHOOKS.md), and apps that send alerts by email
get **email inboxes** whose mail becomes tickets. Nothing else about Helm
becomes public.

Everything here lives in **Tickets → ⇄ Connect apps → Reach Helm from
outside** and is for administrators. **Learn more** opens this guide inside
Helm.

Related: design and decisions in
[CLOUDFLARE_CONNECT_PLAN.md](CLOUDFLARE_CONNECT_PLAN.md) and
[EMAIL_ALERT_INTAKE_PLAN.md](EMAIL_ALERT_INTAKE_PLAN.md); test contracts in
[E2E_TESTING.md](E2E_TESTING.md).

## What becomes public

| Reachable from the internet | Not reachable |
| --- | --- |
| `POST /api/v1/hooks/tickets/<secret>` (webhooks), `/api/v1/intake/coolify/<secret>`, the email Worker's route and one-time test probes | The dashboard, sign-in, every other API route. Cloudflare answers them with 404, and the tunnel only connects to a separate listener that serves nothing else. |

## Set up with Cloudflare

You need a Cloudflare account with your domain on it. In **Reach Helm from
outside**:

1. **Select Sign in with Cloudflare.** Cloudflare asks which account Helm may
   use and shows the permissions (the email ones are optional). Approve, and
   a short page on `helm-connect…workers.dev` shows the Helm you are being
   sent back to; check it is yours and select **Continue to Helm**.
2. **Check the plan Helm shows.** Your domain is chosen for you when only one
   can be used (otherwise pick one; domains the connection cannot manage DNS
   for are marked **No DNS access**). Helm shows the public URL it will
   create, such as `https://hooks.example.com` (**Change** to pick another
   unused name); a public URL that already works is kept.
3. **Email (optional).** "Turn on email and create an inbox" is ticked when
   the domain has Email Routing on. Name the first inbox (default
   **Alerts**); its emails become tickets in Needs triage. If plus addressing
   is off, tick the box that turns it on; it also lets mail to any
   `name+anything@` reach `name@` across the domain. **Email options** set
   the address name (default `helm-alerts`) and an optional **fallback**
   address for mail Helm cannot accept.
4. **Select Set up** and watch the checklist:

   | Step | What happens |
   | --- | --- |
   | Public URL | Tunnel + DNS record, webhook paths only. An already active public URL is kept as is. |
   | Connect the tunnel | `cloudflared` registers with Cloudflare's edge. |
   | Reach Helm from the internet | Helm calls its own public URL through Cloudflare (DNS can take a minute). |
   | Turn on email and create an inbox | Email Worker + routing rule, then your first inbox, when requested. Already-on email is kept. |
   | Forget the Cloudflare credential | The sign-in or token is dropped (sign-ins are revoked at Cloudflare). |

   If the tunnel never connects or the URL never answers, Helm removes the
   public URL it just created, so nothing is left half-built. If only email
   fails, the working public URL stays and the step says why.
5. **Note your inbox address,** shown in the result (for example
   `helm-alerts+alerts-x7k2qm@example.com`), then select **Done**. The panel
   now shows a short summary. [Try it](#test-it).

**Later:** **Turn on email** (when it is off) or **Run setup again**
signs in again and reuses what already works. Everything else, such as
connector status, recent emails, removal, history and the manual forms, is
under **Details, history and manual setup**.

**No sign-in option?** Select **Use an API token instead**. **Create token
on Cloudflare** opens Cloudflare's token page with every permission
selected; choose your account, and under **Zone resources** your domain, then
**Continue to summary → Create token**. Paste the token and select
**Connect**; the rest is the same.

**Credentials.** Helm keeps the sign-in or token only in memory, for at most
30 minutes, tied to the administrator who connected, and never in the
database, logs or responses. Removing things later asks you to connect
again. Revoke a sign-in any time in Cloudflare under **Manage OAuth
authorizations**.

**Sign in with Cloudflare availability.** Helm's Cloudflare app is
currently *private*: only members of the KanterLabs Cloudflare account can
approve it. Everyone else uses the token option, which does exactly the
same setup. Operators can hide the button with `HELM_CLOUDFLARE_OAUTH=off`
or point Helm at their own Cloudflare app (see [Settings](#settings)).

## Test it

Both checks travel the same path real apps use.

| Check | What you do | What it proves |
| --- | --- | --- |
| **Email an inbox** | Send any email to an inbox's address from any mailbox (**Email it** opens your mail app), for example from your phone. | Your mail provider → Cloudflare's MX → the routing rule → the email Worker → Helm. A ticket appears in Tickets → Needs triage within seconds, and the inbox shows "1 email". |
| **Send test** | Select it on a webhook. | Helm posts a ticket to its own public URL through Cloudflare and the tunnel, and shows the ticket it opened (or why it failed). |

If an email does not turn into a ticket, look under **Details, history and
manual setup → Email → Recent emails** (refused mail is listed with the
reason), check that the domain's MX records point to Cloudflare (Email
Routing shows them) and that plus addressing is on.

## Public URL (Cloudflare Tunnel)

The public URL publishes **only** the webhook routes on a public HTTPS
hostname through a Cloudflare Tunnel, with no open ports, router changes or
certificates. [Set up with Cloudflare](#set-up-with-cloudflare) creates it
for you; to do it by hand, open **Details, history and manual setup → Set
up manually** on the Public URL card:

1. Create a token with the **Create token** link (or by hand with
   Account › Cloudflare Tunnel › Edit, plus Zone › Zone › Read and
   Zone › DNS › Edit for the zone that holds your hostname).
2. Enter a **new subdomain** such as `hooks.example.com`, not the domain
   itself: Helm creates a DNS record for it, so the name must be unused.
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
  except webhook POSTs, self-test probes and `/healthz`. A changed tunnel
  configuration still cannot reach the dashboard or the rest of the API.

Watching it:

- **Status:** the card shows a Live / Connecting / Reconnecting badge, how
  many Cloudflare edge connections are up and in which data centres, when it
  connected, restarts and the last connector error. It keeps refreshing, so
  a dropped tunnel shows as Reconnecting without reloading the page.
- **Test public URL:** Helm requests its own public hostname and expects
  the hooks listener to echo a one-time, 30-second nonce
  (`/api/v1/hooks/tickets/probe/<nonce>`). Success proves DNS, Cloudflare,
  the tunnel and its routing all reach this Helm; no ticket is created. A
  failure explains the likely cause (DNS not resolving yet, Cloudflare 530
  when the connector is down, 404 when the tunnel's routing was changed).
  Only the hooks listener answers probes, and only for a nonce it issued.
- **Send test** (on each webhook in the table, while a public URL is
  active): Helm posts a real test ticket for that webhook to its own public
  hostname, through Cloudflare and the tunnel, exactly as an outside app
  would, and shows the ticket it opened or why it failed. The request uses
  a one-time nonce bound to that webhook instead of the webhook's secret
  (which Helm does not keep). Test tickets are low priority and share one
  `dedupe_key`, so repeated tests count on a single open ticket.
- **Details:** the tunnel, DNS record, zone and account IDs, and the exact
  public paths, under *Cloudflare resources and exposed paths*.
- **Elsewhere:** the **Connect apps** button shows a **● Public** pill while
  a public URL is active, and the Admin page shows whether this Helm is
  reachable from the internet, with a **Manage** link.

**Remove public URL** stops `cloudflared` and deletes the tunnel token
immediately. If you also enter an API token, Helm deletes the DNS record and
the tunnel in Cloudflare. Otherwise the URL is listed under **Earlier public
URLs** as *Needs cleanup* with the tunnel and DNS record IDs; **Finish
cleanup…** deletes them later with a token. Retrying without a token changes
nothing.

Requirements and settings:

- **Docker image:** includes `cloudflared` (pinned, checksum-verified).
  Other installs need `cloudflared` on `PATH`, or set
  `HELM_CLOUDFLARED_BINARY`.
- **Network:** the server needs outbound access to Cloudflare on TCP/UDP
  7844 and HTTPS 443.
- **Disabling the feature:** set `HELM_PUBLIC_HOOKS_ADDR=off`.


## Email inboxes

Your apps already email you their alerts: backups, NAS boxes, Proxmox,
Coolify, cron. Point them at a Helm **inbox** instead and every email
becomes a ticket, with no mailbox for Helm to read and no password stored.

```
your apps ──email──▶ helm-alerts+homelab-ops-x7k2qm@example.com
                         │ Cloudflare Email Routing (your domain)
                         ▼
                     Helm ──▶ ticket in Tickets → Needs triage
```

**Create an inbox:** **Email inboxes → Inbox name, Assign to me → Create
inbox.** (Guided setup creates the first one for you.) The
address appears at once and stays visible, with **Copy** and **Email it**.
Put it in your apps' notification settings in place of your own address.

**What an email becomes:**

- **Subject** → ticket title (an empty subject becomes `Email from
  <sender>`); **text part** → description (HTML-only mail is converted to
  text). `X-Priority: 1`/`2`, `Importance: high` or `Priority: urgent` set
  high priority. Attachments are listed on the ticket but not stored.
- **Repeats:** mail from the same From address with the same subject
  (ignoring case, spacing and `Re:`/`Fwd:`) adds to the open ticket's
  repeat count instead of opening another; after it is completed the next
  one opens a new linked ticket. A re-delivered message (same `Message-ID`)
  changes nothing.
- **Evidence** on the ticket: From, Date, Message-ID and attachment names,
  plus SPF/DKIM/DMARC results when Cloudflare reports them.

**Who can send:** anyone who knows the address, like any email address.
Cloudflare already rejects mail that fails the sender's DMARC policy or
fails both SPF and DKIM, and the random part of the address keeps it from
being guessed. If an address attracts spam, **More → Replace address**
issues a new one (the old one bounces at once); **More → Turn off** stops an
inbox. Tickets always stay.

**Each inbox row** shows how many emails arrived and when the last one came.

### How mail reaches Helm

Cloudflare Email Routing receives the mail for one rule,
`helm-alerts@your-domain`; with plus addressing every
`helm-alerts+<inbox>@` address reaches it. A small Email Worker that Helm
deploys passes each message to Helm through the
[public URL](#public-url-cloudflare-tunnel). Design and trade-offs:
[EMAIL_ALERT_INTAKE_PLAN.md](EMAIL_ALERT_INTAKE_PLAN.md).

Turning email on by hand (instead of guided setup) is under **Details,
history and manual setup → Email → Set up manually** and needs:

1. An active **public URL** (the Worker delivers through it).
2. **Email Routing enabled** in Cloudflare for the domain (Email › Email
   Routing), with Cloudflare's MX records.
3. A Cloudflare API token with Account › Workers Scripts › Edit; Zone ›
   Zone › Read and Email Routing Rules › Edit; Zone › Zone Settings › Read
   (Edit if plus addressing is off); Account › Email Routing Addresses ›
   Read (only with a fallback address).

Helm deploys the Worker `helm-email-…` and the routing rule; if any step
fails it deletes what it created and restores plus addressing. The token is
used once and never stored.

### Bounces and Recent emails

| What happened | Sender sees | Recent emails shows |
| --- | --- | --- |
| Unknown, replaced or turned-off inbox address | Bounce: "No Helm inbox uses this address" | Bounced: unknown address |
| No valid From header | Bounce with the reason | Bounced: unreadable |
| Larger than 1 MiB | Bounce, or delivered to the fallback | Bounced: too large |
| Helm or the tunnel down | Delivered to the fallback after two retries | Nothing (Helm never saw it) |
| Email turned off | Bounce: "Helm is not accepting email at this address" | Nothing |

Without a fallback address, mail that reaches the Worker while Helm is down
is left to Cloudflare, which does not document what it does when a Worker
fails; set a fallback if you cannot afford to lose alerts.

**Turning email off** (Email card → **Turn off email**) stops all inboxes at
once. With a token Helm deletes the Worker and rule; without one they are
listed under **Earlier email setups** as *Needs cleanup* with **Finish
cleanup…**. Plus addressing is left on. The public URL cannot be removed
while email is on.

## Troubleshooting

| You see | Do this |
| --- | --- |
| "hostname must be a subdomain…" | Enter a new subdomain such as `hooks.example.com`, not `example.com`. Guided setup picks one for you. |
| "this credential is not allowed to …" | Create the token with **Create token on Cloudflare** (it selects every permission), and on Cloudflare's page include your domain under **Zone resources**. With sign-in, keep the requested permissions. |
| "the credential is not allowed to see any domains" | Same as above: the token was created without a zone. |
| Your domain shows **No DNS access** | Create the token again and include that domain under **Zone resources** (or approve the DNS permission when signing in). |
| "The tunnel did not connect within 45 seconds" | The server needs outbound access to Cloudflare on port 7844 (TCP and UDP) and 443. |
| "DNS for … does not resolve yet" | Wait a minute and select **Test public URL**. |
| "Cloudflare answered HTTP 530" | The connector is down; check the Connector status on the Public URL card. |
| Sign-in shows an error at Cloudflare | Use the token option; Helm's Cloudflare app may not be available to your account yet. |
| "Email Routing is not turned on for …" | In Cloudflare open **Email › Email Routing** for the domain, enable it, then run setup again. |

## Settings

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_PUBLIC_HOOKS_ADDR` | `127.0.0.1:8091` | Loopback listener the tunnel connects to; `off` disables public access. |
| `HELM_CLOUDFLARED_BINARY` | `cloudflared` | Connector binary (bundled in the Docker image). |
| `HELM_CLOUDFLARE_API_BASE` | `https://api.cloudflare.com/client/v4` | Cloudflare API (tests use a local fixture). |
| `HELM_CLOUDFLARE_OAUTH` | `on` | `off` hides **Sign in with Cloudflare**. |
| `HELM_CLOUDFLARE_OAUTH_CLIENT_ID` | Helm's published client | Use your own Cloudflare OAuth client (public, PKCE, no secret). |
| `HELM_CLOUDFLARE_OAUTH_RELAY_URL` | `https://helm-connect.shanekanterman04.workers.dev/cloudflare/callback` | The client's registered redirect; deploy your own relay from `deploy/cloudflare-connect-relay`. |
| `HELM_CLOUDFLARE_DASHBOARD_URL` | `https://dash.cloudflare.com` | Hosts the OAuth endpoints (tests use a local fixture). |
| `HELM_PUBLIC_PROBE_ORIGIN` | empty | Tests only: send self-tests to a loopback origin. |

## API reference

Operations in `/openapi.json`: `getCloudflareConnect`,
`connectCloudflareToken`, `disconnectCloudflare`, `startCloudflareOAuth`,
`finishCloudflareOAuth`, `listCloudflareZones`, `startCloudflareSetup`,
`getCloudflareSetup`, `getPublicEndpoints`, `createPublicEndpoint`,
`testPublicEndpoint`, `disablePublicEndpoint`, `getEmailIntake`,
`createEmailIntake`, `disableEmailIntake`, `postTicketEmail`,
`listEmailInboxes`, `createEmailInbox`, `disableEmailInbox`,
`replaceEmailInboxAddress`, `sendTestTicket`, `getHelpDocument`.
