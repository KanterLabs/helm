# Helm CI migration audit

Read-only audit behind the [migration plan](GITEA_CI_MIGRATION_PLAN.md), made
on 2026-11-12 for Helm task AR-43. Nothing was changed. Sources: this
repository at `0bdb2c9`; the infrastructure checkouts
`actionrunners-infra` and `portfolio-actionrunners-infra`; the DCT, Portfolio
and wiki repositories; the live fleet file; and the GitHub and Gitea APIs.
Paths outside this repository are written as plain text.

## 1. Where Helm runs today

| Fact | Evidence |
| --- | --- |
| All jobs of the latest `main` run used GitHub-hosted `ubuntu-24.04` | GitHub run 43130197159 |
| Workflows exist only in `.github/workflows` | `ci.yml`, `beta-candidate.yml`, `warm-runner-canary.yml` |
| Homelab routing for `browser` is off | `HELM_WARM_RUNNER_ENABLED=false` since 2026-11-07 |
| Both deploy jobs are paused | `HELM_BETA_DEPLOY_PAUSED=true`, `HELM_PRODUCTION_DEPLOY_PAUSED=true` |
| Gitea has run 6 Helm jobs ever, the last on 2026-10-02 | Gitea Actions API |
| No runner is registered for Helm or the organization on Gitea | Gitea Actions API; instance scope not checked |
| Gitea and GitHub tips match | `main` `0bdb2c9`, `beta` `10cadb1` |
| The GitHub repository is public; `main` requires four checks | `checks`, `browser`, `race`, `container`; strict; admins enforced |

Repositories with recent Gitea Actions runs: `portfolio`,
`portfolio-release`, `data_center_tycoon` and `wiki`.

## 2. The pipeline

Hosted durations are from the two most recent push runs.

| Job | Does | Needs | Hosted time |
| --- | --- | --- | --- |
| `checks` | npm audit, OpenAPI and docs checks, web build, gofmt, actionlint, `go vet`, govulncheck, deployment security tests, static build, `docker compose config` | Go, Node 22.23.2, Python, Docker CLI with compose, internet | 134 to 180 s |
| `browser` | Race-instrumented server build and the Playwright suites through `test/e2e/run.sh` | Go, Node, Chromium with system packages, 4 workers, cgroup v2 | 753 to 849 s |
| `race` | Asserts that `browser` succeeded | Nothing | 4 s |
| `container` | `docker build --pull`, smoke run, `docker save`, Trivy scan, GHCR push on `beta` and `main` | Docker daemon, internet, registry credential | 139 to 165 s |
| `beta_deploy`, `deploy` | Tailscale join, then `deploy/coolify-deploy.py` | Protected environment, Coolify token, tailnet | Skipped |

No job runs `go test`; race coverage comes from the instrumented server in the
browser suites. Artifacts: `helm-binary-<sha>`, `helm-image-<sha>`,
`helm-e2e-<sha>`, and `helm-coolify-<lane>-<sha>` from the deploy jobs.

## 3. GitHub-specific constructs

| Construct | Where | On Gitea |
| --- | --- | --- |
| `runs-on` built with `fromJSON` and a `{group, labels}` runner group | `ci.yml:77`, `warm-runner-canary.yml:31` | Use plain `homelab` and `homelab-heavy`; runner groups do not exist |
| `environment:` with protection and scoped secrets | `ci.yml:216`, `:282` | No equivalent; Portfolio moved deployment out of canonical CI |
| `secrets.GITHUB_TOKEN` with `packages: write` for GHCR | `ci.yml:183-191` | Not available; publish from a trusted process |
| `github.com` extraheader in the branch-tip check | `ci.yml:246`, `:312` | `origin` would be Gitea; the check leaves with the deploy jobs |
| `runner.environment == 'self-hosted'` | `ci.yml:84-103` | May be empty under the Gitea runner; do not branch on it |
| `actions/upload-artifact` v7, `actions/cache` v6 | several | Earlier migrations pinned v4-era artifact actions or the `ChristopherHX` Gitea artifact actions, and `actions/cache` v4.1.0 |
| Short action references | all | Pin by full URL and commit, as DCT, Portfolio and the wiki do |
| `workflow_dispatch` | all three | Posts no commit status on Gitea, so it cannot satisfy a required check |
| `matrix.max-parallel` | `warm-runner-canary.yml` | Ignored on Gitea (DCT finding) |

## 4. Tests that pin workflow content

All run in `checks` through `deploy/test-deployment-security.sh`.

- `deploy/security-tests/setup.sh:17-18,39-47` hard-codes the two workflow
  paths and slices `beta_deploy` out of `ci.yml`.
- `deploy/test-ci-runner-policy.py:13-41` requires the job set `checks`,
  `browser`, `race`, `container`, `beta_deploy`, `deploy`, the exact `browser`
  selector string, and `ubuntu-24.04` everywhere else.
- `deploy/security-tests/test_ci_runner_policy.py:18,93-112` mutates
  `.github/workflows/ci.yml` to prove the checker.
