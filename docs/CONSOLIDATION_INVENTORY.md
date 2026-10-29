# Consolidation dependency and preservation contract

Phase 0, TC-315, verified 29 October 2026. This contract is the starting point
for the [product consolidation plan](PRODUCT_CONSOLIDATION_PLAN.md).

Execution branch: `feat/helm-consolidation`, based on private beta `3f0eb583…`.
The unfinished audit branch/PR remains separate. Its unreleased Sol profile and
v2 corrections are not silently included in this branch.

## Live baseline and decisions

| Lane | Current revision | Schema | Authentication | Active resources |
| --- | --- | --- | --- | --- |
| Beta | `3f0eb583e18f2872a11dd963f7262b37d0bb5847` | 40 | Tailnet | One email intake/inbox, one public endpoint and one generic/Coolify webhook |
| Production | `c0f119cf330811389dfd38fbcbd7269dbf6680d5` | 35 | Tailnet | One email intake/inbox and one public endpoint |

Both running images were present and healthy. Both email lanes have received
real deliveries; beta's retained webhook also has delivery history. Removing
provisioning UI is therefore not permission to stop endpoint Resume/runner or
email delivery. New-setup retirement can proceed first; existing delivery stays
until a tested replacement or explicit operator shutdown decision exists.
TC-326 owns that drain and its final decommission gate. Generic/Coolify
webhooks and existing authentication/ingress remain supported.

Aggregate metadata, exact image IDs, per-table counts/hashes and connector
usage are recorded locally in
`.artifacts/consolidation-execution/preservation-baseline.json`. No task bodies,
account content, token values or provider secrets leave the VM in that receipt.
Source inspection does not establish every external API consumer; owners must
check callers during each documented deprecation window.

## Preservation and rollback packet

Fresh online SQLite and protected runtime/config archives were created for
both lanes under their owner-only backup directories with prefix
`consolidation-20261029T232739Z`. Integrity and FK checks passed. Each SQLite
backup was reopened and copied into a fresh temporary database; every recorded
table count/hash matched and a temporary probe table was written/read there.
The temporary verification copies were removed. The retained backups were
rehash-verified after the copy check. Live databases were opened read-only.

The packet covers all current tables, including actors/project ceilings,
tasks, claims, bug data, ticket membership/aliases, comments, labels,
checklists, hierarchy, dependencies, Focus, source/repeat history, PR and audit
records, and private AI run history/content. Keep current stable IDs and late
writes; do not reset or rebuild the database to simplify a feature cut.

This is SQLite backup/restore-copy verification, not a newly run application
rollback-binary test. Previous retained-image read/write compatibility evidence
is in the TC-311 release-validation handoff. Any new schema change must rerun
the populated upgrade and retained application image against a separate copy.
Before an actual beta or production upgrade, refresh the online backup; this
baseline is not a perpetual pre-deployment backup.

Retain the exact current beta and production image IDs from the packet.
Rollback drains/fences affected jobs and changes the application image, while
keeping the same upgraded data volume. An older database restore is a separate
destructive recovery action, never a normal rollback step.

## Capability, route, data and consumer map

| Planned change | Retained consumers / data | Required boundary |
| --- | --- | --- |
| Remove task-draft UI and generation | `/projects/{id}/task-context`, ordinary creation, Codex account management | `router_authed.go` groups task-context with task-draft; do not delete the shared context builder with the draft route. |
| Remove AI project analysis/ranking | Transparent deterministic project ordering and progress | `app/projects.ts` loads intelligence at bootstrap; define a deterministic replacement before removing its endpoint. `observability.go` also references its timeout constant. |
| Remove both general AI features | Audit evidence inference, per-owner Codex credentials, account-generation fencing, cancellation and kill switch | `CodexTaskDrafter` is shared by drafting and project analysis; the independent `codexruntime.Manager` is shared with audits. Keep the latter and shared run types. |
| Simplify Codex settings | Owner-scoped historical runs/steps/input/output and personal audit account connection | `/codex/runs` dispatches before runtime availability; preserve historical feature labels and access even after generation stops. Never drop the content tables as cleanup. |
| Park audits | Authorized existing findings, run history, pinned evidence and portability | Hiding creation/corrections must not delete migrations 038–040 records or break generic audit read routes and v3 portable history. |
| Hide email/provisioning setup | Already active email connectors, public webhook endpoint runtime, source facts and historical receipts | `cmd/helm/main.go` builds the endpoint manager and calls `Resume`; this boot path supports existing traffic after restart. Keep it while any connector needs it. |
| Retire setup/disable UI | An explicit maintenance/decommission path for stored provider resources | Intake references endpoints; receipts cascade on intake deletion. Existing disable guards refuse endpoints used by email. Removing administration without a replacement strands resources. No automatic provider deletion. |
| Keep webhook intake | Hooks-only listener, secret verification, dedupe and queue task identities | The unauthenticated secret hooks surface is separate from human APIs. Keep listener restrictions and do not redirect payload POSTs through UI aliases. |
| Remove offline task snapshots | Session/permission guards, unsaved drafts, update consent and installed-client assets | Offline storage cleanup also runs on logout, 401/403 and project revocation. Preserve those privacy obligations when removing snapshot presentation. |
| Keep installed mobile app | `/sw.js`, manifest/icons, waiting-worker handshake and old hashed assets | Keep the exact-controller update bridge and draft guard. Removing the worker wholesale can leave old installed clients stuck on old shells. |
| Merge Intake views | Existing tickets, bug kind, hidden queue project, key aliases, comments and repeat counts | No task duplication, coercion of every request into a bug, or implicit widening of agent queue-project access. |
| Simplify Work/Progress/Focus | Assignment versus claims, reported progress, required dependencies, current release-work API | Reported progress is not process health; task completion and Focus readiness are not deployment proof. |

