# Gitea shadow CI failure contract

Recorded before the shadow workflow, its runner preparation script and its
policy test were written (Helm cards H1 and H3, task AR-43). It narrows the
"Failure cases" of the [migration plan](GITEA_CI_MIGRATION_PLAN.md) to the
first shadow slice: `.gitea/workflows/ci.yml` with the jobs `checks`, `browser`
and `race` on the canary labels, while GitHub stays authoritative.

Each row names how the failure is detected. "Policy test" means the static
checker run by `deploy/test-deployment-security.sh` in both pipelines, with a
mutation proof that the checker rejects the bad workflow. "Runner" means the
job itself fails with a message that names the cause. "Soak" means it can only
be observed by comparing real runs once the repository is enrolled (H4).
The soak ledger that does that comparison has its own contract,
[GITEA_CI_SOAK_FAILURES.md](GITEA_CI_SOAK_FAILURES.md).

## Trust boundary

| Failure | Detection |
| --- | --- |
| The Gitea workflow reads a secret (`secrets.*`), a repository or organization variable (`vars.*`), or a token context (`github.token`, `gitea.token`, `GITHUB_TOKEN`, `GITEA_TOKEN`). | Policy test. |
| A job declares a protected `environment:`. | Policy test. |
| A step logs in to a registry (`docker login`, a login action, `--password-stdin`) or pushes an image (`docker push`, `ghcr.io`). | Policy test. |
| A deploy step appears: Tailscale, the Coolify deploy script or its `COOLIFY_*` settings, the release tooling, Cloudflare publication, live validation, rollback. Syntax checks of deploy files (`bash -n`, `py_compile`) stay allowed. | Policy test. |
| `permissions` grant anything but `contents: read`, or a job overrides them. | Policy test. |
| The workflow gains a privileged trigger (`pull_request_target`, `workflow_run`) that runs with base-repository context. | Policy test. |
| A checkout leaves credentials in `.git/config` (missing `persist-credentials: false`, or `true`). | Policy test, per checkout step. |
| A job asks for a job container, service containers or the Docker socket, changing the runner contract. | Policy test. |
| A job VM reaches the tailnet, Coolify, Infisical, the LAN or another slot. | Not testable from the workflow. Covered by the H2 isolation canaries on the fleet; the shadow workflow needs no private address, so any such access is a fleet fault. |

## Supply chain

| Failure | Detection |
| --- | --- |
| An action is referenced by tag, branch, short SHA or short `owner/repo` form instead of full URL and 40-character commit. | Policy test. |
| An action outside the reviewed set is introduced, even pinned. | Policy test (allowlist of action repositories). |
| A local composite action other than the reviewed browser validation is used, or it is used without disabling its GitHub-only artifact upload. | Policy test. |
| A downloaded tool (Docker Compose) does not match its pinned SHA-256. | Runner: preparation stops before the tool is installed. |

## Job set and placement

| Failure | Detection |
| --- | --- |
| A job other than `checks`, `browser` and `race` appears (including `container`, `beta_deploy`, `deploy`), a job is missing, or a name is duplicated. | Policy test. |
| `runs-on` is missing, duplicated, an expression, a list, a runner group, or a label outside `ar-helm-canary-short`, `ar-helm-canary-heavy`, `homelab`, `homelab-heavy`. | Policy test. |
| `browser` lands on a short (2 vCPU, 4 GiB) label or `checks`/`race` on a heavy one, wasting the shared fleet or starving the suite. | Policy test (short labels for `checks` and `race`, heavy labels for `browser`). |
| A job loses its 40 minute timeout and holds a slot for the platform's 3 hours. | Policy test. |
| Triggers lose `pull_request`, `push` or `workflow_dispatch`, or `push` becomes branch-filtered during shadow so a pushed branch gets no run. | Policy test. |
| Concurrency cancels a `main` or `beta` run, or stops cancelling superseded branch and pull-request runs. | Policy test pins the group and the cancel expression. |
| `race` passes without `browser` succeeding. | Policy test pins `needs: browser` and the result check. |

## Toolchain on the generic runner

