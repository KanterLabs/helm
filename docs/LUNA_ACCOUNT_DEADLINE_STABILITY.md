# Luna account deadline stability

This document defines the failure contract for Luna HTTP features when the
Codex app-server accepts startup but never answers `account/read`.

## Failure model

The real-process E2E fixture honors `HELM_E2E_CODEX_STALL_ACCOUNT=true` by
consuming `account/read` and leaving its JSON-RPC response pending. It still
answers `initialize`, so the stall is isolated to account initialization/read
and exercises the same boundary as an unresponsive Codex process. The fixture
does not emit prompts, model output, credentials, or private account data in
this mode.

Before the deadline fix, both feature handlers call `Codex.Account` with the
incoming request context before creating their feature timeout. A stalled
`account/read` therefore leaves the HTTP request pending until its caller
disconnects. The regression spec intentionally fails against that behavior
with a Playwright request timeout instead of receiving a structured Helm
response.

## HTTP contract

Each route starts its feature deadline before touching the Codex account and
uses the 20-second Codex account deadline as a child of that feature context.
The account deadline must end the account call while leaving the Helm process
ready to serve subsequent requests. The test client allows 30 seconds for the
HTTP request and requires the structured response within 25 seconds.

| Route | Method | Expected status | Expected error code |
| --- | --- | ---: | --- |
| `/api/v1/projects/{project}/task-draft` | `POST` | `503` | `luna_unavailable` |
| `/api/v1/project-intelligence/analyze` | `POST` | `503` | `luna_timed_out` |

Both responses use the standard Helm error envelope:

```json
{
  "error": {
    "code": "…",
    "message": "…",
    "details": {}
  }
}
```

The messages remain generic and the envelope must not expose prompts, model
output, credentials, account identifiers, actor identifiers, or upstream
Codex error details. A subsequent `GET /readyz` must return HTTP `200` with
`"status":"ok"` after each stalled request.

## Regression evidence

`web/e2e/luna-account-deadline-stability.spec.ts` is skipped unless the stall
fixture is explicitly enabled. It sends requests through the real Helm HTTP
server, asserts the route-specific error envelope and response bound, checks
readiness, and attaches each route response plus its readiness response to the
Playwright test artifact. The attached responses contain only the public error
and readiness envelopes.

Run the fault injection separately from the normal suite:

```sh
HELM_E2E_CODEX_STALL_ACCOUNT=true HELM_E2E_GOFLAGS=-race \
  ./test/e2e/run.sh e2e/luna-account-deadline-stability.spec.ts
```
