# GitHub settings panel: failure list

Written before the tests and code of the device-flow connection panel
(`GitHubConnection.svelte` and its parts). Contracts: C5 and C6 in
`docs/GITHUB_DEVICE_FLOW_PLAN.md`; A3 and A5 in
`docs/GITHUB_DEVICE_FLOW_AMENDMENTS.md`. Each entry names the test that covers
it. `ui` is `web/e2e/github-connection-ui.spec.ts`; `screens` is
`web/e2e/github-connection-screens.spec.ts`.

## Polling

1. **Polling after unmount.** The panel keeps calling
   `GET /integrations/github` after the admin leaves Settings, wasting requests
   and writing into a destroyed component.
   Test: ui "stops polling when hidden and after leaving Settings".
2. **Polling while hidden.** A background tab polls every 3 s for the whole
   15-minute attempt. Polling pauses while `document.hidden`, and one poll runs
   at once when the tab becomes visible again (so the panel is current the
   moment the admin returns from GitHub).
   Test: ui "stops polling when hidden and after leaving Settings".
3. **Overlapping polls.** A slow answer plus a fixed interval stacks requests,
   and a late older answer overwrites a newer one. The next poll is scheduled
   only after the previous one settles; an answer that began before a user
   action (connect, cancel, disconnect, check) is discarded.
   Test: ui "never overlaps polls and ignores an answer older than an action".
4. **No update on approval.** The admin approves on GitHub and the panel
   still shows the code until someone presses refresh.
   Test: ui "connects with one press, a copied code and no refresh".

## Starting an attempt

5. **A double click starts two attempts.** Two `POST /connect` requests, the
   second replacing the first code while the admin is typing it.
   Test: ui "a double click starts one attempt".
6. **A stale code after a new attempt.** After Try again, Reconnect or a
   replaced attempt, the old code stays on screen (even briefly while the new
   request is in flight) and the admin types a dead code.
   Test: ui "never shows an old code once a new attempt starts".
7. **Another administrator's attempt.** `user_code` is absent because someone
   else began the attempt; the panel shows an empty code box or a broken copy
   button instead of saying another administrator is connecting.
   Test: ui "says another administrator is connecting, without a code".

## Code on screen

8. **Expiry while the code is on screen.** The countdown reaches zero and the
   panel still offers a dead code and Open GitHub. On local expiry the code is
   removed, the panel says the code expired and offers Try again.
   Test: ui "removes an expired code and offers Try again".
9. **Clipboard denial.** Copy reports success when the browser refused, or
   fails silently. A denied or missing clipboard is reported in plain words and
   the code is selected so the admin can copy it by hand.
   Test: ui "reports a blocked clipboard honestly and selects the code".
10. **Links that are not `https://github.com/`.** `verification_uri`,
    `install_url` or `authorizations_url` pointing elsewhere (or
    `https://github.com.example`) becomes a clickable link. Only URLs whose
    origin is exactly `https://github.com` are linked; others are not rendered
    as links at all.
    Test: ui "never links anywhere but https://github.com/".
11. **Open GitHub replaces Helm.** The verification page opens in the same
    tab and the admin loses the code and the panel. It opens in a new tab with
    `rel="noopener noreferrer"`.
    Test: ui "connects with one press, a copied code and no refresh".

## Disconnect

12. **Disconnect without confirmation.** One stray click deletes the
    credential. Disconnect asks first, explains that Helm cannot revoke its
    access on GitHub by itself and links to `authorizations_url`; Keep
    connected sends nothing.
    Test: ui "disconnect asks first and explains GitHub-side revocation".

## Messages and accessibility

13. **State changes not announced.** A screen-reader user hears nothing when
    the code appears, GitHub approves, the code expires or GitHub is
    disconnected. A polite live region, present from the first render,
    announces each change; errors use `role="alert"`; focus moves to the new
    state's heading after the admin's own action.
    Test: ui "connects with one press, a copied code and no refresh",
    "removes an expired code and offers Try again",
    "disconnect asks first and explains GitHub-side revocation".
14. **An answer that is not Helm's blamed on GitHub.** A proxy's HTML 502
    page or a network failure arrives without a Helm `error.code`
    (`request_failed`); the panel shows the HTML, a raw code, or says GitHub
    failed. It says: "Helm did not get a proper answer from its own server.
    Try again in a moment."
    Test: ui "an HTML 502 from a proxy is not blamed on GitHub".
15. **Raw codes or GitHub text shown.** `attempt.message`, `error.code`
    values such as `github_signin_unavailable`, or GitHub's own wording reach
    the screen. Every message is Helm's plain sentence chosen from the state
    or code, saying what to do next.
    Test: ui "an HTML 502 from a proxy is not blamed on GitHub" (asserts no
    code or GitHub text is rendered in any error case it drives).
16. **Keyboard-only use fails.** Connect, Copy, Open GitHub, Cancel and the
    disconnect confirmation cannot be reached or operated by keyboard.
    Test: ui "connects with one press, a copied code and no refresh" (driven
    by Tab and Enter), "disconnect asks first and explains GitHub-side
    revocation".

## Compatibility and layout

17. **Any change to the `operator_token` panel.** With
    `connection.mode = operator_token`, or no `connection` object (an older
    server), the panel must render exactly as before: same text, same Test
    connection, same repository selection, no Connect button.
    Test: `github-selection-ui.spec.ts` unmodified; ui "operator_token and an
    older server render today's panel unchanged".
18. **Sideways scrolling or clipping on a phone.** The code, buttons or the
    disconnect sentence overflow at 320 px. Test: screens "no sideways
    scrolling at 320 px in any state", plus screenshots of every C6 row at
    1280 and 390 px in light and dark.

## Found on beta

19. **A server that has never checked GitHub sends Go's zero time**
    (`0001-01-01T00:00:00Z`), and the panel says "Last checked 739934 d ago".
    Seen on beta in the `operator_token` panel. A time before 1970 is not a
    check: the `operator_token` panel says "Last checked never", and the
    connected view says "not checked yet". This is the one intended change to
    the `operator_token` panel's text. Test: `pullRequests.test.ts`.

## Found while wiring the routes (WP3)

20. **The server cannot write or remove its credential file** and answers
    500 `github_storage_failed`. Without its own sentence the panel said
    "Try again", which cannot help: the operator must fix Helm's GitHub
    state directory. Test: ui "an HTML 502 from a proxy is not blamed on
    GitHub" (the `github_storage_failed` step).

## Found by the real-backend proof (WP5)

21. **A connection that has not been checked yet reads as broken.** Right
    after sign-in, and after every restart, the provider has not checked
    GitHub, so the document says `unavailable` ("GitHub access has not been
    checked.") with the zero `checked_at`. The panel showed a red
    **Unavailable** badge and "Helm could not reach GitHub just now" although
    nothing failed. Mocked routes always sent a checked state, so only the
    real binary showed it. An unchecked state is not a failure: the badge says
    Connected and the summary already says "not checked yet". Tests:
    `connection.test.ts`; real backend `github-device-flow-beta.spec.ts`.
22. **"sign-infor @login"**: the reconnect sentence lost the space before
    "for" (Svelte drops whitespace at the start of an `{#if}` block). Test:
    real backend "a refused refresh ends in Reconnect GitHub".
23. **A sign-in that ends during Test connection gets a vague alert.** The
    server answers 409 `github_reconnect_required`; the panel said "The GitHub
    connection changed in the meantime … try again if you still need to",
    while the only useful step is **Reconnect GitHub**. Tests:
    `connection.test.ts`; real backend "a refused refresh ends in Reconnect
    GitHub".
