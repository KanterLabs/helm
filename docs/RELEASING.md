# Preparing a Helm production release

The main entry point is:

```sh
make prepare-release VERSION=v0.2.2
```

Choose the next unused stable version. The command prepares a PR into `main`
from the exact private-beta revision that passed CI and is currently ready.
It writes `VERSION`, a small `.helm-release.json` manifest, versioned release
notes, and a fresh Unreleased page in an isolated Git worktree. It publishes
the release branch to canonical Gitea first, then GitHub, and opens one PR.
Rerunning reuses the matching branch and PR.

**Preparation never checks out or pushes `main`, merges a PR, creates a
version tag, or deploys production.** Your current branch and uncommitted
files remain untouched. Uncommitted local work is not included: the source is
the explicitly reported remote beta SHA, not an implicit snapshot of disk.

## The everyday path

1. Commit the local work you want to test, then run `make publish-beta`.
   This requires a clean checkout and fast-forwards `beta` on Gitea and GitHub
   in that order. GitHub CI builds, tests and deploys private beta.
2. Test the private beta. `make release-status` shows whether the exact
   branch revisions have passed CI and are deployed. A skipped deployment,
   pending/failed check, mismatched revision or missing artifact is not ready.
3. Run `make prepare-release VERSION=v0.2.2`. Review its PR, release notes,
   and pre-merge checklist. Required `checks`, `browser`, `race`, and
   `container` checks still run on the release PR.
4. Merge the reviewed PR into `main`. **This is the production deployment
   action.** The existing CI builds and deploys its exact merge revision
   through the protected production environment.
5. Once production is ready, run `make finish-release VERSION=v0.2.2`.
   It verifies the PR merge, unchanged prepared tree, successful main CI,
   deployment artifact, and exact live production revision. It then
   fast-forwards canonical Gitea `main` to that protected merge, publishes
   matching version tags and release notes on both hosts, and records the
   deployed immutable image digest in the release notes.

The final command does not deploy anything. It refuses to tag a newer,
unrelated `main` commit or publish after a failed deployment. Repeating it
finishes a matching partial publication; conflicting tags or notes are never
overwritten. Keep the release branch until publication finishes.

## Preview, notes, and interrupted work

```sh
python3 deploy/release.py prepare v0.2.2 --dry-run
python3 deploy/release.py prepare v0.2.2 --notes /path/to/reviewed-notes.md
```

By default, notes come from `docs/releases/unreleased.md` on tested beta.
Its old heading and release preamble are replaced. Custom notes are limited
to 12,000 bytes to leave room for deployment details; every generated file
stays at most 20,000 bytes. `VERSION` is the production release identifier;
the web package's internal build version is not changed.

The command prints the isolated checkout location under the repository's
Git common directory. It never resets an existing release branch or deletes
a conflicted checkout. If `main` adds application code absent from beta,
merge it into beta and validate/deploy beta before preparing again. Changes
limited to release metadata can be reconciled during preparation.

After an interrupted push or PR creation, rerun the same preparation command.
If beta/main changed or a branch no longer matches its recorded inputs,
inspect the retained checkout rather than forcing a retry. A manual edit to
release notes is supported when it is committed on the release branch and
published consistently to both hosts; application changes require another
beta validation. Do not change the manifest's recorded source revisions.

## Data-preservation gate before merging

The preparation command does **not** claim that a production backup or
migration rehearsal happened. Its PR includes explicit checklist items for:

- A fresh verified production backup and the currently retained rollback image.
- For changes under `internal/db`, an upgrade rehearsal on a populated copy
  and readiness with the retained image against the upgraded copy.
- Review of release notes, the diff, and the required PR checks.

Follow the current [Coolify backup and rollback procedure](../deploy/coolify/README.md#backup-and-rollback).
Capture the backup timestamp and verification evidence in the PR before
merging. Production keeps its own database, credentials, and account state;
**never copy fake beta tasks to production**. Failed deployment rolls back
the image, not the database. Destructive restore is a separate operation.

## Requirements and host roles

Run on the Linux development host with Python 3.11+, Git, authenticated `gh`,
tailnet connectivity, and the installed Infisical access helper. The command
requests only `lab:/gitea:gitea_token` through that helper. A deliberately
injected `GITEA_TOKEN` is also supported. Secrets stay in process memory and
are never placed in command arguments, Git configuration files, or artifacts.

The remote names and URLs must be exactly:

- `gitea`: `https://gitea.home.shanekanterman.dev/KanterLabs/helm.git`
- `origin`: `https://github.com/KanterLabs/helm.git`

Gitea is canonical; GitHub supplies protected-main PR checks and deployment.
Since there is no automatic push mirror, the commands publish beta/release
branches to Gitea first and GitHub second. The protected-main merge happens
on GitHub, then `finish-release` synchronizes that exact merge back to Gitea
before publishing version tags. `release-status` makes any drift visible.
The commands do not bypass protection or use force pushes.

These commands reuse the existing CI and Coolify service. They do not change
runner tiers, deployment credentials, service configuration, or database mounts.

## Repeatable verification

`make release-test` runs the command flow against disposable real Git
repositories and local HTTP service fixtures. Evidence is written to
`.artifacts/release-pipeline/result.json`; use an explicit fresh directory
when repeating:

```sh
python3 test/release-pipeline/run.py .artifacts/release-pipeline-repeat
```

The [failure contract](RELEASE_PIPELINE_FAILURES.md) explains the boundaries.
The fixture tests do not create a real release, merge main, or touch live data.
