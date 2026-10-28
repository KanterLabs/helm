# Agent access to GitHub audits

## Failure inventory before implementation

An ordinary read token must not spend a human's AI account. An authorization
must bind an exact agent, token, project, account owner and repository selection,
with a bounded expiry and task count. A caller must not choose another account
owner, reuse another token's grant, expand its project ceiling, or apply findings
through this permission. Existing human audit behavior must continue to work.

Check missing, expired and revoked authorizations; deleted/expired tokens;
disabled agents/owners; removed project access; changed repository selection;
operator AI/GitHub disablement; account disconnect and generation changes.
Revalidate before source capture, inference and publication, including durable
checks in write transactions. Revocation during delayed inference must prevent
publication. Cancelling transport alone is insufficient.

Idempotent replay must retain the same requester, token and authorization and
must reauthorize before returning a run. Concurrent agents must not multiply
active runs against one owner's account/project. Store the requester separately
from the AI account owner. Keep credentials and private source out of logs,
test artifacts and administrative responses.

The operator command must require an existing database, explicit owner,
agent/token and project identifiers, and bounded expiry. It must never fabricate
a human session or print a bearer secret. Grants are runtime permissions and
must not enter portable exports. Additive migrations must preserve populated
tasks and history; retained binaries must still read/write ordinary tasks and
must fail closed on delegated audit jobs.

## API contract

Human administrators may create/revoke authorizations for their own AI account
under `/api/v1/projects/{project}/github-audit-authorizations`. An operator with
local database access may use `helm audit-access` to administer the same durable
records. The command is an explicit administration path, not an authentication
bypass. Authorizations expire within 24 hours and default to at most ten tasks.

Agents use `tasks:read` and their existing effective project scope to discover
their own grants, check readiness with `authorization_id`, and start an audit
with that ID plus explicit `task_ids`. Ordinary tokens without a valid matching
grant remain denied. Start requires an idempotency key. Job, evidence and finding
reads keep the existing project and source-access checks. Apply remains human
only; this permission does not change task lifecycle permissions.

## Verification

The focused `web/e2e/github-agent-audit.spec.ts` uses the real
HTTP/auth/SQLite/worker path with synthetic GitHub and inference.
Capture sanitized response artifacts, assert actual requester/account-owner
attribution, exactly one inference for a replay, grant expiry/revocation fencing,
wrong-token/project denial, unchanged tasks, and retained-binary compatibility.
After deployment, run one bounded real audit without approving or applying its
findings, and save only safe status/count/commit metadata as the live receipt.

## Operator command

Token-file failure inventory (recorded before implementation): provisioning
must create only a new expiring, project-bound read token. Require exactly one
of an existing token ID or a new absolute token-file path. Refuse existing
files, symlinks, insecure/unowned parent directories and relative paths before
creating credentials. Never print the bearer secret. A failed grant or token
file write must revoke its new grant, delete its new token, and remove only its
newly reserved file; existing credentials must remain untouched.

An operator with local filesystem access can administer durable authorizations
without creating a human session. Run the same Helm binary version as the
deployment against an existing, regular SQLite database file whose migrations
already match the binary. The command refuses missing files, SQLite URI paths,
symlinks and pending/unknown migrations; use the deployment migration workflow
first if needed. It can operate alongside the server using SQLite locking.

```sh
helm audit-access grant --db /existing/path/helm.db --project PROJECT_REF \
  --owner HUMAN_ADMIN_ACTOR_ID --agent AGENT_ACTOR_ID --token TOKEN_ID \
  --expires-in 1h --max-tasks 10
# Alternatively, deliver a newly provisioned short-lived token to a private file:
helm audit-access grant --db /existing/path/helm.db --project PROJECT_REF \
  --owner HUMAN_ADMIN_ACTOR_ID --agent AGENT_ACTOR_ID \
  --token-file /existing/private-directory/new-token --expires-in 1h
helm audit-access list --db /existing/path/helm.db --project PROJECT_REF
helm audit-access revoke --db /existing/path/helm.db \
  --authorization AUTHORIZATION_ID --owner HUMAN_ADMIN_ACTOR_ID
```

`--owner` must explicitly name an enabled human administrator who owns the AI
account; grant/revoke do not guess an account. The agent must already exist and
satisfy the store's project scope checks. Choose exactly one of `--token` and
`--token-file`. `--token` takes an existing token record ID, never a bearer
secret. `--token-file` creates a new token with only `projects:read` and
`tasks:read`, scoped to this project and expiring at the same time as the grant.
Its path must be absolute and new; existing files and symlinks are refused.
Its existing parent directory must belong to the current user with no group or
other access (normally mode 0700). The secret is written only to the new mode
0600 file, then synced; it is never printed. Failed provisioning revokes its
new grant, deletes its new token and removes its reserved file, leaving existing
credentials alone. The command does not connect accounts, probe GitHub/AI
readiness, or start inference. Local database administration is not a server
authentication or session bypass.

The default expiry is one hour, bounded above by 24 hours. The default task
limit is ten, with accepted values from one through 100. Successful operations
print authorization metadata as JSON, including record IDs and revocation time;
they never print bearer credentials. `helm audit-access --help` describes the
delegated AI usage permission. Revocation retains the authorization for audit
history and fences work using that authorization.
