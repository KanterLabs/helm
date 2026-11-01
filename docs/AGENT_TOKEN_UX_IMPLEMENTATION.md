# Agent token UX implementation

Implemented on 2026-11-01 using the [UX plan](AGENT_TOKEN_UX_PLAN.md) and
[acceptance contract](AGENT_TOKEN_UX_VALIDATION.md).

## Delivered behavior

Settings presents searchable agent groups with token status, exact access
presets, project coverage, creation identity, approximate usage and expiry.
Expired, unknown expiry, never-used and disabled-agent conditions remain
separate. Passive sources have their own section; existing credentials stay
reviewable and revocable without granting new source credentials.

Generation requires selected projects by default, exact permission presets or
all eight Custom scopes, a 90-day default expiry and a review summary. No read
permission is added silently. The server rejects new source/disabled-actor
tokens and explicit projects outside the agent ceiling. Empty project lists
retain the existing actor-ceiling inheritance contract.

The one-time dialog supports clipboard and manual copy, setup guidance,
deliberate discard, navigation/unload protection and PWA blocking. Secrets stay
in memory and disappear on completion or session invalidation. A lost response
uses metadata recovery and explicit candidate retirement; issuance is never
replayed. Refresh failure preserves a successful reveal. Revocation is confirmed;
replacement leaves the original valid until explicit retirement.

## Current beta architecture

The initial implementation used an October 17 checkout. The release port is
based on the actual live beta revision `f045958d6c31e9ca900c4704d166b5e9edc6ed71`,
preserving its modular singleton architecture, membership, sessions and native
runtime admission. It does not replace those features with the older checkout.

- `web/src/App.svelte` composes existing views; `app/tokens.svelte.ts` owns token
  drafts, one-time secrets and lifecycle actions.
- `lib/components/tokens/`, `lib/tokenMetadata.ts` and `styles/tokens.css` own
  presentation, metadata and styling. Existing API/type module exports remain.
- `app/routingLocations.ts` owns location helpers extracted from the router.
  Session, keyboard, unload and PWA guards use the same token state.
- `internal/store/actor_metadata.go` derives intake-source metadata from its
  immutable namespace. The optional Actor field preserves embedded Member JSON
  and `last_seen_at`; no custom JSON marshaler is installed.
- `internal/httpapi/agents.go` adds issuance restrictions while retaining current
  admin/membership/session checks. The current OpenAPI source fragments and
  matching embedded JSON describe the optional metadata.

No schema migration, data reset, grant rewrite or automatic revocation is needed.

## Validation and evidence

Meaningful token browser scenarios and HTTP/SQLite regressions were authored
before the implementation they cover. Initial runs reproduced missing UI controls,
source/disabled issuance and outside-ceiling grants.

The original implementation passed 120 full browser cases plus local onboarding,
with three expected disabled-auth skips, and 230 frontend isolation tests. Those
historical results apply to the original checkout only. Current release validation
must run against the live-source port and is recorded with deployment evidence.

The live-source port passes Svelte checks with zero errors/warnings and 73 focused
existing frontend tests. Twenty focused backend test groups pass, including the
new token restrictions, membership/session behavior, one-time plaintext storage,
OpenAPI assembly and response-writer semantics. The latter was already fixed in
current beta and is not excluded.

Sanitized local artifacts are ignored. Token screenshots mask secrets; automatic
secret-bearing traces/video/screenshots are disabled. Browser bundles retain
checksum manifests. Disposable test databases and credentials never replace
beta data. All maintained changed files must pass the 20,000-byte size gate.

See [the native upgrade protocol](TC335_OPERATOR_TRANSITIONS.md) for deployment;
implementation results alone do not authorize a runtime image or prove a rollout.
