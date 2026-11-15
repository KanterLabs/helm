# Provider isolation failure inventory

Recorded before implementation and isolation tests for TC-297.

- Credential: missing, symlink, non-regular, oversized, world/group-readable,
  unreadable or invalid token file must disable access without echoing secrets.
- Scope/SSRF: malformed repository, arbitrary host, userinfo, encoded traversal,
  redirect, arbitrary pagination URL or unsafe file path must never receive a token.
- Upstream: revoked token/organization permission/404 must hide cached data;
  429 and rate-limit 403 must back off; timeouts and oversized/malformed JSON
  must produce bounded sanitized failures, never raw response bodies.
- Cache: conditional 304 must preserve scoped success; transient failure may
  return stale PR success with an error; permission loss must purge it.
- PR consistency: head changes during fetch must reject mixed snapshots;
  both check runs and legacy statuses must match the head. Failure beats
  pending, missing checks stay unknown, skipped/cancelled stay explicit.
- Coverage: paginated checks/reviews are bounded and report partial coverage;
  unavailable review/check evidence cannot imply passing/approved mergeability.
- Source: commit resolution is pinned; truncated trees, symlinks, submodules,
  missing/binary/oversized files remain omissions. Ref/path input is never a URL.
- Diagnostics: missing credentials remain unconfigured; every configured
  repository's actual metadata/content/PR/check/status endpoints are checked.
  Errors, JSON and text artifacts must never contain the synthetic sentinel.
- Local board access: cached diagnostics and allowlist checks must do no
  network I/O; observed denial and credential rotation must invalidate access
  and generation before a private cached snapshot can be returned.

Isolation tests use only synthetic HTTP responses and a temporary protected
token file. No private repository or live credential is used.

## Branch comparison (TC-384)

Recorded before `branches.go` and its isolation tests.

- Listing: more than three pages of branches must report partial coverage and
  never follow an upstream `Link` URL. A branch with an unprintable or oversized
  name, a malformed tip SHA or a duplicate name is omitted and marks the report
  partial. A branch name is display data only and is never placed in a URL.
- Trunks: without `main` the repository default branch is the base; a default
  branch absent from the listing fails as unavailable rather than inventing a
  base. Without `beta` there is no staging trunk and no request is made for it.
- Comparison: only immutable 40-hex tip SHAs from the same listing are
  compared, always on a page that carries no file patches. A reply whose base
  SHA differs from the request, or whose counts are missing, negative or
  absurd, leaves that branch uncompared; it must never read as merged.
- Missing commit: a 404 for one comparison (tip rewritten after the listing)
  leaves only that branch uncompared. It must not deny the repository or purge
  pull request data. A 401 or 403 still denies and fails the whole report.
- Limits: a rate limit or transient failure mid-scan leaves the remaining
  branches uncompared and partial. Each report starts a bounded number of new
  upstream comparisons with bounded concurrency and a bounded total time, and
  a later report resumes from finished comparisons instead of repeating them.
- Cache: finished comparisons are keyed by repository and SHA pair and are
  purged with every other private cache on credential rotation, removal or
  observed permission loss. A report that spans such a change is discarded.
  Comparison traffic must not evict the conditional-request cache.
- Staging: "already in staging" is claimed only from a successful comparison
  with the staging tip. A failed or skipped comparison is unknown, not false.
- Scope and secrets: a repository outside the allowlist receives no token; a
  missing credential fails without network I/O; errors and JSON never contain
  the synthetic sentinel.

## Credential source and dynamic allowlist (device-flow WP1)

Recorded before `credential.go`, `SetRepositories` and their isolation tests.

- Generation: a refreshed token of the same grant (same identity, new token)
  must keep the generation and the caches, or every 8-hour refresh would mark
  verified pull requests unverified. A changed identity, or a credential that
  appears or disappears, must bump it and purge every private cache.
- Freshness: only a request to GitHub may ask the source to renew. Local reads
  (`Generation`, `AccessAllowed`, `CachedDiagnostics`, scope checks) must use
  `Current` and do no network I/O.
- Source output: an empty, whitespace-padded, multi-line, control-bearing or
  oversized token, or an empty identity, must never reach an Authorization
  header; it reads as unavailable. A source error that is not a provider
  error, or carries a code other than `not_configured`, `unavailable` or
  `reconnect_required`, must surface as `unavailable` without its text.
  `reconnect_required` must reach diagnostics so the panel can say what to do.
- Rejection: a 401 must call `Rejected` exactly once with the token that was
  sent, never for 403 or 404, never twice for one rejected token, and never
  while the provider holds its lock (the source may call back into it).
- Re-entrancy: a source that calls back into the client from `Current`,
  `Fresh` or `Rejected` (for example `SetRepositories`) must not deadlock.
- Allowlist input: invalid, duplicate (including by case) or more than 50
  repositories are dropped; the result is normalized and sorted and does not
  depend on input order. Setting the same set again must not bump the
  generation.
- Allowlist swap: removing a repository must deny it locally at once, purge
  caches and bump the generation. A fetch that passed its scope check before
  the swap must not send the token for, or cache data of, a removed
  repository; a fetch already in flight must discard its result.
- Data race: every read of the allowlist (scope checks, diagnostics, the
  configured check) takes the lock, so concurrent swaps are race-free.
- Leak check across refresh: a file whose content contains the token used to
  fetch it must be refused even if the source refreshed to another token
  while the read was in flight.
- Operator mode: with no source configured the token file behaves exactly as
  before, and an explicit source takes precedence over `TokenFile`.

## Found by the independent review

- A 401 for a token from a renewing source took the deny path: the
  generation moved, every cache was cleared and verified pull requests read
  as unverified. It happened on a routine refresh whenever a request was in
  flight with the token another caller had just rotated away. A refused
  token from such a source is the source's to renew: the provider reports
  it, retries once with the current token, and denies nothing. 403 and 404
  still mean lost permission. The token file, which cannot renew, keeps its
  deny path. `TestRotatedTokenIsRetriedNotDenied`,
  `TestRefusedTwiceIsUnavailableNotDenied`, `TestSourceRejectedOncePer401`.
