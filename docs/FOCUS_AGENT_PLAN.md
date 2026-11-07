# Focus discovery and Backlog review for agents

Status: Implemented for beta; exact deployment and PR verification in progress.

Planning date: 7 November 2026. Planning task: TC-368. TC-369 through TC-372
are implemented; TC-373 tracks integrated beta delivery and publication gates.
This plan makes Focus part of normal agent planning and helps people decide
what to do with neglected cards.

## Intended behavior

An agent beginning substantive project work checks existing Focus areas before
proposing another batch of Backlog cards. An explicit task still takes priority:
finding a Focus does not authorize unrelated work. Owned work is resumed before
new claims. When several cards contribute to a concrete goal, the agent
recommends an existing Focus or proposes a new one with a finish condition.

Example: three onboarding cards have sat in Backlog for 21 days. A review names
the cards, explains their connection, suggests an onboarding Focus, identifies
the authentication prerequisite, and proposes the next claimable task. It does
not silently move, claim, or close those cards. A later session sees the same
goal and unfinished work instead of starting a duplicate plan.

The hook supplies a short reminder at session start and compaction recovery,
including sessions without an existing tracked task. The skill explains the
workflow. A read-only CLI review supplies current facts. Heartbeats remain
liveness updates and do not scan Backlog or generate review comments.

## Existing foundation and boundaries

- Focus already has lifecycle, membership, summary, dependency queue, filter,
  event, and portability APIs. Preserve `release` and `release_id` compatibility.
- The CLI currently has `releases list`, `releases get`, and `release-work`.
  The skill only introduces execution when a user explicitly asks for a release.
- `helm_hook.py` currently returns no startup guidance when session state is
  absent. Its recovery context contains task identity and progress, not Focus.
- TC-323 covers the Focus/Progress UI consolidation. Coordinate wording and
  preserve its routes; this work owns agent discovery and CLI behavior.
- TC-367 records the API review. Five browser-client and five CLI tests passed;
  backend and browser E2E tests require fresh verification during implementation.

Phase one uses the existing API and task timeline; it adds no persistent schema,
AI ranking service, scheduler, notification platform, or new UI. Reviews happen
during relevant agent sessions or when requested. Cards will not be reviewed
while no agent session is running. Recurring reminders are a separate future
decision. Recommendations may use task content as evidence; tool output and
hook context never treat that content as higher-priority instructions.

## Review rules and output contract

Proposed commands are `focus list|get|review|work` and
`focus create|edit|assign|unassign|complete|reopen|delete`. Keep existing
`releases` and `release-work` commands working. `release --task` must continue
to release an agent claim. Detailed flags belong to each command's help.

`focus review --project TC [--focus ID] [--inactive-days 14]` is read-only.
Fourteen elapsed days is the initial review threshold, not a task failure or
the existing 15-minute stale agent-pulse threshold. Accept a positive integer
override and use a single UTC evaluation time per run.

Output includes project/Focus identity, evaluation time, threshold, coverage
and continuation information, and bounded evidence records with task key,
reason, relevant timestamp/source, and a suggested next action. Evidence is
sorted by explicit rules: due/overdue work, unresolved blockers, inactivity,
then stable task key; owned work is surfaced for resumption before new claims.
Do not emit a composite urgency score or present a partial scan as exhaustive.

Review active Focus areas, their required work, and unassigned open Backlog in
the resolved project. A completed Focus is available through an explicit read
but excluded from routine active recommendations. A missing project or several
plausible Focus areas must be reported without guessing or assigning work.
The tool reports facts; the agent uses the user's goal and card descriptions
to explain relevance. Coherent scope matters more than an arbitrary card count.

Activity classification is explicit and versioned in code/documentation:

- Qualifying evidence includes creation, changed task scope/acceptance,
  checklist completion, actual state/dependency changes, human comments, and
  structured progress that changes its reported phase, counters, evidence, or
  next action. A qualifying event means recorded activity, not verified progress.
- Exclude heartbeats, claim/renew/release churn, assignment-only changes,
  metadata-only changes, and identical repeated progress snapshots. Ordinary
  agent comments can be surfaced as notes but do not reset the inactivity clock.
