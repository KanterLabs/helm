# Helm consolidation — implementation queue

Planning task: TC-314. Execution started with TC-315 after authorization.
Remaining cards stay unclaimed Backlog until their work begins. Existing TC-8
supplies assignment work; TC-311 retains separate audit acceptance.

Read [the product and phase plan](PRODUCT_CONSOLIDATION_PLAN.md) and
[audit/retirement/release gates](PRODUCT_CONSOLIDATION_ROLLOUT.md) first.

## Queue and prerequisites

The table indexes implementation-sized work; full measurable criteria are on
each Helm card. Shared preservation, failure-first verification and file-size
requirements are included on every new card.

| Phase | Card | Work | Prerequisites |
| --- | --- | --- | --- |
| 0 | TC-315 | Inventory Helm consolidation dependencies and preservation gates | Planning complete |
| 1 | TC-316 | Park unreliable audit and transfer entry points and correct delivery language | TC-315 |
| 1 | TC-317 | Make portable project transfer preserve checklists hierarchy and policy | TC-315 |
| 2 | TC-318 | Make task creation title first and remove duplicate create actions | TC-315 |
| 2 | TC-319 | Make active board work visible without sacrificing readable cards | TC-315 |
| 3 | TC-320 | Unify ticket and bug intake queries without duplicating tasks | TC-315 |
| 3 | TC-321 | Build one Intake workspace for triage requests and bugs | TC-320, TC-8 |
| 3 | TC-322 | Consolidate personal assignments and agent status into Work | TC-315 |
| 3 | TC-323 | Simplify Progress and Focus around transparent task state | TC-315 |
| 3 | TC-324 | Ship consolidated navigation and preserve existing deep links | TC-318, TC-321, TC-322, TC-323 |
| 4 | TC-325 | Retire embedded task drafting and AI project ranking safely | TC-318, TC-323 |
| 5 | TC-326 | Retire email inbox and Cloudflare provisioning product workflows | TC-315, TC-321 |
| 6 | TC-327 | Retire offline task snapshots while preserving the installed mobile app | TC-315 |
| 8 | TC-328 | Remove retired Helm code and reconcile contracts documentation and tests | TC-324, TC-325, TC-326, TC-327 |
| 8 | TC-329 | Validate the consolidated Helm core and data preservation on beta | TC-328, TC-317, TC-316, TC-319, TC-8 |
| 8 | TC-330 | Release consolidated Helm from tested beta and verify adoption | TC-329 |
| 2 | TC-8 — existing | Searchable human/agent assignee picker | Reuse existing acceptance; coordinate Phase 0 contracts |
| 2 | TC-113 — existing | Human membership, roles and session lifecycle | Required before TC-8; owner explicitly included the full expansion on 30 October |
| 7 | TC-311 — existing | Actual audit judgments, legitimate human apply, preserved data and exact beta promotion | Existing five checkpoints; currently 2/5 handoff |

## Delivery packets

### TC-315 — Phase 0

Establish exactly what may be retired, what it shares with retained features, and how existing users, integrations and records remain safe.

**Implementation boundary:** Navigation/routing, Codex consumers, Cloudflare auth and provisioning, webhook/email connectors, PWA stores, API consumers, persistent data and deployment contracts.

**Acceptance focus:** Publish a capability/route/data/consumer map with keep, merge, retire, or park decisions; inventory configured integration counts and usage without exposing credentials or private content.

### TC-316 — Phase 1

Stop presenting unreliable audit judgments and incomplete transfers as finished features while keeping history accessible and completion metrics honest.

**Implementation boundary:** Audit start/review navigation, portable transfer controls, Roadmap metric labels, capability responses and existing historical read paths.

**Acceptance focus:** Hide normal audit-run/correction entry points until TC-311 acceptance passes; retain authorized historical evidence and findings, and explain the unavailable capability without synthesizing results.

### TC-317 — Phase 1

Restore trust in project transfer by preserving supported task information and completion safeguards across a versioned export/import round trip.

**Implementation boundary:** internal/store/portable* and versioned HTTP/UI contracts; use the feature-audit portability probe as the failure reference.

**Acceptance focus:** Write the failing real round trip first: parent/child tasks, open/completed checklist items and require policy survive export, JSON transport, preview, remapping, import and re-export.

### TC-318 — Phase 2

Make ordinary task capture the fastest path and remove the embedded task-drafting workflow from everyday creation.

