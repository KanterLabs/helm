# Release-bound task planning and execution

Status: proposed (2026-10-04). Not started; planned after v0.2.0.

## Goal

Let a project bind each task or bug to at most one planned release so people
can plan beyond the current board, filter the board by delivery target, and
give an agent an instruction such as:

> Work on all tasks needed for release 1.4.

The agent must be able to resolve that release, understand the complete
dependency-aware work scope, safely claim and finish eligible tasks one at a
time, and stop with an actionable explanation when remaining work cannot
proceed.

This is a product-planning release. It is separate from Helm's deployment
revision (`X-Roadmap-Revision`) and from the existing task action that releases
an agent claim.

## Recommended v1 decisions

- A release belongs to exactly one project.
- A live task belongs to zero or one release. `No release` is a supported,
  filterable state; existing tasks are not backfilled.
- Release membership is explicit. A parent, child, label, due date, or
  dependency never changes membership implicitly.
- Task and bug creation forms may preselect the release currently filtering the
  board, and a child-task form may copy its parent's release as an editable
  default. The submitted task still stores an explicit release ID.
- A release is `planned` until an explicit completion action marks it
  `released`. Finishing the last task makes it ready to release but does not
  automatically claim that a deployment shipped.
- A released release and its membership are frozen. Reopen the release with a
  reason before changing membership or reopening one of its completed tasks.
- The work scope is the release's incomplete direct members plus their
  transitive, same-project prerequisites. The existing dependency graph remains
  the source of truth for ordering.
- An incomplete prerequisite assigned to a different planned release is a
  planning conflict. Show it, but do not let an agent silently work or reassign
  it as part of the target release.
- The server exposes a read-only work queue. Agents continue to use the
  existing versioned claim, progress, complete, and block actions for each
  task; there is no bulk claim or server-side autonomous executor in v1.
- Release names are unique case-insensitively within a project. APIs and saved
  views persist opaque release IDs so renames do not break filters.

## User journeys

### Plan future work

1. Open a project's Releases page and create `1.4` with an optional target
   date and description.
2. Assign tasks and bugs from the create form or task drawer.
3. Open the release to see direct progress, dependency blockers, cross-release
   conflicts, and unassigned prerequisites.
4. Open its filtered board URL or save the release filter as a view.

### Execute a release with an agent

1. The user says `Work on all tasks needed for release 1.4`.
2. The Helm skill resolves one planned release in the named or current project
   and reads its work queue.
3. The agent reports the bounded scope and begins without another confirmation
   when the release is unambiguous and the instruction is explicit.
4. It resumes its own active task first or claims the first claimable task in
   dependency order. It never holds multiple new claims merely to reserve work.
5. It publishes progress and completes or blocks that task through the existing
   lifecycle, then refreshes the queue from the first page.
6. It repeats until all required work is complete or nothing is claimable.
7. It reports `ready to release`, or identifies manual blockers, foreign
   claims, cross-release conflicts, or work requiring new authority. It does
   not mark the release released unless the user separately requests that
   lifecycle action.

### Close a release

1. A human or authorized agent reviews the release summary.
2. `Complete release` rechecks the live task and dependency state inside the
   write transaction.
3. The action succeeds only when the release has at least one direct task, all
   direct tasks are complete, and every live prerequisite is satisfied.
4. Helm records who completed it and when. Reopening requires a reason and is
   visible in activity.

## Domain model

Add the next contiguous migration (currently `021_releases.sql` in this
checkout; choose the next number after rebasing) with an additive release table
and nullable task reference:

```sql
CREATE TABLE releases (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (
        length(trim(name)) > 0 AND length(name) <= 200
    ),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 10000),
    target_date TEXT,
    released_at TEXT,
    released_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX releases_project_name_unique
    ON releases(project_id, lower(name));
CREATE INDEX releases_project_target_idx
    ON releases(project_id, released_at, target_date, created_at, id);

ALTER TABLE tasks
    ADD COLUMN release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT;
CREATE INDEX tasks_project_release_idx
    ON tasks(project_id, release_id, deleted_at, number, id);
```

`target_date` is an ISO `YYYY-MM-DD` calendar date rather than an instant. A
release date is a planning boundary and should not shift across time zones.

The migration must add database guards, following the task hierarchy and
dependency migrations, for invariants that must survive a retained-binary
rollback:

- a task's release must be live and belong to the same project;
- a released release cannot gain or lose members;
- a task in a released release cannot be reopened, moved out of a completed
  semantic state, or soft-deleted until the release is reopened;
- a non-empty release cannot be deleted;
- a release cannot be marked released when it has no members, an incomplete
  member, or an unmet live prerequisite.

Use `ON DELETE RESTRICT` and an explicit empty-release delete action rather than
silently setting task bindings to null. Project removal is not a v1 workflow;
project archival remains unaffected.

### Public shapes

```json
{
  "id": "opaque-release-id",
  "project_id": "opaque-project-id",
  "name": "1.4",
  "description": "Search and release planning",
  "target_date": "2026-11-15",
  "status": "planned",
  "released_at": null,
  "released_by": null,
  "version": 3,
  "summary": {
    "task_count": 12,
    "completed_count": 7,
    "blocked_count": 2,
    "claimed_count": 1,
    "checklist_warning_count": 0,
    "required_task_count": 15,
    "required_completed_count": 9,
    "cross_release_conflict_count": 1,
    "ready_to_release": false
  }
}
```

Task reads add `release_id` plus a compact `release` reference containing
`id`, `name`, `status`, and `target_date`. Populate release references in
batches for collections; do not add a query per card.

Task create and patch input add nullable `release_id`. As with
`parent_task_id`, the store needs a separate set flag so omission preserves the
current value while explicit JSON `null` clears it. Assignment changes bump the
task version and emit one task event.

## API contract

### Release lifecycle

- `GET /api/v1/projects/{project}/releases`
- `POST /api/v1/projects/{project}/releases`
- `GET /api/v1/releases/{release}`
- `PATCH /api/v1/releases/{release}`
- `DELETE /api/v1/releases/{release}` for an empty, planned release only
- `POST /api/v1/releases/{release}/complete`
- `POST /api/v1/releases/{release}/reopen` with a required reason
- `GET /api/v1/releases/{release}/work-queue`

Release reads and the work queue require `tasks:read`; mutations require
`tasks:write`. Existing project ceilings apply before a release or task is
returned. No new token scopes are needed.

Release PATCH, DELETE, complete, and reopen require the release `If-Match`.
Every mutation uses `Idempotency-Key`, returns a fresh ETag where applicable,
and follows the existing redacted error envelope for write-only bearer tokens.

Stable errors include:

- `release_not_found`
- `release_name_exists`
- `release_cross_project`
- `release_already_completed`
- `release_not_completed`
- `release_has_tasks`
- `release_incomplete`
- `release_dependency_conflict`
- `release_frozen`
- `release_queue_changed`

### Task filters

Project task collections, issues, My Work, global search, and saved views add a
release filter:

- project-scoped routes accept `release={id}` or `release=unassigned`;
- global routes and saved views use stable `release_id={id}` or
  `release_id=unassigned`;
- release list routes accept `status=planned|released`, `target_from`, and
  `target_to`.

A project-scoped UI may resolve an exact case-insensitive release name to its
ID before loading tasks. Cross-project APIs never interpret a bare name because
the same release name may validly exist in several projects.

Release assignment participates in the existing project task-collection
revision. Moving a task into or out of a filtered release invalidates an
in-flight task cursor with the existing `task_collection_changed` restart
contract.

### Work queue

`GET /api/v1/releases/{release}/work-queue` is read-only and returns a bounded,
cursor-paginated snapshot:

```json
{
  "release": { "id": "...", "name": "1.4", "version": 3 },
  "snapshot": { "project_revision": 812, "read_at": "..." },
  "summary": {
    "direct": 12,
    "required": 15,
    "completed": 9,
    "claimable": 1,
    "owned": 1,
    "dependency_blocked": 2,
    "manually_blocked": 1,
    "claimed_elsewhere": 1,
    "cross_release_conflicts": 1
  },
  "data": [
    {
      "task": { "id": "...", "key": "TC-42", "version": 8 },
      "relationship": "direct",
      "disposition": "claimable",
      "blocked_by": []
    }
  ],
  "next_cursor": ""
}
```

The scope calculation starts with live direct members and walks the existing
dependency graph toward prerequisites. It does not include hierarchy children
unless those children are direct release members or dependency prerequisites.

Disposition rules are evaluated at one read timestamp:

- `completed`: the task's completion and completed semantic state agree;
- `owned`: the requesting agent owns an active claim;
- `claimable`: incomplete, not in the manual Blocked state, all prerequisites
  satisfied, and no active foreign claim;
