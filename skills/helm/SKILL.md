---
name: helm
description: Track substantive agent work, goals, progress, blockers, and completion in Helm. Use at the start of multi-step implementation, deployment, CI, maintenance, research, or other project work that should remain visible to Shane; keep the Helm task current throughout the work. Do not use for quick factual answers or casual conversation.
---

# Helm

Use Helm as the durable project record for substantive work. The chat plan is temporary; the Helm task is the shared source of truth humans and later agent sessions can inspect. Existing project and task IDs, including the `TC` project key, remain valid during the migration.

## Product knowledge: v0.2.0

Helm v0.2.0 shipped on 2026-10-11 with Tickets, HTTP webhook and email
intake, and guided Cloudflare public access. Read
[references/v0.2.0.md](references/v0.2.0.md) when handling tickets,
integrations, public access, or upgrades from v0.1.1. New intake lands in
the built-in Tickets queue; agent credentials need an explicit project grant
to see it. Ordinary project work still follows the task lifecycle below.

## Start

At session start, run `python3 scripts/update_skill.py` from the installed skill.
It follows GitHub `main`, installs the complete skill when the revision changes,
and reconciles lifecycle hook entries even on a no-op. See
[updates and hooks](references/updates-and-hooks.md) for publication, restart and
trust boundaries. A beta branch push alone does not update installed copies.

1. Run `python3 scripts/helm.py projects` and match the current repository or workstream to an existing project. Do not silently create a project or put work in an unrelated project.
   For relevant substantive project work, read
   [references/focus-planning.md](references/focus-planning.md) and inspect active
   Focus areas before proposing new Backlog work, even when the user has not
   named Focus. Resume owned work first. An explicit task keeps its existing
   scope; discovery does not authorize another workstream.
2. Search for an existing open task before creating one. Preserve the existing project key and task IDs when resuming work:

   ```sh
   python3 scripts/helm.py tasks --project TC --query "short task title"
   ```

3. Resume and claim the matching task:

   ```sh
   python3 scripts/helm.py resume \
     --task TC-1 \
     --operation-id "stable-session-local-id"
   ```

   Or start one with a concrete goal and measurable checkpoints:

   ```sh
   python3 scripts/helm.py start \
     --project TC \
     --title "Publish Helm skill" \
     --goal "Ship a validated public skill and session updater" \
     --step "Validate the skill" \
     --step "Verify the updater" \
     --operation-id "stable-session-local-id"
   ```

   Resume is a claim-and-activate operation: the task must be claimed by this
   session and placed in the project's Active/In progress column before work
   continues. Keep the returned task key. Mention it in normal progress
   updates when useful.

4. On a machine where the lifecycle hook is not already installed, run
   `python3 scripts/install_hooks.py`. The installer merges only the Helm
   command into `hooks.json`, preserves existing policy and commands, and
   never edits Codex trust hashes or `config.toml`. If it reports a change,
   review and trust the new command once in Codex `/hooks`; a later no-op does
   not require another review.
   Read [references/updates-and-hooks.md](references/updates-and-hooks.md) when
   updating the installed skill or checking startup/recovery behavior.

For planning work that should remain unclaimed in Backlog, create a separate,
implementation-sized card with acceptance criteria:

```sh
python3 scripts/helm.py backlog \
  --project TC \
  --title "Show live agent progress on task cards" \
  --goal "Make active agent work understandable without opening activity" \
  --step "Show agent, phase, checkpoint count, and last update" \
  --operation-id "stable-planning-item-id"
```

Do not use `backlog` for the work currently being performed; the current
workstream still needs one active, claimed task.

If the correct project is missing or credentials do not permit the work, report that clearly and continue the underlying task when safe. Do not widen token scopes or create credentials without Shane's authorization.

## Share visual evidence

For UI changes and visual verification, read
[references/task-images.md](references/task-images.md). Humans can attach images
in the task comment composer; agents can use `comment --image`, structured
`progress --image`, or the `attachments` commands. Inspect and redact the actual
capture before upload. The uploader owns secret removal; Helm does not claim to
scan image pixels for secrets. Use synthetic screenshots for repeatable tests.
Confirm the configured environment supports images and use its own credential;
production and beta tokens are independent. A local test screenshot is not a
live task attachment: upload, publish and verify the task before sending its
Activity link. Hooks refresh metadata and do not capture or upload screenshots.

