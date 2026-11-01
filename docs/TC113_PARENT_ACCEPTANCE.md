# Membership parent integration evidence

Status: backend integration reviewed; UI/browser, retained-image and live
upgrade gates remain open. This does not certify deployment or safe downgrade.
See [the port contract](TC113_MEMBERSHIP_PORT.md).

## Review corrections

Independent authorization and data/session reviews identified four concrete
problems. Parent regressions reproduced each before the fixes:

| Problem | Correction and proof |
| --- | --- |
| Cached notification, global ticket, view and watch mutations disclosed saved responses after grants were revoked. | Request hashes include canonical effective authority. A changed-authority retry conflicts instead of disclosing data or executing a second mutation. Real HTTP cases cover all four routes and unchanged-authority retries. |
| A denied global ticket POST could create the hidden queue and retarget integrations. | Check write scope first; only workspace administrators initialize a missing queue. Check queue access before dispatch. Denied member and read-only bearer requests preserve project count and queue absence. |
| Hard-deleted labels could not reach their successful deletion receipt. | Missing-label DELETE reaches the handler's scope/replay checks. An unchanged-authority retry acknowledges its empty receipt; an unrecorded missing deletion remains missing. |
| Capacity eviction removed active login lockouts. | Reclaim expired buckets only, rejecting new buckets at active capacity. Existing lockouts survive unrelated account traffic; expired capacity becomes usable. |

`membership_replay_test.go` uses an actual listener, cookie authentication and
Origin checks, capturing synthetic responses only. Limiter isolation regressions
preceded its fix. The old release-contract spec exceeded 20,000 bytes; complete
replay/filter responsibilities moved to `releases_replay_test.go` and
`releases_filter_test.go` before its fixture was updated. No scenarios were removed.

## Retry compatibility

Keys retain their actor/credential namespace. Body, portable query options and
canonical effective project/scope authority identify a request. Changed authority
returns `409 idempotency_key_reused` unless an earlier authorization check denies
it. Do not blindly submit with a new key: the original operation may have committed.
Refresh permitted state and resolve the original result first.

Pre-binding receipts remain stored but cannot prove the authority snapshot and
may conflict under the new hash. No record is deleted or silently re-executed.
This is distinct from preserved SQL shape and unsafe old-binary authorization.

## Validation and open gates

Sanitized evidence is under `.artifacts/tc113-membership-backend/`:
`parent-review-red.log`, `parent-review-green.log`, their response directories,
`parent-integration.log` and `parent-http-integration.log`. Database/store/auth
packages passed. The initial API run exposed a body-only release receipt fixture;
after updating that fixture, the complete API package passed. Normal embedded
OpenAPI generation/check also passed.

Opus membership/session UI and browser journeys remain. Refresh the live backup
and exercise exact retained images on an isolated upgraded copy before activation.
Old-image authorization parity remains unaccepted; preserve the enforcing
rollback gate. Existing live services and data are unchanged.
