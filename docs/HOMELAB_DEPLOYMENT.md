# Maintainer homelab deployment

This describes how the maintainers deploy Helm on their own homelab. It is not
required for running Helm yourself; see the README quick start.

## Beta and production deployment

The intended homelab path is:

```text
Cloudflare Access
  → tc.shanekanterman.dev (UI and /api/v1/* applications)
  → roadmap-homelab Tunnel (retained infrastructure identity)
  → cloudflared in the Debian 12 `roadmap` LXC (retained guest identity)
  → helm.service on 127.0.0.1:8080
  → /var/lib/roadmap/data/roadmap.db
```

Changes are tested through a private beta environment before production:

```text
beta branch
  → beta GitHub environment and beta-only secrets
  → beta-helm.home.shanekanterman.dev (active private Tailnet target)
  → private Tailnet TLS listener at 10.0.0.39:8443
  → the `helm-beta` LXC (CT 106, 10.0.0.39)
  → an independent /var/lib/roadmap/data/roadmap.db

explicit pull request or merge to main
  → production GitHub environment and ROADMAP_* secrets
  → tc.shanekanterman.dev and the production `roadmap` LXC (CT 103)
```

The guests do not share databases, backups, releases, tunnel tokens, deploy
keys, signing keys, or GitHub environments. See
[`docs/BETA_DEPLOYMENT_PLAN.md`](BETA_DEPLOYMENT_PLAN.md) for the fixed
identity table, promotion contract, and validation gates.

The data root, database and backup names, Unix account, Compose volume,
hostname, tunnel/guest identities, `X-Roadmap-Revision` header, Roadmap API
routes and schema names, and signed Roadmap v1 gateway envelope are stable
compatibility identifiers. See
[`docs/HELM_LEGACY_IDENTIFIERS.md`](HELM_LEGACY_IDENTIFIERS.md) for the
reviewed allowlist.

The production guest has no inbound application or SSH port; the application
and connector communicate over loopback. The beta private profile permits only
the approved homelab-edge LAN source (`10.0.0.101`) to its TLS listener, masks
`cloudflared.service`, and never performs public Cloudflare reconciliation. The
guest does not require a Tailscale interface for this LAN ingress. Releases use
an immutable SHA-tagged Go binary, a constrained Proxmox deployment identity,
and signed bundle/backup/rollback checks. The full bootstrap, host assumptions,
firewall posture, private preprovisioning, and recovery checks are in
[`docs/OPERATIONS.md`](OPERATIONS.md).

After the one-time Proxmox and private Tailnet preprovisioning described there, pushes to
`beta` and `main` run [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).
Both branches run Go/frontend checks, browser tests, and a container smoke
test. A successful `beta` push automatically deploys to the active private
Tailnet target and validates its private readiness; public Cloudflare beta
provisioning remains disabled. The selected target is
`beta-helm.home.shanekanterman.dev`, while a `main` push may deploy only through the
`production` environment and validate <https://tc.shanekanterman.dev>. Normal
production deployment requires the GitHub Actions
secrets `ROADMAP_CLOUDFLARE_API_TOKEN`, `ROADMAP_DEPLOY_SSH_KEY`,
`ROADMAP_DEPLOY_KNOWN_HOSTS`, `ROADMAP_RELEASE_SIGNING_KEY`,
`ROADMAP_CF_ACCESS_CLIENT_ID`, and `ROADMAP_CF_ACCESS_CLIENT_SECRET`; secret
values stay in GitHub or the approved operator secret store and are never
placed in this README or the repository. Create the Cloudflare service token
and save its one-time secret with the manual `cloudflare.sh prepare` procedure
before enabling CI; CI refuses to create a token whose secret would remain
only on an ephemeral runner.

Beta uses corresponding `BETA_*` environment secrets, including
`BETA_ADMIN_EMAIL`, and the configured Tailnet owner login
`ShaneKanterman04@github`, plus a distinct forced SSH account and
release-signing key. Its signed private bundle omits cloudflared;
the beta gateway validates loopback health, private-profile invariants, and an
unauthenticated HTTP 401 after deployment. The optional
`HELM_BETA_DEPLOY_PAUSED=true` setting remains an explicit maintenance stop,
not the normal private-beta state. Dispatch `rollback_sha` from `beta` to roll
back beta, or from `main` to roll back production; neither environment's job
can select the other's gateway. Beta does not use Cloudflare credentials or
public routing. Normal beta pushes build `helm-beta-switchd` from the exact
trusted beta checkout and sign `HELM_RELEASE_REF=refs/heads/beta` into the
bundle, using the fixed `/run/helm-beta-switcher/helm-beta-switchd.sock`
socket.

### Manual feature-branch candidates