## Keep progress current

Post only meaningful milestones, decisions, validation results, and scope changes:

```sh
python3 scripts/helm.py progress \
  --task TC-1 \
  --state working \
  --phase "Updater" \
  --completed 1 \
  --total 3 \
  --message "Skill validation passed; installing the session updater next" \
  --next "Verify a no-op update" \
  --checkpoint-ref "skills/helm/SKILL.md" \
  --checkpoint-ref "skills/helm/scripts/helm.py" \
  --checkpoint-ref "skills/helm/scripts/test_helm.py" \
  --operation-id "stable-session-local-id"
```

Do not post narration, secrets, raw logs, customer data, tokens, local credential paths, or prompt contents. Progress belongs at meaningful checkpoints and before a long external wait, after compaction recovery, and after a material phase completes. Do not turn routine polling or hook heartbeats into comments.

Use the structured options as the canonical progress API so the in-progress
experience can show a concise agent pulse without forcing Shane to read the
full activity log. Structured progress is sent as a claimed, version-checked
task update; when `--state` is omitted, it defaults to `working`. States are
`working`, `waiting`, `verifying`, and `handoff`. Counts must be measurable
checkpoints, not an invented percentage or ETA, and `--checkpoint-ref` can be
repeated for relevant files, tests, or other safe evidence references. Keep
the message to the result or decision and `--next` to the next concrete
action. Use plain `--message` only for backward compatibility when structured
context is unavailable; that form remains a task comment.

The installed lifecycle hook may refresh a claimed task's agent-work liveness
quietly; it does not renew the claim lease. A heartbeat must not create a
comment or activity event, and it must not stand in for a meaningful
structured update. After a session resumes from
compaction, restore the saved task mapping, inspect the safe metadata, and
post the next meaningful checkpoint before continuing; call `resume` only when
the task or claim actually needs reactivation (or when beginning a genuinely
resumed workstream). Local hook state may contain only safe recovery metadata
such as the task key, operation ID, checkpoint counters, and timestamps; it
must never contain tokens, prompts, task content, comments, raw tool output,
or credential paths.

## Preserve bounded agent notes

Use active agent notes only for verified, reusable task knowledge that prevents
another agent from repeating a failed approach or missing a non-obvious
constraint. Notes are not progress, next steps, raw logs, speculation, or a
replacement for the card description. Never put secrets, prompts, credentials,
customer data, or unredacted tool output in a note.

Each task has at most six active notes. Prefer updating an existing note over
adding a similar one, and resolve a note as soon as it is obsolete:

```sh
python3 scripts/helm.py notes list --task TC-1
python3 scripts/helm.py notes add --task TC-1 \
  --category rejected_approach \
  --body "Timestamp joins were rejected because legacy rows can share timestamps." \
  --evidence internal/store/timeline.go
python3 scripts/helm.py notes update --task TC-1 --note NOTE_ID \
  --body "Updated verified finding" --evidence internal/store/timeline_test.go
python3 scripts/helm.py notes resolve --task TC-1 --note NOTE_ID
```

Categories are `known_issue`, `rejected_approach`, `constraint`, and
`workaround`. `start` and `resume` return current active notes. On ordinary
session recovery and after context compaction, the lifecycle hook re-fetches
the active notes from Helm and injects their bounded contents into context. It
never stores note bodies in local hook state or creates notes automatically.
If refresh fails, treat the hook warning as an instruction to run `notes list`
before continuing.

## Coordination inbox and watches

Use notifications and watches when work needs durable coordination beyond the
current claimed task. Do not poll the inbox from lifecycle hooks or treat an
unread item as authority to widen the current task.

```sh
python3 scripts/helm.py notifications list --unread
python3 scripts/helm.py notifications read --notification NOTIFICATION_ID
python3 scripts/helm.py notifications preferences --mentions false
python3 scripts/helm.py watches list --project TC
python3 scripts/helm.py watches add --task TC-42
python3 scripts/helm.py watches remove --watch WATCH_ID
```