**Implementation boundary:** TaskModal, board heading/toolbar, global New actions, quick-add, draft protection and creation APIs.

**Acceptance focus:** Opening New task focuses the title; normal task creation has no rough-idea field, AI prompt or account-login detour. Keep task data and legitimate unfinished manual drafts safe.

### TC-319 — Phase 2

Keep the roomier card design while making active and blocked work discoverable on normal desktop and phone viewports.

**Implementation boundary:** Board layout, column sizing/overflow navigation, card readability, empty columns and responsive controls.

**Acceptance focus:** Use an Opus 5.5 design review to choose a small density/empty-column treatment that preserves readable titles and meaningful state/count cues; avoid adding a large preferences system.

### TC-320 — Phase 3

Support one intake surface over existing tickets and bugs while preserving identity, typed bug details, filing behavior and project authorization.

**Implementation boundary:** Ticket/issue collection and count APIs, queue project visibility, filtering/pagination, filing and task key aliases.

**Acceptance focus:** Define Needs triage and All scopes over existing records; one task keeps one ID, comments, labels, severity, source and occurrence history. Ordinary unfiled requests need not become bugs.

### TC-321 — Phase 3

Give users a clear intake-to-project-work path without separate competing Tickets and Issues destinations.

**Implementation boundary:** TicketsWorkspace/IssuesView replacements or reuse, bug report entry, triage drawer, Intake filters and source details.

**Acceptance focus:** Provide Needs triage and All views with understandable kind/project/severity filters; a filed request stays findable without creating a second card or asking users to learn internal queue-project rules.

### TC-322 — Phase 3

Make one Work destination serve personal assignments and agent coordination with clear Mine and Agents views.

**Implementation boundary:** MyWorkView, LiveWorkRow, AgentWorkPanel, task activity links, liveness labels and state filters.

**Acceptance focus:** Keep Mine for assignments/claims and Agents for reported work; retain waiting, handoff, stale, verifying and working distinctions without implying a heartbeat proves a running process.

### TC-323 — Phase 3

Keep a useful delivery overview and simple goal grouping while removing AI ranking and deployment-like interpretations of Focus readiness.

**Implementation boundary:** RoadmapView, project attention/ordering, Focus planning UI, Explore mode and required-work queue presentation.

**Acceptance focus:** Present completed, active, blocked and due work with explicit task-state scope; use transparent metric sorting and remove Analyze with Luna and AI attention narratives.

### TC-324 — Phase 3

Expose Work, Intake, Progress and project boards as the primary workspace while retaining discoverability and existing links.

**Implementation boundary:** Desktop/mobile navigation, routing/history, command palette, search/saved views, notifications, Timeline and Metrics links.

**Acceptance focus:** Remove standalone Issues/Tickets duplication and Timeline navigation; preserve task Activity and map old timeline/issue/ticket URLs to useful filtered destinations with task/filter context intact.

### TC-325 — Phase 4

Remove the two low-value AI product features without breaking the audit inference runtime, personal account management or existing private history.

**Implementation boundary:** Task-draft and intelligence routes/services/UI, Codex settings, run-history content access, metrics and feature-specific tests.

**Acceptance focus:** Remove new task-draft and AI ranking requests and background triggers; retain deterministic metrics and clearly signal retired capabilities to authenticated legacy clients instead of silently using a different model.

### TC-326 — Phase 5

Remove helpdesk-like email/provisioning scope through an explicit active-connector transition while retaining useful webhook intake and authentication.

**Implementation boundary:** Email inbox creation/settings, setup wizard/provisioning adapters, existing Email Workers/connectors, deployment guides and historical source panels.

**Acceptance focus:** Stop new inbox/provisioning setup and publish a resource/consumer inventory with a replacement or drain plan for each active email connector; retain delivery compatibility until its transition is verified.

### TC-327 — Phase 6

Make Helm an online workspace with useful mobile installation and safe updates, removing the partial read-only board cache.

**Implementation boundary:** offlineBoards/OfflineBoard, IndexedDB snapshot stores, service worker navigation/cache rules, reconnect/session checks and PWA install/update UI.

**Acceptance focus:** Keep responsive layout, manifest/icons, installation and consent-gated updates; replace offline task views with an honest connection-required state and a safe retry path.

### TC-328 — Phase 8

Turn the smaller product surface into a smaller maintained codebase while preserving retained contracts and historical data.

**Implementation boundary:** Retired UI/services/config/dependencies, API deprecation responses, OpenAPI, help/release docs, capability declarations and test runners.

