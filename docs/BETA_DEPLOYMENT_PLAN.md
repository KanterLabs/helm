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
| Public origin | `https://tc.shanekanterman.dev` | private-only `https://beta-helm.home.shanekanterman.dev` (not public; route pending) |
| Proxmox guest | CT 103, `roadmap`, `10.0.0.38` | CT 106, `helm-beta`, `10.0.0.39` |
| Deploy account | `roadmap-deploy` | `helm-beta-deploy` |
| Host state | `/var/lib/roadmap-deploy` | `/var/lib/helm-beta-deploy` |
| Signing trust | `/etc/roadmap-deploy` | `/etc/helm-beta-deploy` |
| Cloudflare tunnel | `roadmap-homelab` | none (private Tailnet profile; public provisioning disabled) |
| Access resources | `Helm owner UI`, `Helm agents API` | none (private route pending) |

The Portfolio deployment owns `beta.shanekanterman.dev`; Helm beta owns only
`beta-helm.home.shanekanterman.dev`, and its public Cloudflare reconciler is
disabled. This is a private Tailnet/split-DNS hostname: the private route is
not activated yet, no public DNS record is allowed, and the reconciler must
never create or mutate the Portfolio hostname or tunnel record.

The beta guest intentionally retains the in-guest compatibility paths and
service names (`/var/lib/roadmap`, `/etc/roadmap`, and `helm.service`). The
private profile masks any old `cloudflared.service` and never bundles, starts,
or restarts it. Beta must start with a fresh database; production databases,
releases, backups, tunnel tokens, deploy keys, signing keys, and Access
credentials are never copied into it.

### Private Tailnet preprovisioning

Before enabling a beta deployment, root must provision these files in CT 106;
they are deliberately outside signed release bundles and are never printed by
CI or the installer:

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

The beta job remains paused with `HELM_BETA_DEPLOY_PAUSED=true` while the
private route and TLS are being verified. When explicitly unpaused, CI builds a
signed beta bundle without Cloudflare preparation/publication and invokes the
guest's localhost health plus unauthenticated-401 validator. It does not claim
that the private hostname is reachable until a root operator completes the
Tailnet route/TLS check.

## Dependency order

1. Add fail-closed production and beta deployment profiles to the Proxmox
   gateway, bootstrap helper, Cloudflare reconciler, and live validator.
2. Add branch-aware CI routing with distinct GitHub environments, secrets,
   concurrency locks, origins, and rollback jobs.
3. Extend deployment security tests to prove a beta action cannot select any
   production identity and a production action cannot select beta.
4. Provision the beta deploy/signing identities and CT only; hold the public
   tunnel, Access apps, and DNS until the private route is separately approved.
5. Push `beta`, require the full checks, then protect `main` so production
   changes arrive through an explicit reviewed merge.

## Promotion and rollback

- A push to `beta` builds and tests that exact commit but remains paused; the
  public beta Cloudflare path is disabled while the private route is pending.
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
- No public beta tunnel, DNS record, Access app, or policy is created before
  the private-route approval gate.
- Private Tailnet acceptance must prove the revision and auth boundary before
  any beta public path is considered; the production revision, database,
  releases, and services remain unchanged throughout beta tests.