Watch only the task or project whose follow-up is useful, and remove stale
watches rather than creating duplicate polling work. Notification read-state,
preference, and watch mutations use replay-safe operation IDs. Reuse the same
`--operation-id` only when replaying the same logical mutation. Project-scoped
credentials see only the intersection of their token and actor project
ceilings; an empty inbox does not prove that no other project has activity.

## Read-only Board Audit

When the user or an explicitly delegated task requests a Board Audit, read
[references/board-audit.md](references/board-audit.md) before inspecting the
board, submitting findings, reconciling approved findings, or performing a
requested batch board change. Audits are read-only and never run from hooks.

## Release-bound execution

When the user explicitly asks to work on all tasks needed for a release, read
[references/release-execution.md](references/release-execution.md) before
resolving the release or taking a lease. Use its bounded queue and existing
task lifecycle; product release completion remains a separate explicitly
authorized action.

## Protect persistent data

When work changes storage, schemas, migrations, backup/restore, or deployment,
make data preservation an explicit goal and checkpoint. Inspect the existing
upgrade and recovery path before editing it. Do not reset, recreate, replace,
or restore over user data as a normal upgrade strategy.

Prefer transactional, retry-safe, additive migrations that remain compatible
with every retained rollback binary. Verify the change against a populated
database, including integrity, relationships, stable identifiers, and expected
row counts. Confirm a verified pre-upgrade backup exists before production
migration and that rollback does not silently restore an older database or
discard writes. Treat a database restore as a separate destructive recovery
operation requiring explicit authorization, an exact backup target, and a
pre-restore snapshot.

Gate a deployment on `/readyz`, not liveness alone. Pending embedded migrations,
inspection failures, writer-lock failures, or capacity failures stop promotion.
An `unknown` newer additive migration is a compatibility warning and may remain
ready for a retained rollback binary; record the warning instead of treating it
as an instruction to restore or recreate the database. Keep `/metrics` on a
direct loopback or separately authenticated private monitoring path and never
publish task text, credentials, IDs, or unbounded route labels through metrics.

## Finish

Complete the task only after the requested outcome and proportionate verification are finished:

```sh
python3 scripts/helm.py complete \
  --task TC-1 \
  --comment "Published and verified the public skill and session updater" \
  --operation-id "stable-session-local-id"
```

If work cannot proceed, move it to Blocked with a specific actionable reason:

```sh
python3 scripts/helm.py block \
  --task TC-1 \
  --reason "Repository visibility change requires organization approval" \
  --operation-id "stable-session-local-id"
```

Before ending a session, leave exactly one durable outcome: complete the task,
block it with an actionable reason, or publish a structured `handoff` or
`waiting` update with the next action. A final prose message or a quiet hook
heartbeat alone is not a handoff. Do not mark a task complete merely because
the current chat is ending; leave safe recovery metadata for the next session
when work remains.
When that work belongs to a Focus, include its ID, remaining relevant task keys,
blockers, and next action in the current task's handoff; re-read live records
on resumption. See the Focus planning reference for bounded review and safe
continuation.

## Authentication and failures

The helper reads canonical `HELM_URL`/`HELM_TOKEN` settings first. During migration it accepts `TC_ROADMAP_URL`, `TC_ROADMAP_TOKEN`, and `ROADMAP_TOKEN`, plus the existing mode-`0600` credential file at `~/.config/tc-roadmap/credentials.json`; conflicting canonical and legacy values fail closed. Read [references/authentication.md](references/authentication.md) only when configuring or troubleshooting access.

The helper writes JSON results to stdout and sanitized errors to stderr. Treat `409` as a concurrency signal: re-read the task and preserve the other actor's work. Never paste credential values into commands, chat, task fields, logs, or source control.

## API reference and workflow client

Read the bundled [agent API reference](references/API_CONTRACT.md) before
calling a less familiar endpoint. It summarizes authentication, exact ETags,
idempotency, pagination, guarded bulk mutations, notifications, watches,
readiness, metrics, dependency graph, timeline, event, and bug lifecycle
contracts so a newly installed skill does not depend on a checkout of the Helm
repository's full documentation.

The safe read-only identity probe is:

```sh
python3 scripts/helm.py auth-check
```

Additional client workflows are available without hand-written HTTP. Read
[workflow commands](references/workflow-commands.md) for dependency, activity,
bug, watch and notification commands and their replay rules.
