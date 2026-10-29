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
| `HELM_LEGACY_ORIGIN` | empty | Optional exact former browser origin for direct scoped bearer API compatibility. Production private uses `https://tc.shanekanterman.dev`; UI GET/HEAD redirects to the canonical origin and old browser writes are rejected. |
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
[BETA_DEPLOYMENT_PLAN.md](BETA_DEPLOYMENT_PLAN.md). Production uses
[`deploy/tailnet-owner-production.env.example`](../deploy/tailnet-owner-production.env.example):
`https://helm.home.shanekanterman.dev` is both the exact public origin and
assertion audience; the listener is `10.0.0.38:8443`, and the only permitted
origin peer is `10.0.0.101`. Both canonical and legacy settings must agree.
Keep the existing admin email to preserve the owner actor. The assertion key
is separate from the origin TLS key and the release-signing key; production
and beta provision separate key bytes even though guest-local paths match.
Never distribute assertion or TLS signing keys to owner/agent clients.

## Luna (Codex task assist)

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_LUNA_ENABLED` | `true` | Offer task assist through each user's own Codex subscription. |
| `HELM_LUNA_MODEL` | `gpt-5.6-luna` | Model for ordinary Luna task and project assistance. |
| `HELM_LUNA_EFFORT` | `medium` | `low`, `medium`, `high`, `xhigh`, `max` or `ultra`. |
| `HELM_AUDIT_MODEL` | `gpt-5.6-sol` | Independent server-owned model for new GitHub audits, including evidence discovery. Does not change ordinary Luna assistance. |
| `HELM_AUDIT_EFFORT` | `medium` | Independent audit reasoning effort; same allowed values as `HELM_LUNA_EFFORT`, subject to the selected model's support. |
| `HELM_CODEX_BINARY` | `codex` | Codex CLI used for sign-in and runs. |
| `HELM_CODEX_HOME_ROOT` | `data/codex-users` | Per-user Codex credential directories. |

Audit model selection is operator-owned, not a client request or grant option.
Each new job captures its profile, including discovery and final assessment.
Changing configuration affects new jobs after restart; existing jobs and
idempotent replays retain their captured values. A new retry job captures the
current profile. There is no silent fallback to the everyday model when an audit
model is unavailable. Verify that the selected model and effort are supported by
the deployed Codex runtime and the account. The evidence-only adapter currently
admits only validated `gpt-5.6-sol` and `gpt-5.6-luna` with Codex CLI 0.153.0;
another model or version requires a new isolation proof. `HELM_LUNA_ENABLED=false` disables
model turns for both ordinary assistance and audits.

See [LUNA_TASK_ASSIST.md](LUNA_TASK_ASSIST.md).

## GitHub beta

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_GITHUB_ENABLED` | `false` | Enable optional read-only GitHub integration and server audit evidence. Missing credentials leave it unconfigured without affecting normal Helm readiness. |
| `HELM_GITHUB_TOKEN_FILE` | empty | Absolute path to a protected regular file containing a fine-grained read-only token. The provider reads it directly; group/other permission bits must be unset. Never supply a raw token in an environment variable. |
| `HELM_GITHUB_REPOSITORIES` | empty | Comma-separated `owner/name` allowlist, normalized and deduplicated, at most 50 repositories. Project administrators select at most ten allowed repository/ref pairs. |

The API origin is fixed to `https://api.github.com`; runtime configuration
cannot redirect authenticated requests to another origin. Audit start also
requires the requesting human's personal AI connection. See the
[GitHub beta API](GITHUB_BETA_API.md) for readiness, PR linking, audit jobs
and explicit portable v3 history.

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
