# GitHub connection manager failure inventory

Recorded before the isolation tests and the code for WP2 of
`docs/GITHUB_DEVICE_FLOW_PLAN.md` (contracts C2–C4, amendments A1, A2, A4).
Each entry names the test that covers it.

## Device flow (flow.go)

- `slow_down` ignored, so GitHub throttles or bans the client: the interval
  grows by 5 s on every `slow_down`, or to GitHub's own `interval` if larger.
  `TestSlowDownLengthensTheInterval`.
- Polling faster than the interval GitHub gave, or below 5 s.
  `TestSlowDownLengthensTheInterval`, `TestDeviceCodeReplyIsValidated`.
- Polling that survives cancel, expiry, disconnect or `Close`.
  `TestCancelStopsPolling`, `TestExpiredAttemptStopsPolling`,
  `TestDisconnectStopsPolling`, `TestCloseStopsPolling`.
- A replaced attempt succeeding late connects the wrong account.
  `TestReplacedAttemptSuccessIsDiscarded`.
- A success arriving after cancel connects anyway.
  `TestSuccessAfterCancelIsDiscarded`.
- A double click (same administrator) starts two attempts or two device codes.
  `TestSameActorGetsThePendingAttempt`.
- `user_code` shown to another administrator. `TestUserCodeOnlyForTheBeginner`.
- A stale tab cancelling the attempt that replaced it.
  `TestCancelWithStaleIDKeepsTheAttempt`.
- HTTP 200 carrying an OAuth `error` treated as success, on any of the three
  token calls. `TestDeviceCodeErrorIn200`, `TestPollOutcomes`,
  `TestRefreshErrorIn200`.
- `expired_token` and `access_denied` not ending the attempt; any other error
  not reported as `failed`. `TestPollOutcomes`.
- A foreign `verification_uri` (phishing page) shown to the administrator.
  `TestDeviceCodeReplyIsValidated`.
- Zero, negative or absurd `expires_in` / `interval` in the device-code reply,
  or a malformed `user_code`. `TestDeviceCodeReplyIsValidated`.
- Zero or absurd `expires_in` in a token reply. `TestTokenReplyIsValidated`.
- An App without token expiry (no `expires_in`, no `refresh_token`) rejected,
  or later "refreshed". `TestAppWithoutExpiryConnectsAndNeverRefreshes`.
- Sign-in begun without a client ID, after `Close`, or while connected.
  `TestBeginRefusals`.
- `/user` failing after a successful exchange leaves a half connection.
  `TestPollOutcomes` (user lookup case).
- GitHub unreachable during polling keeps the attempt pending forever.
  `TestPollOutcomes` (unreachable case).
- A failed sign-in not logged, logged twice, or logged with detail (A4).
  `TestFailedSignInIsLoggedOnce`.

## HTTP client (client.go)

- A client that ignores the operator's proxy (A1). `TestRequestsGoThroughTheProxy`.
- Following a redirect to another host. `TestRedirectIsRefused`.
- An oversized response read into memory. `TestOversizedReplyIsRefused`.
- A malformed (non-JSON, truncated) response accepted. `TestDeviceCodeReplyIsValidated`,
  `TestTokenReplyIsValidated`.
- A request with no timeout hanging a caller. `TestInjectedClientIsConfined`.
- A host other than the fixed web and API origins contacted. Structural: every
  URL is built from `m.web` or `m.api`; `TestDeviceCodeReplyIsValidated`
  shows a returned `verification_uri` is never followed.

## Credential file (store.go)

- Loose file permissions, a symlinked file, a non-regular file, another owner,
  an oversized file, truncated JSON, unknown fields or an unknown version
  trusted. Each is `reconnect_required` and left in place.
  `TestUntrustedFilesAreReconnectRequired`.
- A loose or foreign-owned state directory chmodded, or used silently.
  `TestLooseDirectoryIsRefusedAndExplained`.
- A symlinked state directory refused (operators link data directories).
  `TestSymlinkedDirectoryIsFollowed`.
- A partial write replacing a good file; temp files left behind by a crash.
  `TestWriteFailureKeepsTheOldFile`, `TestCrashLeftoversAreRemoved`.
- New files created with group or other bits. `TestSavedFileIsPrivate`.

