# Connect Cloudflare: guided, automatic public access

Status: implemented on `beta` (2026-10-03), both phases. User guide:
[PUBLIC_ACCESS.md](PUBLIC_ACCESS.md#connect-cloudflare-guided-setup); failure
contract: [E2E_TESTING.md](E2E_TESTING.md#connect-cloudflare-failure-contract);
relay: [deploy/cloudflare-connect-relay](../deploy/cloudflare-connect-relay/README.md).
See [As built](#as-built) for what changed from this plan.

## Problem

Making Helm reachable for outside apps works, but setting it up is expert
work today:

- The admin must know to invent an unused subdomain; typing the domain
  itself fails with "hostname must be a subdomain".
- They must create a Cloudflare API token by hand, choose seven permissions
  from memory and copy it into two separate cards.
- Public URL and Email are separate cards with separate forms, so the order
  and dependencies are left to the user to discover.

Observed on beta (2026-10-03): the owner entered `shanekanterman.dev` and
asked "what do I do here".

## Outcome

One **Connect Cloudflare** flow: the admin authorizes once, picks a domain
from a list, and Helm sets up everything with a visible checklist: a
public URL on a free hostname, a verified round trip, a test ticket, and
optionally email addresses. Every step explains failures in plain words
with the fix. Credentials are never stored.

## Decision

Two ways to get a credential, one setup flow behind them.

| Phase | Credential | Why |
| --- | --- | --- |
| 1 (build now) | A pre-filled **Create token** link: Helm opens Cloudflare's token page with the exact permissions selected; the admin clicks Create and pastes the token once. | Works for every self-hosted install today, with nothing hosted by us. |
| 2 (after its gate) | **Sign in with Cloudflare** (OAuth 2.0 authorization code + PKCE). | No copy-paste at all, consent screen shows exactly what Helm gets, and access can be revoked in Cloudflare. Needs a registered app and a redirect relay because every install has a different address. |

The guided setup (Phase 1) is the main improvement and is reused unchanged
by Phase 2, so no work is thrown away.

## Verified facts

| Fact | Source | Confidence |
| --- | --- | --- |
| Account-token template link: `https://dash.cloudflare.com/?to=/:account/api-tokens&permissionGroupKeys=<url-encoded JSON>&name=<name>`; JSON items are `{"key": …, "type": "read"\|"edit"}`. Account and zone cannot be pre-selected. | [Token template URLs](https://developers.cloudflare.com/fundamentals/api/how-to/account-owned-token-template/) | Official |
| Documented keys: `workers_scripts`, `dns`, `zone`, `zone_settings`. | same | Official |
| Keys for Tunnel, Email Routing rules and addresses: `argotunnel`, `email_routing_rule`, `email_routing_address`. | [cfdata.lol generator](https://cfdata.lol/tools/api-token-url-generator/) (datamined) | Unofficial: verified manually before release (see Tests). |
| OAuth clients: authorization code with a secret, or **PKCE with no secret** for public clients. Public clients can be authorized by any Cloudflare user. | [Create an OAuth client](https://developers.cloudflare.com/fundamentals/oauth/create-an-oauth-client/) | Official |
| Endpoints: `https://dash.cloudflare.com/oauth2/auth`, `/oauth2/token`, `/oauth2/revoke`, `/oauth2/userinfo`. | [Integrate with Cloudflare](https://developers.cloudflare.com/fundamentals/oauth/integrate-with-cloudflare/) | Official |
| Consent screen: user picks accounts, sees required and optional scopes; publisher shown with a shield (amber when the domain is unverified); users revoke under **Manage OAuth authorizations**; account admins can disable OAuth. | [Authorizing an application](https://developers.cloudflare.com/fundamentals/oauth/authorizing-an-application/) | Official |
| Every scope Helm needs exists for OAuth: Zone Read, DNS Write, Cloudflare Tunnel Write, Workers Scripts Write, Email Routing Rules Write, Zone Settings Write, Email Routing Addresses Read. | `GET /client/v4/oauth/scopes` (392 scopes), checked 2026-10-03 | Verified |
| Not documented: redirect URI rules (wildcards, `localhost`, plain `http`), token lifetimes. | — | **Open** (Phase 2 gate) |

## Phase 1: guided setup with a token link

### Experience

Connect apps shows one **Public access** panel (replacing the separate
Public URL and Email setup forms; the status cards stay for running
setups):

1. **Create a Cloudflare token.** A button opens the pre-filled token page in
   a new tab; the panel lists the permissions it selected and says which two
   choices to make on Cloudflare's page (account, and the zone under "Zone
   resources"). Paste the token.
2. **Choose a domain.** Helm lists the zones the token can see, with Email
   Routing status for each. No typing.
3. **Review.** Helm proposes a free hostname (`hooks.<domain>`, else
   `helm-hooks.<domain>`, else a suffixed name), shows the exact paths that
   become public, and offers "Also give webhooks email addresses" (default
   on when Email Routing is ready). If plus addressing is off, the review
   says what turning it on changes and needs a tick.
4. **Set up.** A live checklist: token checked → tunnel created → DNS record
   → connector Live → round-trip test → test ticket → email Worker → email
   rule. Each row shows done, running, skipped or failed with the fix.
5. **Done.** Shows the public URL, a sample webhook URL and email address,
   and **Send test**. The token is discarded.

Existing manual forms stay available under "Set up manually" for experts.

### Plain-language errors (contract for copy)

| Situation | Message (and fix offered) |
| --- | --- |
| Domain typed instead of chosen (manual form) | "Use a new subdomain such as hooks.example.com — Helm creates a DNS record for it." Prefill the suggestion. |
| Token lacks a permission | "This token can't <action>. Create the token with the button above; it selects every permission Helm needs." Name the missing permission. |
| Token sees no zones | "The token isn't allowed to see any domains. On Cloudflare's token page, under Zone resources, choose your domain." |
| Hostname taken | Pick the next free suggestion automatically; say which name was used. |
| Connector not Live in 30 s | "The tunnel is created but this server can't reach Cloudflare on port 7844." Link to network requirements. |
| Email Routing off for the domain | Skip email with "Turn on Email Routing for <domain> in Cloudflare, then run setup again." |

### Architecture

- **Setup run** (`internal/publicendpoint/setup.go`): an in-memory state
  machine per admin request. It holds the token only for its lifetime
  (wiped on completion, failure or a 10-minute timeout), executes the
  existing `Provision`, `Test`, `SendTestTicket` and `ProvisionEmail`
  operations in order, and records each step's status for polling. Steps
  are idempotent: an already-active public URL or email setup is reused
  and marked "already set up".
- **Rollback:** a failure after the tunnel exists leaves the public URL in
  place only if it is Live and passed its test; otherwise the run removes
  what it created (reusing existing undo paths). The report says exactly
  what remains.
- **API** (admins only, never cached, no idempotency replay):

  | Route | Purpose |
  | --- | --- |
  | `GET /api/v1/cloudflare/token-link` | The template URL and the permission list it selects. |
  | `POST /api/v1/cloudflare/zones` | `{api_token}` → zones the token sees, with Email Routing and plus-addressing status. |
  | `POST /api/v1/cloudflare/setup` | `{api_token, zone, hostname?, email?: {local_part, fallback_address?, enable_subaddressing}}` → `202` with a run ID. |
  | `GET /api/v1/cloudflare/setup/{run}` | Step list and outcome; no credentials. |

- **No new storage.** Results live in the existing `public_endpoints` and
  `email_intakes` tables; setup runs are transient.

### Security

- Token used only in memory during a run; never in the database, logs,
  responses or run status. Covered by the existing leak assertions,
  extended to the new routes.
- The token link requests exactly the permissions documented in
  `PUBLIC_ACCESS.md`; a unit test pins the list so it cannot drift from
  `RequiredPermissions` and `EmailPermissions`.
- Zone listing returns names and status only.

## Phase 2: Sign in with Cloudflare (OAuth)

### Design

- **One public OAuth client** registered by KanterLabs, named "Helm",
  PKCE, no secret, required scopes = the Phase 1 list (Email scopes
  optional so users can decline email).
- **Redirect relay:** a static page at a KanterLabs-owned HTTPS address
  (e.g. `https://connect.<kanterlabs domain>/cloudflare/callback`) that
  only redirects the browser to the instance address carried in `state`.
  The relay never sees a usable credential: the authorization code is
  worthless without the PKCE verifier, which never leaves the user's Helm.
- **State:** Helm creates `state` = random nonce + its own origin, keeps the
  nonce and verifier server-side for 10 minutes, and rejects callbacks whose
  nonce it did not issue (prevents CSRF and code injection). The relay
  refuses to redirect to anything but `https://` (or `http://localhost`)
  origins and adds nothing.
- **Exchange:** Helm's server swaps the code at `/oauth2/token`, runs the
  Phase 1 setup with the access token, then calls `/oauth2/revoke`. No
  token or refresh token is stored; removing resources later asks the user
  to sign in again (same model as today's token).
- **Opt-out:** `HELM_CLOUDFLARE_OAUTH=off` hides the button for installs that
  do not want any KanterLabs-hosted dependency; the token link remains.

### Gate (must be answered before building)

1. Create a test OAuth client (needs an API token with **OAuth Clients
   Write**) and confirm which redirect URIs Cloudflare accepts and whether
   the consent screen lets users pick a zone. If Cloudflare accepted
   `http://localhost` or wildcard redirects the relay could be avoided.
2. Choose and own the relay domain; verify it with Cloudflare so the
   consent screen shows the blue "verified" shield instead of amber.
3. Measure access-token lifetime: setup must finish within it.

## Documentation standard (applies to this and every future feature)

Strong documentation is part of "done", enforced by review and CI.

1. **Doc map.** Each topic has one owner document; others link to it.

   | Document | Owns |
   | --- | --- |
   | `docs/PUBLIC_ACCESS.md` (new; moves the Public URL and Email sections out of `TICKET_WEBHOOKS.md`) | Connecting Cloudflare, public URL, email addresses, removal, troubleshooting. |
   | `docs/TICKET_WEBHOOKS.md` | Creating webhooks, payloads, responses, repeats, Send test. |
   | `docs/*_PLAN.md` | Design and decisions; each starts with a `Status:` line and links its user guide and failure contract. |
   | `docs/E2E_TESTING.md` | One failure contract per feature, written before code. |
   | `openapi.yaml` | Machine contract; descriptions are user-readable. |

2. **Definition of done** for any feature: plan status updated; user guide
   section written in task order (prerequisites, steps, what you'll see,
   how to undo, troubleshooting table); failure contract rows; OpenAPI
   entries; screenshots of each UI state attached by the E2E test.
3. **In-app help mirrors the docs.** Every setup card has a short numbered
   procedure and a "Learn more" link to its `PUBLIC_ACCESS.md` anchor
   (served by Helm from the binary so links work on private installs).
4. **`scripts/check-docs.mjs` in `make lint`** (new, blocking):
   - every `operationId` named in a doc exists in `openapi.yaml`, and every
     operation tagged `Tickets` is named in a user guide;
   - every relative link and `#anchor` in `docs/` resolves;
   - every `docs/*_PLAN.md` has a `Status:` line;
   - every Helm env var read in `internal/config` appears in a doc.
5. **Release notes:** each beta push that changes user-visible behaviour
   adds a dated entry to `docs/releases/unreleased.md`; at release its entries
   move into `docs/releases/vX.Y.Z.md`.

## Tests (failure contract to add before code)

| Failure | Observable proof |
| --- | --- |
| The token link asks for the wrong permissions | Unit test: decoded `permissionGroupKeys` equals the documented list; manual release check that Cloudflare's page pre-selects every key (the three datamined keys in particular). |
| A token that sees no zones or lacks a permission half-configures Cloudflare | Fake API: setup stops at the failing step with the documented message; nothing new remains in fake state. |
| The admin has to invent a hostname | Choosing a zone proposes `hooks.<zone>`; when taken, the next free name is used and shown. |
| Setup claims success without a working path | The run completes only after the connector is Live, the round-trip test passed and the test ticket opened. |
| A rerun duplicates resources | Running setup twice reuses the active public URL and email setup and reports "already set up". |
| The token leaks | Absent from run status, responses, database and log. |
| OAuth callback injection (Phase 2) | A callback with an unknown `state` nonce, a reused nonce or a missing verifier is rejected; nothing is exchanged. |

Real verification on beta before announcing each phase: run the whole flow
against `shanekanterman.dev`, then remove everything and confirm in
Cloudflare.

## Rollout

1. Phase 1 to beta behind the existing admin-only Connect apps panel; keep
   manual forms. Docs restructure (`PUBLIC_ACCESS.md`) and
   `check-docs.mjs` land in the same change.
2. Collect one real setup from a non-author self-hoster if possible.
3. Phase 2 after its gate, as an extra button above the token link.

## As built

Recorded 2026-10-03 after implementation and verification.

- **Phase 2 gate answered.** Cloudflare accepted any redirect URL at
  registration but matched only exact ones at `/oauth2/auth`: wildcard
  hosts and paths were rejected and `localhost` had to match its port. The
  relay is therefore required. It runs on
  `helm-connect.shanekanterman04.workers.dev`, shows the destination and
  requires a click (no silent redirect).
- **OAuth client** `f05395c32033f7b61981048f5e523cec` is registered as a
  PKCE client with no secret. It is **private** until a publisher domain is
  verified (see the relay README), so only members of the KanterLabs
  Cloudflare account can approve it today; everyone else uses the token
  link, which runs the same setup.
- **Checklist steps** are: public URL, connector, reachability, email,
  forget the credential. The planned "test ticket" step was dropped because
  setup may run before any webhook exists; the reachability step already
  proves the path, and **Send test** on a webhook opens a real ticket
  afterwards.
- **Sessions:** a pasted token and an OAuth sign-in both become a
  30-minute in-memory session for one administrator, ended (and revoked
  for OAuth) when setup finishes.
- **Help** is served by Helm at `/api/v1/docs/{page}` from guides embedded in
  the binary and rendered in a drawer, instead of `/docs/…` pages.
- **Release notes** for beta changes collect in
  [releases/unreleased.md](releases/unreleased.md) until the next version.
