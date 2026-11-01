# Portable transfer preservation (TC-317)

Failure inventory recorded before implementation:

- JSON export/import silently omits open and completed checklist items, item
  identity/order/timestamps/attribution, parent edges and project require policy.
- Remapped parent or checklist IDs can bind to unrelated destination rows.
- Children can precede parents in archive order; insertion must remain atomic.
- Missing/self/cyclic/cross-project parents, excessive depth/fanout, duplicate
  checklist IDs, invalid completion metadata or excessive item counts must fail
  during read-only preview and before any writes.
- Retrying a remapped import must reuse rows; existing destination tasks and
  policy must never be overwritten or weakened.
- Import into an existing warn project cannot preserve a require policy; reject
  that mismatch rather than reporting success with a weaker safeguard.
- Explicit older formats cannot carry these fields; export and import must
  identify their limitations. Default and UI exports must select the complete
  format and retain the existing v3 PR/audit authorization boundary.
- Focus membership, bug details, dependencies, comments, agent work and v3
  PR/audit histories must keep their current behavior. No credentials, private
  configuration or schema changes belong in the transfer format.

Regression: `go test ./internal/httpapi -run TestPortableV4 -v` creates synthetic
source and destination databases and drives export, JSON transport, preview,
import, retry, re-export and completion over HTTP. Captured responses are saved
outside the repository and their directory is printed by the test. Existing
portable suites cover Focus, dependency, bug, agent and v3 history preservation.

Version and compatibility decisions:

- Default store, HTTP and UI archive selection is v4. `PortableVersion` remains
  the exported v2 constant for callers explicitly opting into the old format;
  `PortableVersionCurrent` identifies the complete current default.
- V4 adds project `checklist_completion_policy`, task `parent_task_id` and
  ordered task `checklist` records with IDs, text, positions, completion state,
  attribution and timestamps. It includes the v3 GitHub section and retains its
  private-source authorization checks. No database migration is introduced.
- Explicit v2/v3 export omits unsupported fields and returns archive warnings.
  Versions 1–3 import with independently generated omission warnings. A legacy
  archive claiming to contain v4-only fields is rejected rather than discarded.
- Existing target policy is retained for legacy archives. V4 policy mismatches
  reject the import. Ambiguous legacy retries onto richer tasks reject with a
  conflict; operators need a fresh v4 archive. New old-format imports default
  to warn because required policy cannot be recovered from missing data.
- V4 task matching includes checklist content. Checklist and parent IDs are
  resolved after task identity remapping; history item IDs follow the same map.
  Reused tasks with a different parent reject rather than changing destination
  rows. Preview checks archive hierarchy and combined destination fanout.
- Transfer is an additive live-record operation, not a full recovery backup.
  Deleted records, credentials, provider selections and executable jobs remain
  excluded. Existing actor fallback attribution remains explicit in remaps.

Evidence receipts are synthetic and contain no credentials or private content.
The HTTP regression uses **separate file-backed temporary databases**; the
ordinary shared-memory HTTP fixture is intentionally not used for two-installation
round trips. The test captures export, preview, import, retry and re-export JSON.