**Acceptance focus:** Remove code and dependencies only after retained-consumer checks and active-integration drain pass. Keep authentication, project boundaries, data readers, rollback compatibility and required deprecation adapters.

### TC-329 — Phase 8

Accept the reduced product through useful real user outcomes, compatibility evidence and preserved existing data before a production release.

**Implementation boundary:** Representative browser/API journeys, populated upgrades, retained rollback images, exact beta revision and bounded CI evidence.

**Acceptance focus:** Verify capture/assign/work/complete; intake/triage/file/reopen; agent claim/progress/handoff; task/PR evidence navigation; search/old links; and installed-mobile upgrade using synthetic data and sanitized repeatable receipts.

### TC-330 — Phase 8

Promote the accepted smaller product through the existing versioned release pipeline with protected data and a clear retirement handoff.

**Implementation boundary:** Existing publish-beta/prepare-release/finish-release flow, canonical Gitea and GitHub mirror, protected production deployment and post-release checks.

**Acceptance focus:** Prepare a release from the exact CI-passing ready beta SHA, publish canonical Gitea before the GitHub mirror, and document accepted changes, parked audit capability and any retained deprecation adapter.

## Shared acceptance and sequencing

- TC-315 is the first execution card. It inventories active consumers and
  records preservation/compatibility contracts before feature retirement.
- Phase 1 safeguards and transfer repair can proceed alongside design for
  ordinary task entry. Do not make design work wait for the entire audit fix.
- TC-318 and the UI portion of TC-8 share TaskModal/drawer concerns. Build the
  reusable identity picker separately, then serialize their form integration.
- TC-320 defines Intake identities, filters, counts and permission behavior
  before TC-321 changes its UI. TC-324 integrates the finished destinations;
  it is not a separate rewrite of their state or APIs.
- TC-325 removes only the two retired AI features after their call sites are
  gone. Codex authentication and audit evidence isolation remain owned by the
  retained runtime. Historical private content remains owner-scoped.
- TC-326 is not complete merely because inbox creation UI disappears. Every
  live connector must be drained/replaced or receive an explicit named handoff.
  Its external cleanup inventory is not authorization to delete cloud resources.
- TC-327 owns the installed-client/service-worker transition. Keep this state
  change serialized with shared PWA update work and deployment verification.
- TC-328 removes unused implementation only after consumers have moved. Keep
  necessary aliases and authorized historical data readers during compatibility.
- TC-329 depends on the assignee picker, preserved transfer, risk containment,
  density work and the integrated reduced product. It does not depend on
  TC-311: audits can stay explicitly parked in the consolidation release.
- TC-330 uses the existing release commands from the exact accepted beta SHA.
  It must not widen the release to untested audit functionality or beta fixtures.

## Verification packets

See [the shared verification packets](PRODUCT_CONSOLIDATION_ROLLOUT.md#verification-packets)
for real user flows, preservation and installed-client acceptance.

## Existing backlog reconciliation

TC-8 and TC-311 are reused rather than duplicated or reclaimed for planning.
Execution inspection found that TC-8 also depends on existing TC-113 (human
membership, roles and session lifecycle). Its old implementation, `98e4292`
on `backlog/tc113-membership`, is not an ancestor of the deployed beta or this
consolidation branch. Review and port that prerequisite against current storage
and authorization contracts before accepting TC-8; do not bypass its dependency
or treat the old handoff as deployed behavior. This adds no replacement identity
platform to the product scope.
The owner selected the full membership expansion on 30 October; TC-113 is now
an explicit execution prerequisite, not a deferred or bypassed dependency. Its
current integration contract is [TC113_MEMBERSHIP_PORT.md](TC113_MEMBERSHIP_PORT.md).
At Phase 0/8, review old AI-ranking, email, offline and automation proposals
against the agreed cuts. Record supersession explicitly; do not silently close
cards with active claims, mark unfinished work complete, or erase already
completed historical feature cards. No general automation expansion is part
of this consolidation.

## Recovery metadata

The planning receipts are in `.artifacts/consolidation-plan/backlog.json`.
Full card definitions are separated by responsibility into `core-work.json`
and `retirement-work.json` in that directory. The seed helper uses stable
lifecycle operation IDs and persisted UUIDv4 dependency operation IDs so a
lost response can be replayed without duplicating the logical mutation.
Future execution uses the normal Helm resume/claim/progress lifecycle; the
planning helper never runs product code or deploys a release.
