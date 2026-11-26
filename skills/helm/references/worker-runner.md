# Task-bound Docker worker

Run `scripts/helm_runner.py` explicitly on a trusted Linux worker host. The
launcher uses the local Docker socket and an existing task on a Helm server
that supports atomic agent admission. This lane runs isolated offline commands
such as fixture checks and static analysis from an operator-selected image. It
covers workloads launched through this command. Interactive T3, Codex, and
other launchers have their own execution paths.

Configure the authorized Helm origin and a dedicated agent token using the
[standard credential configuration](authentication.md). The worker token needs
`tasks:read`, `tasks:write`, and `tasks:claim` on the chosen project. Keep the
host's Docker access, launcher, command arguments, credential configuration,
and image selection under operator control. Use an installed, content-addressed
Linux image with `/usr/local/bin/python3` at version 3.9 or later. Bake inputs
and command dependencies into the reviewed image. Commands execute as UID and
GID 65534, so grant that identity read access to the baked inputs.

This example runs a fixture digest check in a reviewed image and retains the
command's output on the host. Substitute your actual actor, task, and image.

```sh
python3 scripts/helm_runner.py \
  --project TC --task TC-403 --actor AGENT_ACTOR_ID \
  --next-action 'Compute the fixture digest' \
  --finish-condition 'Print the digest for comparison' \
  --run-seconds 15 --image sha256:LOCAL_IMAGE_ID \
  -- /usr/local/bin/python3 /work/check_fixture.py > fixture-result.log
```

The launcher checks the token identity against `--actor`, asks Helm to admit
the existing task, then matches the live task, actor, session, and server fence
immediately before starting. Each invocation generates its own session. It
accepts no existing session or receipt. Workload environment metadata includes
`HELM_AGENT_TASK_KEY`, `HELM_AGENT_SESSION_ID`, `HELM_AGENT_PROJECT`, and
`HELM_AGENT_ACTOR_ID`. The container has a read-only image filesystem, a small
temporary `/tmp`, no external network, and no host bind mounts. Its output and
exit status are available to the launcher caller. Credentials and the fence
stay on the host.

Helm is checked approximately once a second and each request has a two-second
deadline. The launcher stops the container on denial, loss of contact, or a
changed binding. An independent guard shuts down the container's Linux PID
namespace when its latest permit expires, at most four seconds after the last
verified permit, even if the launcher dies.
Set a fixed runtime ceiling from one to 300 seconds with `--run-seconds`.
Normal cleanup confirms container removal before releasing the task claim.
After abrupt host failure or loss of Helm access, the remaining claim expires
on the server. An interrupted launcher can leave a stopped container for the
operator to remove. Revocation is detected at the next check or permit expiry.

The repeatable synthetic journey uses an actual local Helm binary and Docker.
It records the child command's canary, actor and concurrent launch denial,
refusal without live authorization, termination after server revocation or
disconnection, guard expiry after launcher death, and the execution deadline.

```sh
python3 test/deployment-policy/agent_runner_e2e.py \
  --binary /path/to/helm --image python:3.11-bookworm \
  --artifact .artifacts/tc403/docker-worker.json
```
