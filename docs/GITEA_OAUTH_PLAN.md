# Gitea OAuth sign-in: implementation plan

Status: in progress, 2026-11-12. Planning: TC-387; implementation: TC-388.
Facts and evidence: [research record](GITEA_OAUTH_RESEARCH.md). Experience
standard: [experience specification](GITEA_OAUTH_UX.md).
Audience: an orchestrating session and the Opus subagents it starts. Each work
package is a self-contained brief.

## Outcome

A human administrator connects Helm to a self-hosted Gitea (or Forgejo) from
Settings: enter the server address, create one OAuth2 application on the
forge, paste its client ID, sign in on the forge, and return to Helm
connected. Helm holds no client secret, keeps the sign-in alive by refreshing
it, proves the access is limited to reading, and can list the repositories
the account reaches.

This plan delivers the **connection** only. Reading pull requests, checks and
branches from Gitea is the next plan; it builds on contract C1.

## Decisions

1. **Authorization code with PKCE (`S256`), public client.** Gitea has no
   device flow. Helm stores a client ID and no secret.
2. **One registration per forge, by its administrator or user.** No shortcut
   exists; the setup screen makes it a copy-and-paste step.
3. **Scope is the constant `read:repository read:user`.** Never built from
   input, never changed casually: Forgejo refuses a changed scope until the
   user revokes the old grant.
4. **Helm proves the grant is limited.** After sign-in and on every check, a
   read Helm did not ask for must be refused, and where the forge names the
   granted scope in that refusal (Gitea does) it must be read-only. Otherwise
   the connection is kept but marked as more than read-only, with a warning.
5. **One installation-wide connection**, made by a human administrator. The
   docs recommend a dedicated non-administrator forge account.
6. **Everything lives in one 0600 file**: server, client ID, tokens, account.
   Nothing goes in SQLite, the browser, portable archives, logs, prompts,
   arguments or environment variables. No schema migration.
7. **Refresh on demand and daily.** Within 5 minutes of expiry, after a 401,
   and whenever the last refresh is over 24 hours old.
8. **The flow is provider-neutral; Gitea is one descriptor.** Paths, scope
   and endpoints sit in one struct so GitLab can be added without touching
   the flow. Forgejo uses the Gitea descriptor.
9. **On by default** (`HELM_GITEA_OAUTH=off` disables it). It needs
   `HELM_PUBLIC_ORIGIN`, because the forge redirects the browser back to it.
10. **Disconnect deletes the local file** and links to the forge page where
    the user revokes the grant. Gitea has no revocation endpoint.

Out of scope: GitLab and GitHub, several forges at once, client secrets,
reading repository data, per-project repository selection.

## Human gates (Shane)

| Gate | When | What |
| --- | --- | --- |
| G1 | After WP7 | Live proof on the home Gitea (28.3.0): register the application for the Helm origin under test with Confidential Client unticked, connect, press Test connection, list repositories, then revoke on Gitea and confirm Helm asks to reconnect. |
| G2 | After G1 | Approve the beta rollout (manual admission rollout), then `main` separately. |

## Rules for every subagent

- Work in your own git worktree, branched from `feat/gitea-oauth` (created
  from freshly fetched `origin/beta`). Commit there; never push.
- Commit as `Shane Kanterman
  <122749779+ShaneKanterman04@users.noreply.github.com>` for author and
  committer. No AI attribution or co-author lines.
- Read `AGENTS.md` and the research record first. Write your failure list
  **before** tests, and tests **before** the code they cover. Put the list in
  a `failures.md` beside the code.
- A pre-commit hook rejects any staged file over 20,000 bytes (`*.sh` and
  lockfiles exempt). Split by responsibility. Do not edit
  `internal/httpapi/observability.go` (42 KB).
