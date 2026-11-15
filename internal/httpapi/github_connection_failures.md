# GitHub connection routes: failure inventory

Recorded before the tests and the code for WP3 of
`docs/GITHUB_DEVICE_FLOW_PLAN.md` (contract C5, amendments A2 and A3). Each
entry names the test that covers it; the tests live in
`github_connection_test.go` and `github_connection_flow_test.go`.

## Who may call

- A bearer token (agent or human-owned) calls connect, cancel, disconnect,
  the integration document or check. 403, and the manager is never reached.
  `TestGithubConnectionRoutesRejectTokensAndMembers`.
- A signed-in member who is not an administrator calls any of them. 403.
  `TestGithubConnectionRoutesRejectTokensAndMembers`.
- A browser mutation without the Helm origin rides an ambient session (CSRF).
  The router's origin guard refuses it before the route.
  `TestGithubConnectionRoutesRejectTokensAndMembers`.
- A wrong method (GET on connect, DELETE on disconnect) acts anyway. 405.
  `TestGithubConnectionMethodsAndCancelBody`.

## What each administrator sees

- `user_code` sent to an administrator other than the one who began the
  attempt, through connect, cancel, disconnect, the document or check.
  `TestGithubConnectUserCodeOnlyForTheBeginner`.
- The device code, an access or refresh token, or the user code (outside the
  beginner's own response) in any response body, any log line, or any row of
  the SQLite idempotency table. Every flow test scans all three for every
  sentinel at cleanup (`newConnectionEnv`).
- An error message carrying GitHub's text or a code of GitHub's: every error
  body is checked against the fixed sentence table.
  `TestGithubConnectionErrorTable`.

## Modes

- connect, cancel or disconnect in `operator_token` mode reaches a manager, or
  answers anything but 409 `github_operator_managed`; a manager is built in
  that mode at all. `TestGithubConnectionOperatorTokenMode`.
- connect without a client ID answers anything but 409
  `github_signin_unavailable`. `TestGithubConnectionWithoutClientID`.
- `HELM_GITHUB_REPOSITORIES` wrongly required in `device_flow` mode, or not
  applied as a ceiling when set.
  `TestGithubConnectionFlowFollowsTheAllowlist` (no ceiling),
  `TestGithubConnectionCeilingNarrowsTheAllowlist` (ceiling).
- GitHub disabled (`HELM_GITHUB_ENABLED` unset) still builds a manager or
  starts polling. `TestGithubConnectionDisabledBuildsNothing`.

## Statuses (amendment A2)

- Any code of these routes mapped to 502 or 504, which a proxy in front of
  Helm replaces, losing `error.code`. `TestGithubConnectionErrorTable` walks
  every manager code and a foreign error.
- A state problem answered with anything but 409; GitHub being unreachable or
  refusing answered with anything but 424; a malformed body with anything but
  400; 500 for anything but a real server fault. `TestGithubConnectionErrorTable`,
  `TestGithubCheckSyncFailures`.
- A code the panel (`web/src/lib/components/github/connection.ts`) does not
  turn into the right sentence. The table test pins the codes the panel
  names; the remaining ones are read by status there.

## Caching and replay

- A response that a browser or proxy may cache (the document carries the
  user code). Every response of every route here, errors included, carries
  `Cache-Control: no-store`. `TestGithubConnectionNoStoreAndNoReplay`.
- A repeated `Idempotency-Key` replays a stored response instead of reaching
  the manager, or any response is stored in `idempotency_keys`.
  `TestGithubConnectionNoStoreAndNoReplay`.

## Body of cancel

- A missing, null, empty, non-string or over-long `attempt_id`, an unknown
  field, a second JSON value, a non-object or an empty body accepted.
  `TestGithubConnectionMethodsAndCancelBody`.
- A stale attempt ID cancels the attempt that replaced it. 200, attempt kept.
  `TestGithubConnectUserCodeOnlyForTheBeginner`.

## Document shape

- The `connection` object differs from the TypeScript `GithubConnection`
  type: unknown keys, `attempt` present (even `null`) when there is none,
  `user_code` present for someone else, a field missing.
  `TestGithubConnectionDocumentShape`.
- An existing field of the integration document dropped.
  `TestGithubConnectionDocumentShape`.

## The provider allowlist

- The allowlist not following load (a restart with a saved connection), sync
  (`check`), connect or disconnect.
  `TestGithubConnectionFlowFollowsTheAllowlist`.
- `check` not running `Sync` first in `device_flow` mode, so newly installed
  repositories never appear. `TestGithubConnectionFlowFollowsTheAllowlist`.
- `check` running `Sync` in `operator_token` mode.
  `TestGithubConnectionOperatorTokenMode`.

## Background work

- Disconnect while an audit job is running: the job publishes findings made
  with the removed credential. The provider generation fence must fail it as
  `authorization_changed`. `TestGithubDisconnectFencesARunningAudit`.
- Shutdown leaves the manager polling, or stops it before the services that
  read credentials from it; a goroutine survives `StopGithubServices`;
  stopping twice or without starting panics.
  `TestGithubConnectionLifecycle`.
- Startup contacts GitHub. `TestGithubConnectionLifecycle` (no request reaches
  the fake GitHub before a route asks for one).

## Metrics

- The new routes reported as `/api/v1/other`, or a route label that grows
  without bound. `TestGithubConnectionMetricRoutes`.

## Found by the independent review (WP7)

- Another administrator cancels an attempt they did not begin: the cancel is
  answered 200 and changes nothing. `TestGithubConnectionReviewFindings`.
- A script loops connect and cancel, and each loop asks GitHub for a device
  code: beyond the manager's budget the route answers 429 `rate_limited`
  with `Retry-After`, and GitHub is not asked. `TestGithubConnectionReviewFindings`,
  `TestGithubConnectionErrorTable`.
- Connect and disconnect accepted and ignored any body. They take no input:
  an empty body or `{}` only, anything else is 400 `invalid_request`.
  `TestGithubConnectionReviewFindings`.
