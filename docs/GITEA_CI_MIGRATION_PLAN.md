# Helm CI migration to Gitea Actions and ActionRunners

Status: approved to start 2026-11-12; Prepare and Shadow are in progress.
Cutover (H7) still needs Shane's explicit go-ahead.

Planning record for Helm task AR-43. Evidence and file references are in the
[migration audit](GITEA_CI_MIGRATION_AUDIT.md). Facts were checked against
`main` at `0bdb2c9`, the live ActionRunners fleet file and Gitea 28.3.0 on
2026-11-12. It follows the shape of the Data Center Tycoon and Portfolio
migrations.

## Why this exists

Helm's source is canonical on Gitea, but every check, image and release step
still runs on GitHub: pull requests, the four required checks, image
publication, and the tooling that proves where an image came from.
GitHub-hosted runners were a stopgap taken on 2026-10-11 when the old homelab
fleet was retired. Data Center Tycoon, Portfolio and the wiki now run on Gitea
Actions with disposable ActionRunners VMs. Helm has no fleet entry yet. Its
shadow workflow, `.gitea/workflows/ci.yml`, exists but cannot run until H2
enrolls the repository; see the [shadow CI guide](GITEA_CI_SHADOW.md).

## Outcome

- Pull requests, required checks and branch protection for `main` and `beta`
  live on Gitea.
- Every CI job runs in a disposable ActionRunners VM that holds no deployment
  or registry credential.
- Images keep the identity `ghcr.io/kanterlabs/helm@sha256:…`, published from
  the CI artifact by a trusted process on Dev.
- The native admission rollout accepts candidates built on Gitea.
- GitHub is a one-way public mirror with Actions disabled.

Out of scope: re-enabling automatic deployment; changing the native admission
procedure, the registry name or the Coolify services; Helm's product GitHub
integration; new ActionRunners platform features such as persistent caches or
a durable manager host.

## Findings that constrain the design

1. **Tests pin the workflow files.** Checks that run inside `checks` require
   `.github/workflows/ci.yml` and `beta-candidate.yml` at those paths, exactly
   six jobs, and exact `runs-on`, action and secret strings. A move or relabel
   fails CI until those tests change.
2. **Both pipelines can run on the same commit.** The migrated repos keep CI
   in `.gitea/workflows`, which GitHub ignores. Gitea is expected to prefer
   that directory over `.github/workflows`; confirm on 28.3.0 before relying
   on it. Gitea has created no Helm run since 2026-10-02 although pushes
   continued; find out why before adding workflows.
3. **Job VMs reach only Gitea and the public internet.** No tailnet, Coolify,
   Infisical, LAN or other slot. Deploy steps cannot run in canonical CI.
4. **The deploy jobs are already inert.** Both pause variables are `true`, the
   jobs lack inputs `coolify-deploy.py` now requires, and real deployments use
   the native admission rollout from Dev.
5. **The registry name is enforced in shipped code.**
   `ghcr.io/kanterlabs/helm@sha256` is checked by the binary's admission marker
   reader, deploy policy and release tooling. The Coolify VM cannot pull from
   Gitea.
6. **Acceptance requires GitHub evidence.** The acceptance exporter asserts
   `github_zip_digests_verified`, and the provenance verifier reads GitHub's
   run, artifact-digest and job-log APIs. Gitea's artifact API has no immutable
   digest (a DCT finding).
7. **Generic runner profiles have no Docker daemon.** `container` needs one to
   build, run, save and scan. Portfolio solved this with its own template and
   contained Docker-in-Docker.
8. **The fleet has no persistent cache** (AR-13 is open). A job starts cold
   unless its template carries the toolchain and seeded caches.
9. **Hosted timings to match** on GitHub's 4 vCPU and 16 GiB: `checks`
   134 to 180 s, `browser` 753 to 849 s, `container` 139 to 165 s; about 15
   minutes wall clock.
10. **Capacity is shared.** The fleet has 10 slots, 48 vCPU and 108 GiB. DCT
    may hold all 10 slots. Helm needs up to 3 at once.
11. **Release tooling is GitHub-native.** `deploy/release*.py` treats the
    GitHub merge as authoritative and reads GitHub runs, artifacts, pull
    requests and releases. It already cannot pass, because it requires a
    successful deploy job.
12. **Installed Helm skills update from GitHub `main`** and refuse any other
    URL, so the mirror must stay current.
13. **Protection exists only on GitHub.** The repository is public there and
    `main` requires `checks`, `browser`, `race` and `container`. Gitea's `main`
    and `beta` are unprotected.
14. **Dev is a single point of failure.** The runner controller, the wiki
    deployer and the Portfolio coordinator are user services on Dev. Helm's
    checks would depend on it too.

