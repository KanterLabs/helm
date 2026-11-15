# GitHub device-flow sign-in: implementation plan

Status: active, 2026-11-14. Shane chose this over the Gitea connection, which
was removed. Read [the amendments](GITHUB_DEVICE_FLOW_AMENDMENTS.md) first:
where they differ from this plan, they win. Planning: TC-386. Audience: an
orchestrating session and the Opus subagents it starts.
Each work package below is a self-contained brief.

## Outcome

A human administrator connects GitHub from Settings: Helm shows a short code,
they enter it at `github.com/login/device`, and Helm is connected. Nobody
places a token file or registers anything. Access stays read-only and limited
to repositories the administrator chose on GitHub.

Today the operator provisions a fine-grained token in `HELM_GITHUB_TOKEN_FILE`
and lists repositories in `HELM_GITHUB_REPOSITORIES`
([operations](GITHUB_BETA_OPERATIONS.md)). That mode keeps working unchanged.

## Decisions

1. **One KanterLabs-owned GitHub App with device flow enabled.** Its client ID
   is public and ships as a default, like the Cloudflare client ID in
   `internal/config/intake.go`. Helm holds no client secret, private key or
   webhook. Permissions: Contents, Pull requests, Checks, Commit statuses and
   Metadata, all read-only.
2. **Native Go, no `gh` binary.** Device flow is two HTTP calls.
3. **Two exclusive modes, chosen by configuration.** `operator_token` when
   `HELM_GITHUB_TOKEN_FILE` is set; otherwise `device_flow`.
   `HELM_GITHUB_ENABLED=true` is still required in both.
4. **One installation-wide connection**, made by a human administrator.
5. **GitHub decides the allowlist.** In `device_flow` mode the allowlist is the
   repositories reachable through the App's installations, capped at 50.
   `HELM_GITHUB_REPOSITORIES`, when set, is an operator ceiling (intersection).
6. **Expiring tokens with refresh.** Access tokens last 8 hours and refresh
   tokens 6 months; each refresh replaces both. Tokens made by device flow
   refresh without a client secret. An App with expiry turned off returns no
   refresh token and is never refreshed.
7. **Credentials live in one file**, never in SQLite, the browser, portable
   archives, logs, prompts, arguments or environment variables. This is the
   same exposure class as today's token file: a same-UID process can read it.
8. **No schema migration.** An older binary ignores the file and reports
   GitHub as not configured; no data is lost.
9. **Disconnect deletes the local credential.** Helm cannot revoke on GitHub
   without a secret, so the UI links to the user's authorization settings.

Out of scope: GitHub Enterprise Server, webhooks, per-user connections,
writes to GitHub, making `HELM_GITHUB_ENABLED` default to on.

## Human gates (Shane)

| Gate | When | What |
| --- | --- | --- |
| G1 | Any time before G2 | Register the App under KanterLabs: device flow on, webhook inactive, the five read permissions, token expiry on, no OAuth-on-install. Give the orchestrator the client ID and slug. If GitHub insists on a private key before install, generate one and keep it out of Helm. |
| G2 | After WP7 | Live proof on a local run: connect with a real account, then set `access_expires_at` in the file to the past and press Test connection. This proves refresh without a secret on github.com, which is confirmed only in GitHub's Enterprise docs. If it fails, turn token expiry off in the App; the code already handles that. |
| G3 | After G2 | Approve the beta rollout (manual admission rollout), then `main` separately. |

Code and tests do not wait for G1: the defaults stay empty and every test uses
a synthetic GitHub.

## Rules for every subagent

- Work in your own git worktree, branched from `feat/github-device-flow`
  (created from freshly fetched `origin/beta`). Commit there; never push.
- Commit as `Shane Kanterman
  <122749779+ShaneKanterman04@users.noreply.github.com>` for author and
  committer. No AI attribution or co-author lines.
- Read `AGENTS.md` first. Write the package's failure list **before** tests,
  and tests **before** the code they cover. Append failure lists to the
  nearest existing `failures.md` / `FAILURES.md`, or add one to a new package.
- A pre-commit hook rejects any staged file over 20,000 bytes (`*.sh` and
  lockfiles exempt). Split by responsibility. `internal/httpapi/observability.go`
  is 42 KB: do not edit it.
- There is no native Go. Use
  `HELM_E2E_DOCKER_WORK_DIR=$TMPDIR/<wp>/work
  HELM_E2E_DOCKER_BUNDLE_PARENT=$TMPDIR/<wp>/bundles test/e2e/go-command.sh
  test -race ./internal/...` (both directories must exist and be absolute).
  The module cache it leaves is read-only: `chmod -R u+w` before removing.
