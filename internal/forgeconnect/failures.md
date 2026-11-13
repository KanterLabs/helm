# forgeconnect: ways this can fail

Written before the tests and the code (AGENTS.md). Each entry names the
behaviour that must hold and the test that proves it. Contract references
are to `docs/GITEA_OAUTH_PLAN.md`.

## Server address (C4, C3 Probe)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| U1 | Address carries user info (`https://u:p@host`) | `invalid_server_url` | `TestNormalizeBaseURL` |
| U2 | Address carries a query or a bare `?` | `invalid_server_url` | `TestNormalizeBaseURL` |
| U3 | Address carries a fragment | `invalid_server_url` | `TestNormalizeBaseURL` |
| U4 | Odd scheme (`ftp:`, `file:`, `javascript:`) | `invalid_server_url` | `TestNormalizeBaseURL` |
| U5 | Over 255 characters, empty, control characters or spaces | `invalid_server_url` | `TestNormalizeBaseURL` |
| U6 | Upper-case scheme or host, trailing slash | Lower-cased, slash stripped; path case kept | `TestNormalizeBaseURL` |
| U7 | `http` without the opt-in | `insecure_http`; with the opt-in the server records `insecure_http: true` | `TestNormalizeBaseURL`, `TestProbeInsecureHTTP` |
| U8 | Scheme omitted | `https` assumed | `TestNormalizeBaseURL` |
| U9 | Address is a deep page on the forge | Candidates: the page, its parents, the origin; never more than 4 | `TestProbeCandidates`, `TestProbeFromDeepPage` |
| U10 | Literal link-local, unspecified, multicast or broadcast host | `invalid_server_url` before any request | `TestNormalizeBaseURL` |
| U11 | Missing or bad port (`host:abc`) | `invalid_server_url` | `TestNormalizeBaseURL` |

## Outbound client (C4)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| N1 | Host resolves to a link-local address (cloud metadata) | Refused in the dialer `Control` hook, after resolution | `TestControlAddress` |
| N2 | DNS rebinding: name resolves to a safe address when checked, a link-local one when dialled | The check lives only in `Control`, which sees the address actually dialled; there is no earlier resolution to bypass | `TestControlAddress`, `TestGuardedClientRefusesLinkLocalDial` |
| N3 | IPv4-mapped IPv6 link-local (`::ffff:169.254.169.254`), IPv6 link-local with a zone, multicast, unspecified, broadcast | Refused | `TestControlAddress` |
| N4 | Private and loopback addresses | Allowed (self-hosted forges live there) | `TestControlAddress`, every flow test (loopback fake forge) |
| N5 | Redirect to another host | Not followed; the other host is never contacted; the call fails with its code | `TestProbeRedirectNotFollowed`, `TestExchangeRedirectNotFollowed` |
| N6 | Oversized response (over 64 KB, or over 1 MB for a repository page) | Read stops at the cap; treated as a bad answer | `TestProbeOversizedDocument`, `TestRepositoriesOversizedPage` |
| N7 | Slow response | Times out; `server_unreachable` | `TestSlowServerTimesOut` |
| N8 | Untrusted certificate | `tls_error`; no option to skip verification | `TestTLSUntrustedAndExtraRoots` |
| N9 | Private CA in `SSL_CERT_FILE` / `SSL_CERT_DIR` | Trusted in addition to system roots | `TestTLSUntrustedAndExtraRoots` |
| N10 | Injected test client with redirects enabled or a long timeout | Redirects still refused, timeout clamped to 10 s | `TestInjectedClientHygiene` |
| N11 | Environment proxy (`HTTPS_PROXY`) would bypass the address guard | Guarded client ignores proxies | `TestGuardedClientIgnoresProxy` |
| N12 | Connection refused | `server_unreachable` | `TestProbeUnreachable` |

