# Agent token server failure scenarios

Status: implementation acceptance, 2026-11-01.

These scenarios are recorded before the server changes and their HTTP/SQLite
integration coverage. The browser flows are specified separately in
[token UX validation](AGENT_TOKEN_UX_VALIDATION.md).

- A passive intake actor appears as an ordinary automation identity and can
  acquire new credentials despite its attribution-only role. Classification
  must use the server-owned immutable intake namespace; changing a name or
  description must not change it. Ordinary actor creation chooses its own
  random ID and cannot forge that namespace through the public API.
- An automation agent with a source-like name or copied source description is
  incorrectly denied credentials. No editable label participates in the rule.
- Existing intake tokens disappear or are automatically revoked when intake
  actors become distinguishable. Inventory must retain their metadata and
  they remain valid until an administrator explicitly revokes them.
- A caller bypasses the project picker and issues outside-ceiling grants,
  receiving a token that authenticates with no effective access. New issuance
  rejects any requested project outside the actor ceiling, including mixed
  allowed/denied lists. Resolve accepted IDs, keys and slugs before comparison.
- Empty project IDs are accidentally reinterpreted as no access or all
  workspace access. Preserve inheritance of the actor ceiling. An unrestricted
  actor may still receive explicit grants or an unrestricted token.
- Validation changes reinterpret existing grant records. Existing token lookup
  continues to intersect stored token restrictions with the actor ceiling;
  inventory retains the original restriction even if effective access differs.
- A disabled identity receives a new unusable credential. Reject issuance;
  preserve the actor, historical tokens and attribution.
- Rejected requests insert orphan token rows or token-created events. Confirm
  inventory counts are unchanged; never echo plaintext in failures or artifacts.

The tests use the complete HTTP router, authentication, real store and a
disposable SQLite database. Test output contains only synthetic names and
status/count assertions, never bearer values or full issuance response bodies.
No schema change is needed; existing storage and rollback readers are retained.

## Modular API contract failures

The contract also requires a responsibility-based extraction before editing its
oversized authored source. Preserve every endpoint, schema, description and
local reference in the assembled document. Each source fragment and embedded
JSON fragment must stay within the maintained-file limit. Reject missing,
cyclic or directory-escaping includes, duplicate YAML keys and stale generated
fragments. Update documentation tooling and the container copy path to consume
the same source assembler. Compare the assembled contract with the original
before adding the intake metadata and issuance validation documentation.
