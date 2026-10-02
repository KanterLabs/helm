# Coolify email intake for Helm

Status: v1 implemented as a Coolify **webhook** intake, 2026-10-02. Tracking:
TC-255. The mailbox connector, Tickets workspace, intake settings card, mutes
and backfill below remain proposed and are deferred until real alert volume
justifies them.

## Implemented v1: webhook intake

Coolify v4 has a webhook notification channel that posts
`traefik_version_outdated` as JSON with a per-server `uuid`,
`current_version` and `latest_version`. That removes the mailbox, IMAP state,
MIME parsing and sender-authentication problems. The webhook carries no
signature, delivery ID or custom headers, so:

- Authentication is a 32-256 character URL-safe secret in the path:
  `POST /api/v1/intake/coolify/{secret}`. Disabled intake and a wrong secret
  both return the generic `404`. Request logs and metrics record only the
  `/api/v1/intake/coolify/:secret` template.
- Without a delivery ID there is no receipt-level dedup. Exactly-once applies
  to *work*: `alert_conditions` has a unique
  `(integration, condition_key)` constraint (key = server UUID + offered
  version), and correlation and task creation commit in one transaction. A
  retried delivery can over-count occurrences but never duplicates a task.
- A new condition creates a `kind=task` in the configured project's Backlog,
  assigned to the configured human, unclaimed, and produces one assignment
  notification. A repeat only increments the occurrence count and adds an
  `alert.repeated` Activity event. It never edits, versions, reopens or
  recreates the task (`repeated` when open, `retained` when completed or
  deleted). A new offered version creates linked follow-up work.
- `test` is `ignored` and other events are `unsupported`, so neither creates
  a task. Any invalid server in a multi-server notice rejects the whole
  delivery. Bodies over 64 KiB return `413`. A missing project, Backlog column
  or assignee returns `503`.
- Tasks expose read-only `alert_source` evidence, shown in an Alert source
  panel in the task drawer.

Configuration (all three are required together):

| Variable | Meaning |
| --- | --- |
| `HELM_COOLIFY_WEBHOOK_SECRET_FILE` or `HELM_COOLIFY_WEBHOOK_SECRET` | Path secret; the file must be owner-only. Set exactly one. |
| `HELM_COOLIFY_PROJECT` | Target project ID, key or slug, resolved per delivery. |
| `HELM_COOLIFY_ASSIGNEE` | Enabled human actor ID or email, resolved per delivery. |

Beta reads these from the `beta` environment secret
`BETA_COOLIFY_WEBHOOK_SECRET` and the variable `BETA_COOLIFY_PROJECT`. The
assignee is the beta admin. The failure contract and its E2E proof live in
`docs/E2E_TESTING.md` and `web/e2e/coolify-intake.spec.ts`.

Real Coolify delivery to beta is live (transaction
`helm-coolify-intake-20261002`, 2026-10-02). The Coolify container on VM123
posts to a dedicated homelab-edge listener, `http://10.0.10.2:19610`, which
exists on VLAN 10 only and admits only VM123. The edge forwards only `POST
/api/v1/intake/coolify/*` to the beta origin without Tailnet forward auth.
Coolify allowlists `10.0.10.2` as an internal webhook target and sends only
`traefik_version_outdated`. The infrastructure registry
(`registry/networks.yaml` listener
`homelab-helm-beta-coolify-intake-19610`) and
`homelab/coolify/helm-beta-intake/README.md` record the path, rollback and
secret rotation. Production still needs its own target project and listener
decision.

## Outcome and scope

Coolify sends to a dedicated receiving mailbox. A small connector reads those
messages and submits normalized alerts to Helm. Helm creates ordinary Infra
Backlog tasks assigned to Shane, with source information and repeat history.
A dedicated Tickets workspace supports human triage and follow-through. Board
and My work remain alternate views of the same native tasks. An email creates
work; assignment does not claim a task
for an agent or authorize infrastructure changes.

Shane selected a dedicated alerts mailbox. Its provider and final address are
still open. An alias forwarding into personal Gmail alone is not a dedicated
receiving account. SMTP2GO remains the configured outbound transport; receiving
access must be configured separately. Keep the current Gmail notification copy.

First supported fixture: Coolify's Traefik outdated notification. A task titled
"Review Traefik update on localhost" records the reported installed and offered
versions, source and receipt time. Offered versions are evidence from the email,
not independently verified upgrade recommendations. Failure notices may receive
high priority; update notices default to normal. Unknown formats enter an intake
review queue and create no task until explicitly routed.

## Verified source foundations

Inspection used the local `beta` checkout at
`cf5affa98d934f754a57b7ce18fab2b580b5cff5`. The checkout has unrelated pending
changes. Production equivalence has not been verified; rebase and recheck the
canonical source before implementation.