## Probe (C3)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| P1 | Discovery lacks `authorization_code`, `refresh_token` or `S256` | `not_a_forge` | `TestProbeDiscoveryRequirements` |
| P2 | No discovery document anywhere / not JSON / HTML page | `not_a_forge` | `TestProbeNotAForge` |
| P3 | Issuer differs only by case or trailing slash | Accepted | `TestProbeIssuerVariants` |
| P4 | Issuer on another path of the same host | Re-probe that issuer once; settle there | `TestProbeIssuerVariants` |
| P5 | Issuer on another host | `issuer_mismatch` with the advertised issuer | `TestProbeIssuerVariants` |
| P6 | Issuer is not a URL, or over 255 characters | `not_a_forge`; the issuer is never echoed | `TestProbeIssuerVariants` |
| P7 | Re-probed issuer advertises yet another issuer | `not_a_forge`, no loop | `TestProbeIssuerVariants` |
| P8 | Forge installed under a sub-path | Settled base keeps the path; all later calls use it | `TestProbeSubPath`, `TestSignInSubPath` |
| P9 | Version endpoint refused anonymously or malformed | `Version` empty, product `gitea`, probe still succeeds | `TestProbeVersion` |
| P10 | Version with `+gitea-` | Product `forgejo` | `TestProbeVersion` |
| P11 | Upstream body reaches an error string | Never: error strings carry the code only | `TestNoSecretLeaks` |

## File store (C2)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| S1 | Partial write (crash before rename) | `gitea.json` is untouched; stray temp files are ignored and later removed | `TestStorePartialWriteLeavesOldFile` |
| S2 | Write failure (disk full, read-only) | `storage_failed`; memory keeps the old state; temp file removed | `TestStoreWriteFailure` |
| S3 | Group/other permission bits on the file | Loads as `reconnect_required`, left in place | `TestStoreLoadRejects` |
| S4 | Symlink, directory or other non-regular file | Loads as `reconnect_required`, left in place | `TestStoreLoadRejects` |
| S5 | File owned by another user | Loads as `reconnect_required` | `TestStoreLoadRejects` |
| S6 | Over 32 KB, truncated JSON, unknown version, unknown kind, invalid base URL or client ID inside | Loads as `reconnect_required`, left in place | `TestStoreLoadRejects` |
| S7 | Written file mode or directory mode wrong | File 0600, directory 0700 | `TestStoreRoundTrip` |
| S8 | Missing file | `disconnected` | `TestStatusDisconnected` |
| S9 | A refresh finishing after Disconnect resurrects the file | Writes are fenced by a generation; a stale write is dropped | `TestDisconnectDuringRefresh` |

## Sign-in (C1, C3)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| F1 | Feature disabled or no public origin | `unavailable`; status says `disabled` / `public_origin_missing` | `TestUnavailable` |
| F2 | Malformed client ID | `client_unknown`, no network | `TestBeginValidatesClientID` |
| F3 | Authorize URL built from input other than the settled base | Always `{base}/login/oauth/authorize` with all seven parameters, fixed scope, no `openid` | `TestBeginAuthorizeURL` |
| F4 | State reused | `state_invalid` the second time | `TestFinishStateRules` |
| F5 | State expired (over 10 minutes) | `state_invalid` | `TestFinishStateRules` |
| F6 | Another actor's state | `state_invalid`; the owner's sign-in still works | `TestFinishStateRules` |
| F7 | Second `Begin` by the same actor | Replaces the first; the first state is refused | `TestFinishStateRules` |
| F8 | `Begin` while connected | `already_connected` | `TestBeginWhileConnected` |
| F9 | `Begin` in `reconnect_required` with empty input | Reuses stored server and client ID | `TestReconnectReusesServer` |
| F10 | Callback `error=access_denied` | `denied`; pending consumed | `TestFinishUpstreamErrors` |
| F11 | Callback `server_error` mentioning "different scope" | `scope_conflict` | `TestFinishUpstreamErrors` |
| F12 | Any other callback error | `authorize_failed` | `TestFinishUpstreamErrors` |
| F13 | Exchange error mentioning "client secret" | `client_confidential` | `TestExchangeErrors` |
| F14 | Exchange `invalid_client` otherwise | `client_unknown` | `TestExchangeErrors` |
| F15 | Other exchange errors (bad verifier, used code, 500, not JSON) | `exchange_failed` | `TestExchangeErrors` |
| F16 | Token response missing a field, wrong type, absurd lifetime (0, negative, over 30 days), oversized or whitespace in the token | `exchange_failed`, nothing stored | `TestTokenResponseValidation` |
| F17 | Empty refresh token | Accepted; connection lasts until expiry, then `reconnect_required` | `TestNoRefreshToken` |
| F18 | Identity call fails after exchange | `exchange_failed`, nothing stored | `TestExchangeErrors` |
| F19 | Exchange succeeds but the file cannot be written | `storage_failed`, nothing kept in memory | `TestStoreWriteFailure` |
| F20 | Empty code with no upstream error | `authorize_failed` | `TestFinishStateRules` |
| F21 | A different redirect URI at exchange than at authorize | Same string used at both | `TestBeginAuthorizeURL` (fake forge compares) |

