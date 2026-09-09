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
| Public origin | `https://tc.shanekanterman.dev` | private-only `https://beta-helm.home.shanekanterman.dev` (not active) |
| Proxmox guest | CT 103, `roadmap`, `10.0.0.38` | CT 106, `helm-beta`, `10.0.0.39` |
| Deploy account | `roadmap-deploy` | `helm-beta-deploy` |
| Host state | `/var/lib/roadmap-deploy` | `/var/lib/helm-beta-deploy` |
| Signing trust | `/etc/roadmap-deploy` | `/etc/helm-beta-deploy` |
| Cloudflare tunnel | `roadmap-homelab` | none (private route pending) |
| Access resources | `Helm owner UI`, `Helm agents API` | none (private route pending) |

The Portfolio deployment owns `beta.shanekanterman.dev`; Helm beta owns only
`beta-helm.home.shanekanterman.dev`, and its public Cloudflare reconciler is
disabled. This is a private Tailnet/split-DNS hostname: the private route is
not activated yet, no public DNS record is allowed, and the reconciler must
never create or mutate the Portfolio hostname or tunnel record.

The beta guest intentionally retains the in-guest compatibility paths and
service names (`/var/lib/roadmap`, `/etc/roadmap`, `helm.service`, and
`cloudflared.service`). They are isolated by the LXC boundary. Beta must start
with a fresh database; production databases, releases, backups, tunnel tokens,
deploy keys, signing keys, and Access credentials are never copied into it.

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