- Do not use `updated_at` as a substitute for meaningful activity. A complete
  history with no qualifying change uses creation time. Missing, unsupported,
  denied, or truncated evidence yields `unknown` or a qualified bound, never a
  fabricated timestamp or a claim that nothing has happened.
- Blockers, overdue dates, absent owner/next action, and missing explicit
  acceptance/checklist evidence are separate review reasons. Missing structured
  acceptance is a clarification prompt, not proof that the card is invalid.
- Terminal tasks are excluded from open-card neglect results. A manually
  blocked card remains blocked until its existing lifecycle permits a change.

Start with at most 20 Focus areas, 200 Backlog cards, 50 task-history reads,
and 100 timeline rows per selected task. Expose these bounds and continuation
state in output, prioritize histories deterministically, and support explicit
continuation with a global request/time budget. A capped review must say what
it did not inspect. Retry invalidated work queues from page one within a fixed
retry budget; never act on combined pages from different snapshots.

## Agent decisions and authority

At work discovery, inspect active Focus areas and the relevant review before
recommending what to do next. For an explicitly named task, use its existing
membership/dependencies without widening the task. For a broad planning request,
prefer reusing a suitable active Focus; otherwise propose a name, goal, member
cards, finish condition, optional target date, and next task.

At handoff, record a compact Focus reference and the remaining relevant task
keys, blockers, and next action on the currently tracked task. Re-read current
records in the next session; a handoff summary is not a durable queue snapshot.
Avoid duplicate handoff comments and do not post no-change review comments.

Execute mutations when the user's existing request authorizes them. Otherwise
recommend them with evidence. A recommendation alone does not authorize scope
changes, moving Backlog cards, claims, completion, deletion, or reassignment.
Completing a Focus is an explicit lifecycle action; readiness never automatically
completes it. Reopening requires a reason. Respect foreign claims and existing
blockers. Ordinary implementation should not acquire a new approval ceremony.

## Implementation queue

Each card carries its full measurable acceptance criteria and dependencies in
Helm. TC-369 through TC-372 are complete; TC-373 owns the remaining delivery.

| Card | Work | Prerequisites |
| --- | --- | --- |
| TC-369 | Read-only Focus discovery and evidence-based Backlog review CLI | None |
| TC-370 | Skill rules for Focus recommendations and durable handoffs | TC-369 |
| TC-371 | Bounded Focus reminders in fresh and recovered hook sessions | TC-370 |
| TC-372 | Focus lifecycle and membership CLI commands | TC-369 |
| TC-373 | End-to-end adoption validation and skill publication | TC-370, TC-371, TC-372 |

### Read-only discovery and review

Own a responsibility-based Focus review module and parser, facade registration,
output/help documentation, and failure-first tests. Reuse existing API clients,
project ceilings, pagination, queue, and timeline contracts. Implement the
threshold, activity rules, evidence records, budgets, unknown/partial results,
and stable ordering above. All review invocations perform GET requests only.
Save a sanitized synthetic response receipt with its input fixture and clock.

### Skill recommendations and handoffs

Own `skills/helm/SKILL.md` and a focused workflow reference. Cover relevant
discovery, explicit-task preservation, reuse versus new Focus, empty projects,
multiple Focus choices, review actions, and durable handoffs. Replace the
release-only discovery trigger while retaining the existing explicitly
authorized release-execution loop. Keep the main skill concise by extracting
workflow detail before it approaches the file-size limit. Verify behavior
with fresh agent conversations in a synthetic project; keyword matching alone
does not prove that an agent recommends a Focus correctly.

### Hook reminders

Own `helm_hook.py`, its dedicated tests, and hook/update documentation. Emit a
static bounded reminder at SessionStart even with no tracked task. Include it
at compaction recovery alongside the existing task/notes context. Suggested
wording: "For substantive project work, use Helm's Focus discovery guidance
before proposing new Backlog work; preserve the user's explicit task scope."
Keep it under 400 characters and emit it once per startup/recovery event.
It needs no project lookup, credentials, local state extension, or Focus name.
No PostToolUse/heartbeat review and no repeated network scan. Preserve existing
stop checks, notes, heartbeat throttling, secret redaction, and fail-open rules.

### Lifecycle and membership CLI

