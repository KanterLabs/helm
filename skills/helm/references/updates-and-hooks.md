# Installed skill updates and lifecycle hooks

The checkout and the installed skill can be different revisions. The updater
fetches only `https://github.com/KanterLabs/helm.git`, branch `main`, and
installs its `skills/helm` directory. A beta-only push does not reach ordinary
session updates. Follow the repository release process: canonical Gitea source
first, public GitHub mirror afterward. Do not edit trust hashes or reset task
data to make an update appear successful.

From the installed skill directory:

```sh
python3 scripts/update_skill.py
python3 scripts/install_hooks.py
```

The updater reports `updated` and `revision` as JSON; `.source-revision`
records the installed source commit. It checks remote `main`, validates the
skill, installs a changed revision, and reconciles hooks. When the installed
revision already matches, it returns `updated: false` but still reconciles
hooks. Hook reconciliation is best effort: an update can succeed while warning
that hooks need manual review. Check that warning independently.

The installer merges the Helm commands into `hooks.json`, preserving unrelated
commands and policy. It does not edit `config.toml` or Codex trust hashes. If
it changes hook configuration, review and trust the command in Codex `/hooks`;
a no-op needs no new review. SessionStart matches startup, resume, clear, and
compact. Restart or recover a session to verify the installed behavior rather
than relying only on checkout tests.

## Expected startup and recovery

Each SessionStart emits one static Focus discovery reminder under 400
characters, including fresh sessions without a tracked task, missing session
identity, or unreadable/malformed private state. This reminder requires no
project lookup, credentials, state extension, or API call. It preserves the
user's explicit task scope and points to skill guidance rather than choosing
a Focus automatically.

Mapped sessions also recover safe task identity, operation ID, state, and
checkpoint counts. Current bounded agent notes are fetched from Helm; note
bodies are never persisted in hook state. If refresh fails, the hook warns
without exposing the private error and asks the agent to check the task.
Compaction recovery includes the same single reminder alongside that context.

PostToolUse and PreCompact may refresh due liveness for an eligible mapped
task; they never perform Focus discovery/review or generate review comments.
Heartbeat is throttled, does not renew the claim, and cannot replace a
meaningful structured progress update. Before the initial snapshot,
PostToolUse may remind the agent to publish it without sending a heartbeat.
Waiting/handoff sessions are not kept alive automatically. Existing stop
checks require a durable outcome for active work and fail open when recovery
or the service is unavailable.

## Verify a publication

Record old/new installed revisions, the updater JSON result, a no-op second
update, and preserved unrelated hook entries. Replay a fresh SessionStart and
compaction recovery with sanitized synthetic state, then inspect their JSON
context and ordinary heartbeat/stop behavior. Keep receipts free of task text,
credentials, prompts, and private user content. If rollback is required,
redeploy the previous known-good skill source through the established release
path and verify the installed revision and hooks again; rollback does not
restore or reset the task database.
