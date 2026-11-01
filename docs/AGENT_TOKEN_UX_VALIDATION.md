# Agent token UX implementation and validation

Acceptance companion to [the UX plan](AGENT_TOKEN_UX_PLAN.md), 2026-11-01.
Status: implemented and verified (2026-11-01). See the
[implementation record](AGENT_TOKEN_UX_IMPLEMENTATION.md) for evidence.

## Implementation guardrails

The original stale checkout contained oversized maintained files: `App.svelte` 473,017 bytes,
`app.css` 161,888, `lib/api.ts` 49,911 and `lib/types.ts` 37,082. These measurements
describe that checkout. The release port uses the current modular beta source,
preserving its existing singleton state and live membership controls. Before touching any of
them, split by responsibility until every edited maintained file, including the
original entry point, is at most 20,000 bytes. Begin extraction near 16,000.
Moving just the token markup does not make the oversized entry point compliant.

Keep the root as application composition, with separate settings, shell,
board, task-detail and feature modules as required; split shared styles by
their owning UI and clients/types by domain. Preserve exports, selector
behavior, routes, documentation links and existing tests. Measure with
`check-file-sizes` before editing, during implementation and before handoff.
Do not bypass the hook or relabel authored code as generated. Preserve all
unrelated working-tree edits.

Initial UI slices reuse existing token storage and expiry fields. Never change
agent IDs, token grants, claims, or historical attribution to tidy the list.
If explicit source provenance needs additive storage, require populated-data
migration evidence, a verified pre-upgrade backup and compatibility with
retained rollback binaries. No reset, automatic restore, or bulk revocation.

## Verification contract

Write failure scenarios and the meaningful failing end-to-end coverage before
implementation. Prefer the real disposable Helm server through
`test/e2e/run.sh`; use isolation only with its failure modes recorded first.
Never add unit tests after writing the implementation they cover.

Retain explicit passing desktop and phone screenshots plus sanitized captured
responses or traces. Scrub bearer values, copy buffers and one-time reveal
content; a synthetic token is still omitted from published artifacts. Retain
the harness checksum manifest. Cover keyboard focus, screen-reader names,
light/dark contrast, 320px layouts, stale responses and authorization failures.
Run frontend checks and the focused real flow after the change; broaden only
for extraction or newly discovered cross-feature risks. Planning itself does
not require a build or a browser test run.

Required real-flow scenarios:

- Create Task worker for two selected projects with a finite expiry; assert
  exact request grants and successful allowed work, denied unrelated projects
  and project modification, and matching inventory metadata after reload.
- Cover all eight Custom scopes without automatic grant expansion; selected
  project mode with no selection is invalid. The server rejects forged
  outside-ceiling requests and retains actor/token intersection for old grants.
- A double click issues one POST. A lost response leads to metadata recovery,
  not replay. Successful issuance plus refresh failure still reveals the token.
- Escape, manual copy, denied clipboard, deliberate discard, focus restoration,
  navigation, logout, session switch and PWA activation respect secret lifetime.
  After close/reload, plaintext is absent from DOM and browser stores/caches.
- Expired credentials fail real authentication and show Expired at the exact
  boundary. Missing/invalid dates, duplicate names, no expiry, Never used and
  disabled actors have honest distinct presentation.
- Intake sources move to their section without changing actor IDs/history.
  New direct issuance is denied. Legacy source credentials remain discoverable
  until an administrator explicitly revokes them; empty automation agents
  remain eligible for creation.
- Revocation makes the old bearer fail. Manual replacement keeps both working
  until explicit retirement, then only the replacement works. Permission or
  ceiling changes during the flow never broaden the grant.
- Verify 320px and desktop layouts in both themes, keyboard-only completion,
  named actions, screen-reader announcements, visible focus and no horizontal
  overflow. Never capture the reveal secret or request authorization headers.