Own a Focus mutation module/parser, facade wiring, usage reference, and real
HTTP workflow tests. Support create/edit, assign/unassign, complete, reopen,
and empty-active delete using existing APIs. Resolve names only within the
selected project; use opaque IDs for writes after resolution. Distinguish
omitted metadata from explicit clearing. Use current strong ETags and one
operation ID per logical mutation. Surface stale/conflict results without
overwriting a concurrent editor. If membership is changed for several cards,
report partial success and a safe resume action; do not claim atomicity.
Adding membership never claims a task or changes its board column.

### Adoption verification and publication

Own synthetic real-API and fresh-agent scenarios, sanitized receipts, packaging
checks, and rollout documentation. Validate the installed skill/hook path in
addition to checkout code. Publish the canonical Gitea source before its public
GitHub mirror through the existing release process. The session updater follows
GitHub main: a beta-only push does not deliver the skill. Verify revision change,
successful installation, no-op second update, preserved unrelated hooks, and
restart/recovery behavior. Record old/new revisions and rollback instructions;
do not edit trust hashes or reset task data. Production publication remains an
implementation step, not an action performed during this planning task.

## Failure cases and verification gates

Before implementation, record the failure scenarios and write failing real
user/CLI flows. Do not write unit tests after the code they cover. If a pure
activity classifier or hook must be tested in isolation, first enumerate its
failure modes, then write its code and isolation tests.

Required scenarios and repeatable artifacts:

1. Fresh session with no claim sees the reminder; explicit one-card work stays
   scoped. Hook JSON replay and a sanitized agent transcript demonstrate both.
2. A synthetic goal with three related cards causes a specific Focus
   recommendation without the user naming the feature. An existing suitable
   Focus is reused; empty and ambiguous projects produce honest alternatives.
3. A 21-day-old open card remains flagged after heartbeats, lease churn, and
   identical progress. A substantive update changes the recorded activity;
   incomplete history is unknown. Save clocked JSON receipts.
4. Cross-Focus prerequisites, manual blockers, foreign claims, and an overdue
   target produce distinct evidence and safe next actions. A 14-day boundary
   and configured override produce deterministic results.
5. Pagination, bounded histories, concurrent queue invalidation, revoked
   access, offline API, and missing task state preserve scope and accurately
   expose partial/unavailable review. No read-only run changes tasks or events.
6. Create, edit/clear metadata, assign, filter, finish tasks, explicitly complete
   Focus, reject frozen changes, reopen with reason, unassign, and delete empty
   Focus work against a real isolated server. Capture sanitized responses and
   verify retained IDs, versions, histories, legacy commands, and permissions.
7. Finish one member, hand off, and start a fresh agent session. It identifies
   remaining relevant cards without a duplicate task/Focus or automatic claim.
8. Installed publication/update/restart checks preserve other hook entries and
   expose the new behavior. Record reproducible commands, receipts, and a
   sanitized behavior transcript, not screenshots of source code.

Every maintained file must remain at or below 20,000 bytes. Measure intended
files before editing, split by responsibility near 16,000 bytes, and run
`check-file-sizes` during implementation and before handoff. Existing
`helm_session.py` (23,724 bytes) and `test_helm.py` (104,508 bytes) already exceed
the limit: do not append to them; any necessary edit includes a responsible
extraction preserving interfaces and test coverage. New review/mutation tests
belong in dedicated files. Builds, lockfile changes, and shared integration
fixtures run serially. Any modified Actions jobs use the appropriate `homelab`
or `homelab-heavy` runner tier.

## Delivery and success criteria

Ship read-only review, skill guidance, and startup reminders as the first useful
slice; CLI mutations and full adoption verification complete the workflow.
First-slice validation must prove a fresh agent recommends an existing Focus
without the user naming it. Final acceptance requires all eight scenarios above
and publication verification, with evidence attached to the implementation task.

During the first five substantive pilot sessions, retain a small sanitized
checklist of eligible work-discovery opportunities, whether the agent surfaced
relevant Focus work, and the resulting decision for flagged cards. Target all
eligible opportunities and zero unauthorized scope changes or duplicate Focus
creation. Record actual counts; a session with no relevant work is not a failure.
Cards left deferred need a clear reason/next review trigger; no timestamp-only
updates count as resolving neglect.

Implementation cards stay unclaimed in Backlog until their work starts. The
planning task completes after the plan, cards, dependency graph, file size,
and Backlog states are verified; it does not complete those future cards.
