# GitHub beta real-backend browser failures

Scenarios recorded before the harness implementation. The browser never routes or
fulfills application API traffic: SQLite, authentication, routes, provider HTTP,
job claims, evidence validation, finding review and apply are the real modules.
Only GitHub upstream HTTP and the personal AI adapter use synthetic data.

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

## Branches view (TC-384)

Scenarios recorded before the endpoint, view and browser test.

- A project without a selected repository must explain how to select one. It
  must not show an empty graph or contact GitHub.
- A repository outside the project selection must be rejected before any
  upstream request, and a project-scoped token must not read another project.
- The page must state how the staging branch compares with the base branch and
  draw every unmerged, recently updated branch with its ahead and behind
  counts. Stale and merged branches stay hidden until their toggle is pressed,
  and each toggle shows its count while hidden.
- A branch whose comparison failed must be reported as not compared. It must
  not be drawn as merged or as zero commits ahead.
- A branch already contained in staging must be marked in words, not only by
  colour.
- Revoked upstream access must remove branch names already on the page at the
  next refresh and show the permission message. Access returns only after a
  real provider request succeeds.
- Switching project or leaving the view during a slow load must not paint the
  previous project's branches.
- Desktop light, desktop dark and phone layouts must not overflow sideways.
  The run keeps screenshots and the captured API response, and none of them
  contains the sentinel credential.
