# Luna project intelligence plan

## Product outcome

Use the existing actor-isolated Codex integration with `gpt-5.6-luna` at low
reasoning effort to put the projects that need a human's attention first and
to explain why. The same bounded metric snapshot can also power a small set of
high-value workspace insights: delivery risk, stalled work, unblock leverage,
flow anomalies, and planning-data gaps.

This is a recommendation feature. It must not move tasks, change project
metadata, archive projects, or persist a shared canonical project order.

## User experience

Add a project-order control with three modes:

- **Smart**: favorites stay pinned, then Luna-ranked projects appear in stable
  attention bands (`Act now`, `Watch`, `Steady`, and `Quiet`).
- **Recent**: preserve today's favorites, recents, then alphabetical behavior.
- **A–Z**: favorites stay pinned and all remaining projects sort by name.

Smart mode is a per-human preference. The sidebar, project switcher, command
palette, and all-project Roadmap use the same ordered project IDs. Search
results remain relevance-sorted rather than smart-sorted.

Changing a displayed order must not navigate, replace the active project, or
change the existing root-route fallback project. New projects and favorite
toggles are re-ranked locally without waiting for a page reload.

Each smart-ranked project can show one compact, accessible reason such as
“2 handoffs need attention” or “Focus due in 4 days.” A later details popover
can show:

- attention band and up to three reason codes;
- the bounded metrics used for the recommendation;
- snapshot age and whether the result came from Luna or the deterministic
  fallback; and
- **Refresh with Luna** and **This was / was not useful** actions.

Smart mode is usable before Codex is connected: it shows a deterministic
metric order and offers connection for explanations and richer insights. A
Luna error, quota limit, timeout, or stale cache never prevents project
navigation.

### Manual invocation for the first release

The MVP never invokes Codex automatically. Selecting Smart mode, loading or
refreshing a page, signing in, changing a project, receiving an event, cache
expiry, or a failed request must not start a Luna turn. The user must press an
explicit **Analyze with Luna** button for every model call.

One button press creates at most one Luna turn. There are no automatic retries,
scheduled/background refreshes, prefetches, polling-triggered calls, or
server-start recovery calls. While a call is in progress the trigger is
disabled, cancellation remains available, and the UI shows that the connected
Codex subscription is being used. The previous cached result and deterministic
order remain usable before, during, and after the call.

Unit, integration, and browser tests use a fake Codex service by default. Any
live-subscription test must be excluded from normal test commands, require a
separate explicit operator opt-in, and be visibly identified as consuming the
connected subscription. Test retries must never repeat a live model turn.

## Decision boundary

Deterministic code owns:

- authorization and the set of visible projects;
- archived-project exclusion and favorite pinning;
- metric definitions and time windows;
- validation, stable tie-breaking, cache freshness, and fallbacks; and
- every mutation (none are allowed in this feature).

Luna owns:

- placing non-favorite projects into attention bands;
- ordering projects within a band when the evidence is meaningful;
- selecting allowlisted reason codes; and
- producing short explanations and cross-project observations.

The model receives aggregates, opaque project IDs, and optional project names
only. It does not receive task titles, descriptions, comments, progress prose,
credentials, account metadata, or unrestricted event payloads. Historical
content is unnecessary for the first version.

## Metric snapshot

Build one authorization-scoped `ProjectIntelligenceSnapshot` for at most 200
live projects. Every field is server-derived at a single `as_of` timestamp.
Use batched aggregate queries; do not call the existing per-project count
queries in a loop.

### Existing signals to reuse

- Project totals: open, completed, and overdue tasks.
- Board state: backlog, ready, active, blocked, and completion ratio.
- Deadlines: overdue and due in the next seven days.
- Priorities: open urgent and high-priority tasks.
- Agent coordination: active claims; working, waiting, verifying, handoff,
  stale, and action-needed pulses.
- Dependencies: tasks with unmet prerequisites and direct dependent counts.
- Hierarchy: blocked children, stale child work, and action-needed children.
- Focus health: target date, completion gap, blocked/claimed counts, checklist
  warnings, required dependency completion, and cross-focus conflicts.
- Issue health: open/untriaged S1–S2 bugs and seven-day reopened bugs.
- Event recency: last material task/project event and rolling created,
  completed, reopened, and progress counts.
- Per-human unread assignment, blocker, mention, and state-change
  notifications grouped by project.

