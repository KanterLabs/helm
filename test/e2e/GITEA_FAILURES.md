# Gitea connection end-to-end proof: failure inventory

Recorded before writing `web/e2e/gitea-connect.spec.ts` or
`test/e2e/fake-gitea`. Part 1 of WP5 in
[the plan](../../docs/GITEA_OAUTH_PLAN.md); the experience standard is
[the UX specification](../../docs/GITEA_OAUTH_UX.md) and the fake answers as
[the research record](../../docs/GITEA_OAUTH_RESEARCH.md) says a real forge
does. Each entry names what could go wrong and how the proof catches it.

## The journey on the real binary

- Connecting takes more than the promised entries: an extra field, a
  required Continue press, or a second page of setup. The spec records every
  value it types and every control it presses outside navigation, and asserts
  the exact list: server address, client ID, the forge's Approve button. The
  `http` consent tick (needed only because the fake is plain `http` on
  loopback) and the single **Sign in with Gitea** press that leaves for the
  forge are recorded separately and asserted too, so any further entry fails.
- The `http` warning is skipped or can be bypassed: sign-in proceeds without
  ticking "Continue without encryption". The spec asserts the warning
  appears, that step 2 is not shown before the tick, and that the settled
  address keeps "without encryption".
- The probe settles on another address than the one typed, or Helm sends the
  browser somewhere other than the probed forge. The spec asserts the found
  row names the fake's address and that the browser lands on the fake's
  authorize page.
- Helm asks the forge for another scope or without PKCE `S256`. The fake
  records the scope and challenge method of every authorize request; the spec
  asserts `read:repository read:user` and `S256`.
- The forge's consent is scripted around instead of clicked. The fake serves
  a real consent page with **Approve** and **Deny** buttons; the spec clicks
  them like a person.
- Connected omits the account, the read-only badge or the repository count,
  or the count is a page size instead of the total. The fake serves 57
  repositories across two pages with `X-Total-Count`; the spec asserts
  "Helm can read 57 repositories", **View** lists all 57 and offers a filter.
- **Test connection** reports success without calling the forge. The fake
  counts identity calls; the spec asserts the count moved.
- An expired access token surfaces to the person instead of being refreshed.
  The fake expires the current access token on request; after **Test
  connection** the spec asserts the refresh counter moved, the panel still
  says connected and shows no check problem.
- Helm keeps using a spent refresh token in one-time refresh mode (as
  Forgejo does by default), or treats a short lifetime as a failure. The fake
  refuses reused refresh tokens with `unauthorized_client` "token was already
  used"; the spec runs several refreshes in that mode and asserts no reuse was
  refused and the connection stays.
- A revoked grant leaves the panel claiming it is connected, or shows a raw
  error. After revocation on the forge, **Test connection** must show **Sign
  in again**; the fake answers 401 with Gitea's body on the API and
  `invalid_grant` "grant does not exist" on refresh.
- Signing in again asks for the address or client ID again, or uses other
  values. The spec types nothing; the fake asserts the same client ID and
  redirect URI, and the panel returns to connected.
- Deny on the forge loses the typed values or shows an upstream message. The
  fake redirects with `access_denied` and a distinctive description; the spec
  asserts Helm's own `denied` message, the description absent, and step 2
  restored with the address and client ID.
- A confidential application is reported as a generic failure. The fake
  answers the exchange like Gitea (`unauthorized_client`, "invalid empty
  client secret"); the spec asserts the `client_confidential` message.
- A full-access grant goes unnoticed. The fake answers the limit check 200;
  after **Test connection** the spec asserts the full-access warning and that
  the read-only badge is gone.
- Disconnect leaves state behind: the panel stays connected, or the file
  remains. The spec confirms the dialog and asserts step 1 and that
  `gitea.json` is gone.
- The connection does not survive a reload or a new browser session (it
  lived in the browser, not the server). The spec reloads and opens a fresh
  browser context and asserts the connection and its count.
- `gitea` or `gitea_error` stays in the address bar after the return.

## Secrets

- An authorization code, access token or refresh token reaches the Helm
  server's output, the SQLite database or its WAL, a Helm API response, the
  browser console, or retained browser evidence. Every secret the fake issues
  carries a per-run random sentinel (`<sentinel>-code-…`, `-access-…`,
  `-refresh-…`) that never appears in source. The spec scans the live log,
  database files, captured responses and console; `run-worker.sh` scans every
  retained file after Helm stops, including inside trace archives. The
  authorization code legitimately passes through the browser's address bar
  on the forge's redirect (that is how OAuth works), so the code sentinel is
  exempt only inside Playwright trace archives; access and refresh tokens are
  exempt nowhere.
- The connection file is readable by others. The spec asserts `gitea.json`
  is a regular 0600 file in a 0700 directory and that it holds the issued
  tokens (checked as booleans, so a failure message cannot print them).
- An assertion failure prints a token into the report. Token checks compare
  booleans only.

## The fixture

- The fake answers unlike a real forge, so the proof passes against
  something Helm will never meet. Token errors are HTTP 400 JSON with the
  research record's `error` and `error_description`; the API's 401 body is
  `{"message": "invalid username, password or token"}` with no
  `WWW-Authenticate`; repositories are capped at 50 per page with
  `X-Total-Count` and `Link`; the version string is `28.3.0`; the consent page
  is shown on every sign-in of a public client.
- PKCE is not really verified. The fake checks `S256` of the verifier against
  the stored challenge and refuses a mismatch with `unauthorized_client`
  "failed PKCE code challenge"; codes are single use.
- A redirect URI other than the registered one is accepted. The fake shows a
  400 "Unregistered Redirect URI" page instead of redirecting.
- The fake's port collides with another fixture or worker. It binds an
  ephemeral loopback port and writes it to a file; `run-worker.sh` waits for
  that file and `/__state` before starting the browser, and stops the fake
  with the other fixtures.
- Specs sharing a worker see each other's connection. The spec resets the
  fake and disconnects Helm before and after, in `finally`.
- The fake's state endpoint leaks secrets into test output. `/__state`
  returns counters and flags only, never codes or tokens.
- A wait on the 60-second "refreshed token rejected again" window makes the
  spec flaky. The spec orders scenarios so no forced expiry follows a refresh
  within a minute (revocation runs before the forced refresh), and uses short
  lifetimes, not forced expiry, for the one-time refresh check.

## Not covered here

- Restarting the Helm process: the worker owns it. Reloading and a fresh
  browser context prove the connection lives on the server; loading the file
  at start is covered by the Go tests in `internal/forgeconnect`.
- A real Gitea or Forgejo: part 2 of WP5 (`test/giteaproof/run.sh`).
