# GitHub beta real-backend browser failures

Scenarios recorded before the harness implementation. The browser never routes or
fulfills application API traffic: SQLite, authentication, routes, provider HTTP,
job claims, evidence validation, finding review and apply are the real modules.
Only GitHub upstream HTTP and the personal AI adapter use synthetic data.

- New server runs use v2 assessments. A legacy-only synthetic model response
  must not be mistaken for a worker or browser timeout. Retain v1 compatibility
  for older captures, but return exact captured condition/task identities for v2.
- Ready recommendations require affirmative unstarted/readiness task context;
  the placeholder `ready.go` constant and merged PR do not prove completion.
  Unknown completion conditions remain unknown and cite captured source only.
- The current column-review selection, preview and receipt must preserve the
  earlier no-mutation-on-review, explicit apply, stale-skip, exact-citation,
  revocation, cancellation/retry and delegated-attribution coverage. Terminal
  failed jobs should report safe diagnostics promptly instead of exhausting
  every browser timeout/retry.

- A selected repository is required; a PR URL must persist its task version,
  verify from upstream, survive server restart, and appear in the board summary.
- Creating or analyzing a merged PR must never complete its task. Approval and
  preview must not move a task; only explicit apply may move an unchanged,
  approved recommendation to Ready. Changed tasks must be skipped.
- An audit started from the browser must survive navigation and page reload;
  findings must cite actual captured IDs and exact immutable SHAs. A branch-only
  audit must work without fabricating a linked PR.
- Cancelling a delayed AI attempt must fence late completion. Explicit Retry
  must create a linked new run without overwriting the cancelled run.
- Revoked upstream access must hide private PR snapshots and prevent reading
  retained evidence or applying recommendations. Restoring access requires a
  real provider check, not a browser-only state change.
- No sentinel credential may appear in API responses, browser storage, prompt
  captures, logs, screenshots, traces, exports or child arguments/environment.
  The synthetic adapter receives evidence text and has no token file or process
  execution capability. Credential transport is an owner-only temporary file.
- Desktop light/dark and phone rendering must preserve reachable controls and
  avoid horizontal overflow. Successful runs retain screenshots, Playwright
  traces, and sanitized captured response evidence in the artifact directory.

The shell accepts a prebuilt harness, never builds or replaces embedded web
assets implicitly, preserves artifacts, and cleans up its temporary database,
token and running process. Its restart control reopens the same SQLite database.
Control routes exist only in this dedicated loopback-only test executable.
