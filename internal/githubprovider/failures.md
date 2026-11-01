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
