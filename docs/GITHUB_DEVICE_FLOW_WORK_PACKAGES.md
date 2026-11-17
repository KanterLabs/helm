# GitHub device-flow implementation work packages (historical)

> Retired in TC-391: this is historical documentation. The GitHub product
> integration and Branches view are removed. See [removal and preservation](GITHUB_REMOVAL.md).

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
