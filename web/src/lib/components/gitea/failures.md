# Gitea connection panel: failure list

Written before the spec (`web/e2e/gitea-connection-*.spec.ts`) and the
components in this directory. Standard: `docs/GITEA_OAUTH_UX.md`; API:
contract C5 in `docs/GITEA_OAUTH_PLAN.md`. Each entry names the check that
covers it. "Spec" means the mocked-route browser spec; every visible state is
also screenshotted at 1280 px and 390 px and checked for horizontal overflow
at 320 px.

## From the WP4 brief

1. **A double click starts two sign-ins.** The sign-in button must disable
   synchronously on the first press. Spec: double-click **Sign in with
   Gitea**; exactly one `POST /oauth/start` is recorded, and the button shows
   a spinner while disabled. Same for **Sign in again**.
2. **`gitea` or `gitea_error` is left in the address bar** after handling, so
   a reload repeats the announcement or the error. Spec: after both returns
   the URL is `/settings` with any unrelated query parameters kept.
3. **An unknown error code** shows a raw code, an upstream message or
   nothing. Spec: an unknown code from the probe and from the return both
   show "Something went wrong connecting to Gitea." with **Try again**; the
   server's `message` text never appears.
4. **An `authorize_url` outside the probed server** is followed (an open
   redirect or a phishing hop). Spec: a start response pointing at another
   host, and one at `https://gitea.example.com.evil.example/…` (a prefix
   trick), are refused; the browser stays on Helm with an explanation.
5. **The `http` warning is bypassed.** Spec: an `http://` address shows the
   warning and checkbox; step 2 does not open and Continue does not proceed
   until it is ticked; the next probe sends `allow_insecure_http: true`.
6. **Clipboard denial** throws or claims success. Spec: with
   `navigator.clipboard.writeText` rejecting, the copy button reports that
   copying did not work and selects the value so it can be copied by hand;
   with it working, the live region announces "Copied redirect URI".
7. **Disconnect without confirmation.** Spec: **Disconnect** first shows a
   confirmation with the revoke link; no `POST /disconnect` until confirmed;
   **Cancel** sends nothing.
8. **State changes not announced.** Spec: the polite live region announces
   the check result, copy confirmations, connection, test result and
   disconnection; errors are tied to their field with `aria-describedby`.
9. **Non-administrators see the panel.** Spec: a signed-in member sees no
   Gitea section and the browser never requests `/integrations/gitea`.

## Added for this panel

10. **A slow check is overtaken by a newer one** and the stale result wins.
    Spec: hold the first probe, change the address, let the second finish,
    then release the first; the found row names the second address. The
    first request is aborted.
11. **A check per keystroke** floods the server. Spec: typing a host sends
    one probe after the pause, not one per character; a value that does not
    look like a host sends none until Enter, blur or Continue.
12. **Blur after Enter probes twice** for the same value. Spec: Enter then
    Tab sends one probe.
13. **Paste extraction picks the wrong token** ("Client" from "Client ID:
    …") or keeps surrounding whitespace. Spec: pasting
    `Client ID: <uuid>` and a padded bare ID both fill only the ID; an
    invalid shape keeps the button disabled with a hint.
14. **Values lost after a failed sign-in.** Spec: start a sign-in, land on
    the (stubbed) forge, return with `gitea_error=denied`; address, Forgejo
    or Gitea label and client ID are restored from `sessionStorage`, step 2
    is open and the message is shown and focused.
15. **Values linger after success or disconnect** in `sessionStorage`.
    Spec: the draft key is gone after `gitea=connected` and after
    disconnecting.
16. **Corrupt or foreign `sessionStorage` data** breaks the panel. Covered by
    parsing defensively (unknown shape is ignored); exercised by the spec's
    return-with-error test seeding nothing for the no-draft case, where the
    message still shows at the top of the setup.
17. **Wrong product name.** Once the server is Forgejo every label (section
    title, step 2 heading, buttons, messages) says Forgejo. Spec: Forgejo
    probe and Forgejo connected state.
18. **The status load fails** and the section silently vanishes or spins
    forever. Spec: a 500 on the first `GET` shows a message with **Retry**.
19. **Layout jumps** when a check finishes or step 2 opens. Covered by a
    reserved result row and a skeleton for the first load; screenshots show
    the result row in place while checking.
20. **Long values overflow** at 320 px (redirect URI, addresses, logins,
    repository names). Spec: overflow assertion on every state, with a long
    synthetic redirect URI.
21. **`issuer_mismatch` without the advertised address**, or **Use that
    address** not re-checking. Spec: the button fills the field with
    `details.issuer` and probes it.
22. **`already_connected` leaves the setup visible.** Spec: **Show the
    connection** reloads the status and shows the connected view.
23. **Repository list problems**: loaded before asked for, filter shown for
    ten or fewer, truncation hidden, load failure swallowed. Spec: no
    `GET /repositories` until **View**; filter present with 12 repositories
    and absent with 3; truncated note; filter narrows the list and reports
    no matches.
24. **`full` access shown like `scoped`.** Spec: `scoped` shows the
    **Read-only access** badge; `full` shows the warning block and no badge.
25. **Reconnect sends stale or invented values.** Spec: **Sign in again**
    posts empty `base_url` and `client_id`, and its `authorize_url` is checked
    against the stored server.
26. **Unavailable shown as an error or as a setup form.** Spec: both
    reasons show their sentence and no field or button.
27. **Focus lost** when a step opens or after returning. Spec: the step 2
    heading has focus after a successful check; the result has focus after a
    return; the address field keeps focus after a failed check.
28. **An `Idempotency-Key` sent** on these routes (the contract forbids it).
    Spec: no recorded request carries the header.
29. **Test connection failure hidden.** Spec: a check that returns
    `check.state: unreachable` shows and announces the problem.