The protected beta branch also carries a separate manual workflow,
`.github/workflows/beta-candidate.yml`, for trying a schema-compatible feature
branch on the private beta hostname. Dispatch it from `beta` with both the
exact 40-character lowercase `candidate_sha` and its canonical
`candidate_ref` (`refs/heads/<branch>`). Admission resolves that ref in the
canonical `KanterLabs/helm` repository, requires the same tip SHA, rejects
forks and the `main`/`beta` branches, and rejects unsafe Git ref syntax. A
stale SHA or a branch that moves during admission fails closed. The trusted
beta commit must also be an ancestor of the candidate commit, so candidates
must be based or rebased on the current beta revision; an unrelated older
feature branch cannot pass schema equality and strand the beta switcher/API.

Candidate source is tested without beta secrets: short checks run on
`homelab`, while browser, race, and container checks run on `homelab-heavy`.
Every candidate checkout is pinned to the admitted SHA. Admission also
requires the candidate's `internal/db/migrations/` tree and
`internal/db/db.go` migration engine to be byte-identical to trusted beta, and
rejects any `go.mod` or `go.sum` change. The module-file restriction prevents a
candidate from silently changing the dependency/toolchain inputs used by the
trusted controller and deployment; a dependency update must land in beta first
through its normal reviewed path. This is intentionally conservative and can
require a separate reviewed beta change before a candidate can be admitted.
Binary, container, and provenance artifacts are named with the candidate SHA;
the provenance records the canonical candidate ref and the exact trusted beta
workflow/ref/SHA.

The `homelab` and `homelab-heavy` selectors follow the canonical
`KanterLabs/infrastructure` `homelab/ci-runners/README` contract: every job
gets one ephemeral runner pod and private Docker-in-Docker daemon, with no host
Docker socket, host home directory, or static deployment credential mounted.
The workspace and daemon disappear with the job, so candidate work cannot
persist into a later job; the workflow still pins every checkout and writes
secrets only below the per-job `$RUNNER_TEMP` directory.

Within this candidate workflow, only the final `candidate_deploy` job can read
`BETA_*` secrets. It checks out
the exact `github.sha` from `refs/heads/beta`, and fails closed unless the
workflow identity is exactly
`KanterLabs/helm/.github/workflows/beta-candidate.yml@refs/heads/beta` with
`GITHUB_WORKFLOW_SHA == GITHUB_SHA`. It then verifies all candidate
metadata/artifact checksums, builds `helm-beta-switchd` from that trusted beta
source, and invokes the trusted `deploy/deploy-ci.sh` with
`HELM_RELEASE_REF=refs/heads/<candidate-branch>`. The beta owner environment
enables the switcher with `HELM_BETA_SWITCH_ENABLED=true` and the fixed
`/run/helm-beta-switcher/helm-beta-switchd.sock` socket. The job never uses
candidate source as workflow or deployment-script input, and production's
`main` workflow and credentials are unchanged. A paused beta environment
prevents the
secret-bearing candidate deploy while still allowing non-secret checks to
finish.

## Backups and rollback

For moving live project data between Helm installations, use the versioned
[portable export/import format](PORTABILITY.md). Portable imports are
validated, dry-runnable, conflict-aware, additive, and transactional; they do
not replace the database. The separate backup/restore workflow below remains
the disaster-recovery path for exact SQLite state.

Before each install, the host takes a SQLite online backup, verifies its
checksum and `PRAGMA integrity_check`, and stores it under
`/var/lib/roadmap/backups`. A pre-upgrade backup is complete only when its
release SHA, schema identifier/digest, database digest, and integrity metadata
are recorded alongside the verified backup. Additive schema migrations run in
transactions; the candidate release first performs its preflight checks on a
copy before the active database is touched. The database is kept outside
release directories and is never replaced by an executable upgrade. By
default, five releases and fourteen backups are retained.

The active release is switched atomically. Failed Helm health or
cloudflared checks automatically restore the previous release and restart the
services. A binary/release rollback changes only the active release pointer:
it never restores an older database or discards writes accepted by the
running service. A retained release can also be selected with the workflow's
`workflow_dispatch` `rollback_sha` input, or from a configured deployment
shell with:

```sh
./deploy/deploy-ci.sh rollback <40-character-release-sha>
```

The live validator can optionally send the Cloudflare service-token headers to
`/api/v1/roadmap` without an application bearer token. CI requires this probe
with `CF_ACCESS_CLIENT_ID` and `CF_ACCESS_CLIENT_SECRET` (mapped from the
production secrets above); it expects the origin's JSON `401` error and echoed
`X-Request-ID`, which distinguishes Helm from an Access edge error.

For manual backups, database restores, first-time bootstrap, and the exact
operator permissions, follow [`docs/OPERATIONS.md`](OPERATIONS.md). A
database restore is a separate, explicitly requested operation against one
exact retained backup; the restore helper first takes a new recoverable
`pre-restore` snapshot of the current database before installing the selected
copy.
