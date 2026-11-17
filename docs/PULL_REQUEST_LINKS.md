# Pull request links

GitHub and Gitea/Forgejo support consists only of saved PR URL links on tasks.
Open a task's Details tab, enter a **Pull request URL**, and select **Link pull
request**. Several PRs can link to one task, and one PR can link to several
tasks. **Remove** detaches the link from that task.

Accepted HTTPS URL shapes:

- GitHub: `https://github.com/owner/repository/pull/123`
- Gitea/Forgejo: `https://your-forge.example/owner/repository/pulls/123`

The host, repository and PR number identify the link. Query strings, fragments
and supported PR subpages normalize to the PR itself. Different Gitea hosts
remain separate. Helm does not contact either service when saving or reading
links, and does not verify that a PR exists. No account connection, credentials,
repository selection, branch view, checks, reviews, remote status or source
fetching is available. Existing saved GitHub PRs appear as plain links.

## API

- `GET /api/v1/tasks/{task}/pull-requests` returns `{ "items": [...] }`.
- `POST` to the same path with `{ "url": "https://github.com/owner/repo/pull/123" }`
  returns `{ "link": {...}, "task": {...} }` and status 201.
- `DELETE /api/v1/tasks/{task}/pull-requests/{link}` returns `{ "task": {...} }`.

Reads require `tasks:read`; writes require `tasks:write`, project access, the
current task version in `If-Match`, and permission to modify the task's claim.
Mutations accept `Idempotency-Key`. Adding an already linked canonical URL is
idempotent and does not advance the task version. Links never replace task
text or comments. Each task supports up to 100 links.

## Preservation

Existing durable artifact tables store these links without a schema migration.
GitHub keeps its historical `owner/repository` storage identity; Gitea includes
its host in the storage identity to avoid collisions.

Portable version 5 includes both providers using the retained artifact archive
shape, without cached metadata or credentials. Versions 1–4 remain importable.
Explicit export to versions 2–4 rejects data containing Gitea identities rather
than silently omitting or misrepresenting those links. A retained older binary
can continue reading and writing the same database; use the current binary to
export or import the complete link set. Private historical audit evidence keeps
its existing access restrictions.

Failure scenarios and real-server acceptance were written before implementation
in `web/e2e/pull-request-links.spec.ts`: save/reload/remove, canonical duplicates,
multiple hosts/tasks, stale writes, denied access, unsafe URLs, mobile keyboard
use, draft preservation and portable roundtrips. The test retains synthetic
HTTP receipts, a screenshot and a trace. Run it with
`test/e2e/run.sh e2e/pull-request-links.spec.ts e2e/github-removal.spec.ts`.
