# Helm agent API reference

This is the compact contract reference shipped with the Helm agent skill. The
canonical, human-edited repository contract is
[`docs/API_CONTRACT.md`](../../../docs/API_CONTRACT.md); this file keeps the
agent workflow self-contained after the skill is installed.

## Authentication and safe output

The skill reads `HELM_URL` and `HELM_TOKEN` from the process environment or a
user-owned mode-`0600` JSON file. The Helm application token is sent as
`Authorization: Bearer ...`. Optional `HELM_CF_ACCESS_CLIENT_ID` and
`HELM_CF_ACCESS_CLIENT_SECRET` are Cloudflare Access edge credentials; they are
not a substitute for the Helm application token and are sent only as the two
`CF-Access-*` headers. Never put either credential in argv, task data, logs, or
temporary files.

`auth-check` performs `GET /api/v1/auth/me`, which is read-only. Success is
sanitized to actor identity fields. Failure is a bounded JSON error with the
HTTP status, machine code, message, and an actionable hint. The CLI emits
machine-readable errors on stderr for all commands.

Helm human authentication modes are `local`, `cloudflare`, `tailnet`, and
development-only `disabled`. Tailnet mode is deployment-owned: a private edge
resolves the actual Tailscale peer, requires the configured untagged owner, and
forwards `X-Helm-Tailnet-Assertion`, a short-lived HMAC assertion bound to the
exact origin, method, and request URI. Raw Tailscale and proxy identity headers
are not credentials, and agents must never forge or replay the private-edge
assertion. Tailnet and Cloudflare deployments do not use local password setup
or login; Tailnet status keeps `setup_required: false` and may remain
`configured: false` until the first verified owner request reconciles its human
actor. Cookie, Cloudflare, and Tailnet mutations require the exact configured
`Origin`; bearer mutations are exempt. Setup, login, and logout reject
`Idempotency-Key`. The bundled client still authenticates agents with the
scoped Helm bearer token; Tailnet network identity is not a substitute for it.

## Common request rules

- API paths are relative to `/api/v1`.
- Task and project references may be opaque IDs or stable keys/slugs; task key
  matching is case-insensitive.
- `If-Match` must be the exact strong ETag version, formatted as `"vN"`, for
  task mutations. Re-read after a `409` conflict and preserve other actors'
  work.
- New workflow mutations use one UUIDv4 idempotency key per logical operation.
  The generated `operation_id` is returned in the result; pass it again to
  replay the same operation safely. Notification and watch commands require a
  UUIDv4. Earlier compatibility commands use UUIDv4 by default but retain
  explicit non-UUID operation IDs in the deterministic `helm-<sha256>`
  namespace for older automation. Multi-step commands
  deterministically derive a distinct UUIDv4 key per HTTP mutation from the
  operation ID, method, path, and body because the server reserves keys per
  request target.
- A normal write is retried only when it carries an idempotency key. Heartbeat
  is the sole keyless write and is server-side idempotent.
- Successful task mutations return a full task only to humans or callers with
  `tasks:read`; write-only bearer tokens receive exactly `{ "id": "...",
  "version": N }` with the strong ETag. Project write-only responses reduce to
  `{ "id": "..." }`, and column write-only responses to `{ "id": "...",
  "project_id": "..." }`. Direct reads still require the corresponding read
  scope, replays retain the reduced response, and stale/claim errors must not
  leak read-protected fields. Do not assume a mutation response contains task,
  project, or column details.

## Operational diagnostics

- `GET /healthz` is liveness only.
- `GET /readyz` (and `/ready`) returns `200` with `ready: true` only when the
  bounded database, schema, migration, writer-lock, writable-capacity, and
  storage checks pass. Pending embedded migrations or inspection failures are
  degraded. Unknown newer additive migrations are compatibility warnings and
  may remain ready so retained rollback binaries can continue serving. Output
  is bounded and excludes filesystem paths and database error strings; the
  writable-capacity preflight cannot prove effective ACL, id-mapped mount, or
  service-privilege access.
- `GET /metrics` returns bounded Prometheus text. Unauthenticated access is
  permitted only for a direct loopback peer with no forwarding or proxy
  identity headers. Remote collection uses normal Helm authentication and must
  remain on an authenticated private path; task text, request bodies,
  credentials, project/task IDs, and user-provided labels are never metrics.

A failed readiness gate pauses promotion. It is not permission to recreate or
restore over the database; follow the verified-backup and populated-data
migration rules in the main skill.

## Lease commands

```sh
python3 scripts/helm.py renew --task TC-42 --lease-seconds 1800
python3 scripts/helm.py release --task TC-42
```

Both commands resolve the task first, then send the exact current `If-Match`
version and one UUIDv4 idempotency key to the corresponding lease endpoint.
Only the current claim owner may renew or release an active lease.

## Read commands

```sh
python3 scripts/helm.py auth-check
python3 scripts/helm.py projects --all
python3 scripts/helm.py tasks --project TC --all
python3 scripts/helm.py events --after 0 --project TC
python3 scripts/helm.py timeline --task TC-42 --kind agent_progress
python3 scripts/helm.py timeline --project TC --before CURSOR
python3 scripts/helm.py dependencies list --task TC-42
python3 scripts/helm.py issues --severity untriaged --resolution unresolved
```

Collection results use `{ "data": [...], "next_cursor": "..." }` (projects
and tasks additionally identify their project). Other collection routes retain
opaque offset cursors supplied back as `--cursor`; task board routes return
`tc1` keyset cursors carrying the ordering boundary, project, project revision,
task-collection revision, and fixed `read_at`. If either revision changes,
continuation returns typed `409 task_collection_changed` with
`details.restart=true`; discard the cursor and restart from the first page.
Task/project timelines use the opaque keyset cursor as `--before`; the event
feed uses a monotonic integer `--after` cursor. `--all` follows pages and
returns an empty terminal cursor. Timeline `--kind` accepts `agent_progress`,
`comment`, or `task_change`.