## Refresh (C3, decision 7)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| R1 | Refresh `invalid_grant` (grant revoked) | `reconnect_required`; tokens wiped; server, client ID and account kept | `TestRefreshErrorMapping` |
| R2 | Refresh `invalid_client` (application deleted, or confidential) | Same as R1 | `TestRefreshErrorMapping` |
| R3 | Refresh `unauthorized_client` (one-time token reused, unparseable) | Same as R1 | `TestRefreshErrorMapping` |
| R4 | Network failure during refresh, token not yet expired | Old token kept and returned | `TestRefreshNetworkFailure` |
| R5 | Network failure during refresh, token expired | `server_unreachable`, state stays connected | `TestRefreshNetworkFailure` |
| R6 | Refresh returns byte-identical tokens | Success | `TestRefreshIdenticalBytes` |
| R7 | One-time refresh mode | Newest refresh token always stored and used; never the old one | `TestOneTimeRefreshMode` |
| R8 | Two concurrent refreshes | One upstream call (single flight); both callers get the new token | `TestConcurrentRefreshSingleFlight` |
| R9 | Persist fails after a successful refresh | `storage_failed`; new token not used; the file keeps the old pair | `TestRefreshPersistFailure` |
| R10 | Crash between refresh and persist | The file still holds the previous pair; on restart that pair is used | `TestRefreshPersistFailure` |
| R11 | API 401 | One refresh and one retry | `TestAPI401RefreshAndRetry` |
| R12 | A refreshed token rejected again within a minute | `reconnect_required` | `TestAPI401RefreshAndRetry`, `TestRejected` |
| R13 | `Rejected` with a stale token | Ignored | `TestRejected` |
| R14 | Refresh due: within 5 minutes of expiry, or older than 24 hours | `Fresh` refreshes; otherwise not | `TestFreshRefreshPolicy` |
| R15 | Malformed refresh response (missing token, bad lifetime) | Treated as transient: old token kept while valid | `TestRefreshErrorMapping` |

## Check, repositories, status, disconnect

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| C1 | Limit check 403 / 200 / 401 / 500 | `scoped` / `full` / refresh-and-retry then `reconnect_required` / `unknown` | `TestLimitCheck` |
| C2 | Server down during check | `check.state` `unreachable`, `checked_at` updated, connection kept | `TestCheckOutcomes` |
| C3 | Grant revoked during check | `check.state` `unauthorized`, state `reconnect_required` | `TestCheckOutcomes` |
| C4 | Missing or malformed `X-Total-Count` | Count unchanged | `TestCheckOutcomes` |
| C5 | Repositories over 10 pages | 500 listed, `truncated: true` | `TestRepositoriesPagination` |
| C6 | Short last page | Stops, `truncated: false` | `TestRepositoriesPagination` |
| C7 | Malformed repository JSON | `server_unreachable` | `TestRepositoriesPagination` |
| C8 | Status does network I/O | Never (fake forge counts calls) | `TestStatusNoNetwork` |
| C9 | Status JSON field names drift from C5 | Golden key set | `TestStatusJSONShape` |
| C10 | `reconnect_required` hides server, client ID or account | They stay | `TestRefreshErrorMapping` |
| C11 | Disconnect leaves the file or the identity | File removed, identity changes on the next connect | `TestDisconnect` |
| C12 | Identity changes on refresh | Never | `TestRefreshIdenticalBytes` |