## Refresh (refresh.go)

- Two concurrent refreshes spend the rotating refresh token twice.
  `TestConcurrentRefreshIsSingleFlight`.
- A refresh abandoned because its caller was cancelled, losing the rotated
  pair. `TestRefreshSurvivesCancelledCaller`.
- A write failure between refresh and persist falls back to the dead pair.
  `TestRefreshStorageFailureKeepsNewPair`.
- A crash between refresh and persist leaves a dead pair on disk: the next
  refresh reports `reconnect_required` cleanly. `TestCrashAfterRefreshReconnects`.
- A reply without a refresh token wipes the old one.
  `TestRefreshWithoutRefreshTokenKeepsOldOne`.
- An expired or bad refresh token retried forever.
  `TestExpiredRefreshTokenIsReconnectRequired`.
- A network failure throws away a still-valid access token.
  `TestRefreshNetworkFailureKeepsValidToken`.
- A refresh changes the identity (would unverify every pull request).
  `TestRefreshKeepsIdentity`.
- `Rejected` for a stale token, or repeated, causes extra refreshes.
  `TestRejectedForcesOneRefresh`.
- A token with whitespace or control characters handed to the provider.
  `TestTokenReplyIsValidated`, `TestUntrustedFilesAreReconnectRequired`.

## Installations (installations.go)

- Pagination stopping early, or running without bound; truncation not reported.
  `TestSyncPaginationAndTruncation`.
- More than 20 installations or 50 repositories accepted.
  `TestSyncPaginationAndTruncation`.
- Mixed-case repository names duplicated or mismatched with the ceiling.
  `TestSyncNormalizesAndAppliesCeiling`.
- An empty ceiling intersection reported as connected.
  `TestSyncNormalizesAndAppliesCeiling`.
- The allowlist not following load, sync or disconnect.
  `TestRepositoriesFollowLoadSyncDisconnect`.
- A 401 during sync not reported to the refresh logic.
  `TestSyncRetriesAfterRejectedToken`.

## Secrets and errors

- Any token, refresh token, device code or user code in a log line or an error
  string. Every test runs under `newEnv`, whose cleanup scans every captured
  log line and every returned error for all four sentinels.
- An error without a stable code, or a code WP3 could map to 502/504.
  `TestErrorCodesAreStable`.

## Configuration (internal/config)

- A malformed client ID or slug accepted; a state directory with control
  characters; the default not beside the database.
  `TestGitHubDeviceFlowEnvironment` in `internal/config/github_test.go`.

## Found by the independent review (WP7)

- A failing refresh is retried on every read: with GitHub's token endpoint
  down, each provider read (the audit loop ticks every second) asks it
  again and waits for it. One exchange per 30 seconds after a failure; a
  still-valid token keeps working meanwhile. `TestFailedRefreshBacksOff`.
- One 401 for a token fresh from a refresh ends the sign-in and wipes a
  refresh token that still works. The first such 401 refreshes once more;
  only a second one in a row, or `bad_refresh_token`, ends the sign-in.
  `TestRejectionAfterRefreshRetriesBeforeEnding`,
  `TestRejectedForcesOneRefresh`.
- A sign-in that ends by the clock alone (the refresh token's lifetime
  passes) never tells the allowlist listeners, so the provider keeps the old
  list. `TestClockExpiryNotifiesListeners`.
- Another administrator cancels an attempt they did not begin (the attempt
  ID is in everyone's status). Only the beginner cancels; anyone may still
  replace it with their own. `TestCancelByAnotherAdministratorIsIgnored`.
- A connection replaced while a refresh is in flight: `Fresh` answers with
  the old identity or a false `not_configured`, flipping the provider's
  generation twice. It answers for the connection held now.
  `TestFreshAnswersForTheConnectionHeldNow`.
- Nothing bounds how often an administrator's script makes Helm ask GitHub:
  every connect after a cancel requests a device code, and every concurrent
  Test connection reads all installations again. At most 20 device-code
  requests a minute; concurrent syncs share one run. The cap must not bite
  people: 6 was low enough that the real-backend proof's own attempts hit
  it. A clock that steps backwards must not hold the cap shut either.
  `TestBeginBudget`, `TestConcurrentSyncsShareOneRun`.