- There is no native Go. Use `HELM_E2E_DOCKER_WORK_DIR=$TMPDIR/<wp>/work
  HELM_E2E_DOCKER_BUNDLE_PARENT=$TMPDIR/<wp>/bundles
  test/e2e/go-command.sh test -race ./internal/...` (both directories must
  exist and be absolute). `chmod -R u+w` its module cache before removing it.
- Run lint as `env -u SSH_CONNECTION -u SSH_CLIENT -u SSH_TTY make lint`.
- Scratch goes under `$TMPDIR`, never `/tmp`.
- Never contact a real forge or use a real credential. Use a distinct
  sentinel string for every secret so leaks can be scanned for.
- Do not create Helm cards; the orchestrator owns tracking.
- Report: branch, head SHA, files changed with sizes, each command run and
  its result, failure-list entries added, and anything you could not verify.

## Contracts

### C1. Manager: `internal/forgeconnect`

```go
type Options struct {
	Enabled      bool         // HELM_GITEA_OAUTH
	PublicOrigin string       // empty makes sign-in unavailable
	StateDir     string       // holds gitea.json
	HTTPClient   *http.Client // constructor-only test seam
	Now          func() time.Time
}
type BeginInput struct {
	BaseURL, ClientID string
	AllowInsecureHTTP bool
}
func New(Options) (*Manager, error) // loads the file; no network, no goroutines
func (m *Manager) Start(ctx context.Context) // daily keep-alive
func (m *Manager) Close()
func (m *Manager) Status() Status // no network
func (m *Manager) Probe(ctx context.Context, baseURL string, allowHTTP bool) (Server, error)
func (m *Manager) Begin(ctx context.Context, actorID string, in BeginInput) (authorizeURL string, err error)
func (m *Manager) Finish(ctx context.Context, actorID, state, code, upstreamError, upstreamDescription string) error
func (m *Manager) Check(ctx context.Context) Status
func (m *Manager) Repositories(ctx context.Context) (list []Repository, truncated bool, err error)
func (m *Manager) Disconnect(actorID string) error

// For the adapter that follows this plan.
func (m *Manager) Current() (token, identity string, err error) // no network
func (m *Manager) Fresh(ctx context.Context) (token, identity string, err error)
func (m *Manager) Rejected(token string) // the API answered 401 for it
func (m *Manager) BaseURL() string
```

`identity` is a random ID created at sign-in. It changes on connect and
disconnect, never on refresh. Errors carry one of these fixed codes:
`unavailable`, `invalid_server_url`, `insecure_http`, `server_unreachable`,
`tls_error`, `not_a_forge`, `issuer_mismatch`, `already_connected`,
`state_invalid`, `denied`, `scope_conflict`, `authorize_failed`,
`client_confidential`, `client_unknown`, `exchange_failed`, `storage_failed`,
`reconnect_required`.

### C2. File: `<StateDir>/gitea.json`

Directory 0700, file 0600, owned by the service user. Write a temp file in
the same directory, fsync, rename, fsync the directory. A file that is not
regular, has group or other bits, has another owner, exceeds 32 KB, fails to
parse or has an unknown version loads as `reconnect_required` and is left in
place.

Fields: `version` (1), `id`, `kind` (`gitea`), `server` (`base_url`,
`product`, `version`, `insecure_http`), `client_id`, `access_token`,
`refresh_token`, `access_expires_at`, `refreshed_at`, `account` (`login`,
`id`), `access` (`scoped` | `full` | `unknown`), `connected_by`,
`connected_at`, `checked_at`.

### C3. Forge calls (Gitea descriptor)