Verified source entry points:

- `internal/httpapi/task_context.go`, `task_draft.go`,
  `project_intelligence.go`, `github_lifecycle.go`, `github_audit_routes.go`.
- `internal/codexruntime/evidence.go`, `run.go`, account-generation support;
  `internal/httpapi/codex_account.go` and `luna_runs.go`.
- `cmd/helm/main.go`, `internal/publicendpoint/manager.go`,
  `internal/httpapi/public_endpoints.go`, `email_intake.go`.
- `internal/db/migrations/026*`–`028*`, `031*`–`034*`, `038*`–`040*`;
  `internal/store/portable_v3_export.go`.
- `web/src/app/projects.ts`, `session.ts`, `offline.ts`, `pwaGuard.ts`;
  `web/src/lib/offlineBoards.ts`, `api/client.ts`, `pwa.ts`;
  `web/vite.config.ts` and `internal/httpapi/server_assets.go`.

## Compatibility destinations and notices

These are the agreed targets for implementation, not aliases already shipped.

| Existing entry | Consolidation behavior |
| --- | --- |
| `/my-work` and its source/filter state | Work, preserving Mine/Agents intent, Focus filter and selected task |
| `/issues` | Intake with bug scope, preserving project/query/status filters |
| `/tickets` and `queue`, `project`, `q`, `ticket` query parameters | Intake with the equivalent queue and selected task; filed/old keys continue to resolve |
| `/tickets?connect=1` and Cloudflare callback result parameters | Retained connector maintenance/result view during drain; never discard an in-flight setup result silently |
| `/roadmap`, `/p/{slug}/roadmap`, Explore parameters | Progress with the same project scope and Explore state |
| `/p/{slug}/timeline` | Useful retained project activity context or task Activity; no unrelated unfiltered landing |
| `/p/{slug}/focus` and legacy `/releases?release={id}` | Existing Focus identity and selection; current releases-to-focus alias remains |
| `/p/{slug}/audits` and `/{auditId}` | Authorized read-only history while creation/correction capability is parked |
| `/p/{slug}/tasks/{key\|id\|number}` with activity query/hash intents | Same task and requested Activity context; preserve browser history/drawer origin |
| `/search`, shared/saved filter links | Same permitted results; advanced editing can be moved without deleting saved views |
| `/settings`, `/admin`, metrics paths | Existing permission-checked destination or equivalent Operations section |
| `/web/dist/*`, `/sw.js` | Asset/update compatibility, not UI redirects |
| Existing REST task/context/agent/Focus/event APIs | Retained contracts; no silent scope or lifecycle changes |
| Retired generation/setup endpoints | Clear authorized capability/deprecation response after inventory, with no silent model fallback or false delivery success |

UI aliases preserve query, hash, selected record, Back/Forward and unsaved work.
They are not used for webhook or API request rewriting. Authorization remains
the intersection of actor and token project ceilings. A tagged service client
does not become a human merely because the UI or navigation was consolidated.

## Phase 0 result and open operational obligations

The consumer map, current populated baselines and fresh backup/copy evidence
are recorded. This phase changes no product behavior and deletes no resources.

The following remain later acceptance gates, not excuses to stop safe core work:

- TC-326: name a replacement or approve stopping each active email connector
  before runtime/resource retirement. Keep existing delivery compatibility meanwhile.
- TC-327/329: test an actual installed old PWA and physical iPhone; source and
  desktop emulation alone do not prove that transition.
- TC-311: real audit judgments and legitimate owner-human application remain
  incomplete; the consolidation core can release with the capability parked.
- TC-329/330: refresh the backup at deployment time and prove retained binary
  compatibility for the exact schema/revision being promoted.

The read-only source inventory was independently reviewed and its key coupling
points checked by the parent. Aggregate operator receipts are local under
`.artifacts/consolidation-execution/`; full backups remain protected on VM124.
