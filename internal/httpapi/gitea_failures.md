# Gitea connection routes: ways this can fail

Written before the tests and the code (AGENTS.md), for WP3 of
[the plan](../../docs/GITEA_OAUTH_PLAN.md) (contract C5). The manager's own
list is [`internal/forgeconnect/failures.md`](../forgeconnect/failures.md).
Handler tests drive the real `forgeconnect.Manager` against a loopback fake
forge through `Server.ServeHTTP`, with distinct sentinels for every secret.

## Configuration (`internal/config/gitea.go`)

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| G1 | `HELM_GITEA_OAUTH` unset | On | `TestGiteaEnvironment` |
| G2 | `HELM_GITEA_OAUTH` is `true`, `1`, `OFF ` or anything but `on`/`off` | Startup error naming the setting | `TestGiteaEnvironment` |
| G3 | `HELM_FORGE_STATE_DIR` unset | `forge` beside the database file, so it follows the data volume | `TestGiteaEnvironment` |
| G4 | `HELM_FORGE_STATE_DIR` carries a control character | Startup error | `TestGiteaEnvironment` |
| G5 | The settings are not wired into `FromEnv` | `FromEnv` returns them | `TestGiteaEnvironmentFromEnv` |

## Wiring and lifetime

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| W1 | The manager cannot be built (bad public origin, no state directory) | One log line with the code only; Helm starts; the status route says `available: false`, `disabled`; every other route `503 unavailable`; `/readyz` unaffected | `TestGiteaManagerOptionalAtStartup` |
| W2 | `HELM_GITEA_OAUTH=off` | Status `available: false`, `disabled`; probe and start `503 unavailable`; no forge contacted | `TestGiteaDisabledAndNoOrigin` |
| W3 | No `HELM_PUBLIC_ORIGIN` | Status `public_origin_missing`; probe and start refused with `unavailable` (sign-in never offered) | `TestGiteaDisabledAndNoOrigin` |
| W4 | Shutdown order: keep-alive outlives the server, or closing twice / before start panics | Started with the server's lifetime; stopped after HTTP is drained; `Stop` waits, is idempotent and safe with no manager | `TestGiteaLifecycle` |

## Who may call

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| A1 | Agent bearer token on any route | `403`, manager not called | `TestGiteaRoutesRefuseNonAdministrators` |
| A2 | Signed-in non-administrator human on any route | `403` (callback: `303 state_invalid`), manager not called | `TestGiteaRoutesRefuseNonAdministrators` |
| A3 | No session on a JSON route | `401` | `TestGiteaRoutesRefuseNonAdministrators` |
| A4 | Callback with no Helm session (cookie expired, other browser) | `303 /settings?gitea_error=state_invalid`, never raw JSON | `TestGiteaCallbackWithoutSession` |
| A5 | Callback reaches a different administrator than the one who started | `state_invalid`; the starter's sign-in is not completed by the other | `TestGiteaCallbackOtherAdministrator` |
| A6 | POST without the public `Origin` (cross-site form) | `403 csrf_origin` (existing guard) | `TestGiteaRoutesRefuseNonAdministrators` |

## Responses

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| R1 | A response is cacheable | `Cache-Control: no-store` on every route, success and error | every route test via `giteaCall` |
| R2 | A response is stored for idempotent replay | Not wrapped in `s.mutation`; an `Idempotency-Key` is neither required nor replayed: a repeated key reaches the manager again | `TestGiteaNoReplayStore` |
| R3 | Status JSON differs from C5 / the UI types | Exact key set; `server`, `account`, `connected_at`, `checked_at` are `null` when absent; `access` defaults to `unknown`; `check.state` defaults to `ok` | `TestGiteaStatusDocumentShape` |
| R4 | Unknown fields, wrong types, missing `base_url`, non-object body | `400 invalid_request`, manager not called | `TestGiteaStrictRequests` |
| R5 | Each manager error code | Fixed status (400 / 409 / 502 / 503 / 500) and a fixed message of ours; `issuer_mismatch` carries `details.issuer` | `TestGiteaProbeErrors`, `TestGiteaErrorStatusTable` |
| R6 | Unknown route or method under the prefix | `404 not_found` | `TestGiteaStrictRequests` |
| R7 | The settled `base_url`, `redirect_uri` and `register_url` missing from the probe answer | Present and consistent | `TestGiteaJourney` |

## Callback and secrets

| # | Failure | Required behaviour | Test |
| --- | --- | --- | --- |
| C1 | Upstream `error_description` (or any upstream text) reflected into the redirect | Only a C1 code ever appears in `Location`; fallback `exchange_failed` | `TestGiteaCallbackOutcomes` |
| C2 | Authorization code or state in a request log line or metric label | Logs and metric labels carry the route template only | `TestGiteaJourney` (log and `/metrics` scan) |
| C3 | Code or state leaks to the next page through `Referer` | `Referrer-Policy: no-referrer` on the redirect; `Location` carries neither | `TestGiteaCallbackOutcomes` |
| C4 | Access token, refresh token, code, state or upstream text in any response body, `Location` or log line | Never; sentinel scan across the whole journey and every error path | `TestGiteaJourney`, `TestGiteaCallbackOutcomes` |
| C5 | Callback denied / scope conflict / replayed state | `303` with `denied` / `scope_conflict` / `state_invalid` | `TestGiteaCallbackOutcomes` |
| C6 | New route labels unregistered (metrics collapse to `other`) or registered by editing `observability.go` | Registered from `gitea_metrics.go`'s `init` | `TestGiteaMetricRoutes` |