- `internal/httpapi/tasks.go:16`: project-scoped task creation, `tasks:write`,
  replay handling and project authorization.
- `internal/httpapi/tasks.go:384`: existing assignment, label and priority fields.
- `internal/httpapi/tasks.go:690`: scoped comments and replay handling.
- `internal/store/tasks.go:220`: transactional task creation, labels and events.
- `internal/store/notifications.go:474`: assignment notification on task creation.
- `internal/store/types.go:900`: event and notification effects share a transaction.
- `docs/API_CONTRACT.md:185`: task, comment, timeline and lifecycle APIs.
- `web/src/App.svelte:706` and `:7863`: My work defaults to Live and exposes Assigned.
- No mailbox reader or email receipt/correlation ledger was found in `internal`,
  `cmd`, `docs` or `deploy` in this checkout.

Existing generic API idempotency is insufficient as the sole receipt guarantee:
`internal/httpapi/server.go:1380` runs the Store mutation, then `:1388` saves its
replay response separately. A crash in that gap can commit a task without a
replay record. The mutex at `:1363` is process-local, and the replay namespace
at `:1406` includes token identity. Keep that existing credential isolation;
mail receipt identity needs its own stable integration-scoped uniqueness.

## Architecture

Use a separately supervised, Docker-free mailbox worker and an authenticated,
project-scoped native alert intake endpoint. The exact route and schema names
are implementation decisions. Avoid chaining ordinary task/comment API calls
as the authoritative ingestion transaction.

The core Store operation commits receipt identity, correlation decision, task
creation or occurrence update, and activity/notification effects together.
Uniqueness belongs in SQLite, not a process mutex. Retries return the recorded
outcome after authorization, including after lost responses, worker restarts and
credential rotation. Changed payload under the same receipt identity conflicts.
Integration identity is stable and explicitly scoped to Infra; token rotation
must not grant access to another integration's receipt ledger.
Routing is pinned in source configuration: project, Shane's actor, destination
and existing labels. Mail content cannot select privileged fields or a source
identity. Prefer a narrow `intake:write` capability if the existing scope model
supports its addition cleanly.

Keep message receipt identity separate from condition identity:

- Receipt: provider message ID, or mailbox identity plus UIDVALIDITY and UID.
  Preserve a normalized message fingerprint for bounded UID-reset recovery;
  Message-ID alone is insufficient when absent, reused or duplicated.
  A body digest alone must not suppress distinct messages: legitimate repeated
  alerts can have identical bodies. Without stable cross-epoch evidence, route
  reset ambiguity to review instead of claiming exactly-once reconciliation.
- Alert family: integration, alert type and stable resource identity. Do not
  correlate solely by subject, displayed server name or version number.
- Condition/episode: the family plus fixture-proven distinguishing facts such
  as an offered target version or deployment execution identity. A materially
  new target/execution may create linked work even if an earlier task is open.
- Occurrence: the work item for one condition episode, linked to related earlier
  work whether it is open or completed.

For multi-resource mail, record one receipt and deterministic child alerts.
Every item must have a terminal recorded disposition before acknowledgement;
an individual parse failure must not silently discard the rest.

## Default behavior

| Event | Proposed behavior |
| --- | --- |
| Same message fetched again | Return the existing disposition; no new task, count or notification. |
| New message for an open condition | Update last-seen and count; preserve human title, description, assignment, priority, column and claim. |
| Notification burst | Aggregate occurrence activity; notify once on task creation, not every repeat. |
| Same exact condition after completion | Retain evidence without reopening or recreating completed work. |
| New target version or distinct failure episode | Create a linked Backlog task; preserve earlier work and notify once. |
| Previously received mail processed after completion | Associate historical evidence without reopening or creating new work. |
| Unknown but authenticated Coolify format | Hold a bounded intake review item with no task until triaged; avoid merging unrelated conditions. |
| Malformed, oversized or unauthenticated mail | Record a quarantine disposition and operator-visible reason; continue other messages. |
| Paused connector | Stop intake without deleting mail, ledger or tasks; resumption uses durable receipt state. |
| Recovery notice matching an alert | Attach recovery evidence; leave completion to Shane. |
| Informational success or automatic response | Record an ignored disposition and reason. |

Use provider receipt metadata for ordering, not the sender-controlled Date
header. It cannot establish when a delayed condition originally occurred;
ambiguous delayed messages should require review rather than force a recurrence.
Bootstrap starts from an explicit activation boundary. Historic backfill is a
separate bounded preview/replay action. No automatic replies or attachment
execution are needed. Initial attachment ingestion is out of scope.

## Human experience

