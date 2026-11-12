# Gitea shadow CI

How the shadow pipeline in [`.gitea/workflows/ci.yml`](../.gitea/workflows/ci.yml)
runs and how to read it. It is Helm card H3 of the
[migration plan](GITEA_CI_MIGRATION_PLAN.md); its failure contract is
[GITEA_CI_SHADOW_FAILURES.md](GITEA_CI_SHADOW_FAILURES.md).

GitHub Actions stays authoritative until cutover. A red or missing shadow run
blocks nothing; it is evidence for the 20-run shadow gate (H4).

## What runs

| Job | Label | Work |
| --- | --- | --- |
| `checks` | `ar-helm-canary-short` (2 vCPU, 4 GiB) | The commands of GitHub's `checks`, then the `helm-binary-<sha>` artifact |
| `browser` | `ar-helm-canary-heavy` (4 vCPU, 16 GiB) | The race-instrumented browser suites through the shared composite action, then the `helm-e2e-<sha>` evidence |
| `race` | `ar-helm-canary-short` | Passes only if `browser` succeeded |

`container` waits for the Helm runner template with contained Docker (plan
D3). `beta_deploy` and `deploy` are never ported (plan D6).

Triggers: every pull request, a push to any branch, and a manual run. A newer
push or pull-request update cancels the older run, except on `main` and
`beta`. Manual runs post no commit status on Gitea, so they never stand in for
a pull-request or push result.

## Where it differs from GitHub, and why

Each job runs as root in a disposable `node:24-bookworm` container that
reaches only Gitea and the public internet. There is no Docker daemon, no
`sudo` and no cache that survives the job.

- **Toolchain.** `setup-go` and `setup-node` install Go and Node with caching
  off, then [`prepare-runner.sh`](../.gitea/scripts/prepare-runner.sh) stops
  the job unless Go equals the `go.mod` toolchain, Node is 22.23.2, Python is
  3.11 or newer and the job is root. `GOTOOLCHAIN=local` stops Go from
  switching toolchains behind that check.
- **Compose.** `docker compose config` becomes `docker-compose config` with
  the standalone Compose v5.6.1 binary, checked against its pinned SHA-256. It
  renders and validates `compose.yaml` without a Docker CLI or daemon. Nothing
  else in `checks` or `browser` needs Docker, `sudo` or a GitHub-only
  variable.
- **Ubuntu tools.** `checks` installs gawk, jq, sqlite3, zip and zstd, which
  GitHub's image ships and bookworm lacks, and fails unless each exists. A
  local replay found three effects: bookworm's mawk (1.3.4-20200120) has no
  `{n}` intervals, so the deploy gateway's hwaddr check rejected a valid CT;
  the Service Auth probe needs `jq`; and the restore SQL overlay test skips
  itself silently without `sqlite3`. The gateway check would fail the same way
  on any Debian 12 host whose `awk` is that mawk.
- **Chromium.** `npx playwright install --with-deps chromium` installs its
  system packages directly, since the job is root, and the script launches
  Chromium once before the suites start.
- **Root.** GitHub's hosted jobs are not root, so the deployment security
  tests take their root-only rollback path here for the first time.
- **Artifacts.** Uploads use `ChristopherHX/gitea-upload-artifact`, the action
  the wiki, DCT and Portfolio use on this server. The shared
  `browser-validation` action is called with `upload-evidence: 'false'`, so its
  GitHub `upload-artifact` v7 step is skipped here and still runs on GitHub.
- **No caches.** No `actions/cache` and no setup caches: every job starts
  cold until the Helm template seeds them, so expect slower jobs than hosted.
- **actionlint** still lints `.github/workflows` only. It rejects the
  full-URL action references Gitea uses, so the policy test below checks the
  Gitea workflow instead.

## The policy test

`deploy/test-deployment-security.sh`, which runs inside `checks` in both
pipelines, sources `deploy/security-tests/gitea-shadow-policy.sh`. It runs:

- `deploy/test-gitea-ci-policy.py`: no secret, variable, token, environment,
  registry login, image push or deploy step; only reviewed actions, pinned by
  full URL and commit; every checkout sets only `persist-credentials: false`;
  exactly `checks`, `browser` and `race`, each on an allowed label
  (`ar-helm-canary-*` now, `homelab`/`homelab-heavy` after cutover) with a 40
  minute timeout.
- `deploy/security-tests/test_gitea_ci_policy.py`: mutation proofs that the
  checker rejects each failure in the contract and accepts the cutover labels.
- A parity check that every command in GitHub's two `checks` steps also runs
  in the shadow job, so the shadow cannot pass by doing less.

Run it locally with `./deploy/test-deployment-security.sh`, or only the new
parts with:

```sh
python3 deploy/test-gitea-ci-policy.py .gitea/workflows/ci.yml
python3 deploy/security-tests/test_gitea_ci_policy.py --artifact "$TMPDIR/gitea-proofs.json"
```

## Starting and reading a run

Until H2 enrolls the repository in the runner fleet nothing can run: no
runner serves the canary labels, and Gitea has created no Helm run since
2026-10-02 for reasons H2 must find. After enrollment, push the branch to the
`gitea` remote and open the repository's Actions tab on Gitea. A push with no
run is an enrollment failure, not a pass.

- **Agreement.** For each commit, compare the shadow jobs with GitHub's
  `checks`, `browser` and `race`. Record any disagreement before counting the
  commit toward the gate.
- **Timing.** Compare each job's wall clock, including queue time, with the
  hosted figures in the [audit](GITEA_CI_MIGRATION_AUDIT.md).
- **Evidence.** `helm-e2e-<sha>` holds the browser artifacts and
  `helm-runner-metrics`, the same bundle as on GitHub.
  [test/e2e/README.md](../test/e2e/README.md) explains the bundle.
- **First run.** It must list only these three jobs. If Gitea also starts the
  `.github/workflows` jobs, disable the fleet entry and stop: those jobs
  reference variables, environments and secrets.
