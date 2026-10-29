# Helm product consolidation plan

Status: execution authorized, 29 October 2026. Phase 0 execution is tracked in
TC-315 and [the dependency/preservation contract](CONSOLIDATION_INVENTORY.md).
Later gates remain acceptance requirements, not claims of deployed behavior.

Planning task: TC-314. Implementation queue and dependencies:
[PRODUCT_CONSOLIDATION_WORK.md](PRODUCT_CONSOLIDATION_WORK.md).

## Phase overview

| Phase | Result | Work |
| --- | --- | --- |
| 0 | Known consumers and preservation contracts | TC-315 |
| 1 | Honest capability states and lossless transfer | TC-316–317 |
| 2 | Fast creation, named ownership, readable active work | TC-318–319; TC-8 |
| 3 | Work / Intake / Progress and compatible navigation | TC-320–324 |
| 4 | Retired drafting/ranking, retained audit runtime | TC-325 |
| 5 | Retired email/provisioning with safe connector drain | TC-326 |
| 6 | Online PWA with safe installed-client transition | TC-327 |
| 7 | Independently accepted actual audit judgments | TC-311, existing |
| 8 | Smaller codebase, beta acceptance and production release | TC-328–330 |

## Outcome

Helm becomes a focused workspace for people and external agents to capture
tasks and bugs, coordinate work, and inspect evidence. The primary path is:

**Capture → Triage → Project work → Evidence → Explicit completion.**

Success means fewer competing destinations, faster ordinary task entry,
understandable ownership, preserved data, and honest statements about what is
known. Reducing visible scope must also reduce maintained services and code.

The consolidation release can ship while audits remain parked. Audit quality
is an independent acceptance gate, not a reason to hold every simplification.

## Target workspace

| Surface | Purpose | Boundary |
| --- | --- | --- |
| Work | Mine and Agents views across projects | Assignment, claim ownership and reported agent progress remain distinct. |
| Intake | Needs triage and All incoming work/bugs | Existing records keep their identity. Requests are not forced to become bugs. |
| Progress | Task-state overview, deadlines and blockers | No claim of shipment without actual deployment evidence. |
| Project board | Execute and inspect the work | Readable cards, obvious active work, one primary creation action. |
| Project Focus | Group tasks around a goal | Existing required-work and dependency readiness remain; this is not software deployment. |
| Search | Find work and open saved filters | Advanced saved-view editing is secondary. |
| Task drawer | Description, criteria, relations, comments, history, PR evidence | Internal actor IDs are replaced with name-based selection. |
| Settings / Operations | People, agents, authentication, integrations, diagnostics | Technical setup stays out of everyday task flows. |

The notification bell stays available. Explore remains a small control within
Progress. Board-level Timeline and the separate Issues/Tickets destinations
leave primary navigation; useful history, old links and data remain.

## Agreed scope

| Decision | Features | Treatment |
| --- | --- | --- |
| Keep | Boards; task metadata; checklists; hierarchy; same-project dependencies; bug lifecycle; agent API/skill; comments/history; PR links | Preserve behavior, permissions and identifiers; improve usability. |
| Simplify | Task creation; assignment; Focus; Progress; Work; search/saved views; in-app notifications | Fewer primary controls and clearer language. No new notification or scheduling platform. |
| Consolidate | Tickets and Issues into Intake; personal assignments and Live into Work; diagnostic navigation into Operations | Reuse existing records and APIs where possible, with compatibility routes. |
| Retire | Embedded Luna task drafting; AI project ranking; email inbox product workflows; Cloudflare provisioning wizard; offline task snapshots; standalone Timeline destination | Retire surfaces, then consumers, then unused implementation. No silent loss of old content. |
| Park | Audit creation/corrections until real acceptance; portable transfer until lossless acceptance | Retain authorized history. Explain unavailability. Do not present incomplete outcomes as finished. |
| Preserve operationally | Codex account support needed by audits; authentication; existing ingress; generic/Coolify webhooks; CI, backups, release and rollback tools | Product cuts do not authorize deleting shared infrastructure or credentials. |

No expansion into Gantt, cross-project dependencies, general automation,
outbound notification delivery, IMAP, attachments/replies or built-in coding
agent scheduling is part of this plan.

## Phase 0 — Establish the retirement and preservation contract

