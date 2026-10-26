# GitHub token beta validation and rollout

Companion to [the implementation plan](GITHUB_TOKEN_BETA_PLAN.md).
Acceptance owner: TC-298, with feature coverage in TC-291 through TC-293 and
server execution in TC-300. See [execution design](GITHUB_TOKEN_BETA_EXECUTION.md).

## Acceptance and evidence

Write end-to-end failure scenarios before feature code. For isolated provider
logic, record how it can fail before its implementation and isolation tests.
Use fake GitHub and AI adapters with synthetic repositories and a sentinel token;
production configuration keeps the real API origin fixed. Existing audit
browser tests mock the whole API and are insufficient on their own.

The integrated test must persist a link, restart Helm, show its real API
summary, click Run audit, capture GitHub evidence and execute the real server
job, close/reopen the page and see its findings, approve without a task move,
then explicitly apply only the eligible unchanged recommendation. No external
agent process, checkout, copied request or CLI submission is involved. Save a
sanitized screenshot/trace or captured response on success. Also audit a
selected branch with no linked PRs; tasks must show honest source coverage.

Cover duplicate/variant URLs, unlink/relink, one PR on multiple tasks, several
repos on one task, drawer-switch races and preserved drafts. Cover revoked /
expired tokens, organization approval, missing scopes, 404, timeout, 429,
partial pages, no checks, failing checks, dismissed reviews, force-push and
out-of-order refreshes. Verify evidence cannot cross project boundaries or
remain visible after deselection. Verify task-page 51, Back navigation,
initial load errors, keyboard focus, 320/390/1440 widths, light/dark and WebKit.
Assert the sentinel never appears in logs, API payloads, exports, prompts,
child-process environments/arguments, browser storage or artifacts. Prove
model tools cannot read the token file, unrelated data or invoke shell/network
operations. A merged PR must not complete a task.

Exercise durable queue ownership, duplicate clicks/request replay, concurrent
claims, expired leases, cancellation/late completion, shutdown and restart.
Queued jobs resume; interrupted AI attempts require explicit Retry rather
than silent repeated inference. Retry links to the previous run, preserves
its evidence and cannot publish duplicate findings. Cover AI unavailable,
disconnected account, timeout, malformed output, invented citations, bounded
input/output, and missing/binary/oversized files. Prove unknown evidence
produces partial coverage, never a confident all-clear. A force-push during
fetch must not mix commits. Permission revocation fences both publication
and evidence-dependent Apply, not merely the UI.

Capture worker job outcomes separately from existing audit statuses. Prove
the legacy create/submit flow still works and cannot inject/finalize results
into worker-owned runs. Keep GitHub and AI readiness failures independent
from application readiness and ordinary task work.

Run proportionate repository checks after focused scenarios pass:

```sh
check-file-sizes
make build
go test ./internal/db ./internal/config ./internal/store ./internal/httpapi
go vet ./...
./deploy/test-deployment-security.sh
HELM_E2E_GOFLAGS=-race ./test/e2e/run.sh e2e/pr-links.spec.ts e2e/audit.spec.ts
```

`pr-links.spec.ts` is proposed. Add provider tests and run the required CI
suites before release; serialize builds and E2E because the harness replaces
embedded assets. Any CI job created or modified uses `homelab` or
`homelab-heavy` per the current policy unless a live incompatibility is
demonstrated and reported. Revalidate existing hosted-runner exceptions;
this plan does not silently migrate infrastructure or rewrite workflows.

## Data preservation and beta rollout

Use the current [Coolify runbook](../deploy/coolify/README.md) and
`deploy/coolify-deploy.py`; the older LXC beta runbook/switcher is retired.
Private beta has separate data/config under `/srv/helm/beta`. The deployer
changes image and revision only, so token provisioning is a distinct step.

1. Start from the current canonical beta revision; reconcile the GitHub
   mirror and preserve unrelated local work. Record candidate and previous
   image digests and revisions. Select the next migration number at execution
   time; do not edit existing migrations.
2. Use additive tables and test populated upgrades, IDs, relationships,
   comments, claims, audit history, jobs and PR artifacts. Durable links and retained
   audit evidence belong in a new explicit portable archive version; keep
   v1/v2 imports working. Older importers reject unknown fields, so document
   v3 support and test it rather than silently altering v2. Credentials and
   disposable cache stay out of portable exports. Export retained findings
   and evidence, not executable leases or live job credentials; imports never
   launch jobs. Full database backup retains operational job history.
3. Take a verified online pre-upgrade database backup with integrity, foreign
   key and checksum evidence. Run candidate schema preflight on a protected
   disposable copy. Run the retained beta binary against the upgraded copy,
   including old API writes and restart; prove new rows survive. Stop the new
   worker before rollback; old binaries must not claim its jobs or mutate
   worker-owned run results through legacy routes. Do not reset
   the database or roll back by restoring an older snapshot.
4. Provision only beta's token file and config, verify service UID access,
   and confirm runtime/config backups remain root-only: they contain secrets.
   Verify the requesting human's AI connection and the isolated audit adapter
   are usable on beta; GitHub token access alone cannot execute an audit.
   The existing backup service covers both lanes; use its established path
   without changing production configuration or database content.
5. Publish the intended beta revision through canonical Gitea and its GitHub
   deployment mirror. Complete required checks and deploy its immutable image
   via Coolify. Require the expected revision from both `/healthz` and
   `/readyz`, not liveness alone.
6. Compare preserved record IDs/counts and smoke-test a real authorized PR,
   refresh and an audit started and reviewed entirely in Helm. Verify
   missing/disabled token or AI connection leaves the rest of Helm usable.
   Take a verified post-release backup.
7. Roll back image/revision only, retaining current data and additive tables.
   If retained-binary compatibility is not proven, stop promotion and fix it.
   Destructive restore requires separate authorization and an exact backup.

Production promotion is not part of this beta prototype. The plan is complete
when its scope, ordered cards and measurable release gates are recorded;
implementation is complete only after the live beta checks above pass.