## Design decisions

### D1. Shadow first, then cut over

Add `.gitea/workflows/ci.yml` beside the GitHub workflows and run it on canary
labels while GitHub stays authoritative. Compare both pipelines on the same
commits. Cut over only after the shadow gate. This keeps a working pipeline at
every step and makes rollback a settings change.

### D2. Canonical CI holds no secrets

Port `checks`, `browser`, `race` and `container`. `container` builds, smoke
tests, scans and uploads the image archive, but does not log in to a registry.
Pull requests and pushes use the same jobs. This matches Portfolio and the
wiki.

### D3. A Helm runner template

Build one Helm template from the generic one, as Portfolio did: Go at the
`go.mod` toolchain, Node 22.23.2, Playwright Chromium with its system
packages, seeded module and npm caches, and contained Docker-in-Docker for
`container`. Map `homelab` to a 2 vCPU, 4 GiB profile for `checks` and
`homelab-heavy` to a 4 vCPU, 16 GiB profile for `browser` and `container`.
Proposed limits: 1 short, 2 heavy, 3 concurrent. A toolchain bump means a new
template version, so the workflow must fail clearly when the template is stale.

### D4. Images stay on GHCR, published from Dev

A publisher on Dev admits only a successful push run on the exact `beta` or
`main` tip, pulls the image artifact, verifies its hash, pushes it to GHCR by
digest, and records an immutable receipt. Reusing Portfolio's publisher is the
starting point. The registry name does not change, so the shipped binary,
deploy policy and Coolify are unaffected.

### D5. Provenance from Gitea plus the receipt

Replace GitHub run evidence with: the Gitea run and job records, the artifact
hash measured by the publisher, the existing OCI blob and registry-byte
checks, and the publication receipt. The exporter accepts either source during
the transition and records which one it used. No evidence flag is set to true
for something that was not checked.

### D6. Deployment stays operator-run

The native admission rollout from Dev remains the only deployment path. The
`beta_deploy` and `deploy` jobs, their Tailscale and Coolify secrets and the
tests that pin them are removed, not ported.

### D7. Pull requests and protection move to Gitea

Protect `main` and `beta` on Gitea: required contexts for the four jobs, no
direct or force push, no admin override. GitHub becomes a one-way mirror fed by
the same bridge DCT and Portfolio use, with Actions disabled. The repository
stays public there, and the skill updater keeps reading it.

### D8. Release tooling follows cutover

`deploy/release*.py` gets a Gitea adapter for runs, pull requests and releases,
and takes "deployed" evidence from native admission receipts instead of a CI
deploy job. Until then promotions stay plain pull requests, as #64 and #67
were.

## Failure cases

Written before implementation. Each becomes a check, canary or test. The
first shadow slice narrows them in the
[shadow failure contract](GITEA_CI_SHADOW_FAILURES.md).

- A job VM reaches the tailnet, Coolify, Infisical, the LAN or another slot.
- A canonical job, including one from a pull request, can read a secret,
  registry credential or deploy token.
- During shadow the Gitea pipeline passes a commit GitHub fails, or the
  reverse.
- `container` needs a privileged path that weakens VM isolation.
- The publisher pushes bytes that differ from the artifact, publishes from a
  failed or superseded run, or produces two digests for one commit.
- Provenance is accepted for an image not built by a successful push run on
  the exact branch tip.
- The first Gitea-built candidate fails acceptance or the native rollout.
- A required check can be bypassed by direct push, force push, admin override
  or a manual run standing in for a pull-request check.
- The mirror lags, diverges or runs in the wrong direction, or the skill
  updater serves a stale `main`.
- Helm queues behind DCT long enough to be slower than hosted, or is cancelled
  by a timeout tuned for another repository.
- Dev is down, no checks run, and nothing says why.
- Rollback fails because GitHub workflows, secrets or protection were removed
  too early.

## Implementation queue

| Card | Work | Prerequisites |
| --- | --- | --- |
| H0 | Decide the questions under Decisions (done 2026-11-12) | None |
| H1 | Commit the failure contract (shadow slice done); record hosted p50 and p95 per job from the last 30 push runs | H0 |
| H2 | Enroll Helm: bot identity and tokens, Helm template, profiles, fleet entry on canary labels, Gitea edge allowlist check, dashboard baseline | H0 |
| H3 | Add the shadow workflow and update the pinned tests to cover both pipelines (`checks`, `browser`, `race` and their policy test added; `container` waits for the H2 template; unexercised until H2) | H1, H2 |
| H4 | Shadow soak: 20 consecutive green runs with parity, timings and isolation canaries | H3 |
| H5 | Trusted publication: Dev publisher and receipts for Helm | H3 |
| H6 | Gitea provenance and acceptance; deploy one Gitea-built candidate to beta | H5 |
| H7 | Cutover: protection, production labels, mirror, GitHub Actions off | H4, H6 |
| H8 | Release tooling adapter and agent pull-request workflow | H7 |
| H9 | Update Helm docs, workspace policy files and the wiki | H7 |
| H10 | Cleanup after 14 days: remove GitHub workflows, secrets and variables; retire VM128 | H7 |