Map each retiring feature to its screens, routes, background work, data,
credentials, external resources and remaining consumers. Inventory installed
clients, API/skill consumers and configured email/webhook connectors using
safe metadata. Identify the currently deployed beta/prod revisions and their
retained rollback images; do not assume a feature branch is live.

Record which old URLs remain aliases, which authorized history routes remain
available, and which execution endpoints will return a clear retired-capability
response. Check permissions before exposing resource-specific retirement facts.
Use a small explicit capability policy; do not build a general feature-flag
platform to remove features.

Establish populated-data baselines for tasks, typed bugs, ticket membership,
key aliases, comments, labels, Focus, checklists, hierarchy, dependencies,
claims, actors/scopes, source/repeat history, PR links, audit history and private
AI history. Reuse the existing backup and compatibility tools.

**Exit gate:** a reviewed consumer/resource/data map, named compatibility
obligations, backup/rollback procedure and an acceptance dataset. No code
deletion, connector shutdown or external resource deletion occurs in this phase.

## Phase 1 — Stop false confidence and protect transfer fidelity

Remove normal audit-run/correction entry points while TC-311 remains open;
retain protected read access to historical findings and evidence. Park transfer
controls until the known omissions are repaired. Incompatible API transfer
paths must fail clearly or disclose explicitly reviewed limitations; a silent
success that loses supported information is unacceptable.

Correct Progress and other UI copy immediately: a semantic completed count is
“Tasks marked complete.” PR merge, implementation support, task completion and
deployment remain separate facts. Project-level views describe project scope.

Repair portable transfer using the reproduced defect: a v3 round trip lost
checklist items, parent links and `require` policy with no warnings. Introduce a
new archive version if needed. Preserve old-format readability and explicit
limitations, remap relation IDs correctly, and keep imports additive and
idempotent. Include Focus, bugs, dependencies, comments, agent history and PR/
audit history already supported by existing formats. Exclude credentials.

**Exit gate:** the populated round trip preserves all selected fields and
relationships; required-checklist completion remains blocked after import;
preview is read-only; invalid/cyclic relations fail atomically; unrelated
destination records are unchanged. Retained-binary compatibility and integrity
checks pass for any storage changes. Transfer returns to normal UI only then.

## Phase 2 — Make everyday task work simple

Task creation starts with the title. Remove rough-idea/AI drafting and account
connection detours from the form. Show ordinary fields directly and advanced
relations only when needed. Keep safe drafts, destination selection, explicit
submission, idempotent retries and useful validation.

Replace raw actor-ID inputs with the existing TC-8 searchable human/agent
picker work. Disabled or inaccessible actors cannot be newly assigned;
historical references still render. Assignment and claims retain different
labels. Reuse existing identity data rather than expanding authorization scope.

Use one create action per context. Retain useful per-column quick-add, but
remove competing New task/Create task buttons opening the same modal.

Use Opus 5.5 for the UI design review, including a density/empty-column solution
that exposes active and blocked work without making titles cramped. Prefer a
small, understandable treatment over a large layout-preferences system.

**Exit gate:** users can create, assign, edit and find active work on desktop
and phone without knowing IDs or interacting with AI. Keyboard/touch flows,
readable cards, overflow navigation, selection and draft protection remain.

## Phase 3 — Consolidate navigation and the work model

### One Intake surface

Build a unified read model over existing ticket membership and bug kind.
“Needs triage” identifies incoming work; “All” provides discoverable historical
and filed items with clear kind/project filters. Retain the hidden Tickets
system project and all actor/token project ceilings. Do not grant an agent
access to queue work merely because it can access some project board.

One task keeps one immutable ID. Filing keeps old key aliases, comments,
source facts, occurrence counts, watches and typed bug details. A filed ticket
remains a ticket; a request need not be classified as a bug. Classification,
triage, filing, resolution and reopening remain explicit guarded actions.

Reuse the current intake mechanisms. Keep generic and Coolify webhooks under
advanced integration settings. Avoid creating a second entity or copying cards
just to make the navigation simpler.

### One Work surface

Use Mine and Agents views, reusing current assignments and Live information.
Show reported phase, checkpoint count, last update and next action. Preserve
working, verifying, waiting, handoff and stale distinctions. Explain that a
reported pulse does not prove a running process; the browser currently polls.