- Run `make lint` as `env -u SSH_CONNECTION -u SSH_CLIENT -u SSH_TTY make lint`.
- Scratch goes under `$TMPDIR`, never `/tmp`.
- Tests never contact github.com and never use a real credential. Use
  distinct sentinel strings for every secret so leaks can be scanned for.
- Do not create Helm cards; the orchestrator owns tracking.
- Report: branch, head SHA, files changed with sizes, each command run and its
  result, failure-list entries added, and anything you could not verify.

## Contracts

These are fixed so packages can be built apart. Change one only through the
orchestrator.

### C1. Provider seam: `internal/githubprovider/credential.go`

```go
type CredentialSource interface {
	// Current returns the token held now. It performs no network I/O.
	// identity changes only when authorization may have changed (different
	// token-file content, connect, disconnect). A refreshed token of the
	// same grant keeps its identity.
	Current() (token, identity string, err error)
	// Fresh is Current, but first renews a token that is close to expiry.
	Fresh(ctx context.Context) (token, identity string, err error)
}
```

- Errors are `*Error` with code `not_configured`, `unavailable`, or the new
  `reconnect_required` ("GitHub sign-in expired; reconnect GitHub.").
- `Options.Source` is new. Nil means a file source over `Options.TokenFile`
  whose identity is the token's SHA-256, which is today's behaviour exactly.
- The provider's generation bumps when identity or presence changes, **never
  on a refresh**. Otherwise every 8-hour refresh would mark verified pull
  requests unverified (`GithubService.verified` is keyed by generation).
- `func (c *Client) SetRepositories([]string)` replaces the allowlist
  (normalize, dedupe, sort, cap 50). A changed set resets caches and bumps the
  generation. Every read of the allowlist takes `c.mu`.
- `fetch` uses `Fresh`; `Generation`, `scoped`, `AccessAllowed` and
  `CachedDiagnostics` use `Current`.
- A source may also implement `Rejected(token string)`. The provider calls it
  once when GitHub answers 401 for that token, before its existing deny path.

### C2. Connection manager: `internal/githubconnect`

```go
type Options struct {
	ClientID, AppSlug string   // empty ClientID turns sign-in off
	StateDir          string   // holds connection.json
	Ceiling           []string // optional HELM_GITHUB_REPOSITORIES
	HTTPClient           *http.Client // constructor-only test seams
	WebOrigin, APIOrigin string       // default github.com / api.github.com
	Now                  func() time.Time
}
func New(Options) (*Manager, error) // loads the file; no network, no goroutines
func (m *Manager) Start(ctx context.Context) // owns the polling goroutine
func (m *Manager) Close()
func (m *Manager) Status(actorID string) Status           // no network
func (m *Manager) Begin(ctx context.Context, actorID string) (Status, error)
func (m *Manager) Cancel(actorID, attemptID string) Status
func (m *Manager) Sync(ctx context.Context) error          // installations
func (m *Manager) Disconnect(actorID string) error
func (m *Manager) OnRepositories(func([]string)) // load, sync, disconnect
```

`Manager` implements `CredentialSource` and `Rejected`. Its identity is a
random ID created at connect time. `Status` marshals to the `connection`
object in C5. The server polls GitHub; the browser polls Helm. The device code
never leaves the process. One attempt exists at a time: the same actor's
`Begin` returns the pending attempt, another administrator's replaces it.

### C3. Credential file: `<StateDir>/connection.json`

Directory 0700, file 0600, owned by the service user. Write to a temp file in
the same directory, fsync, rename, fsync the directory. On load, a file that
is not regular, has group or other bits, has another owner, exceeds 16 KB,
fails to parse or has an unknown version is treated as `reconnect_required`
and left in place.

Fields: `version` (1), `id`, `access_token`, `refresh_token`,
`access_expires_at`, `refresh_expires_at`, `login`, `user_id`, `connected_by`,
`connected_at`, `repositories`, `repositories_truncated`,
`repositories_checked_at`.

### C4. GitHub calls

All use a fixed origin, a 10-second timeout, no redirects, a 64 KB response
cap and `Accept: application/json`. GitHub reports OAuth errors with HTTP 200
and an `error` field.

| Call | Notes |
| --- | --- |
| `POST {web}/login/device/code` | `client_id`. Returns `device_code`, `user_code`, `verification_uri`, `expires_in`, `interval`. Reject a `verification_uri` other than `{web}/login/device`. |
| `POST {web}/login/oauth/access_token` | `client_id`, `device_code`, `grant_type=urn:ietf:params:oauth:grant-type:device_code`. `authorization_pending` continues; `slow_down` adds 5 s; `expired_token` and `access_denied` end the attempt; anything else is `failed`. |
| same, refresh | `client_id`, `grant_type=refresh_token`, `refresh_token`. Single flight. Persist the new pair before using it. `bad_refresh_token` means `reconnect_required`. |
| `GET {api}/user` | `login`, `id`. |
| `GET {api}/user/installations` | Up to 20 installations. |
| `GET {api}/user/installations/{id}/repositories` | `per_page=100`, up to 3 pages each; set `repositories_truncated` beyond that or beyond 50. |