## Phases and gates

**1. Prepare (H1, H2).** No Helm repository behaviour changes. The template is
built and verified offline; the fleet entry uses `ar-helm-canary-short` and
`ar-helm-canary-heavy`. **Gate:** a canary job on each label proves the
toolchain versions, a Chromium launch, a contained `docker build`, and blocked
access to every private address in the failure list.

**2. Shadow (H3, H4).** The Gitea workflow runs on every push and pull request
while GitHub's four checks remain required. **Gate:** 20 consecutive commits
where both pipelines agree; Gitea p95 wall clock no worse than hosted p95 plus
20 percent, including queue time; no secret present in the Gitea repository.

**3. Publication and provenance (H5, H6).** The publisher runs in dry-run mode
first, then publishes shadow images under the normal name. **Gate:** one
candidate built on Gitea passes acceptance and a full native rollout on beta,
with recovery to the previous GitHub-built image rehearsed on a copy.

**4. Cutover (H7).** In one window: protect Gitea branches, switch the fleet
entry to `homelab` and `homelab-heavy`, start the mirror, disable GitHub
Actions, and stop pushing to GitHub by hand. **Gate:** a pull request cannot
merge on Gitea without the four contexts; a direct push is refused; the mirror
shows the new tip within its interval; the skill updater installs it.

**5. Follow-through (H8, H9).** Release tooling, agent instructions and
documents catch up. **Gate:** `make release-status` reports from Gitea; no
document tells an agent to open Helm pull requests on GitHub.

**6. Cleanup (H10).** After 14 days and at least one production rollout from a
Gitea-built image. **Gate:** GitHub holds no Helm secret or variable, the
pinned tests reference only `.gitea/workflows`, and VM128's reservation is
released through AR-22.

## Rollback

- **Before cutover:** disable the fleet entry and delete the shadow workflow.
  GitHub was never changed.
- **After cutover, before cleanup:** re-enable GitHub Actions, restore the
  manual dual push, remove Gitea's required contexts, disable the fleet entry.
  GitHub workflows, secrets and protection are still in place for this reason.
- **Images:** a Gitea-built candidate that misbehaves is handled by the
  existing accepted-recovery sequence. Rollback never restores a database.
- **After cleanup:** rollback means repeating the hosted cutover of
  2026-10-11. Do not start cleanup until that is acceptable.

## Decisions

Shane said to proceed on 2026-11-12 without changing these recommendations, so
each one is the working decision. Any of them can still be reversed before
cutover.

1. **Where pull requests live.** Recommended: Gitea, like DCT and Portfolio.
   Keeping them on GitHub would leave the public repository as the trust
   boundary and keep hosted runners for every pull request.
2. **Deploy jobs.** Recommended: remove them (D6). Porting needs a release
   template with tailnet access and a separate trusted repository, for a path
   that is paused today.
3. **Registry.** Recommended: stay on GHCR (D4). Moving needs a binary change
   and a way for Coolify to pull from Gitea.
4. **Template or generic profiles.** Recommended: a Helm template (D3). Generic
   profiles cannot run `container` at all and would run `browser` cold.
5. **Outside contributors.** After cutover a pull request opened on public
   GitHub gets no checks. Recommended: accept this and say so in the README.
6. **Capacity.** Recommended: accept queueing behind DCT at first, measure it
   during shadow, and cap DCT only if the gate fails. Do not raise fleet
   ceilings.
7. **VM128.** It is stopped but still reserves 8 vCPU, 31 GiB and an address.
   Recommended: retire it in H10 with the old single-VM plan.
8. **Helm's own pull-request links.** The product links only GitHub pull
   requests. After cutover Helm's own would be on Gitea. Recommended: track a
   Gitea provider as separate product work, not part of this migration.
9. **Dependence on Dev.** Recommended: proceed and record it as a known risk;
   the durable manager host is platform work (AR-8, AR-27).

## Success criteria

- Thirty days after cutover Helm has run zero jobs on GitHub-hosted runners.
- Median time from push to four green checks is no worse than hosted.
- A production rollout has used a Gitea-built image with complete provenance.
- No Helm deployment or registry credential exists in any CI system.
- The wiki has an ActionRunners page and an enrollment runbook, and its Helm
  pages match how Helm ships.
