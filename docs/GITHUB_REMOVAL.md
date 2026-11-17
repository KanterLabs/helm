# GitHub feature removal

TC-391 removes the Branches view and GitHub product integration from beta.
GitHub remains a source mirror and CI/registry provider for this repository;
that infrastructure is independent of the removed product feature.

Failure cases and acceptance, written before implementation:

- Branches, GitHub settings, pull-request linking/badges, or GitHub audit
  controls remain accessible through normal navigation or a saved URL.
- Removed integration, branch, repository selection, artifact, authorization,
  or audit-start endpoints still perform work, including mutation requests.
- Legacy GitHub environment settings still start a client, polling, refresh,
  sign-in, audit executor, or background request to GitHub.
- A removed route silently falls through to an unrelated mutation.
- Previously stored GitHub records or historical migrations are deleted, or
  a candidate/recovery/rollforward journey loses existing tasks or attachments.
- Removing remote access checks accidentally exposes retained private audit
  evidence through generic audit or portable-export routes. Preserve denial.
- Generic human Board Audit, ordinary tasks, comments, Focus, portability,
  agents, or unrelated settings stop working.
- The new GitHub App or its beta credential remains active after retirement.

`web/e2e/github-removal.spec.ts` exercises the real server, task flow, UI and
removed endpoints and attaches screenshots plus captured HTTP status evidence.
Run with `test/e2e/run.sh e2e/github-removal.spec.ts`. Deployment additionally
requires exact image provenance, verified fresh backups, populated candidate
and retained recovery tests, live readiness, and live absence of the feature.
Historical SQLite tables and retained import/export representations remain
for compatibility; no schema reset, destructive migration or data restore is
part of this removal.
