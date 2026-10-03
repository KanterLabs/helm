# Helm sign-in relay (Cloudflare Worker)

Status: deployed 2026-10-03 at
`https://helm-connect.shanekanterman04.workers.dev/cloudflare/callback`.
Design: [../../docs/CLOUDFLARE_CONNECT_PLAN.md](../../docs/CLOUDFLARE_CONNECT_PLAN.md).
User guide: [../../docs/PUBLIC_ACCESS.md](../../docs/PUBLIC_ACCESS.md#connect-cloudflare-guided-setup).

## Why it exists

Cloudflare OAuth clients accept only exact redirect URLs: no wildcards, and
`localhost` must match its port (verified 2026-10-03 against
`dash.cloudflare.com/oauth2/auth`). Every self-hosted Helm has its own
address, so Helm's shared OAuth client registers this Worker as its one
redirect. The Worker sends the browser back to the Helm encoded in the
OAuth `state` (`<nonce>.<base64url origin>`).

## Security properties

- **No usable credential passes through it.** Helm uses PKCE; the
  authorization code cannot be redeemed without the verifier, which never
  leaves the Helm that started sign-in.
- **No silent redirects.** It shows the destination and a **Continue to
  Helm** button, so a crafted link cannot quietly deliver someone's sign-in
  to another site.
- **Destination rules.** Only `https://` origins (or `http://localhost` and
  `http://127.0.0.1` for tests) without credentials or paths.
- **Strict headers:** `Content-Security-Policy: default-src 'none'`,
  `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, no framing.
- **Stateless:** no storage, no logging of codes.

The browser test `web/e2e/cloudflare-connect.spec.ts` runs this exact file
(through `test/e2e/relay-server.mjs`) in the full sign-in flow.

## Deploy or update

```sh
CLOUDFLARE_ACCOUNT_ID=<account id> \
CLOUDFLARE_API_TOKEN_FILE=<file with a token holding Workers Scripts Edit> \
  deploy/cloudflare-connect-relay/deploy.sh
```

It uploads `worker.js` as `helm-connect` and enables its `workers.dev`
route. Redeploying is safe and does not affect existing sign-ins.

## The OAuth client

| Field | Value |
| --- | --- |
| Client ID | `f05395c32033f7b61981048f5e523cec` (`DefaultCloudflareOAuthClientID` in `internal/config/intake.go`) |
| Type | Authorization code + PKCE, `token_endpoint_auth_method: none` (no secret) |
| Redirect | this Worker's `/cloudflare/callback` |
| Required scopes | `zone.read`, `dns.write`, `argotunnel.write` |
| Optional scopes | `workers-scripts.write`, `email-routing-rule.write`, `zone-settings.write`, `email-routing-address.read` |
| Visibility | **private**: only members of the owning Cloudflare account can approve it |

Manage it with `POST/GET/PATCH /accounts/{account}/oauth_clients` (token
permission **OAuth Client Write**).

### Publishing to every Cloudflare user

Cloudflare requires a verified publisher domain before a client can be
public (and shows a blue "verified" shield instead of amber):

1. Set `client_uri` to a site on a domain KanterLabs controls, add a logo,
   privacy and terms URLs.
2. Publish the verification value Cloudflare returns in
   `client_uri_verification.text` (`cloudflare_oauth_client_publisher=…`)
   on that domain, then request verification.
3. Change the client's visibility to public.
4. Optionally move this relay to a custom domain on the same site; update
   the client's redirect and `DefaultCloudflareOAuthRelayURL` together.

Until then, people outside the account use the token option, which runs the
identical setup.

## Running your own

Operators who do not want any KanterLabs-hosted dependency can register
their own client (same settings, their own relay deployment) and set
`HELM_CLOUDFLARE_OAUTH_CLIENT_ID` and `HELM_CLOUDFLARE_OAUTH_RELAY_URL`, or
hide the button with `HELM_CLOUDFLARE_OAUTH=off`.