- `dependency_blocked`: at least one live prerequisite is incomplete;
- `manually_blocked`: the card is in a semantic Blocked column;
- `claimed_elsewhere`: another actor has an active lease;
- `cross_release_conflict`: an incomplete prerequisite belongs to another
  planned release.

Order owned work first, then claimable tasks in topological prerequisite order.
Break ties by priority, board position, task number, and task ID. The queue
must treat tasks with no dependencies as claimable; the current
`dependency=ready` task filter intentionally excludes them and cannot be used
as the queue implementation.

Each cursor captures the release version, project event/task-collection
revision, filter, and read timestamp. Any task, dependency, claim, or release
change returns `409 release_queue_changed` with `restart: true`. An agent
restarts from the first page after every mutation, so it never acts on the next
task from a stale plan.

Do not add a bulk-claim endpoint. Optimistic versions, lease ownership,
dependency checks, checklist policy, and task activity remain authoritative at
the individual task mutation boundary.

## Agent and CLI behavior

Extend the Helm client without overloading the existing singular `release`
command, which releases a task claim:

- `helm.py releases list --project TC [--status planned]`
- `helm.py releases get --project TC --release 1.4`
- `helm.py release-work --project TC --release 1.4`
- retain `helm.py release --task TC-42` as the compatibility command for
  releasing a claim; document `unclaim` as a clearer alias.