| Purpose | Call | Rule |
| --- | --- | --- |
| Probe | `GET {base}/.well-known/openid-configuration` | Needs both grants and `S256`. Input may omit the scheme (assume `https`) or be any page on the forge: try the given path, each parent path, then the origin, at most 4 candidates. A document whose `issuer` is another path on the same host re-probes that issuer. An `issuer` on another host is `issuer_mismatch` (return the advertised issuer). |
| Authorize | Browser to `{base}/login/oauth/authorize` | `client_id`, `redirect_uri`, `response_type=code`, `state`, `code_challenge`, `code_challenge_method=S256`, `scope`. |
| Exchange | `POST {base}/login/oauth/access_token` | Form: `grant_type=authorization_code`, `client_id`, `code`, `redirect_uri`, `code_verifier`. |
| Refresh | Same path | Form: `grant_type=refresh_token`, `client_id`, `refresh_token`. Single flight. Persist before use. |
| Identity | `GET {base}/api/v1/user` | `Authorization: Bearer`. |
| Version | `GET {base}/api/v1/version` | A `+gitea-` suffix means Forgejo. |
| Limit check | `GET {base}/api/v1/user/orgs?limit=1` | 403 is `scoped`, 200 is `full`, anything else `unknown`. |
| Repositories | `GET {base}/api/v1/user/repos?limit=50&page=N` | At most 10 pages; then `truncated`. |

`redirect_uri` is `{PublicOrigin}/api/v1/integrations/gitea/oauth/callback`,
identical at authorize and exchange.

Error mapping (token endpoint errors are HTTP 400 JSON):

| Upstream | Code |
| --- | --- |
| Callback `error=access_denied` | `denied` |
| Callback `server_error` mentioning "different scope" | `scope_conflict` |
| Exchange error mentioning "client secret" | `client_confidential` |
| Exchange `invalid_client` otherwise | `client_unknown` |
| Refresh `invalid_grant`, `invalid_client` or `unauthorized_client` | `reconnect_required`; wipe tokens, keep server, client ID and account |
| API 401 | Refresh once and retry; a refreshed token rejected again within a minute is `reconnect_required` |
| Network failure during refresh | Keep a token that has not expired |

A refresh may return the same token bytes; never treat that as failure.

### C4. Outbound request rules

The server contacts an address an administrator typed, so:

- Base URL: `http` or `https`, a host, an optional path prefix; no user info,
  query or fragment; at most 255 characters. Lower-case the scheme and host
  and strip the trailing slash.
- `http` only with `allow_insecure_http: true`; record it and warn in the UI.
- Refuse link-local, unspecified, multicast and broadcast addresses in the
  dialer's `Control` hook, after resolution. Private and loopback addresses
  are allowed: self-hosted forges live there.
- No redirects, 10-second timeout, 64 KB response cap (1 MB for a repository
  page).
- Follow the operator's proxy settings; a locked-down server may have no other
  way out. Through a proxy the dialer sees only the proxy, so check the target
  before the request leaves: a literal address outright, a name by what it
  resolves to locally.
- Never pass an upstream body or `error_description` to the browser or a log.
  Map to the C1 codes.
- TLS uses system roots plus `SSL_CERT_FILE` and `SSL_CERT_DIR`. No option to
  skip verification.

### C5. HTTP API: `/api/v1/integrations/gitea`

All routes: human administrators only (reject bearer tokens, as
`codex_account.go` does), `Cache-Control: no-store`, not wrapped in
`s.mutation`.

| Route | Behaviour |
| --- | --- |
| `GET` (root) | Status document. |
| `POST /probe` | `{"base_url", "allow_insecure_http"}` → `server` (with the settled `base_url`), `redirect_uri`, `register_url`. Saves nothing. |
| `POST /oauth/start` | `{"base_url", "client_id", "allow_insecure_http"}` → `{"authorize_url"}`. Pending state is per actor, in memory, 10 minutes. |
| `GET /oauth/callback` | Calls `Finish`, then 303 to `/settings?gitea=connected` or `/settings?gitea_error=<code>` with `Referrer-Policy: no-referrer`. |
| `POST /check` | `Check`; returns the status document. |
| `GET /repositories` | `{"repositories": [{"full_name", "private", "archived"}], "truncated"}`. |
| `POST /disconnect` | Deletes the file. |

Errors are `{"error": {"code": <C1 code>, …}}` in the existing envelope.