Refresh when the access token is within 10 minutes of expiry, or after
`Rejected`. A network failure keeps a still-valid token.

### C5. HTTP API

`GET /api/v1/integrations/github` keeps every existing field and gains:

```json
"connection": {
  "mode": "device_flow",
  "signin_available": true,
  "state": "connected",
  "login": "octocat",
  "connected_at": "2026-11-11T18:00:00Z",
  "install_url": "https://github.com/apps/<slug>/installations/new",
  "authorizations_url": "https://github.com/settings/apps/authorizations",
  "repositories_truncated": false,
  "attempt": {
    "id": "…", "state": "pending", "user_code": "WDJB-MJHT",
    "verification_uri": "https://github.com/login/device",
    "expires_at": "…", "message": ""
  }
}
```

- `mode`: `operator_token` | `device_flow`.
- `state`: `disconnected` | `connected` | `no_repositories` |
  `reconnect_required`.
- `attempt.state`: `pending` | `expired` | `denied` | `failed`. It is absent
  when there is none. `user_code` is sent only to the human who began it.

New routes, all human-administrator only (reject bearer tokens, as
`codex_account.go` does), `Cache-Control: no-store`, and **not** wrapped in
`s.mutation`, so no response is stored for idempotent replay:

| Route | Behaviour |
| --- | --- |
| `POST /integrations/github/connect` | `Begin`; returns the integration document. 409 `github_operator_managed` in `operator_token` mode; 409 `github_signin_unavailable` without a client ID. |
| `POST /integrations/github/connect/cancel` | Body `{"attempt_id": "…"}`. |
| `POST /integrations/github/disconnect` | Deletes the credential. |

`POST /integrations/github/check` (existing) runs `Sync` first in
`device_flow` mode.

### C6. Settings panel states

| Condition | Panel shows |
| --- | --- |
| `operator_token` | Today's panel, unchanged. |
| `device_flow`, sign-in unavailable | Not configured, with operator guidance. |
| `disconnected` | **Connect GitHub**. |
| attempt `pending` | The code (large, copyable), a link to the verification page, time left, **Cancel**. Poll every 3 s while visible. |
| attempt `expired` / `denied` / `failed` | The reason and **Try again**. |
| `no_repositories` | **Install Helm on your repositories** (`install_url`) and **I've installed it** (check). |
| `connected` | "Connected as @login", repository count, **Choose repositories on GitHub**, **Test connection**, **Disconnect** (confirmed, with the `authorizations_url` link). |
| `reconnect_required` | **Reconnect GitHub**. |

## Work packages

### WP1. Provider seam

Depends on: nothing. Read: `internal/githubprovider/{client,access,types}.go`,
`failures.md`, `internal/httpapi/github_service.go`.

Change: add C1. No behaviour change in `operator_token` mode: every existing
test passes unmodified.

Failure list must cover at least: a refresh bumping the generation; an
allowlist swap during an in-flight fetch caching data for a removed
repository; a data race on the allowlist; a source returning an empty or
whitespace token; repeated `Rejected` calls for one 401; invalid, duplicate
or more than 50 repositories.

Done when: `go test -race ./internal/githubprovider/ ./internal/httpapi/` is
green and a test shows a token change with a stable identity keeps the
generation.

### WP2. Connection manager and configuration

Depends on: WP1. Read: C2–C4, `internal/publicendpoint/connect.go` (session
and pending-state pattern), `internal/config/{github,intake,config}.go`.

Change: new package `internal/githubconnect` (split into flow, store, refresh,
installations and status files). Config: `HELM_GITHUB_APP_CLIENT_ID`,
`HELM_GITHUB_APP_SLUG`, `HELM_GITHUB_STATE_DIR` (default `data/github`), with
`DefaultGitHubAppClientID` and `DefaultGitHubAppSlug` constants left empty.
Validate formats; never expose an origin override at runtime.

Failure list must cover at least: `slow_down` ignored; polling that survives
cancel, expiry, disconnect or `Close`; a replaced attempt succeeding late; a
success arriving after cancel; two concurrent refreshes; a crash or write
failure between refresh and persist; a partial write; loose permissions,
symlink, wrong owner, truncated JSON, unknown version; HTTP 200 carrying an
error; oversized or malformed responses; a redirect; a foreign
`verification_uri`; an App without token expiry; an expired refresh token;
pagination and truncation; mixed-case repository names; an empty ceiling
intersection; any secret in a log line or error string; zero or absurd
`expires_in`.