## Notifications and watches

```sh
python3 scripts/helm.py notifications list --unread --all
python3 scripts/helm.py notifications read --id ID_1 --id ID_2
python3 scripts/helm.py notifications preferences --mentions false
python3 scripts/helm.py watches add --project TC
python3 scripts/helm.py watches remove --watch WATCH_ID
```

Notification reads accept `notifications:read` (or `tasks:read`); notification
and watch mutations accept `notifications:write` (or the documented task or
project write fallbacks). The useful routes are:

- `GET /notifications?unread=true` for the cursor-paginated newest-first inbox;
- `POST /notifications/{notification}/read` or `PATCH /notifications/{notification}`
  with `{ "read": true|false }` for one owned item;
- `POST /notifications/{notification}` as the compatibility alias for marking
  one owned item read;
- `POST /notifications/read` with either `{ "all": true }` or up to 200 unique
  IDs to mark visible items read;
- `GET|PATCH /notification-preferences` for `assignments`, `mentions`,
  `blockers`, and `state_changes` (all default enabled); and
- `GET|POST /projects/{project}/watch`, `GET|POST /tasks/{task}/watch`, plus
  `GET /watches` and `GET|DELETE /watches/{watch}` for owned watches; generic
  `POST /watches` accepts exactly one `project_id` or `task_id`.

All notification read-state, preference, and watch writes are idempotent and
should carry one UUIDv4 key per logical request; none use `If-Match`.
Preference PATCH requires at least one recognized boolean and rejects nulls.
Creating an existing watch returns a conflict rather than a duplicate. A task
watch is project-scoped;
the task-watch read also returns an inherited project watch. Bearer visibility
is the intersection of the token project allow-list and the actor's durable
project ceiling. A scoped identity with no intersection sees no rows, and
project-less notifications are hidden from scoped identities.

Assignment changes, mentions, dependency block/unblock changes, and task state
transitions fan out to direct recipients and matching watches. Deduplication
prevents replayed events from creating duplicate inbox items. A write-only
bearer without `notifications:read` or `tasks:read` receives only `id` and
`read_at` from a single-item read-state mutation. Due-date and claim-expiry
reminders and email, push, or webhook delivery are not implemented.

## Guarded bulk task mutations

`POST /projects/{project}/tasks/bulk` requires `tasks:write` and accepts 1–100
mutations. Supply one request-level idempotency key to make the complete result
replay-safe. Every item belongs to the path project and supplies a task
reference, its positive current `version`, and exactly one operation: `move`,
`assign`, `priority`, `labels`, `due_at`, `complete`, or `block`. Move items
also require the destination column, expected source column, and non-empty
provenance. Assignments, labels, and due dates accept JSON null to clear them;
empty, malformed, or over-limit envelopes fail as a top-level `400`.

`partial` mode is the default and commits valid items independently, returning
`status: "partial"`. `atomic` mode returns `complete` or `failed` and rolls the
whole batch back if any item fails; rolled-back items are `skipped` with
`atomic_rollback`. In both modes the HTTP response can be `200` while individual
results are `applied`, `skipped`, or `conflict`; inspect every item. The server
rechecks versions, active claims, dependencies, lifecycle permission, project
ceilings, and destination rules in the same transaction as each write. Bearer
`complete` and `block` items also require `tasks:claim` and a claim owned by
that actor. Idempotent replay returns
the original bounded result and cannot apply an item twice. Applied task bodies
are full for read-scoped callers and `{ "id": "...", "version": N }` for
write-only bearers; committed items emit their normal task/comment events with
bulk provenance, while mutation rate and resource budgets apply once per body.

## Dependency commands

```sh
python3 scripts/helm.py dependencies add \
  --task TC-42 --prerequisite TC-41 --operation-id UUIDV4
python3 scripts/helm.py dependencies remove \
  --task TC-42 --prerequisite TC-41 --operation-id UUIDV4
```

The client reads the dependent task version before each mutation and sends
`POST /tasks/{task}/dependencies` with exactly
`{"prerequisite":"..."}`, or `DELETE
/tasks/{task}/dependencies/{prerequisite}`. The server enforces same-project
edges, project ceilings, direct-edge limits, cycle prevention, claims, exact
ETags, and replay-safe idempotency. Stable conflict codes include
`dependency_cycle`, `dependency_limit_exceeded`, `dependency_cross_project`,
`dependency_already_exists`, and `idempotency_key_reused`.

## Bug workflows

```sh
python3 scripts/helm.py bug-report --project TC --title "Crash" \
  --actual-behavior "The command exits" --expected-behavior "The command completes"
python3 scripts/helm.py bug-triage --task TC-43 --severity s2
python3 scripts/helm.py bug-resolve --task TC-43 --resolution fixed
python3 scripts/helm.py bug-duplicate --task TC-43 --duplicate-of TC-44
python3 scripts/helm.py bug-reopen --task TC-43 --reason "Regression is reproducible"
```

Bug creation posts `kind: "bug"` with nested `bug.actual_behavior` (required)
and optional structured fields. Issue discovery uses `GET /issues` and supports
the server filters, cursor, and project ceiling. Triage requires severity;
resolve accepts `fixed`, `duplicate`, `not_planned`, `cannot_reproduce`, or
`works_as_designed`; duplicate resolution additionally requires
`duplicate_of`; reopen requires a reason. All lifecycle mutations send the
current task `If-Match` and one UUIDv4 key.
