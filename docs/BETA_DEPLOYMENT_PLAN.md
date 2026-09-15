# Beta branch and deployment plan

## Goal

Give Helm a permanent `beta` branch whose successful pushes deploy to an
isolated test installation. Production remains reachable only from `main`, so
promoting a tested beta revision requires an explicit pull request or merge to
`main`.

## Fixed environment identities

| Boundary | Production | Beta |
| --- | --- | --- |
| Git branch | `main` | `beta` |
| GitHub environment | `production` | `beta` |
| Deploy lock | `helm-production` | `helm-beta` |
| Origin | `https://tc.shanekanterman.dev` (public Cloudflare) | `https://beta-helm.home.shanekanterman.dev` (private Tailnet only; no public route) |
| Proxmox guest | CT 103, `roadmap`, `10.0.0.38` | CT 106, `helm-beta`, `10.0.0.39` |
| Deploy account | `roadmap-deploy` | `helm-beta-deploy` |
| Host state | `/var/lib/roadmap-deploy` | `/var/lib/helm-beta-deploy` |
| Signing trust | `/etc/roadmap-deploy` | `/etc/helm-beta-deploy` |
| Cloudflare tunnel | `roadmap-homelab` | none (private Tailnet profile; public provisioning disabled) |
| Access resources | `Helm owner UI`, `Helm agents API` | none (private Tailnet; no public Access resources) |

The Portfolio deployment owns `beta.shanekanterman.dev`; Helm beta owns only
`beta-helm.home.shanekanterman.dev`, and its public Cloudflare reconciler is
disabled. This is a private Tailnet/split-DNS hostname: the private route is
active, no public DNS record is allowed, and the reconciler must
never create or mutate the Portfolio hostname or tunnel record.

The beta guest intentionally retains the in-guest compatibility paths and
service names (`/var/lib/roadmap`, `/etc/roadmap`, and `helm.service`). The
private profile masks any old `cloudflared.service` and never bundles, starts,
or restarts it. Beta must start with a fresh database; production databases,
releases, backups, tunnel tokens, deploy keys, signing keys, and Access
credentials are never copied into it.

### Private Tailnet preprovisioning

The active beta guest is preprovisioned with these root-managed files in CT
106; they are deliberately outside signed release bundles and are never
printed by CI or the installer:

| Path | Owner/mode | Purpose |
| --- | --- | --- |
| `/etc/roadmap/tailnet-owner.env` | `root:root`, `0600` | fixed private profile environment, including the existing admin email and distinct `HELM_TAILNET_OWNER_LOGIN` |
| `/etc/roadmap/tailnet.key` | `roadmap:roadmap`, `0600` | HMAC assertion key read directly by `helm.service` |
| `/etc/roadmap/tailnet-origin.crt` | `root:root`, `0644` | private-origin TLS certificate |
| `/etc/roadmap/tailnet-origin.key` | `roadmap:roadmap`, `0600` | private-origin TLS private key read by `helm.service` |

The exact non-secret owner-environment template is
[`deploy/tailnet-owner.env.example`](../deploy/tailnet-owner.env.example).
Copy it to `/etc/roadmap/tailnet-owner.env`, replace both admin-email
placeholders with the existing admin email, and set `root:root` ownership and
mode `0600`; never add key bytes to this file.

The owner environment must use `HELM_AUTH_MODE=tailnet`, the exact private
origin and audience, `10.0.0.39:8443`, the paths above, and the approved
homelab-edge LAN origin peer `10.0.0.101` (with matching `ROADMAP_*` aliases).
The firewall permits only that source IP to TCP/8443; it deliberately does not
require a Tailscale interface inside CT 106. The release owner environment is
signed for provenance and must match this preprovisioned file; it contains
paths and identity values only, never key bytes. The helper's separate key
file and any Tailnet edge configuration are root-managed.

The beta job is enabled for private auto-deploy. Each successful push to
`beta` builds a signed beta bundle without Cloudflare preparation/publication,
deploys it to CT 106, and invokes the guest's private-profile, localhost
health, and unauthenticated-401 validator. The private Tailnet route and TLS
are active; `HELM_BETA_DEPLOY_PAUSED=true` remains available only as an
explicit maintenance stop. The normal beta job builds `helm-beta-switchd`
from its exact trusted beta checkout and signs `HELM_RELEASE_REF=refs/heads/beta`
into each beta bundle; the controller always uses
`/run/helm-beta-switcher/helm-beta-switchd.sock`.

## Manual schema-compatible feature candidates

Feature branches can be tried on the existing private beta installation with
the protected [`beta-candidate.yml`](../.github/workflows/beta-candidate.yml)
workflow. This is a beta-only admission path; it does not change the
production workflow or permit a candidate branch to provide deployment
workflow, scripts, or secrets.

### Dispatch contract

Dispatch the workflow from the exact protected `refs/heads/beta` ref and
provide both required inputs:

| Input | Required value |
| --- | --- |
| `candidate_sha` | Exactly 40 lowercase hexadecimal characters, identifying the commit to test and deploy |
| `candidate_ref` | A canonical safe branch ref such as `refs/heads/feature/release-ui`; it must not be `refs/heads/main` or `refs/heads/beta` |

Admission hard-codes the canonical `KanterLabs/helm` repository, rejects fork
dispatches, validates the ref with Git's ref checker plus an ASCII-safe
allowlist, and resolves `candidate_ref` through the canonical origin. The
resolved branch tip must equal `candidate_sha`; the fetched commit is then
resolved again by its SHA. The trusted event SHA must equal the current
canonical `refs/heads/beta` tip. If either branch moves or a supplied SHA is
stale, the run stops before any candidate artifact or beta secret is used. The
trusted beta commit must also be an ancestor of the candidate commit: rebase
the feature branch onto current beta before dispatching. This prevents an
older or unrelated schema-compatible branch from omitting the visible
switcher/API that the trusted beta deployment expects.

