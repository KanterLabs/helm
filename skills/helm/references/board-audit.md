# Read-only Board Audit

Run a Board Audit only when the user or an explicitly delegated task requests
an inspection. It is on-demand and never runs from lifecycle hooks:

```sh
python3 scripts/helm.py audit --project TC
```

The command follows every task page for the `backlog` and `active` semantic
states by default. Repeat `--state` (or `--semantic-state`) to inspect a
different set of semantic states; `--limit` controls the API page size and
`--evidence-limit` bounds recent comment evidence per task. Every request made
by `audit` is `GET`, including optional comments used for bounded evidence.

Each returned task context includes its key, immutable ID, version, current
column semantic state/name, title, bounded description plus parsed goal and
acceptance criteria, priority, claim lease, agent-work snapshot, liveness,
recent safe evidence, and a rubric verdict: `correct`, `needs_attention`, or
`move_proposed`. A suggested destination, numeric confidence, concise reason,
warnings, and evidence references accompany the verdict. Stale or missing
agent pulses are warnings only; they never prove that work is abandoned or
complete. The v1 audit considers semantic placement only and never changes a
task or reorders its numeric position.

Agents must analyze the returned `rubric` and task contexts without mutating
the board. During an audit do not claim, renew, release, move, complete,
block, comment, publish progress, emit activity, or call any write endpoint.
The agent owns the semantic judgment in each finding. If a durable record is
requested, save the audit JSON and submit it separately; submission persists
the agent's findings but does not move tasks:

```sh
python3 scripts/helm.py submit \
  --project TC \
  --input audit.json \
  --scope board \
  --operation-id "audit-session-1"
```

Submission creates a run, appends each finding with a deterministic
idempotency key, and finalizes the run. Submitted findings always begin
pending; approval is never imported from the input file. When the web UI has
already queued the run, process that exact run instead of creating a duplicate:

```sh
python3 scripts/helm.py submit \
  --project TC \
  --audit AUDIT_ID \
  --input audit.json \
  --operation-id "audit-session-1"
```

The CLI resolves `--project`, so bearer tokens need `projects:read` plus
`tasks:read`; submission and reconcile apply additionally need `tasks:write`.
To inspect
approved findings, reconciliation is preview-first and read-only by default:

```sh
python3 scripts/helm.py reconcile --audit AUDIT_ID
```

The preview re-fetches paginated findings, current columns, and current tasks;
it skips queued, running, or failed runs, changed snapshots, unapproved
findings, active claims, unavailable
destinations, and lifecycle destinations (`active`, `blocked`, or `completed`)
while reporting the required follow-up action. It never mutates a task unless
`--apply` is explicitly supplied. Applying an eligible backlog/ready move
uses the captured version and source column with `source: board_audit` and a
stable idempotency key, so rerunning a command is safe. Reconciliation never
reorders numeric positions; lifecycle actions remain explicit claim/resume,
block, or complete operations.

For an explicitly requested batch board change, read the bundled API reference
before using the bounded bulk task endpoint. Capture every task's current
version, preserve per-task claim and dependency rules, and supply one
idempotency key for the batch. Prefer `partial` when independent valid items
should proceed and `atomic` only when the user requires all-or-nothing
behavior. A `200` response is not blanket success: inspect every item for
`applied`, `skipped`, or `conflict`, then re-read conflicted tasks. Never turn a
bulk request into an unreviewed lifecycle transition.