Add a Tickets navigation destination at `/tickets`. Intake
creates `kind=task` with an explicit assignee and Backlog destination; it leaves
`claimed_by` and `agent_work` absent. A normal triage action can promote work to
Ready. Intake notifications open the ticket directly. The existing My work >
Assigned view also shows the same task; the default Live view stays available
for agent coordination.

| Human queue or status | Native meaning |
| --- | --- |
| Needs triage | Two separate sections: ticket tasks in `backlog`, and review-only receipts without task keys. |
| My open tickets | Ticket tasks assigned to the current human, excluding completed work; includes new and waiting tickets. |
| Ready | Ticket tasks in a `ready` semantic column. |
| In progress | Ticket tasks in an `active` semantic column. |
| Waiting | Ticket tasks in a `blocked` semantic column, with a human-readable reason. |
| Completed | Ticket tasks in `completed`; ignored or quarantined messages are not completed tickets. |

Human status derives from existing columns, never agent progress or claim
ownership. Dependency readiness is shown separately from Waiting. Resolve the
project's actual destination columns and retain existing claim, dependency and
checklist guards. Assignment alone does not start work.

Use a list/detail split on desktop and separate list/detail screens on phones.
Rows show key, subject, resource/source, priority, status, assignee, age and
repeat count. Default order is priority then oldest task creation, so repeated
emails do not continually reorder the queue. Search and URL-backed filters
cover project, assignee, status, priority, source/resource and ordinary task
text. Persist selection, filters and scroll through reload and Back navigation.
Offer fast assign, priority and state controls with explicit pending, saved and
conflict feedback. Keyboard navigation and a readable compact mobile layout
are required, alongside non-color status labels and visible focus.

The focused detail view has a compact subject/status/assignee/priority header,
then What needs attention, human notes/checklists, email evidence and concise
activity. Use human-name assignment pickers instead of requiring actor IDs.
Keep agent execution details secondary and show them when relevant. Share task
and evidence components with the Board drawer rather than maintaining two
copies of the same task data.

Triage updates assignee, priority and Ready destination together with the
native version guard. Start moves to Active without creating a claim. Waiting
records a reason; Resume returns to Ready. Complete and Reopen use native
transitions. Preserve drafts by task ID, protect navigation with unsaved edits,
and prevent delayed responses for ticket A from affecting selected ticket B.
When an action removes the ticket from its queue, show where it moved and
advance focus predictably.

Add an Email alert source panel in the existing Details drawer: integration,
resource, reported condition/versions, first and last receipt, repeat count,
and previous occurrence. Show this evidence in the focused ticket detail as
well as the existing drawer. Show sanitized occurrence events in Activity;
do not manufacture assignment changes or copy arbitrary mail into comments
that can trigger mentions. Notify through the existing assignment category
once on creation, honoring user preferences. Dedicated escalation delivery is
deferred until its routing and rate limits are explicitly designed.

An intake Settings card should show target project, assignee, selected rules,
enable/pause, last poll and sanitized errors. Show recent dispositions and
unknown-format review items. Preview does not mutate tasks. Quarantine review
and explicit retry/mute actions preserve the receipt ledger. Connector settings
and health stay in Settings; the Tickets triage view surfaces the same review
items with Create task and Ignore actions. Opening a receipt creates no task.

Human notes use ordinary task comments and an Add internal note composer.
Notes follow the task's project visibility; internal does not mean personal.
Machine email evidence stays immutable and separate, with no reply-by-email
control. A repeated email must not overwrite a note or interrupt an edit.

Provide resource/family mute choices of 24 hours, 7 days, or until unmuted.
Retain receipts while suppressing new tasks and notifications; leave existing
task status unchanged. Expiry considers the next received event instead of
releasing a backlog of suppressed mail. Ignore/dismiss is an intake disposition
before task creation; an existing task uses ordinary completion. Reading a
notification or completing work never silently mutes the source.

Unknown mail stays review-only until triaged, keeping the human work queue
actionable while retaining every accepted message's disposition. Known
multi-resource templates may fan out;
ambiguous multi-resource templates stay in review until safely routed.

## Mail and operational contract

