# Ordinary-language source retrieval failure inventory

Recorded before the TC-311 retrieval implementation changes.

- The discovery schema must enforce the same literal-query format as the
  provider boundary. An invalid generated query is a model-output failure,
  not a GitHub outage; its closed diagnostic must survive persistence without
  copying the generated term or any provider error into public history.

- Refinement can select a caller and request a helper in the same pinned file.
  Re-emitting only the selected caller leaves that explicit question unresolved.
  Preserve both bodies within existing windows/excerpt limits and cached reads.
- Receiver-qualified declaration identity is distinct from a provider's literal
  search query. Search for its lexical name, retain its receiver identity, and
  resolve the pending question only from the captured exact body. A signature
  or call site alone must not establish the requested helper implementation.

Search-first/declaration-selector inventory, recorded before implementation:

- An ambiguous, unanchored criterion can bypass optional search and spend both
  planning rounds on guessed filenames. Require a search-only initial response
  when multiple production owners exist and there is no explicit/linked owner.
  Reject bypasses before reads; do not refill budgets or silently fall back.
- A correct file still permits metrics/constructor distractors and invented line
  ranges. Offer pinned syntax-tree declaration selectors in refinement and resolve
  their bodies on the server. Distinguish receiver-qualified declarations; reject
  nonexistent/ambiguous selectors and retain truncation gaps within current bounds.
- Several required declarations can share one file. A path-keyed plan must not
  overwrite the first declaration with the last; retain each within the existing
  three-window ceiling and name truncation rather than silently dropping bodies.
- Empty final refinement currently conflates an unresolved evidence question with
  no further source needed. Require an explicit source-complete signal on the new
  staged path, retain unresolved questions as named gaps, and never clear missing
  search owners, required paths, truncation or budget failures with that signal.

- Actual concept-search regression: the first three field/metrics mentions can
  hide a later exact function declaration. A planner can then choose unrelated
  line numbers despite the correct file. Prefer exact named function bodies from
  the pinned Go syntax tree, including late conditions beyond a 24-line snippet;
  preserve whole-line, window, byte and excerpt bounds and name truncation gaps.

- An initial ownership prefix can contain constructors and unrelated owned
  helpers. Following them before semantic body refinement spends the unchanged
  card discovery/read budgets without establishing the quoted behavior.
- Standard-library methods on a value from `database/sql.Open`, an explicitly
  typed `*sql.DB` parameter, or a typed field are external calls. They must not
  become required repository helpers. Import aliases, lexical shadowing, and
  owned methods with identical names must remain distinguishable. Unknown
  receiver provenance and malformed/non-Go source must stay conservative.
- A planned 101-line behavior window can have its decisive owned call in the
  middle. Endpoint-only excerpts and reference scanning omit that call. Capture
  whole UTF-8 lines within existing byte ceilings; missing/inaccessible owned
  bodies remain named gaps and prevent unsupported completion.
- Hundreds of tests/documents can force an unnecessary directory route and lock
  refinement into a guessed constructor owner. Offer the global production
  index when it fits the existing 512-entry/48-KiB ceiling. Preserve explicit
  paths and bounded routing for genuinely large production trees.
- A refined candidate can be canceled, revoked, drift to another selected source,
  or invent a path. Existing fences must run before/after inference and reads;
  pinned provider identity, cache deduplication, per-card fairness, all budgets,
  v1 compatibility, and strict assessment publication remain unchanged.

The provider-HTTP/store/worker scenarios and exact-source provenance cases are
written and run red first. Each worker scenario saves a sanitized capture and
read/query receipt. Reuse existing cancellation/revocation/source-boundary
scenarios rather than adding a redundant broad suite. The final isolated actual
probe keeps the original goal and main SHA, retains audit history, and removes
only its newly granted access, copied credentials and temporary container.

Additional criterion-linked retrieval inventory, recorded before this fix:

- Filename-only guesses miss runtime checks whose owner is named for a broader
  responsibility. The bounded planner needs literal concept search, not an
  ever-growing filename heuristic. Search hits remain default-branch hints and
  must be checked against the captured SHA tree before any provider read.
- A follow-up that sees only a file prefix cannot reject an irrelevant offline
  helper selected beyond that prefix. Supply its actual selected source windows
  with line numbers so the next request can name the remaining evidence question.
- Every call in a semantic behavior window is not a required repository helper.
  Semantic follow-ups must name the criterion question and a literal search term;
  ordinary `Close`/`Stat` calls must not drain the unchanged four-request budget.
- Reject malformed searches, empty questions, and injected query operators before
  calling the provider. Search, planning, cached reads and re-excerpting retain
  their existing shared budgets and source/cancellation/authorization fences.
- A concept search can return several pinned alternatives as well as paths absent
  from the captured tree. Absent hints are never reads; alternative results must
  not all become required helpers once one owner supplies the requested source.
  Literal searches also surface exact pinned owners beyond the bounded initial
  filename index without enlarging the index, discovery or read ceilings.

Search-first implementation and single actual comparison:

- Ambiguous unanchored v2 criteria now use a required search-only first stage;
  filename-only responses fail before reads. Single owners, explicit anchors,
  linked PR routing and v1 retain their existing route. Search and inference
  still share four discoveries/card, 48/run and two refinement rounds.
- Refinement selects receiver-qualified pinned Go declarations without guessed
  coordinates. The server retains at most three 101-line body windows, including
  several declarations in one file; truncation remains a required source gap.
  Non-Go windows and bounded directory routing retain their existing limits.
- Empty refinement explicitly distinguishes no further source needed from an
  unresolved question. Missing owned definitions, search owners and budget gaps
  remain required; source-complete is never a criterion-satisfied judgment.
- Provider-HTTP/store/worker red/green receipts cover bypass, correct-file/wrong-
  body distractors, multiple declarations, missing/ambiguous selectors, body
  truncation, empty unresolved/complete refinement and missing owners. Existing
  provenance, helper, cancellation, revocation and source-drift coverage remains.
  The parent focused HTTP/store acceptance command passes. All maintained files
  are below 20,000 bytes; no repository source is executed during retrieval.

Actual audit `a10b61f6cc360e71e11271f51e268cfd` kept the original criterion,
main `c0f119cf330811389dfd38fbcbd7269dbf6680d5`, account and configured
Codex CLI 0.153.0 / gpt-5.6-luna. Mandatory search and declaration selection ran,
but only `internal/db/db.go` was inspected. The output mechanically matched
`correction_recommended:completed`; **this is not a semantic acceptance pass**.
Its cited Preflight body (495–577) migrates a temporary copy before InspectSchema,
so the assessment's claim that pending migrations on the original database are
rejected is unsupported. The operator-facing service readiness implementation
and invocation were absent. No second inference was attempted.

Next design: require question-level operating-context and entrypoint/call-site
provenance for selected bodies, so an offline copy validator cannot substitute
for the quoted service behavior. Parent-owned assessment guidance should require
tracking object identity, original versus copied state, and mutations before a
check. Missing context stays unknown. The criterion asks operators to CHECK;
it does not require automatic deployment or traffic gating. Do not weaken proof
validation or tune the goal, model, baseline, budgets or source paths to pass.

Sanitized receipts, red/green logs, source/binary hashes and cleanup are under
`/tmp/helm-tc311-search-first-20261029T022042Z-handoff/`. A setup-only probe typo
was corrected before any job was queued; it caused no inference retry. All 22
candidate attempts are retained. The new grant was revoked, copied credentials,
token, environment and container removed, candidate integrity/FKs checked, and
live tables/images verified unchanged. The disabled-auth loopback comparison
does not establish beta human authentication or satisfy remaining release gates.
