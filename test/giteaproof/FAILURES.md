# Real-forge proof: how it could pass wrongly or leave a mess

Written before the proof. Each entry names the guard that answers it.

## Talking to the wrong thing

1. **Port clash.** Another agent's process already listens on the port picked
   for a forge or for Helm, so the proof talks to someone else's server.
   Guard: ports are picked by binding port 0; `docker run -p` fails loudly on
   a taken port and the start is retried on a new port. The forge must then
   answer with a per-run nonce as its `APP_NAME` on the login page, and Helm
   must answer `/api/v1/auth/status` with `setup_required: true` before setup.
2. **Fixed listeners in Helm.** Helm opens a public-hooks listener on 8091 by
   default, which clashes with other runs. Guard: `HELM_PUBLIC_HOOKS_ADDR=off`.
3. **Wrong forge or version.** An override or a moved tag starts a different
   product. Guard: `/api/v1/version` must match the family (Forgejo carries
   `+gitea-`, Gitea does not) and begin with the image tag when the tag is a
   version; the probe's `product` and `version` must equal it.
4. **Forge not ready yet.** The HTTP port opens before migrations finish, or
   the container restarts. Guard: wait for `/api/v1/version` over TLS and a
   running container state, with a deadline; the admin is created only after.
5. **Asserting against the wrong account.** The admin who owns the fixtures
   signs in, so every repository (including the hidden one) is reachable and
   access looks fine. Guard: Helm signs in as a separate account whose
   `is_admin` is false per the admin API; the status `account.login` and
   `account.id` must equal that account's; the admin's login must differ.
6. **Fixture silently missing.** A failed fixture call makes "hidden repository
   absent" trivially true. Guard: every fixture call must return its expected
   status, and the hidden repositories must be readable by the admin.
7. **Forge settings not applied.** A misspelled environment variable leaves
   the 3600-second token or reusable refresh tokens in place, so "refresh
   works" proves nothing. Guard: the stored expiry must be at most 30 seconds
   after the refresh time, and replaying a superseded refresh token directly
   at the forge must be refused (one-time mode is really on).
8. **Refresh did not happen.** `POST /check` says `ok` on a still-valid token.
   Guard: wait until the stored `access_expires_at` has passed; after the
   check the stored access and refresh tokens must differ from before
   (compared by hash, never printed).
9. **Restart not real.** The "restart" reuses the same process. Guard: the
   old process must have exited and a new PID answers; the database and state
   directory are the same.
10. **Revocation not real.** The settings-page script finds no revoke link and
    "revokes" nothing, and a later failure is coincidental. Guard: at least
    one revoke link for Helm's grant must be found and posted, and the page
    must no longer list it afterwards.
11. **Error redirect for the wrong reason.** `gitea_error=state_invalid` could
    come from a missing Helm session rather than the other administrator.
    Guard: the second administrator is a real `admin` member signed in with
    its own session (`/auth/me` checked); no-session and forged-state cases
    are separate assertions.
12. **Consent page skipped or shaped differently.** The script follows a
    redirect it did not expect. Guard: the consent form must be present when
    expected; every redirect target must be Helm's exact callback with a
    `state` equal to the one in `authorize_url`.
13. **Connected but full access.** Guard: `access` must be `scoped`.
14. **Disconnect leaves the file.** Guard: the file must be absent after
    `POST /disconnect`, and present with mode 0600 in a 0700 directory, owned
    by this user, while connected.

## Secrets

15. **Tokens printed by the proof itself.** Guard: token values live only in
    memory (compared by SHA-256 in memory, never written); output and the
    result file carry only states, codes, counts and booleans. The artifact
    directory is scanned with everything else.
16. **Tokens in Helm's output or SQLite file.** Guard: every access token,
    refresh token and authorization code seen during the run (read from the
    connection file and from the forge's redirects) is searched for in Helm's
    stdout, stderr, `helm.db`, `-wal`, `-shm` and every other file in its data
    directory except the connection file itself, and in the artifacts.
17. **A leak scan that finds nothing because it had nothing.** Guard: the scan
    must have at least one access token, one refresh token and one code, each
    at least 20 characters.
18. **Fixture passwords and admin tokens.** Generated per run, never written
    to disk outside the private temporary directory, never printed.

## Mess

19. **Stale containers** after a failure or Ctrl-C. Guard: every container
    name carries a unique run prefix and a label; the trap removes exactly
    the IDs this run created, with their volumes (`/data` is a tmpfs).
20. **Removing someone else's container or image.** Guard: removal is by
    recorded container ID only; an image is removed only if it was absent
    before this run pulled it, and only without `--force`.
21. **Helm left running.** Guard: the driver kills its Helm on exit, and the
    shell trap kills any PID recorded in the private PID file.
22. **Temp files left.** Guard: one private temporary directory under
    `$TMPDIR` holds Helm's data, certificates and the binary; the trap removes
    it (making the Go module cache writable first).
23. **A pass with a crashed step.** Guard: `set -Eeuo pipefail`, the driver
    exits non-zero on any failed assertion, and the result file records
    `passed: false` unless every assertion passed.