Action-needed rows open the relevant task/context. Do not invent a new approval
engine, scheduler or promise that Helm can launch all coding work.

### Simple Progress and Focus

Retain transparent completed/active/blocked/due metrics and project navigation.
Remove AI analysis/ranking and keep deterministic ordering. Preserve Explore,
zoom, safe exit and return from task details. Focus retains its goal, membership,
date and required-work guards; advanced readiness detail stays secondary.

### Navigation and compatibility

Make Work, Intake, Progress and project boards primary. Search, notification
bell and Settings remain easy to reach. Saved-view editing moves under advanced
search. Metrics moves under Settings/Operations. Remove standalone Timeline
navigation while retaining task Activity.

Old issue, ticket, timeline, my-work and roadmap links map to useful equivalent
views with filters/task context preserved. Deep links, Back/Forward and shared
views must not silently land on unrelated unfiltered screens. Keep old API
consumers working through adapters until the documented compatibility gate.

**Exit gate:** a complete report/webhook → Intake → triage → project work →
resolve/reopen journey, a personal/agent handoff journey, and old-link/browser
history journeys pass with permission and phone/keyboard coverage.

## Phase 4 — Remove the two general AI features

After their entry points are gone, retire task drafting and project ranking
routes, services, background triggers, prompts and unused dependencies. Old
authenticated clients receive an understandable retired-capability response;
they do not silently trigger another model or fail with a generic outage.

Retain personal Codex connection/logout, account-generation fencing, the
evidence-only audit adapter, server-owned immutable audit profiles and any
required worker/cancellation support. Retain owner-scoped reading of private
historical runs; no new drafting/ranking runs are generated. Update disclosure
of stored input/output content and existing retention. Do not delete histories,
credentials or tables as an incidental cleanup.

**Exit gate:** no creation or project view starts the retired model turns;
manual task work and deterministic Progress work; audit account readiness and
cancellation retain their protections; historical content is still protected
and readable. No shared authentication/runtime component was removed by mistake.

## Phase 5 — Retire email and product-level provisioning

This is an operational retirement, not just deleting a Settings panel.

1. Stop new inbox/provisioning setup and remove promotional/setup UI.
2. For every active email connector, record a replacement or explicit shutdown
   decision and test its destination. Keep temporary delivery compatibility
   until that transition is verified. Preserve the tickets and source history.
3. Once active dependencies are drained, stop the unneeded runtime and retire
   routes with documented sender/client behavior. Do not return success for a
   delivery that was dropped. Produce an exact external-resource cleanup list.

Generic/Coolify webhooks, current ingress, Cloudflare Access authentication,
Tailnet authentication and release hosting stay intact. Cloudflare provisioning
credentials/resources must be distinguished from credentials/resources still
used by auth or existing webhook reachability.

Deleting Workers, tunnels, DNS or credentials is a separate reviewed operator
action against exact resources. This planning approval does not authorize
irreversible cloud cleanup. If an active connector has no accepted replacement,
the drain gate remains open and its adapter has a named maintenance owner.

**Exit gate:** no active delivery silently breaks, existing intake/history is
readable, retained webhooks and auth pass, and each retired external dependency
has either verified decommission or an explicit time-bounded handoff. Do not
call full retirement complete while leaving an unowned production adapter.

## Phase 6 — Remove offline task caching, keep mobile installation

Keep responsive layout, PWA manifest/icons, installation and consent-gated
updates. Offline displays an honest connection-required state, not an old task
snapshot presented as current. Reconnection still checks the session before
writes; failed writes are never queued or replayed automatically.

Remove snapshot capture/viewer code and only the known Helm snapshot stores
after the updated client activates. Do not wipe all IndexedDB, localStorage or
caches. Preserve unsaved drafts and old hashed assets needed by open clients.
Respect existing update/draft guards and the service-worker lifecycle.

**Exit gate:** upgrade an installed old PWA, exercise offline/reconnect and
session expiry, verify safe unsaved drafts and image rollback, and check an
actual iPhone Home Screen launch/update. Server tasks and historical data stay
unchanged; obsolete local snapshots are removed deliberately.

## Audit and release gates

Continue with [Phases 7–8 and execution rules](PRODUCT_CONSOLIDATION_ROLLOUT.md):
actual audit acceptance, code retirement, compatibility, beta/prod gates and
image-only rollback. Audits remain a separate optional release gate.