### New derived metrics for the first release

These need no new source-of-truth tables:

- `completed_7d` and `completed_30d` (throughput);
- `created_7d` and `created_30d` (arrival rate);
- `net_open_change_7d` (arrivals minus completions);
- median and p85 completion age over 30 days;
- oldest active-task age and p85 open-task age;
- current blocked-task age from the latest transition event;
- count of tasks whose completion would directly unblock other work;
- last material activity age; and
- share of active work with a fresh progress pulse.

If transition history cannot establish an age, return `null`; never substitute
task `updated_at`, because unrelated edits would make the metric misleading.
Define event metrics explicitly: de-duplicate lifecycle events by task and
qualifying transition, do not count a heartbeat as published progress, and do
not treat a currently reopened task as a current completion. Reuse the stable
disposition/dependency/priority tie-break patterns in the Focus work queue
rather than creating a second incompatible task-ranking vocabulary.

### Later metrics worth introducing

Add these only after the MVP proves useful:

- A compact task-state interval table, updated transactionally with lifecycle
  changes, for accurate time-in-state, cycle-time decomposition, and blocked
  duration across repeated transitions.
- Per-human `last_viewed_at` and bounded view counts, opt-in and used only for
  that human's ordering. Do not export project or actor identifiers as
  Prometheus labels.
- Handoff latency (first handoff pulse to next meaningful human or agent
  action) and waiting duration.
- Focus scope churn (tasks added/removed after planning) and forecast error.
- Bug time-to-triage and time-to-resolution percentiles.
- Recommendation feedback and rank overrides, stored as bounded enums rather
  than free text.

## Ranking contract

Use a closed output schema. A response contains:

```json
{
  "projects": [
    {
      "project_id": "opaque-id-from-input",
      "attention": "act_now",
      "reason_codes": ["action_needed", "focus_due_soon"],
      "summary": "Two handoffs need attention before the near-term focus can finish.",
      "confidence": "high"
    }
  ],
  "workspace_insights": [
    {
      "kind": "unblock_leverage",
      "project_ids": ["opaque-id-from-input"],
      "summary": "Finishing one prerequisite would release several ready tasks."
    }
  ]
}
```

Validation requires exactly one result for every supplied project, no unknown
IDs, enum-only attention/reason/insight values, bounded strings, and at most
five workspace insights. Reject the entire model result on an invalid ID or
shape. The prompt treats the snapshot as untrusted quoted data and forbids
tools, networking, file access, instructions in data, and mutations.

The beta uses the existing Luna kill switch and configured Codex model, fixes
project analysis to low effort, and has a bounded timeout shorter than
interactive task drafting. Separate per-feature operator controls remain a
possible rollout hardening step if the shared kill switch proves too broad.

The existing per-actor Codex session serializes turns. Project intelligence
must be lower priority than interactive task drafting: return a cached or
deterministic result immediately when that actor's runtime is busy, and allow
only one manually requested intelligence run per actor at a time. Do not queue
a request to run later: require the user to trigger it again after the runtime
becomes available.

## Stability and fallback

The deterministic fallback computes an attention tuple from, in order:

1. waiting, handoff, stale, or otherwise action-needed work;
2. overdue work and near-term Focus delivery risk;
3. severe bugs and dependency-unblock leverage;
4. blocked/active WIP age and recent net-open growth;
5. recent material activity; and
6. lowercase project name plus stable ID.

Favorites remain outside the model order. Preserve the prior Luna order when
attention bands and material reason codes are unchanged; this avoids jitter
from small score changes. Cache by actor, input snapshot hash, prompt/schema
version, and model settings. Serve stale-but-valid results with a visible
timestamp until the user explicitly requests another analysis. Cache expiry
never triggers a model call. Results from a different actor or a different
authorization ceiling must never be reused.

## Other useful Luna insights

Prioritize insights that combine several trustworthy signals and lead to a
specific human decision:

1. **Attention queue** — which projects need intervention now and the metric
   evidence for each.
2. **Unblock leverage** — projects where completing one prerequisite or
   resolving one handoff releases the most downstream work.
3. **Delivery risk** — a near-term Focus has too much incomplete, blocked,
   stale, or dependency-bound scope for its target date.
4. **Stall detection** — active WIP is aging while progress pulses and material
   events have stopped.
