# In-app notifications and watches

Helm's notification slice is a durable, in-app coordination surface. Migration
021 is additive: it adds `watches`, `notification_preferences`, and
`notifications` without changing existing projects, tasks, claims, comments,
or activity events.

## Watches and inbox

- `POST /api/v1/projects/{project}/watch` follows a project.
- `POST /api/v1/tasks/{task}/watch` follows one task.
- `GET /api/v1/watches` lists the authenticated actor's watches.
- `DELETE /api/v1/watches/{id}` removes an owned watch.
- `GET /api/v1/notifications?unread=true` reads the newest inbox items.
- `POST /api/v1/notifications/{id}/read` marks one item read.
- `PATCH /api/v1/notifications/{id}` with `{"read":true|false}` changes one
  item's read state.
- `POST /api/v1/notifications/read` with `{"all":true}` marks visible unread
  items read, or with `{"ids":[...]}` marks up to 200 visible items read.
- `GET`/`PATCH /api/v1/notification-preferences` reads or updates the active
  categories: `assignments`, `mentions`, `blockers`, and `state_changes`.

Assignment changes, `@actor-id`/`@display-name` mentions, dependency
block/unblock events, and task state transitions fan out to direct recipients
and matching watches. Inbox inserts use `(recipient, dedupe_key)` uniqueness,
so a retried event cannot duplicate an item. Preferences default to enabled
and are checked when an item is inserted.

Bearer reads are project-scoped by the intersection of the token's project
allow-list and the actor's durable `actor_projects` ceiling. A scoped token
with no intersection sees no inbox or watch rows; project-less notification
rows are not exposed to scoped identities. Single, bulk, and mark-all read
mutations apply the same visibility rules.

## Deliberately deferred work

Due-date and claim-expiry reminders are not active in this slice because they
require a safe event-driven scheduler. Rule storage/execution is deferred to
TC-165 (safe event-driven automations). Email, push, webhook, and other
external delivery is deferred to TC-166 (atomic external delivery). No
automation or external-delivery tables, API routes, scopes, or runtime are
included here.