## Keep-alive and lifetime

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| K1 | Idle install lets a 30-day refresh token lapse | Wakes on the interval; refreshes when `refreshed_at` is over 24 hours old | `TestKeepAlive` |
| K2 | Goroutine survives `Close` | `Close` cancels and waits; idempotent; `Start` twice starts one loop; `Start` after `Close` starts none | `TestKeepAliveLifetime` |
| K3 | `New` starts goroutines or contacts the network | Never | `TestStatusNoNetwork` |

## Secrets

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| X1 | Access token, refresh token, code, verifier, state or upstream `error_description` / body in an error string or log line | Never; scanned with distinct sentinels across a full scenario | `TestNoSecretLeaks` |
| X2 | Secrets in the status document | Never | `TestNoSecretLeaks` |

## Last check survives a restart (WP3)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| R1 | After a restart the status says 0 repositories and no check result until someone presses Test connection | `repository_count` and `check` are stored in the file; `Status()` after `New` shows the last known values | `TestLastCheckSurvivesRestart` |
| R2 | A version 1 file written before these fields existed | Still loads as connected; count 0, empty check | `TestLastCheckSurvivesRestart` |
| R3 | A stored count or check that is out of range or not one of the known values | The file is not trusted (`reconnect_required`), never echoed | `TestLastCheckSurvivesRestart` |
| R4 | A failed check after a good one | The failure is stored, the last good count is kept | `TestLastCheckSurvivesRestart` |

- **A failed sign-in is invisible to the operator.** Nothing was logged for a
  denied, forged or refused sign-in. `Finish` now logs one line with the code
  only. Test: `TestFailedSignInIsLogged`; the per-test sentinel scan proves no
  upstream text rides along.

## From the independent review

Each has a test in `review_test.go` unless noted.

- **"Read-only" claimed without proof.** The limit check only showed the grant
  was not full access. A grant widened on the forge afterwards (scope belongs
  to the grant on Gitea) still looked limited. Gitea names the granted scope
  in its refusal; any scope beyond `read:*` or `public-only` now reports
  `full`. Test: `TestLimitCheckReadsGrantedScope`.
- **A refresh reply without a refresh token threw the old one away.** Test:
  `TestRefreshWithoutNewRefreshTokenKeepsOld`.
- **A caller that went away mid-refresh lost a pair the forge had already
  rotated**, ending the connection in one-time mode. The exchange now
  outlives its caller's context. Test:
  `TestCancelledCallerDoesNotLoseRotatedTokens`.
- **Cloud metadata outside 169.254.0.0/16 was reachable**: `fd00:ec2::254`,
  `100.100.100.200`, and link-local addresses wrapped in NAT64 or 6to4. Test:
  `TestGuardBlocksOtherMetadataAddresses`.
- **Saving re-permissioned a directory Helm did not create**, and a symlinked
  directory could never be saved to. Tests:
  `TestStoreLeavesForeignDirectoryAlone`, `TestStoreFollowsSymlinkedDirectory`.
- **A repository listed on two pages appeared twice.** Test:
  `TestRepositoriesAreDeduplicated`.
- **Any callback with the wrong state cancelled the sign-in in progress.**
  Test: `TestWrongStateKeepsPendingSignIn`.
- **A token could be sent to a server connected a moment later**: the token
  and the base URL were read under separate locks. They now come from one
  snapshot; covered by the race tests, not a dedicated one.
- **`TestKeepAlive` raced the save.** It now waits for the stored time.

Known and accepted: two administrators finishing sign-in at the same moment
leaves the loser's grant on the forge until it is removed there.

## Found on beta

- **A server whose only way out is an egress proxy could never reach the
  forge.** The client ignored `HTTPS_PROXY` on purpose, so on a host that
  blocks direct egress every probe timed out. It now follows the operator's
  proxy policy. Because the dialer then sees only the proxy, the target is
  checked before the request leaves. Tests: `proxy_test.go`.
- **A proxy used to reach a blocked address.** A literal link-local or
  metadata address, or a name that resolves to one locally, is refused and
  never sent to the proxy. Test:
  `TestProxyCannotBeUsedToReachBlockedAddresses`.
