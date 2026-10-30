# Membership UI parent acceptance

The UI implementation is accepted for source review. TC-113 remains incomplete
and membership has not been deployed. Live activation also requires
[the recovery gates](TC113_ROLLBACK_GATE.md). The original
[Opus handoff](TC113_UI_HANDOFF.md) records the worker's earlier source and evidence.

## Corrections from parent review

| Reproduced issue | Correction |
| --- | --- |
| Successful self-demotion left old project names and cached work visible. A failed status refresh also retained administrator controls. | Apply the accepted mutation's actor immediately; clear workspace, offline, admin and revealed-token state; fence old reads and reload permitted projects. Shared cleanup lives in `resetSessionWorkspace.ts` and retains the server cookie during access reconciliation. |
| A delayed revocation probe could sign out a newer login. An unavailable probe left the current revoked cookie's workspace visible. | Check generation and component lifetime after every awaited probe. An own-session list's explicit 401 clears local authentication, including when the status probe failed. |
| Initial own-session failure had no retry; admission recheck failures were hidden. | Offer session reload after failure and show the admission error on the waiting screen. |
| A required local membership fixture could pass with every journey skipped. | The coordinator owns a required-phase marker; the spec asserts local authentication for that phase. Main-suite discovery retains its intentional skips. Single-spec focused options remain opaque and reach local validation. |
| Browser hooks ran before final reports and SQLite/log copies existed. | Store only one-way fingerprints in private fixture state. Scan after every writer finishes, remove contaminated generated evidence before upload, and fail. Malformed fingerprints cannot certify unchecked artifacts. Password-field assertions report booleans rather than echoing values. |

Agent results are fenced against access/session changes. Ordinary members skip
the optional administrator beta probe. Denied member reads clear retained
directory data; a failed refresh keeps uncertain writes' original keys.

## Evidence and scope

Under `.artifacts/tc113-membership-ui/`:

- `parent-red`: four intended UI failures plus two test-selector failures;
  the eight original cases passed. The two tests were corrected before their
  product fixes and rerun in `parent-session-red`, reproducing both session
  problems through real cookies and explicitly delayed/unavailable transport.
- `parent-harness-red.log` / `parent-harness-green.log`: the two phase
  regressions ran red before their implementation and then passed.
- `parent-evidence-red.log` / `parent-evidence-green.log`: four isolated
  final-evidence cases ran red before the gate existed, then passed. They cover
  final report/database contamination, lookalike bytes, malformed fingerprints
  and ordinary non-membership runs.
- `parent-green`: 14 membership cases, seven creation/notification cases and
  one local onboarding case passed with race detection, no retries or flakes.
  Fourteen main-suite membership skips are intentional; all 14 required local
  cases ran. GitHub/deadline suites were outside this focused selection.
- The 71 runner proofs passed. The final credential gate scanned 79 files,
  removed zero, and passed; the generated manifest verified every retained
  file. Screenshots and selected synthetic responses remain with the reports.
- `parent-check.log`: Svelte check had zero errors/warnings and docs passed.
  `parent-tested-source.json` records tested code bytes across documentation
  edits and branch integration; it is not a replacement for candidate CI.

Real local authentication covers member grants, disable/reactivation,
password/reset/revocation, self-demotion, cookie invalidation and retries.
Pending identities and Cloudflare/Tailnet modes still use labelled synthetic
responses. This does not verify those live ingress paths or retained images.

The membership fixture's four ports remain in onboarding's unused upper half;
worker allocation and cleanup proofs pass. Historical artifacts and live
databases were not changed by this acceptance.
