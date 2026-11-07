# Skill publication and lifecycle updates

The canonical repository is Gitea; GitHub is its public mirror and the installed
skill's distribution source. Publish reviewed source changes to both. The
updater follows `KanterLabs/helm` **main**, not beta, a feature branch or a PR.
A pending PR does not update agents. Publishing this skill does not deploy an
application or change an installation's credentials.

## What updates, and when

1. The installed global session policy asks the agent to run
   `python3 scripts/update_skill.py` before substantive work. That policy injects
   instructions; it does not itself fetch GitHub or run the updater.
2. The updater checks the remote main revision. A matching `.source-revision`
   yields `updated:false` without cloning. Otherwise it fetches a sparse copy of
   `skills/helm` and replaces the complete installed directory atomically.
3. The package includes `SKILL.md`, references, all split `helm_*.py` helpers,
   `helm_hook.py`, its installer and the updater itself. Later hook invocations
   execute the new script at the existing stable command path.
4. The updater reconciles lifecycle hook entries on both update and no-op paths.
   It preserves unrelated hooks and does not edit `config.toml` or trust hashes.
   If reconciliation changes `hooks.json`, its notice asks for Codex `/hooks`
   review. A script-only update with identical hook configuration does not
   rewrite that file.

An already running agent may have read the previous instructions or imported
old Python modules. New sessions load the new skill. Run the updater explicitly
and reload instructions/restart a long-lived helper when an immediate refresh
is needed; a repository push is not a broadcast into running processes.

## Ownership boundaries

| Item | Managed by this updater? |
| --- | --- |
| Installed `skills/helm/**`, including lifecycle scripts | Yes, from GitHub main |
| Helm entries in local `hooks.json` | Reconciled additively |
| Existing credential files and task/session state outside the skill | Preserved |
| `~/.codex/hooks/global_session_policy.py` and its policy modules | No; separate local installation |
| Other skills, unrelated hooks and Codex trust configuration | No |
| Helm servers, deployment webhooks and GitHub Actions jobs | No |

Keep a global bootstrap/update policy separate from task workflow detail.
Screenshot commands, safe capture guidance and environment authentication live
inside the skill so the normal package update distributes them. Lifecycle hooks
recover task metadata and quietly refresh liveness; they never take screenshots,
upload files, provision credentials or manufacture progress.

## Verification

```sh
python3 scripts/update_skill.py
python3 scripts/helm.py comment --help
python3 scripts/helm.py attachments --help
python3 scripts/install_hooks.py
```

The second updater run should report `updated:false` for the same revision.
Review any installer change notice; do not rewrite trust configuration to hide
it. If fetching fails, retain the installed copy and report the failure.

Repository maintainers can run the process-level publication rehearsal:

```sh
python3 test/skill-updates/run.py --receipt /tmp/helm-skill-update.json
```

It runs an old installed updater against a local Git fixture, publishes the new
package, imports the split screenshot CLI and updated hook, and verifies a
remote-check-only no-op. It checks that unrelated hooks, credentials, session
state and trust configuration survive. The receipt contains no real credential
or private task content. This rehearsal does not publish source; follow it with
an actual update after GitHub main contains the reviewed change.

## Focus startup and recovery

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
