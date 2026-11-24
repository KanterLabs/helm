# Agent work admission

On supporting Helm servers, `start` and `resume` each admit one task in a
single server mutation. The mutation checks task eligibility, the project's
Active capacity, and whether the agent already owns active work. An agent
can have one active assignment across projects. Follow a denial's guidance
before retrying. A denied `start` leaves no task to clean up.

## Find and admit work

Run `select-next --project TC` to read the project's highest-ranked eligible
task, or use `resume --task TC-1` for the task in the current assignment.
Selection is advisory. A subsequent `resume` checks eligibility again and
takes the slot atomically. If the result has `"task": null`, inspect its
reason and the project before proposing new work. When Helm returns a
capacity or ownership conflict, continue work already admitted to this
session or leave a durable handoff before switching.

For a new task, `start --goal` supplies the default finish condition and the
first `--step` supplies the default next action. Helm preserves the goal and
the full list of checkpoints on the card. You can override admission details
with `--next-action` and `--finish-condition`, and you must supply
`--next-action` when there are no checkpoints. For an existing task, `resume`
uses saved admission details when available. It can also use the first
unchecked criterion or checkpoint and a recognized `Goal` in the card
description. Supply either missing field explicitly. Use a concrete action
and a condition you can verify.

```sh
python3 scripts/helm.py select-next --project TC
python3 scripts/helm.py resume --task TC-1 \
  --next-action "Run the isolated upgrade test" \
  --finish-condition "The upgraded fixture preserves every task and event" \
  --operation-id "stable-session-local-id"
```

## Keep the session and operation IDs stable

Codex gets a local session identifier automatically. Outside Codex, export
`HELM_AGENT_SESSION_ID` with a unique, stable value for the work session.
Use the same ID and Helm credential for admission, progress, heartbeat,
renewal, release, completion and blocking. Keep the local state directory
available throughout the session. The CLI stores the original admission fence
in private local state and sends it on subsequent lifecycle requests. A new
run must use a new session ID. A released or expired admission no longer
authorizes changes.

When retrying a request after a lost response, reuse its `--operation-id`
and the same input. A new logical mutation needs a fresh operation ID. A
replayed receipt must still match the live admission before the CLI reports
success. If the server reports that the fence has ended, stop and obtain
fresh admission before returning to work.

## Server compatibility

The CLI reads `/api/v1` before starting or resuming. The current versioned
API advertises `agent_work_admission: true` when it supports admission.
An older Helm v1 server that omits the capability uses the original
create/claim or claim/activate sequence. `select-next` requires the new API.
Set `HELM_REQUIRE_AGENT_ADMISSION=1` in environments where admission is
required to disable legacy compatibility, including during rollout or
rollback. If the advertised admission endpoint denies a request or cannot
verify the fence, the CLI stops with that error.