Use TLS IMAP or the selected provider's OAuth API. For IMAP, persist mailbox
UIDVALIDITY and UID, fetch without relying on unread flags, and acknowledge only
after durable disposition. Do not advance a high-water mark over unrecorded
failures. Recovery after mailbox rebuild/UID reset requires a bounded rescan and
receipt reconciliation. [RFC 9051](https://www.rfc-editor.org/rfc/rfc9051.html)
defines UID validity, immutable message identity and read-only mailbox access.

Parse bounded MIME/plain text/HTML as data. Validate allowed recipient/source
using trusted provider authentication metadata; a matching From header alone
does not authenticate Coolify. Never trust arbitrary message-supplied
Authentication-Results, links, commands or instructions. Credentials come from
protected service configuration/Infisical; do not expose them to task bodies,
logs, UI or metrics. Intake uses a dedicated least-privilege actor, not Shane's
human session or an administrator credential.

Apply bounded batches, timeouts, backoff and durable retry/quarantine state.
Expose sanitized status, last successful poll, lag and retry counts. Keep task
text and message IDs out of metric labels. No public SMTP listener is required.
Network changes should preserve private DNS, Tailscale access and existing
Coolify SMTP delivery. Measure worker resources before choosing production caps.

## Implementation and validation

Ticket membership must be durable, separate from editable labels and from the
`task`/`bug` kind. An additive task-linked ticket record can represent origin
(`email` or `manual`) and source linkage; create membership atomically with the
native task. Support a deliberate New ticket action, not accidental inclusion
of all project tasks. Exact schema names remain implementation decisions.

Add the same ticket predicate to listing, search, saved views and server counts;
apply project visibility before pagination or aggregation. My open tickets
uses assignee only, not the existing My work count that includes claimants.
Counts cannot be inferred from the loaded page. Global search currently uses
offset pagination; define ticket cursor/refresh behavior explicitly and handle
changed collections without duplicated rows. Source/resource filters need
server support; searching email evidence or notes is a separate explicit scope,
not an assumed capability of existing task search.

| Card | Deliverable | Gate |
| --- | --- | --- |
| TC-256 | Atomic external alert intake, receipt ledger and correlation | Real HTTP concurrency/crash/replay/scope tests; additive populated-data migration verification. |
| TC-257 | Dedicated mailbox connector and Coolify parser | Depends on TC-256; real disposable-mailbox to Helm tests, restart and UID-reset recovery. |
| TC-258 | Intake settings, pause/status and shared source/review controls | Depends on TC-256; accessible browser flows with synthetic mail and sanitized artifacts. |
| TC-260 | Human Tickets workspace, durable membership, scoped queues/filter/count APIs and navigation | Depends on TC-256; accurate server counts/pagination and desktop/mobile queue flows. |
| TC-261 | Focused ticket detail and fast human triage | Depends on TC-260; guarded actions, draft preservation and human completion flows. |
| INFRA-172 | Private deployment and real Coolify delivery | Operational gate on all five TC cards; backup, readiness, restart/repeat and Gmail-copy verification. |

The cards are unclaimed Backlog. Helm dependency edges are project-local, so
INFRA-172's cross-project gates are recorded in its comment and this plan.

Before writing code, document failure cases and the repeatable end-to-end test
artifacts, following AGENTS.md. Core validation must cover failpoints around
commit, lost response, concurrent workers, token rotation, edits during intake,
late mail, multi-resource partial failures and unknown templates. Connector
validation must cover TLS/auth failure, provider outage, poisoned messages,
UID reset, read-flag changes and restart without cursor loss. Production
promotion requires a verified pre-upgrade backup, populated migration checks,
retained rollback compatibility and `/readyz`. Roll back by pausing intake and
retaining data; a database restore is not a routine rollback.

Browser acceptance must demonstrate the sample task in Infra and Assigned,
absence of agent claims/progress, user edits surviving genuine repeats,
completion surviving identical notices, linked work for a new target, recovery
without automatic completion, review/ignore across reload, explicit preview and
backfill boundaries, pause/resume and mute expiry. Use synthetic mail and a real
Helm database; retain sanitized response counts and mobile/keyboard screenshots
or traces. The dedicated Tickets workspace is required; a full rule language
remains outside the initial scope.

Human Tickets acceptance also covers the same task ID/key across Tickets,
Board and Assigned after reload; label removal preserving membership; more than
200 fixtures with scoped counts and paginated filters; triage applying all
fields or none on conflict; waiting and starting without agent work; task
switching with delayed saves and protected drafts; queue refresh during another
session's changes; phone Back navigation restoring context; and keyboard search,
row navigation, opening, returning and note submission without shortcut capture
inside inputs. Retain synthetic desktop/mobile screenshots or traces against
a real Helm process and populated database. Any new ticket storage requires the
same additive migration and rollback preservation gates as intake storage.

Before enabling production, choose the receiving provider/address, verify its
trusted sender-auth metadata, resolve Shane's actor identity explicitly, and
confirm a stable Coolify resource identifier in representative synthetic
templates. Provider OAuth provisioning and mailbox credentials remain open;
this design does not presume access has already been granted.
Message excerpt/receipt retention and any historical import are also open
operator choices. Start with Coolify only; other senders are a later scope.
