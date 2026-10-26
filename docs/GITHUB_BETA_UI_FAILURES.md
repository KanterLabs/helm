# GitHub beta UI failure inventory

Written before the TC-291/292/293/297 web implementation. Contract:
[GITHUB_BETA_API.md](GITHUB_BETA_API.md). Browser coverage lives in
`web/e2e/pr-links-ui.spec.ts` and `web/e2e/github-audit-ui.spec.ts`; both use
synthetic, credential-free API fixtures. Real-API integration belongs to the
TC-298 pass.

## Task drawer: Link, Unlink and Refresh

- A pasted URL that is not a GitHub pull request (other host, issue URL,
  missing number, `javascript:`) must be refused in the browser and never
  sent. URL variants (`/files`, query, fragment, trailing slash, `www.`)
  must show the same `owner/repo #N` preview before submit.
- A PR already linked to this task must be refused locally without a write.
- A failed link (stale version, claim conflict, disallowed repository,
  network) must keep the typed URL and the form open, show the typed server
  message and leave the existing list untouched. A stale version refreshes
  the task so the next attempt uses the current ETag.
- A link that persists while verification fails must render as an explicit
  *Unverified* row with repository and number only: no title, checks,
  review or merged state may be invented. Retry/Refresh must remain usable.
- Switching the drawer to another task during a link, unlink, refresh or list
  request must not show the first task's response on the second task. The
  unsent URL draft of each task survives switching back.
- Unlink must remove only this task's row, move focus to a stable control and
  update the task version without discarding unsaved drawer edits.
- *Unavailable* (permission loss) must hide cached private title/check data.
  *Stale* and *Partial* snapshots must say so next to the last refresh time.
- Merged, deployed and task completion are three separate facts. A merged
  PR with failing checks shows both. Deployment is *Not tracked here*.
  Nothing in the drawer completes or moves the task.
- *Checks passed* never reads as *Ready to merge*; an approval is labelled
  a review observation. Open PR/checks links use only validated
  `https://github.com/` URLs.

## Board badge

- Cards without PRs, or whose batched summary request failed, show no badge
  and no false state. Badge requests are batched (at most 200 IDs) and never
  issued per card or per PR.
- One PR reads `#N`; several read `N PRs`. Failing, pending, unknown,
  unverified, unavailable, merged, closed and passing have distinct text in
  the accessible name; colour only supplements it.
- The badge is its own button outside the card's main button, opens the
  drawer and focuses the Pull requests heading. Card dimensions and title
  hierarchy are unchanged.

## Admin GitHub settings

- The token is never requested, rendered or stored. Not configured,
  Connected, Permission required, Unavailable and Rate limited each show an
  actionable explanation. Non-admins never see the panel.
- Repository selection offers only the server allowlist, caps at 10, uses
  the selection version for If-Match and reloads the authoritative selection
  after a conflict instead of overwriting it.
- A GitHub outage keeps the rest of Settings usable.

## Run audit, job progress and evidence

- Run audit shows selected repositories/refs, task scope and separate
  GitHub and AI readiness before anything starts. Start is disabled until
  both are ready and at least one repository is in scope; an AI or GitHub
  problem explains how to fix it. No CLI, local agent or copied request.
- Double-clicking Start sends one idempotency key. A conflicting active run
  offers to open the existing audit instead of starting another.
- Stages show only what the server reports: Queued, Reading GitHub,
  Analyzing, Checking findings, then Complete/Partial/Failed/Cancelled/
  Interrupted. Coverage is the measured task/file/omission counts, not a
  percentage. Leaving and returning (reload/deep link) resumes from server
  state; navigation alone never cancels.
- Cancel is idempotent and disabled while pending; Retry creates a linked new
  run and navigates to it while the old run remains reviewable.
- External-agent runs (job endpoint `404`) keep the original list, review and
  per-finding preview/apply flow unchanged.
- Evidence citations resolve only manifest item IDs; unknown legacy refs stay
  plain text. Snippets render as text, pinned to repository, path, lines
  and captured SHA. An evidence access error shows the typed message and no
  retained content.
- Finding tasks that are not on the loaded board page are fetched by ID so
  titles and Open task links stay correct.
- Approval changes no task. Apply preview performs no mutation; explicit
  Apply sends only eligible approved findings and reports Applied, Skipped
  or Needs follow-up per item. Nothing is completed automatically.
- At 390px and 1440px there is no horizontal overflow; keyboard users can
  reach every control, Escape closes inline evidence and returns focus.