- `deploy/security-tests/ci-and-private-deployment-policy.sh` asserts exact
  strings: concurrency groups, environment names and URLs, both pause
  variables, every `vars.*` and secret, the GHCR image name and digest regex,
  the Tailscale action pin, action version comments, and
  `persist-credentials: false` after every checkout.
- `deploy/security-tests/gateway-command-and-release-contract.sh:97-101,140-142`
  pins `beta-candidate.yml` and the image artifact name.
- actionlint (`ci.yml:60`) scans `.github/workflows` only and knows the labels
  in `.github/actionlint.yaml`.
- `web/scripts/check-docs.mjs` validates relative links; `README.md:273`,
  `docs/BETA_DEPLOYMENT_PLAN.md:84` and `docs/HOMELAB_DEPLOYMENT.md:61` link to
  workflow files.

## 5. Tooling that calls GitHub

| Consumer | Dependency |
| --- | --- |
| `deploy/release_support.py` | Requires `origin` to be the GitHub URL and both tips equal; reads GitHub runs and jobs by name; downloads `helm-coolify-<lane>-<sha>`; lists pull requests |
| `deploy/release_prepare.py` | Records a GitHub run ID in `.helm-release.json`; opens the release pull request with `gh` |
| `deploy/release_finish.py` | Treats the merged GitHub pull request and GitHub `main` as authoritative, then fast-forwards Gitea; creates releases on both |
| `test/deployment-policy/runtime_acceptance_provenance.py` | GitHub run, artifact `digest`, artifact ZIP and job-log APIs; parses exact checkout and upload log lines |
| `test/deployment-policy/runtime_acceptance_bundle.py:48-49` | Asserts `oci_blob_hashes_verified` and `github_zip_digests_verified` |
| Rollout scripts under `.artifacts/*-deploy/provenance/` | Operator copies of the verifier, pinned to GitHub run IDs |
| `test/e2e/prepare.py:202-203` | Records `GITHUB_RUN_ID` and attempt in build provenance |
| `skills/helm/scripts/update_skill.py:23,228-247` | Updates from GitHub `main` only |
| `test/release-pipeline/`, `test/deployment-policy/release_journeys.py` | Fake `gh` covering runs, jobs, downloads, pull requests and releases |
| `Dockerfile:42` | Downloads cloudflared source from GitHub at build time; works from a job VM |

The product's own GitHub integration (`internal/githubprovider`, the Branches
view, pull-request links) is separate from CI and reads the GitHub mirror.

## 6. Image identity

- Built only by `container`. The single label is
  `org.opencontainers.image.source=https://github.com/KanterLabs/helm`.
- Pushed as `ghcr.io/kanterlabs/helm:<sha>` on `beta` and `main` pushes;
  deployed only by digest. Pull-request runs never publish.
- The name is hard-coded in `internal/deployadmission/marker.go:81`,
  `deploy/rollback_policy.py:15`, `deploy/release_support.py:24`,
  `deploy/admission/bootstrap.py:42`, and many fixtures.
- Coolify pulls anonymously; the package must stay public.
- The provenance flags are asserted only by repository tooling, not found in
  the admission operator source. Confirm against the installed operator
  before H6.

## 7. ActionRunners as it is today

From `homelab/ci-runners/actionrunners/` and the live `fleet.json`, which has
uncommitted edits in the `actionrunners-infra` checkout that the controller
reads.

- **Model.** A controller on Dev polls Gitea for queued jobs, registers an
  ephemeral runner per job with a repository-scoped token, clones a template
  into one of 10 fixed slots, and destroys the VM after one job.
- **Profiles.** `standard` 2 vCPU, 4 GiB, 40 GB as `homelab`; `heavy` 4 vCPU,
  16 GiB, 100 GB as `homelab-heavy`; repository-specific variants for DCT and
  Portfolio on their own templates. Labels are mapped per repository.
- **Ceilings.** 10 VMs, 48 vCPU, 110,592 MiB. Limits: DCT 2 short and 8 heavy
  with 10 concurrent; Portfolio 2 and 1; `portfolio-release` 1 and 1; wiki 1;
  canary 4.
- **Isolation.** Generic jobs run in an unprivileged container with no Docker
  socket. A slot reaches the Gitea edge, public ports 80 and 443, DNS and NTP;
  private ranges, the tailnet and other slots are dropped. Only Portfolio's
  release template gets a one-use tailnet key minted on Dev.
- **Limits.** Job timeout 3 hours; runner unclaimed after 15 minutes.
- **Enrollment** is a `fleet.json` edit plus a bot token, a registration token
  and a controller restart. There is no runbook or interface (AR-15 is open).
- **Not yet delivered.** Persistent caches (AR-13), a durable manager host
  (AR-8, AR-27), the cold-start p95 gate, retirement of the old runner VMs
  (AR-22).
- **Helm.** VM128 `helm-ci` is stopped, reserves an address and 8 vCPU with
  31 GiB, and is not counted as free. No capacity is reserved for Helm and no
  ordering is stated.
- **Edge allowlist.** A hand-added Gitea allowlist entry covers only four slot
  addresses in the notes; whether it was widened to all ten is not recorded.