| Failure | Detection |
| --- | --- |
| Go differs from the `go.mod` toolchain, or Go silently downloads another toolchain. | Runner: preparation compares `go env GOVERSION` with `go.mod`; `GOTOOLCHAIN=local` forbids switching. |
| Node is not 22.23.2 (the container's own Node 24 wins on `PATH`). | Runner: preparation checks `node --version`. |
| Python is older than the tests support, or `git` is missing. | Runner: preparation checks both. |
| The job does not run as root, so `apt-get` through Playwright's `--with-deps` cannot install Chromium's packages. | Runner: preparation checks the user id first. |
| Chromium installs but cannot launch (missing library, sandbox refused as root). | Runner: preparation launches Chromium once before the suites start. |
| `docker compose config` cannot run because the runner has no Docker CLI. | Runner installs the pinned standalone Compose binary, which renders and validates `compose.yaml` without a daemon. A missing binary fails the step. |
| A check needs a Docker daemon or `sudo`. | Runner: the command fails. None in `checks` or `browser` does today (the image build is `container`, deferred); this must stay true, and any new one is a parity failure, not a skip. |
| The deployment security tests take a different branch as root: GitHub-hosted runners are not root, so the root-only paths of the rollback and restore tests first run here. | Runner and local container replay before enrollment; a failure is fixed, never skipped. |
| The container lacks a tool GitHub's Ubuntu image has, and a test fails or quietly skips itself. Found by the local replay: bookworm's mawk 1.3.4-20200120 has no `{n}` intervals (the deploy gateway's hwaddr check rejects a valid CT); `jq` is missing (the Service Auth probe); the restore SQL overlay test skips itself without `sqlite3`. | Runner: `checks` installs gawk, jq, sqlite3, zip and zstd, then fails unless each command exists and `awk` supports intervals. |
| The job runs out of 4 GiB memory or 40 GB disk on the short label. | Soak: compare durations and failures; move to heavy only with evidence. |

## Gitea behaviour differences

| Failure | Detection |
| --- | --- |
| `actions/upload-artifact` v7 or `actions/cache` v6 do not work on Gitea. | Avoided: the workflow uses the Gitea artifact action proven by the wiki, DCT and Portfolio and no cache action. Policy test rejects the v7 and v6 pins. |
| An evidence or binary artifact is missing or misnamed (`helm-binary-<sha>`, `helm-e2e-<sha>`). | Policy test pins names and `if-no-files-found: error`; soak confirms they download. |
| A step depends on `GITHUB_WORKFLOW_SHA` or branches on `runner.environment`, which Gitea may leave empty. | Policy test. |
| `workflow_dispatch` runs are mistaken for pull-request evidence (they post no commit status). | Documented in the shadow guide; soak compares push and pull-request runs only. |
| Gitea ignores `.gitea/workflows` or runs `.github/workflows` too, so the GitHub jobs (with `vars`, environments and secrets) start on the fleet. | Soak: the first shadow run must list only the three jobs. Unexercised until enrollment. |
| Gitea creates no run at all (as since 2026-10-02). | Soak: a branch push with no run within the controller interval is a failure of enrollment, not a pass. |

## Parity with GitHub

| Failure | Detection |
| --- | --- |
| A job on Gitea skips or weakens a command that GitHub runs, so it passes a commit GitHub fails. | Review against `.github/workflows/ci.yml`; the shadow guide lists every intended difference. Soak compares results per commit. |
| Gitea fails a commit GitHub passes (flaky, resource, network). | Soak; each disagreement is triaged and recorded before the 20-run gate counts. |
| The shared composite action changes for Gitea and alters the GitHub path. | GitHub keeps running it unchanged; the new input defaults to the old behaviour, and the existing pinned workflow tests pass untouched. |
| A command added to GitHub's `checks` is never ported to the shadow job. | Parity check in the policy module: GitHub's two `checks` steps must run the same commands, Compose spelling aside. |
| Editing the Gitea policy test weakens the existing `.github/workflows/ci.yml` checks. | The existing checks and their mutation proofs keep running unmodified beside the new ones. |