```json
{
  "available": true, "unavailable_reason": "",
  "redirect_uri": "https://helm.example/api/v1/integrations/gitea/oauth/callback",
  "state": "connected",
  "server": {"base_url": "https://gitea.example", "product": "gitea",
             "version": "28.3.0", "insecure_http": false},
  "client_id": "…", "account": {"login": "helm-bot", "id": 7},
  "access": "scoped", "repository_count": 12,
  "connected_at": "…", "checked_at": "…",
  "check": {"state": "ok", "code": ""},
  "register_url": "https://gitea.example/user/settings/applications",
  "revoke_url": "https://gitea.example/user/settings/applications"
}
```

`state`: `disconnected` | `connected` | `reconnect_required`.
`unavailable_reason`: `disabled` | `public_origin_missing`.
`check.state`: `ok` | `unreachable` | `unauthorized`.

### C6. Settings panel

The [experience specification](GITEA_OAUTH_UX.md) is the contract: the three
steps, every message, layout, accessibility and the proof required. Navigate
to `authorize_url` only if it starts with the probed base URL.

## Work packages

### WP1. Store, outbound client and probe

Depends on: nothing. Read: research record, C2–C4,
`internal/githubprovider/client.go` (`readToken` and its HTTP hygiene).

Change: start `internal/forgeconnect` with the descriptor, URL
normalization, the guarded HTTP client, the file store and `Probe`.

Failure list must cover at least: a URL with user info, query, fragment or an
odd scheme; a host that resolves to a link-local address, including by DNS
rebinding between check and dial; a redirect to another host; an oversized or
slow response; a discovery document lacking a grant or `S256`; an issuer that
differs only by case or trailing slash (accept) or by host (refuse); a
sub-path base URL; a partial write; loose permissions, symlink, wrong owner,
truncated JSON, unknown version; an upstream body reaching an error string.

Done when: `go test -race ./internal/forgeconnect/` is green.

### WP2. Sign-in, refresh and check

Depends on: WP1. Read: C1, C3,
`internal/publicendpoint/connect.go` (`StartOAuth`, `FinishOAuth`).

Change: `Begin`, `Finish`, refresh, `Check`, `Repositories`, `Disconnect`,
`Status`, the credential methods, and the keep-alive.

Failure list must cover at least: state reuse, expiry and another actor's
state; a second `Begin` by the same actor; `Begin` while connected; each row
of the C3 mapping; a token response missing a field or carrying an absurd
lifetime; two concurrent refreshes; a crash or write failure between refresh
and persist; one-time refresh mode; identical bytes from a refresh; the limit
check returning 200, 403, 401 and 500; repository pagination and truncation;
goroutines surviving `Close`; any secret in a log line or error string.

Done when: race tests against an `httptest` forge are green, including a
sentinel scan of captured logs and errors.

### WP3. Configuration, routes, wiring and OpenAPI

Depends on: WP2. Read: C5, `internal/httpapi/cloudflare_connect.go`,
`codex_account.go`, `github_dispatch.go`, `internal/config/intake.go`,
`openapi/README.md`.

Change: `HELM_GITEA_OAUTH` (`on` | `off`, default `on`) and
`HELM_FORGE_STATE_DIR` (default: a `forge` directory beside the database
file, so it follows the data volume); routes in a new
`gitea_connection.go`; build, start and stop the manager with the server;
register route labels from a new file with an `init` that adds to
`metricRouteTemplates`; OpenAPI paths and schemas, then
`npm run openapi:generate` from `web/`.

Failure list must cover at least: a bearer token or non-administrator on any
route; a callback without a Helm session, or from a different administrator;
the authorization code or state in a log, metric label or `Referer`; a
cacheable or replay-stored response; an upstream message reflected into the
redirect; sign-in offered without a public origin; shutdown order.

Done when: handler tests are green, lint passes and `npm run openapi:check`
is clean.

