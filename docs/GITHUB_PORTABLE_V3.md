# Portable archive v3: GitHub records and retained audits

Failure inventory recorded before implementation and isolation tests:

- Adding GitHub records silently to v2 breaks older strict decoders. Default
  store export remains v2; callers explicitly request v3. Reject a GitHub
  envelope on a v1/v2 import. Continue importing unextended v1/v2 archives.
- Exporting a broader project set can leak private relationships/evidence.
  Bound every export query to the already-authorized selected project IDs;
  omit findings and captured tasks whose tasks are outside the live archive.
- PATs, disposable PR snapshots and executable jobs must never migrate.
  Export explicit durable columns only. Do not export settings, provider/account
  generations, request keys, cache metadata, captures, leases or previews.
- A dangling, duplicate or cross-project relationship can partially import or
  attach evidence to another project. Validate the entire v3 graph before writes;
  import in the same transaction as the legacy records, remapping all FKs.
- Conflicting IDs and artifact natural identities can collide or duplicate on
  retry. Use deterministic remaps and compare mapped retained records before reuse.
- Imported recommendations can accidentally look current when task versions
  match. Mark every imported audit in audit_imports; no job or apply preview is
  imported. Reads must mark these findings changed and mutation/apply paths reject
  imported runs. Preserve the original observation and review decision as history.
- Retained evidence can contain unsafe URLs, oversized text, or malformed IDs.
  Validate bounded typed manifests and safe citations, with no executable payload
  or arbitrary fetch. Import never contacts GitHub or invokes inference.
- References inside retained evidence can retain source task/audit identifiers.
  Remap manifest audit/task/column IDs and recognized task/column/audit citation
  namespaces. Manifest item IDs are local to each manifest and remain stable.

The focused tests use populated synthetic SQLite databases, explicit v2/v3
exports, malformed graph imports, collision/retry imports and captured JSON.
Set HELM_PORTABLE_CAPTURE to retain the synthetic archive outside the test temp
folder. Never use a production archive or credentials as a test artifact.

## Contract

`ExportPortable(ctx, projectIDs)` retains version 2 behavior.
`ExportPortableVersion(ctx, projectIDs, version)` accepts 2 or 3; unsupported
versions fail before opening a transaction. Version 3 adds an optional `github`
envelope with `artifacts`, `task_artifacts`, `audit_runs`, `audit_findings` and
`audit_evidence` arrays. Version 3 without the envelope is valid and empty.

Only durable PR identity and task relationships migrate. Imported PRs begin
unverified with no cache. Audit run/findings and bounded evidence remain history;
operational jobs, leases, previews and credentials are excluded. Imported audits
are explicitly marked as imported and cannot launch, append, finalize or apply.
Migration 039 adds provenance and retained manifest tables separately from jobs.

Unfinished source runs are retained as terminal `partial` history, with the
source update time as the finalization time and an import warning. Captured task
versions, finding decisions and evidence are preserved; import provenance makes
every recommendation stale regardless of coincidentally matching task versions.
`audit_imports.archive_digest` fingerprints the complete source audit subtree so
a changed finding/manifest cannot silently merge into an earlier imported run.
Artifact imports invalidate affected project task collection revisions while
leaving task versions unchanged, as with other portable relationship inserts.

Before adding database guards, the isolation test also attempts valid direct SQL
finding edits and run transitions on an imported audit. Migration 039 must reject
these writes even when a retained binary cannot consult import provenance.

Evidence association integration failure inventory, before implementation:
retained PR/check `items[].task_ids` can refer to colliding, deleted, unknown or
cross-project source tasks. Imports must remap valid associations without
mutating the caller, reject unknown/cross-project/duplicate references before
writes, and preserve old manifests that omit this optional field. Exports must
remove unavailable associations and explicitly mark coverage partial. Optional
captured column names and semantic states remain historical values unchanged.