## 8. What the earlier migrations did

| | DCT | Portfolio | Wiki |
| --- | --- | --- | --- |
| Workflows | Moved to `.gitea/workflows`, same labels | Moved; expression in `runs-on` replaced by plain labels | Written for Gitea |
| Qualification | Branch with `ar-dct-canary-*` labels, baseline, then label switch | Five canary workflows, cutover and rollback rehearsal | None recorded |
| Publish and deploy | None in CI | Removed from canonical CI; separate `portfolio-release` repository, a coordinator and publisher on Dev, one-use tailnet key | Deployer on Dev pulls the artifact; CI holds no secrets |
| Images | None | Dev publisher pushes the artifact to GHCR and writes a receipt | Built on the Coolify VM |
| Protection | Required `CI / ci-verdict (pull_request)` | `main` and `beta` protected; `main` accepts only promotion from `beta` | None; direct pushes |
| GitHub | Actions disabled; one-way mirror from a timer on the Coolify VM | Same | Not mirrored |
| Rollback | Script that swaps fleets; old VM kept | Ordered pause, unenroll, restore; 7-day retention | Image restore |

Differences that cost them time: the missing `GITHUB_WORKFLOW_SHA`; artifact
expiry deleting artifacts of active runs; one transient artifact upload
refusal; nested Docker falling back to a blocked DNS server; guest poweroff
refused by logind; a deadline keyed on the wrong marker that cancelled a
15-minute build; Gitea dropping the runner name once an ephemeral runner
deletes itself.

## 9. Documents that are stale or must change

- **This repository.** `docs/HOMELAB_DEPLOYMENT.md`, `docs/OPERATIONS.md` and
  `docs/BETA_DEPLOYMENT_PLAN.md` describe the retired container deployment and
  `homelab` runners. `docs/RELEASING.md` says a merge to `main` deploys
  production. `docs/CI_SINGLE_VM_PLAN.md` plans the VM128 route this migration
  replaces. `README.md` has a GitHub Actions badge and clone URL.
- **Workspace policy.** `/home/shane/projects/CLAUDE.md` says ARC runner pods
  are the platform; `/home/shane/projects/AGENTS.md` says the homelab fleet is
  retired and hosted runners are the default. Both date from 2026-10-14 and
  neither mentions ActionRunners.
- **Infrastructure.** The runner operating contract still describes Portfolio
  as mid-cutover, and DCT's own CI documents still name its old runner VM.

## 10. Wiki review

Checkout `6490114`, equal to the server on 2026-11-12. Seventeen pages.

**Relevant content**

- `docs/about/how-this-wiki-ships.md` and `.gitea/workflows/ci.yml` are a
  complete small example: one `homelab` job, actions pinned by URL, an
  artifact, and a deployer on Dev that admits only the newest successful push
  run for the exact `main` commit.
- `docs/infrastructure/network-map.md` places runner VMs at `.44` to `.53`,
  the controller on Dev and the dashboard at `actions.home.shanekanterman.dev`.
- `docs/infrastructure/service-operations.md` and
  `docs/runbooks/deploy-and-rollback.md` describe Helm's lanes and state that
  Gitea is canonical and GitHub is the execution mirror.
- `docs/runbooks/gitea-backup-pool.md` excludes Actions artifacts and logs from
  backup, so provenance must not depend on them surviving.

**Gaps**

- No ActionRunners page and no enrollment runbook;
  `docs/runbooks/add-private-app.md:282-284` says the runbook does not exist.
- No list of which repositories run on Gitea Actions, no guide for leaving
  GitHub Actions, nothing on Gitea Actions secrets or the GitHub mirror.
- Nothing on Helm's native admission rollout.

**Contradictions**

- Runner reach: the wiki workflow says runners reach "nothing private", while
  `docs/access/private-apps.md:80-87` says CI runners are granted Coolify and
  LAN access to Gitea, Coolify and Proxmox.
- Helm deployment: `docs/runbooks/deploy-and-rollback.md:130` says GitHub
  branch pushes trigger the protected deployment workflow. Both deploy jobs
  are paused and deployments are operator-run. The page cites Helm at
  `1c54493`.
- Gitea version: the backup runbook restores to Gitea 1.27; the server reports
  28.3.0.
- `docs/runbooks/add-private-app.md:90` gives edge port `19514` as the next
  free one; `docs/access/wiki-sign-in.md:53` already uses it.

## 11. Unknowns to settle in H1 and H2

- Why Gitea creates no Helm runs today, and whether it prefers
  `.gitea/workflows` when both directories exist on 28.3.0.
- The exact status context names Gitea reports for the four jobs.
- Whether the Helm suites run under the template's container settings:
  cgroup metrics in `test/e2e/runner-metrics.py`, process subreapers, port
  ranges, and Chromium without `sudo`.
- Cold versus seeded timings for `browser` on the heavy profile.
- Whether Portfolio's publisher and the mirror bridge can be reused as they
  are or must be generalized.
- Whether the installed admission operator inspects provenance fields.