### WP4. Settings UI

Depends on: C5 and C6 only; runs alongside WP1 and WP2. Read: the experience
specification,
`web/src/lib/components/CloudflareSetup.svelte` (return-from-sign-in
handling), `web/src/lib/components/github/GitHubIntegrationSettings.svelte`,
`web/e2e/cloudflare-connect.spec.ts`.

Change: types and API methods; a `GiteaConnection.svelte` panel in Settings;
a mocked-route Playwright spec that screenshots every state and every error
message at 1280 px and 390 px and asserts no horizontal overflow at 320 px.

Failure list must cover at least: a double click starting two sign-ins;
`gitea` or `gitea_error` left in the address bar after handling; an unknown
error code; an `authorize_url` outside the probed server; the `http` warning
bypassed; clipboard denial; disconnect without confirmation; state changes
not announced to assistive technology; non-administrators seeing the panel.

Done when: `npm run check`, `npm run build` and the spec pass, with
screenshots attached.

### WP5. Proofs

Depends on: WP3 and WP4 merged. Read: `test/e2e/fake-cloudflare`,
`test/e2e/run-worker.sh`, and the research scripts in
`/var/tmp/agent-artifacts-1000/helm-gitea-oauth-research/`.

Change, in two parts:

1. **Routine suite.** A `test/e2e/fake-gitea` beside `fake-cloudflare`,
   started by `run-worker.sh`, serving C3 with controls for consent, token
   lifetime, one-time refresh, revocation and full-access grants. A spec
   drives the real binary through the whole journey: probe, register, sign
   in, check, list, forced refresh, revoke then reconnect, disconnect.
2. **Real-forge proof.** `test/giteaproof/run.sh`, opt-in because it needs
   Docker: start Gitea 28.3.0 and Forgejo 17, register a public application,
   and drive Helm's real backend through sign-in (scripting the forge's
   consent as the research scripts do), a 5-second token lifetime, one-time
   refresh and revocation. Bind containers to `127.0.0.1`; remove them and
   any image the script pulled.

Done when: both pass, the existing suite is unchanged, and a scan of browser
artifacts, server output, the SQLite file and the proof's output finds no
sentinel.

### WP6. Documentation

Depends on: WP3 merged; runs alongside WP5. Change: a new
`docs/GITEA_OAUTH.md` for operators (setup with screenshots from WP4, the
dedicated-account advice, the full-access warning, private certificate
authorities, restore-means-reconnect, each error code); pointers from the
README and in-app help. Confirm backup and runtime-archive scripts do not
collect the forge state directory; exclude it if they do.

Done when: `npm run docs:check` passes and every file is under 20,000 bytes.

### WP7. Independent review

Depends on: everything merged. Start a fresh subagent with the diff and
C1–C5 only. It checks: no secret reaches a log, error, response, SQLite, a
prompt, an argument or the environment; the C4 rules hold on every outbound
call; who can call each route; state and PKCE handling; file permissions and
atomic writes; goroutine lifetime. Fix findings, then run the full
`test/e2e/run.sh` and the real-forge proof.

## Order

| Wave | Packages | Notes |
| --- | --- | --- |
| 1 | WP1, WP4 | In parallel. |
| 2 | WP2 | May go to the same subagent as WP1. |
| 3 | WP3 | Only package that regenerates OpenAPI. |
| 4 | WP5, WP6 | In parallel. |
| 5 | WP7, then G1 and G2 | |

The orchestrator merges each package into `feat/gitea-oauth` in wave order
and reruns that wave's "done when" checks on the merged branch before
starting the next.

## After this plan

- **Gitea adapter:** pull requests, reviews, commit statuses, branches and
  source through `Fresh` and `BaseURL`, with a provider-neutral repository
  model. The research record lists the endpoints already proven to work.
- **GitHub sign-in:** a device-flow connection for github.com can use the
  same `Current` / `Fresh` / `Rejected` shape.