Admission also requires the exact workflow identity
`KanterLabs/helm/.github/workflows/beta-candidate.yml@refs/heads/beta` and
`GITHUB_WORKFLOW_SHA == GITHUB_SHA`. The admission checkout fetches full Git
history (and explicitly unshallows if needed) before the ancestry check; a
shallow graph is rejected rather than allowing an unverifiable result.

### Candidate checks and schema gate

The short candidate checks run on the `homelab` runner. Browser, race-detector,
and container checks run on `homelab-heavy`. Those jobs check out only the
admitted candidate SHA and have no beta environment or deployment secrets.
Candidate binary, container/runtime, browser-diagnostic, and metadata artifact
names include that SHA, for example
`helm-beta-candidate-binary-<candidate_sha>` and
`helm-beta-candidate-image-<candidate_sha>`.

Before those jobs start, admission compares the candidate and trusted beta
trees for both `internal/db/migrations/` and the migration engine
`internal/db/db.go`, and rejects any difference in `go.mod` or `go.sum`. It
also records a deterministic digest of all four boundaries and requires the
candidate digest to equal beta's. This gate is intentionally stricter than
checking only the latest migration number: changing, removing, or renaming an
embedded migration, changing its engine, or changing dependency/toolchain
inputs is not compatible with the live beta database/controller boundary. The
module-file rule is a deliberate tradeoff: a candidate that needs a dependency
or Go-toolchain update must first land that reviewed change on beta, then be
redispatched; it avoids silently changing the trusted controller's build
inputs during candidate admission.

The metadata artifact is produced by the trusted beta workflow source and
contains the canonical candidate repository/ref/SHA, `trusted_ref=refs/heads/beta`,
the exact trusted `github.sha`, workflow ref/SHA, schema paths, and schema-gate
result. The secret-bearing job requires the same metadata copy in every
candidate artifact and verifies the candidate binary, Codex runtime, and
container archive checksums before packaging anything for the guest.

### Secret-bearing deployment boundary

Within this workflow, only `candidate_deploy` requests the `beta` GitHub
environment. It checks out
`ref: ${{ github.sha }}` after confirming `github.ref=refs/heads/beta` and the
exact workflow identity above; it never checks out `candidate_ref` or
`candidate_sha` as source. The job builds
`dist/helm-beta-switchd` from that trusted beta checkout, sets the fixed
`HELM_BETA_SWITCH_SOCKET=/run/helm-beta-switcher/helm-beta-switchd.sock`,
and supplies the candidate binary/runtime artifacts to the trusted
`deploy/deploy-ci.sh`.
The owner environment carries:

```text
HELM_RELEASE_REF=refs/heads/<candidate-branch>
HELM_BETA_SWITCH_ENABLED=true
HELM_BETA_SWITCH_SOCKET=/run/helm-beta-switcher/helm-beta-switchd.sock
```

The deployment invokes `deploy-ci.sh deploy <candidate_sha>` with the beta
SSH/signing secrets and performs the same private readiness check and
release-only automatic rollback as the normal beta deployment. It never
prepares Cloudflare or uses production secrets. The `HELM_BETA_DEPLOY_PAUSED`
maintenance stop also blocks this final secret-bearing job. A candidate run
must be dispatched again from beta after an intentional pause is lifted.

The `homelab` and `homelab-heavy` labels follow the canonical
`KanterLabs/infrastructure` `homelab/ci-runners/README` runner contract: one
ephemeral runner pod and one private Docker-in-Docker daemon per job, with no
host Docker socket, host home directory, or static deployment credential
mounted. The workspace and Docker daemon disappear when the job ends, which
provides the clean-job boundary for candidate artifacts and secrets. Workflow
steps still use `persist-credentials: false`, exact-SHA checkouts, and
`$RUNNER_TEMP` cleanup as defense in depth.

## Dependency order

1. Add fail-closed production and beta deployment profiles to the Proxmox
   gateway, bootstrap helper, Cloudflare reconciler, and live validator.
2. Add branch-aware CI routing with distinct GitHub environments, secrets,
   concurrency locks, origins, and rollback jobs.
3. Extend deployment security tests to prove a beta action cannot select any
   production identity and a production action cannot select beta.
4. Provision the beta deploy/signing identities and CT, complete the private
   Tailnet route/TLS setup, and keep the public tunnel, Access apps, and DNS
   disabled for beta.
5. Push `beta`, require the full checks, then protect `main` so production
   changes arrive through an explicit reviewed merge.

## Promotion and rollback

- A push to `beta` builds, tests, and automatically deploys that exact commit
  to the private Tailnet target; the public beta Cloudflare path remains
  disabled.
- A pull request from `beta` to `main` runs the normal checks. Merging it makes
  a new immutable `main` commit, which is the only automatic production
  deployment trigger.
- A workflow dispatch from `beta` with `rollback_sha` selects only a retained
  beta release. The same dispatch from `main` selects only production.
- Failed live validation automatically rolls the selected environment back to
  its previous retained release. Binary rollback never restores a database.

## Verification gates

- Shell syntax, actionlint, deployment-security tests, frontend/OpenAPI checks,
  Go tests, race tests, browser tests, and container smoke checks pass.
- The beta gateway reports CT 106 and the production gateway reports CT 103.
- Beta and production use different forced SSH users and Ed25519 signing keys.
- No public beta tunnel, DNS record, Access app, or policy is created; beta
  remains reachable only through its active private Tailnet route.
- Every private auto-deploy proves the revision and auth boundary before it is
  considered ready; the production revision, database, releases, and services
  remain unchanged throughout beta tests.
