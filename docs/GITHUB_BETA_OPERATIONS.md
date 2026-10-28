# GitHub audit beta operations

This prototype reads the GitHub mirror. Gitea remains the canonical source.
It runs in private Helm beta; production configuration is unchanged.

## Runtime configuration

The existing Coolify service reads `/srv/helm/beta/config/runtime.env` and
mounts that directory read-only at `/etc/roadmap`. Preserve all existing
configuration and add only:

```dotenv
HELM_GITHUB_ENABLED=true
HELM_GITHUB_TOKEN_FILE=/etc/roadmap/github-token
HELM_GITHUB_REPOSITORIES=KanterLabs/helm
```

Provision the token through the secure secret input. Store it in the mounted
`github-token` file, owned by runtime UID/GID 65532, mode 0400. Do not put its
value in environment variables, task records, command arguments or logs.
The provider accepts only regular owner-protected files and detects rotation.
Use a selected-repository token with read access to Contents, Pull requests,
Checks and Commit statuses. A connection check must succeed before claiming
the integration works; token presence alone is insufficient.

Each project selects allowed repositories and branches in the GitHub UI.
The requesting human must connect their own AI account in Helm. This beta's
evidence inference is validated with Codex 0.153.0 and `gpt-5.6-luna` only;
other executable versions or models fail closed. See
[the runtime boundary proof](AUDIT_INFERENCE_BOUNDARY.md).

Agent execution requires an explicit short-lived grant bound to the agent,
its exact bearer token, project selection version, expiry and task limit.
The granting human administrator owns the AI account used for inference.
Use the owner-authorized `helm audit-access` provisioning flow described in
[agent GitHub audits](GITHUB_AGENT_AUDITS.md); bearer tokens cannot create or
revoke grants. HTTP revocation is restricted to the granting administrator.
Agents use `tasks:read`, explicit task IDs and the grant ID for readiness and
start. Retry and Apply remain human actions; an agent can cancel only its
own exact-token job, even after its grant expires. Selection changes,
revocation and lost permissions fence execution without deleting history.

Provisioning a grant does not establish live GitHub or AI readiness. Verify
readiness, start and completion through the intended runtime before reporting
live operation; synthetic harness results establish only the tested boundary.

## First activation and data preservation

1. Verify a fresh online database backup and protected runtime archive,
   including hashes, SQLite integrity and foreign keys. Preserve the existing
   beta data mount; never replace the database during deployment.
2. Exercise migrations 037–039 against populated data using the candidate
   binary and retained live binary. Verify original records and relationships,
   candidate and retained writes, and preservation of populated new tables.
3. Deploy the validated immutable image with GitHub execution disabled. Require
   the expected revision from both private `/healthz` and `/readyz` before
   enabling GitHub. This prevents private source findings from being created
   during the first deployment's automatic legacy-image rollback window.
4. Activate the protected token and configuration, restarting only the same
   verified Coolify beta service/image. If activation fails, restore the prior
   configuration on that image; do not restore a database or downgrade code.
5. Verify the live connection, selected repository/ref, PR metadata and audit
   flow. Approval must leave tasks unchanged. Preview and Apply are separate;
   lifecycle transitions remain normal task actions. Take a post-release backup.

## Rollback boundary

Migrations are additive. The retained pre-feature binary can read and write
ordinary tasks on schema 39 without losing new PR/audit history. SQL guards
prevent it from appending worker findings, finalizing active worker runs or
changing imported audit history. Stop and fence the new worker before any
code rollback; queued jobs remain resumable by a capable server.

**A pre-feature binary cannot revalidate GitHub permissions when reading
source-derived finding reasons.** Once such history exists, a normal rollback
must use a source-aware image. If an older image is required, first deny audit
and audit-finding API surfaces on every ingress, including the legacy bearer
route, and keep them denied until source-aware code is restored. Ordinary task
access can remain available. Database compatibility does not establish this
authorization compatibility. Never delete retained evidence or restore an old
database to work around it.

## Repeatable verification

- Provider/config/runtime and backend race tests use synthetic credentials.
- `test/githubbeta/run.sh` exercises the real HTTP/store/worker path with fake
  external GitHub and inference adapters. Build current frontend assets and
  the harness first, then supply `HELM_GITHUB_BETA_BINARY`. The complete
  `test/e2e/run.sh` suite builds and executes this harness automatically;
  focused spec runs keep their requested scope.
- Browser artifacts include screenshots, traces and responses. The harness
  scans them for its credential sentinel; no live account is used.
- Portable HTTP defaults to v2; the UI explicitly exports v3. V3 preserves
  durable PR relationships and immutable audit history, excludes credentials,
  disposable status caches and executable jobs, and revalidates private source
  access before returning evidence. Imports never enqueue work.

Repository capture is bounded. Omitted files/checks and partial coverage remain
visible; a merged PR never proves deployment or task completion. Source access
loss hides retained evidence and findings without deleting the historical data.