5. **Flow anomaly** — arrivals are rising, throughput is falling, reopen rates
   jumped, or blocked WIP deviates materially from that project's baseline.
6. **Agent attention** — waiting/handoff/stale work is concentrated on one
   project or active work lacks fresh pulses.
7. **Planning hygiene** — high-priority work lacks a Focus, due date,
   acceptance checklist, owner, or dependency information.
8. **Quiet-project review** — a project has no open work or material activity
   and may be a candidate for manual archive review.

Defer automatic estimates, automatic archiving, automatic task movement, and
people-performance scoring. The available data cannot support those actions
reliably or safely.

## API and storage shape

Add a human-session-only API surface:

- `GET /api/v1/project-intelligence` returns the best cached or deterministic
  result immediately and is guaranteed never to invoke Codex.
- `POST /api/v1/project-intelligence/analyze` is the only model-invoking route.
  It requires a direct human-session request from the explicit UI trigger,
  starts at most one turn, and returns the completed result or a stable
  non-retried outcome.
- A later `POST /api/v1/project-intelligence/feedback` endpoint can record a
  bounded useful/not-useful signal for a specific recommendation version.

The response carries `as_of`, `source`, `stale`, `snapshot_hash`,
`recommendation_version`, and the ordered project results. Bearer-token agents
are out of scope for the UI recommendation endpoint; the raw aggregate store
method still accepts an explicit project ceiling for reuse and tests.

Recommendations remain an in-memory, per-actor bounded cache; deterministic
results are the restart fallback. Luna execution metadata is retained in the
shared private run ledger for debugging, capped at 200 rows per actor and
excluding prompts, model output, project/task prose, and account metadata. Any
future durable recommendation or feedback cache must use an additive
migration and the same populated-database and retained-rollback checks.

## Privacy-safe operational metrics

Add aggregate metric families with bounded labels only:

- recommendation requests by `source` (`luna`, `cache`, `fallback`) and
  `outcome`;
- Luna refresh duration and queue/busy skips;
- schema-validation failures, usage-limit outcomes, and timeouts;
- cache hits, stale serves, and refreshes; and
- useful/not-useful feedback totals by insight kind.

Never label telemetry with actor ID, project ID/key/name, model output, prompt
content, or Codex account metadata. Structured logs follow the same rule.

## Rollout and evaluation

1. Ship the aggregate snapshot and deterministic ordering behind a feature
   flag; compare its output to hand-reviewed fixtures.
2. Add the low-effort Luna ranker in manual preview mode and never call Luna
   without the button.
3. Beta-test Smart as the device-local default with a one-click Recent/A–Z
   escape hatch; add bounded feedback after the offline gate passes.
4. Revisit the default after usage shows whether rankings stay useful and
   stable.

The offline fixture gate covers empty, quiet, active, deadline-heavy,
blocked, agent-heavy, issue-heavy, and noisy workspaces. It requires 100%
schema validity and ID membership, deterministic permission filtering, correct
high-risk attention bands, stable fallback ordering, no cross-actor cache
reuse, and correct cancellation/timeout/limit classification. Integration and
browser tests cover disconnected Codex, cached results, stale refresh, mode
switching, favorites, project search, keyboard navigation, narrow viewports,
and a Luna failure during an interactive draft. They also prove that page
loads, Smart-mode selection, cache expiry, API reads, failed analyses, and test
retries never invoke Codex, while one explicit trigger invokes it exactly once.

## Implementation slices

1. **TC-231 — Project metric snapshot and deterministic order** — batched store query,
   typed API contract, fixed windows, fallback ranker, authorization tests,
   and populated-database query/performance coverage.
2. **TC-232 — Low-priority Luna ranker** — prompt/schema validation, per-actor
   manual single-flight coordination, cached fallback, separate low-effort
   config, privacy-safe operational metrics, and fixture evaluation.
3. **TC-233 — Smart project navigation** — per-human mode preference, shared ordering
   across navigation surfaces, an explicit Analyze with Luna control, reason
   UI, freshness/source states, accessibility, and browser coverage.
4. **TC-234 — Workspace insight panel** — allowlisted cross-project insights with metric
   evidence, task/project deep links where supported, and no mutation actions.
5. **TC-235 — Feedback and rollout controls** — bounded feedback, preview/opt-in flags,
   offline evaluation command, dashboards/alerts, and operator documentation.