Done when: race tests against an `httptest` GitHub are green, including a
scan of captured logs and errors for every sentinel.

### WP3. HTTP routes, wiring and OpenAPI

Depends on: WP2. Read: C5, `internal/httpapi/github_{dispatch,routes,
lifecycle,service}.go`, `codex_account.go`, `openapi/README.md`.

Change: routes in a new `github_connection.go`; build the manager in
`configureGithubServices`, pass it as `Options.Source`, connect
`OnRepositories` to `SetRepositories`, and start and stop it with the GitHub
services. Register the new route labels from a new file with an `init` that
adds to `metricRouteTemplates`. Add OpenAPI paths and schemas, then
`npm run openapi:generate` from `web/`.

Failure list must cover at least: a bearer token or non-administrator calling
any new route; `user_code` shown to another administrator; connect in
`operator_token` mode; a cacheable or replay-stored response; the allowlist
not following load, sync or disconnect; disconnect during a running audit job
(the generation fence must stop it); shutdown order.

Done when: handler tests are green, `make lint` passes, and
`npm run openapi:check` is clean.

### WP4. Settings UI

Depends on: C5 and C6 only; runs alongside WP2. Read:
`web/src/lib/components/github/GitHubIntegrationSettings.svelte`,
`web/src/lib/{api,types}/github.ts`, `web/e2e/github-selection-ui.spec.ts`,
`web/e2e/support/githubFixture.ts`, `CloudflareSetup.svelte`.

Change: types and API methods; a new `GitHubConnection.svelte` used by the
settings panel; a mocked-route Playwright spec that screenshots every C6 row.

Failure list must cover at least: polling after unmount or while hidden;
overlapping polls; a double click starting two attempts; a stale code after a
new attempt; expiry while the code is on screen; disconnect without
confirmation; clipboard denial; links that are not `https://github.com/`;
state changes not announced to assistive technology; any change to the
`operator_token` panel.

Done when: `npm run check`, `npm run build` and the new spec pass, with
screenshots attached to the run.

### WP5. Real-backend proof

Depends on: WP3 and WP4 merged. Read: `test/githubbeta/*`,
`web/e2e/github-beta-integration.spec.ts`, `test/e2e/run-suites.py`,
[validation](GITHUB_TOKEN_BETA_VALIDATION.md).

Change: give the harness a `device-flow` credential mode (no token file) and
serve the C4 endpoints from the synthetic upstream, with controls to approve,
deny or expire an attempt, change installed repositories, reject the current
access token and fail a refresh. Add a spec for the whole journey: connect,
install, select a repository, verify a pull request, refresh (the pull request
stays verified), lose a repository, disconnect.

Done when: the new spec and the unchanged existing GitHub suite pass through
`test/githubbeta/run.sh`, and a scan of browser artifacts, harness output and
the SQLite file finds no sentinel.

### WP6. Documentation

Depends on: WP3 merged; runs alongside WP5. Change: a new
`docs/GITHUB_DEVICE_FLOW.md` (routes, states, configuration, the G1
registration checklist, disconnect and revocation, restore-means-reconnect);
short pointers from `GITHUB_BETA_OPERATIONS.md`, `GITHUB_BETA_API.md` and the
README. Confirm the backup and runtime-archive scripts do not collect
`data/github`; exclude it if they do.

Done when: `npm run docs:check` passes and every file is under 20,000 bytes.

### WP7. Independent review

Depends on: everything merged. Start a fresh subagent with the diff and C1–C5
only, not this plan's conclusions. It checks: no secret reaches a log, error,
response, SQLite, the idempotency table, a prompt, an argument or the
environment; file permissions and atomic writes; fixed origins and no
redirects; who can call each route; goroutine lifetime. Fix findings, then
run the full `test/e2e/run.sh`.

## Order

| Wave | Packages | Notes |
| --- | --- | --- |
| 1 | WP1 | Merge before anything else starts. |
| 2 | WP2, WP4 | In parallel. |
| 3 | WP3 | Only package that regenerates OpenAPI. |
| 4 | WP5, WP6 | In parallel. |
| 5 | WP7, then G2 and G3 | |

The orchestrator merges each package into `feat/github-device-flow` in wave
order, reruns that wave's "done when" checks on the merged branch, and only
then starts the next wave. After G1, a one-line change sets the two default
constants.

## Sources

- [Generating a user access token for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
- [Refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens)
- [Authorizing OAuth apps: device flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)
- Existing design: [token plan](GITHUB_TOKEN_BETA_PLAN.md),
  [execution](GITHUB_TOKEN_BETA_EXECUTION.md),
  [Cloudflare connect](CLOUDFLARE_CONNECT_PLAN.md).