The skill's natural-language workflow for `work all tasks needed for release
X` must:

1. resolve exactly one planned project release;
2. publish the release name and current queue summary as the execution scope;
3. resume an owned task before taking another lease;
4. claim no more than one new task at a time;
5. use the existing task's goal, acceptance criteria, dependencies, checklist,
   and latest activity as its concrete work contract;
6. if the existing two-step resume claims a task but cannot move it to Active,
   treat the still-owned claim as resumable work and retry/recover rather than
   claiming another card;
7. refresh the queue after each completion, block, released claim, stale ETag,
   or scope-changing event;
8. include newly added direct tasks on the next refresh, while calling out the
   material scope change;
9. stop rather than touching a task in another planned release, overriding a
   foreign claim, bypassing a blocker, deploying, or performing another action
   that lacks authority;
10. finish only when every required task is complete, or leave a structured
   waiting/handoff report that names each blocker and next action.

This workflow is orchestration guidance, not a background daemon. A future
release-run resource may record long-lived autonomous runs, snapshots, and
budgets, but it is out of scope for v1.

## Web experience

Add a project-scoped `/p/{slug}/releases` page linked from the project heading.
Keep it out of the existing two-item Board/Timeline tablist and the already
full mobile bottom navigation.

The page shows planned releases first by target date, then released history.
Each row/card includes target date, direct completion, required completion,
blocked/claimed counts, readiness, and an `Open filtered board` action. Create
and edit use the existing modal, validation, optimistic conflict, focus
restoration, and confirmation patterns.

Add release assignment to:

- task creation;
- bug creation as `Target release`, distinct from bug `Affected version`;
- the task drawer beside priority, due date, and assignee;
- the child-task form as an editable default copied from the parent.

Add a compact release chip to board cards, search results, Issues, My Work, and
the drawer. Cross-project views prefix the project key when the release name is
not otherwise clear.

Add `All releases`, each planned release, released history, and `No release`
to the board and issue filters. Persist issue/global-search release filters in
their URLs and saved views. When a filtered quick-add creates a task, explicitly
assign that filter's release; without a release filter, leave it unassigned.

On project changes, clear or revalidate the project-local release filter so an
ID from the previous project cannot produce an empty board. If changing a task
assignment removes it from the current filtered board, use the existing
collection refresh announcement instead of leaving a stale card.

Release management and assignment are online-only in v1. Offline board
snapshots may display the release reference already embedded on cached tasks,
but they must not offer a release mutation or pretend a release list is fresh.

Rename the visible task action from `Release` to `Release claim` to avoid
confusing claim lifecycle with the new product entity. Keep the API route and
compatibility CLI command unchanged.

## Events and activity

Emit bounded, project-scoped events:

- `release.created`
- `release.updated`
- `release.completed`
- `release.reopened`
- `release.deleted`
- `task.release_changed`

The task event includes old/new release IDs and names. Release events include
only safe IDs, name, target date, and status changes. Map assignment and release
lifecycle events into readable task/project timeline entries, and use them to
refresh open release, board, Roadmap, and queue views.

## Data preservation and compatibility

- Make the schema migration additive, transactional, and retry-safe. Existing
  tasks remain `release_id = NULL`; never infer releases from labels, due dates,
  titles, Git tags, or bug affected versions.
- Add a populated-database migration test from the immediately preceding
  schema. Preserve every project/task/actor ID, relationship, row count, and
  field value, then verify integrity, foreign keys, migration idempotence, and
  new release guards.
- Prove a retained pre-release binary can open the upgraded database, create an
  unassigned task, and edit an ordinary field without clearing a release ID.
  Database guards must keep it from violating frozen released work.
- Extend portable export/import to format v2 with releases and task release
  references. New binaries import both v1 and v2; v1 archives import every task
  as unassigned. Validate complete ID remapping and transactional rollback on
  an invalid cross-project reference.
- Before deployment, verify a pre-upgrade production backup, migration against
  populated restored data, and compatibility with each retained rollback
  binary. Binary rollback never restores the database or discards post-upgrade
  writes.

## Delivery slices

### 1. Release persistence and invariants

- Add the migration, release/task store types, CRUD/lifecycle operations,
  batched task release enrichment, summaries, and events.
- Integrate create/patch assignment and frozen-release lifecycle checks.
- Upgrade portability to v2 while retaining v1 import.
- Prove populated migration, retained-binary behavior, concurrency, integrity,
  and rollback compatibility.

### 2. Release API, filters, and queue

- Add scoped, versioned release routes and stable errors.
- Add task, issue, My Work, search, and saved-view release filters.
- Implement the dependency-aware, snapshot-safe work queue without bulk
  mutation.
- Update OpenAPI and contract tests, including project-ceiling redaction and
  pagination invalidation.

### 3. Release planning UI

- Add the project Releases page, task/bug assignment, board chips, URL filters,
  saved views, and Roadmap links/rollups.
- Preserve dirty drafts and existing board pagination/order behavior.
- Cover empty/unassigned/released states, project switching, online/offline
  behavior, mobile sizing, keyboard use, and screen-reader announcements.

### 4. Agent release-work workflow

- Add release discovery and work-queue client commands plus an `unclaim` alias.
- Teach the Helm skill the single-claim refresh loop and precise stop rules.
- Test ambiguity, own/foreign claims, tasks without dependencies, dependency
  ordering, queue invalidation, cross-release conflicts, and handoff output.

### 5. Integrated release gate

- Exercise create release → assign work → filter → execute dependency order →
  ready-to-release → complete/reopen across API, CLI, and browser tests.
- Run the full Go, web, generated OpenAPI, portability, deployment-security,
  bundle, and live validation gates.
- Rehearse the populated-data upgrade and retained-binary rollback before a
  separately authorized deployment.

Slices 1 and 2 are sequential. UI and agent workflow can proceed in parallel
after the API contract is fixed. The integrated gate depends on all four.

## Acceptance gate

The feature is ready when all of the following are true:

- Existing tasks remain unassigned and unchanged after migration.
- A task/bug can be assigned, reassigned, cleared, displayed, searched, and
  filtered by one project-local release with version and scope enforcement.
- Release progress distinguishes direct membership from required dependency
  closure and surfaces cross-release conflicts and completed-task checklist
  warnings. A project `require` policy still prevents the underlying task from
  completing; a `warn` policy does not silently hide the warning.
- `release-work` returns tasks with no prerequisites as well as dependency-ready
  tasks, never recommends a manually blocked or foreign-claimed task, and
  invalidates stale queues.
- The Helm skill can work a multi-task release one claim at a time and produces
  a correct ready, waiting, or handoff outcome.
- Completing a release is explicit, transactional, and impossible while any
  required work is incomplete.
- Released membership survives task reads, portable export/import, deployment,
  and retained-binary rollback without a database restore.
- Browser behavior is accessible and responsive, and all generated contracts
  and full repository checks pass.

## Out of scope for v1

- Mapping releases to Git tags, commits, deployment SHAs, changelogs, or a CI
  deployment action.
- One task belonging to multiple releases or retaining a full membership
  history after reassignment.
- Automatic release assignment from parents, labels, dates, or dependencies.
- Release-to-release dependency edges; conflicts are derived from task edges.
- Cross-project releases or cross-project task dependencies.
- Automatic release completion, bulk claims, background agents, scheduling,
  budgets, notifications, or approval workflows.
