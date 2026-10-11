# Configuration reference

Every setting Helm reads, from `internal/config`. `make lint` fails when a
`HELM_*` setting is missing here or from another tracked doc
(`web/scripts/check-docs.mjs`), so this page stays complete.

`HELM_*` names are current; the older `ROADMAP_*` names still work. Where a
setting has several accepted names they are listed together; if more than one
is set they must agree, or Helm refuses to start. Values are read once at
startup.

## Server

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_ADDR` | `:8080` (`0.0.0.0:8080` in Compose) | Listen address. `cloudflare`, `tailnet` and `disabled` auth modes require a loopback address. |
| `HELM_DB` | `data/roadmap.db` (`/data/roadmap.db` in Compose) | SQLite database path. Tunnel run tokens live in a `public-endpoints` directory beside it. |
| `HELM_PUBLIC_ORIGIN` | none | External URL users reach Helm at, as a normalized origin (no trailing slash). Required for `local` and `cloudflare` auth; also where "Sign in with Cloudflare" returns. |
| `HELM_SECURE_COOKIES` | `true` | Mark session cookies `Secure`; must be `true` in `cloudflare` and `tailnet` modes. Set `false` only for plain-HTTP local use. |
| `HELM_DEMO_SEED` | `false` (`true` in Compose) | Seed demo projects on first start; must be `false` in SSO modes. |
| `HELM_RELEASE_SHA` | empty | 40-character commit the binary was built from, shown by `/api/v1` and checked by deployment tooling. |

## Sign-in

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_AUTH_MODE` | `local` | `local`, `cloudflare`, `tailnet`, or `disabled` (development only, loopback only). |
| `HELM_ADMIN_EMAIL` | empty | Administrator identity; required in `cloudflare` and `tailnet` modes. |
| `HELM_CLOUDFLARE_ISSUER`, `HELM_CF_ACCESS_ISSUER` | empty | Cloudflare Access team domain (`https://<team>.cloudflareaccess.com`); required in `cloudflare` mode. |
| `HELM_CF_ACCESS_AUDIENCES`, `HELM_CLOUDFLARE_AUDIENCES` | empty | Comma-separated Access application AUD tags (UI and API). |
| `HELM_CLOUDFLARE_AUDIENCE`, `HELM_CLOUDFLARE_AUD` | empty | Single-audience form of the above. |
| `HELM_CLOUDFLARE_JWKS_URL`, `HELM_CF_ACCESS_JWKS_URL`, `HELM_CLOUDFLARE_CERTS_URL` | issuer's `/cdn-cgi/access/certs` | Signing keys for Access assertions. |
| `HELM_TAILNET_OWNER_LOGIN`, `HELM_TAILNET_ADMIN_EMAIL` | empty | Tailnet login that maps to the administrator (`tailnet` mode). |
| `HELM_TAILNET_AUDIENCE` | empty | Must equal `HELM_PUBLIC_ORIGIN` in `tailnet` mode. |
| `HELM_TAILNET_ASSERTION_KEY_FILE`, `HELM_TAILNET_AUTH_KEY_FILE`, `HELM_TAILNET_KEY_FILE` | empty | Owner-only file with the edge's HMAC assertion key. |
| `HELM_TAILNET_TLS_ADDR` | empty | TLS listener for the trusted edge. |
| `HELM_TAILNET_TLS_CERT_FILE`, `HELM_TAILNET_TLS_KEY_FILE` | empty | Certificate and key for that listener. |
| `HELM_TAILNET_ALLOWED_PEER_IPS` | empty | Comma-separated edge IPs allowed to connect. |

The private beta's exact values are in
[BETA_DEPLOYMENT_PLAN.md](BETA_DEPLOYMENT_PLAN.md).

## Luna (Codex task assist)

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_LUNA_ENABLED` | `true` | Offer task assist through each user's own Codex subscription. |
| `HELM_LUNA_MODEL` | `gpt-5.6-luna` | Model requested from Codex. |
| `HELM_LUNA_EFFORT` | `medium` | `low`, `medium`, `high`, `xhigh`, `max` or `ultra`. |
| `HELM_CODEX_BINARY` | `codex` | Codex CLI used for sign-in and runs. |
| `HELM_CODEX_HOME_ROOT` | `data/codex-users` | Per-user Codex credential directories. |

See [LUNA_TASK_ASSIST.md](LUNA_TASK_ASSIST.md).

## Public access and alert intake

Public URLs, email addresses and guided Cloudflare setup are configured by
`HELM_PUBLIC_HOOKS_ADDR`, `HELM_CLOUDFLARED_BINARY`,
`HELM_CLOUDFLARE_API_BASE`, `HELM_CLOUDFLARE_OAUTH`,
`HELM_CLOUDFLARE_OAUTH_CLIENT_ID`, `HELM_CLOUDFLARE_OAUTH_RELAY_URL`,
`HELM_CLOUDFLARE_DASHBOARD_URL` and `HELM_PUBLIC_PROBE_ORIGIN`; see
[PUBLIC_ACCESS.md § Settings](PUBLIC_ACCESS.md#settings). The Coolify
webhook intake (`HELM_COOLIFY_*`) is described in
[COOLIFY_EMAIL_INTAKE_PLAN.md](COOLIFY_EMAIL_INTAKE_PLAN.md#implemented-v1-webhook-intake).

## Beta release switching

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_BETA_SWITCH_ENABLED` | `false` | Show the beta release switcher; requires `tailnet` mode on the beta origin. |
| `HELM_BETA_SWITCH_SOCKET`, `HELM_BETA_SWITCH_SOCKET_PATH` | `/run/helm-beta-switcher/helm-beta-switchd.sock` | Unix socket of the root-owned switch controller. |
