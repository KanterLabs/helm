# Connect GitHub by signing in

An administrator connects Helm to GitHub from **Settings**: press **Connect
GitHub**, type a short code on github.com, approve, and Helm is connected.
Nobody copies a token. Access is read-only and limited to the repositories
you install Helm's GitHub App on.

**Before you start:** sign-in works only once Helm knows the client ID of a
GitHub App with device flow turned on. Helm ships no client ID yet, so until
an operator sets `HELM_GITHUB_APP_CLIENT_ID` (see [Settings](#settings)) the
panel says **GitHub sign-in is not set up** and the button does not appear.
Whoever publishes the App follows [Registering the GitHub App](#registering-the-github-app).

Related: [operations](GITHUB_BETA_OPERATIONS.md), [API](GITHUB_BETA_API.md),
[every setting](CONFIGURATION.md#github-beta).

## Token file or sign-in?

Helm talks to GitHub in one of two modes, chosen at startup:

| Mode | When | Who sets it up |
| --- | --- | --- |
| `operator_token` | `HELM_GITHUB_TOKEN_FILE` is set | An operator places a fine-grained token in a protected file and lists repositories in `HELM_GITHUB_REPOSITORIES`. The panel is the token panel; sign-in is off. |
| `device_flow` | `HELM_GITHUB_TOKEN_FILE` is unset | An administrator signs in from Settings. GitHub decides which repositories Helm sees. |

Both need `HELM_GITHUB_ENABLED=true`. Use sign-in unless you must manage the
token yourself (for example, a token tied to a machine account). To switch
from a token file to sign-in, unset `HELM_GITHUB_TOKEN_FILE` and restart.

## Connect GitHub

You must be signed in to Helm as an administrator. Open **Settings** and find
**GitHub pull requests & audits**.

1. Press **Connect GitHub**. The panel says "Getting a code from GitHub…",
   then shows **Enter this code on GitHub** with a code such as `WDJB-MJHT`.
2. Press **Copy code**, then **Open GitHub**. github.com/login/device opens in
   a new tab. Paste the code and approve Helm. The code expires after the
   time shown under it ("code expires in …"); screen readers hear a warning
   with one minute left.
3. Come back to Helm. The panel updates by itself within a few seconds; you
   do not need to reload.
4. If Helm is not yet installed on any repository, the panel says **One more
   step: choose repositories**. Press **Install Helm on your repositories**,
   pick the repositories on GitHub, then press **I’ve installed it**.
5. The panel says **Connected as @your-login** with the number of
   repositories available. Pick a project below it and choose that project's
   repositories, as in [selected repositories](GITHUB_BETA_API.md#integration-and-selected-repositories).

Only the administrator who pressed **Connect GitHub** sees the code. Another
administrator sees **Another administrator is connecting GitHub** and can
press **Connect it myself instead**, which replaces the first code.

## Choose repositories later

When connected, press **Choose repositories on GitHub** to add or remove
repositories on GitHub, then **Test connection**. Helm re-reads the list each
time you press **Test connection** or **I’ve installed it**.

Helm reads up to 20 installations and up to 300 repositories in each, and
keeps at most 50 repositories (the first 50 by name). When GitHub lists more,
the panel says "GitHub lists more repositories than Helm reads, so Helm uses
the first 50." Choose fewer on GitHub to control which ones.

If `HELM_GITHUB_REPOSITORIES` is set, it is a ceiling: Helm uses only
repositories that are both installed and on that list. If none are, the panel
stays at **One more step: choose repositories**.

## What the panel shows

| Panel heading (badge) | Meaning | What to do |
| --- | --- | --- |
| **GitHub is turned off on this Helm** (Not configured) | `HELM_GITHUB_ENABLED` is not `true` | Ask the operator to set it and restart Helm. **Setup guide** opens this page. |
| **GitHub sign-in is not set up** (Not configured) | GitHub is on, but there is no client ID | Ask the operator to finish [Settings](#settings) and restart Helm. **Setup guide** opens this page. |
| **Connect Helm to GitHub** (Not connected) | No connection | Press **Connect GitHub**. |
| **Enter this code on GitHub** (Waiting for GitHub) | Your code is waiting | Enter it on GitHub, or press **Cancel**. |
| **Another administrator is connecting GitHub** (Waiting for GitHub) | Someone else's code is waiting | Wait, or **Connect it myself instead**. |
| **The code expired** | Not entered in time | **Try again** for a new code. |
| **Sign-in was declined** | The request was declined on GitHub | **Try again** if that was a mistake. |
| **Sign-in did not finish** | GitHub could not be reached, refused, or Helm could not save | **Try again**; if it repeats, the operator checks Helm's log (see [Messages](#messages-and-what-to-do)). |
| **One more step: choose repositories** (Needs repositories) | Signed in, no repository available | **Install Helm on your repositories**, then **I’ve installed it**. |
| **Connected as @login** (Connected) | Working | **Choose repositories on GitHub**, **Test connection**, **Disconnect**. |
| **GitHub sign-in needs renewing** (Reconnect needed) | The sign-in ended (see [Token lifetime](#token-lifetime-and-refresh)) | Press **Reconnect GitHub** and enter the new code. |

When connected, the badge can also read **Unavailable**, **Rate limited** or
**Permission required** after a test; the panel then says what to do.

**Install Helm on your repositories** and **Choose repositories on GitHub**
appear only when `HELM_GITHUB_APP_SLUG` is set. Without it, install the App
from its page on GitHub and press **I’ve installed it**.

## Messages and what to do

Messages after a button press:

| Message | Cause and fix |
| --- | --- |
| Helm could not reach GitHub. Wait a minute, then try again. | GitHub was unreachable, or refused the request (HTTP 424). If it persists, the operator checks [network access](#network-access) and that the App has device flow on and the client ID is right. |
| GitHub sign-in is not set up on this Helm. Ask your operator to set it up, then reload this page. | No client ID, or GitHub is disabled. |
| An operator manages GitHub for this Helm with a token file, so sign-in here is off. Reload the page to see the current setup. | `HELM_GITHUB_TOKEN_FILE` is set. |
| Helm could not save or remove its GitHub credential. Ask your operator to check Helm's GitHub state directory, then try again. | See [The credential file](#the-credential-file). |
| The GitHub connection changed in the meantime. The panel now shows the latest state; try again if you still need to. | Someone else connected or disconnected, or the sign-in ended. The panel reloads. |
| Helm did not get a proper answer from its own server. Try again in a moment. | A proxy page or network failure between your browser and Helm, not GitHub. |
| Helm is offline, so it cannot change the GitHub connection. Reconnect, then try again. | Your browser is offline. |
| Your Helm session has ended. Sign in again, then try again. | Sign in to Helm again. |
| Only a Helm administrator can change the GitHub connection. | Ask an administrator. |
| Too many tries in a short time. Wait a minute, then try again. | Wait. |
| Helm could not start GitHub sign-in. Try again in a moment. | Any other failure of **Connect GitHub**, **Try again** or **Reconnect GitHub**. |
| Helm could not cancel the sign-in. Try again in a moment. | Any other failure of **Cancel**. |
| Helm could not test the connection. Try again in a moment. | Any other failure of **Test connection** or **I’ve installed it**. |
| Helm could not disconnect GitHub. Try again in a moment. | Any other failure of **Disconnect GitHub**. |

While a code is shown, "Helm could not check just now. It will check again in
a few seconds." (or the same with "did not get a proper answer from its own
server") means one check failed; Helm keeps checking.

After **Copy code**: "Copied. Paste it on GitHub." If the browser blocks
copying, the code is selected for you: press Ctrl+C (⌘C on a Mac).

After a test:

| Message | Fix |
| --- | --- |
| Test passed. Helm can read your repositories. | None. |
| Helm still sees no repositories. Finish installing on GitHub (choose at least one repository), then press I’ve installed it again. | Install the App on at least one repository (and on the ceiling list, if set). |
| Helm cannot read one or more repositories. Use Choose repositories on GitHub to give it access, then test again. | Grant access on GitHub. |
| Helm could not reach GitHub just now. Other Helm work is unaffected; test again in a few minutes. | Wait. |
| GitHub is limiting how often Helm can ask. Helm slows down by itself; test again later. | Wait. |

Helm writes one warn line per failed sign-in, with only a code:
`github sign-in failed` (`expired`, `denied`, `github_unreachable`,
`github_refused`, `storage_failed`). Other lines: `github connected`,
`github disconnected`, `github sign-in ended`, `github refresh not saved`
and `github state directory refused` (with the reason).

## Settings

Set these in Helm's environment and restart. Values are read once.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_GITHUB_ENABLED` | `false` | Must be `true`. |
| `HELM_GITHUB_TOKEN_FILE` | empty | Leave unset for sign-in. Set, it selects `operator_token` mode. |
| `HELM_GITHUB_APP_CLIENT_ID` | empty (no App published yet) | The GitHub App's client ID: 1–100 letters, digits, `.`, `_` or `-`. Empty turns sign-in off. |
| `HELM_GITHUB_APP_SLUG` | empty | The App's slug, the last part of `https://github.com/apps/<slug>`: lowercase letters, digits and single hyphens. Needed for the install buttons. |
| `HELM_GITHUB_STATE_DIR` | a `github` directory beside `HELM_DB` | Where the credential file lives. Must be on persistent storage. |
| `HELM_GITHUB_REPOSITORIES` | empty | Optional ceiling: comma-separated `owner/name`, at most 50. |

```dotenv
HELM_GITHUB_ENABLED=true
HELM_GITHUB_APP_CLIENT_ID=<the App's client ID>
HELM_GITHUB_APP_SLUG=<the App's slug>
# HELM_GITHUB_TOKEN_FILE must not be set.
```

Helm refuses to start on a malformed client ID or slug. There is no setting
for GitHub's addresses, a client secret or a private key: Helm uses none.

## Network access

Helm calls only `https://github.com` (device code and token exchange) and
`https://api.github.com` (your account and installations), on port 443.
Calls go through the proxy named by `HTTPS_PROXY` when one is set (`NO_PROXY`
is honored). Each call has a 10-second limit and redirects are refused.
Your browser opens github.com itself, for the code and the install page.

## The credential file

The connection is one file, `connection.json`, in the state directory (by
default `github/` beside the database: `/data/github` in the container image,
`/var/lib/roadmap/data/github` with the packaged `HELM_DB`).

- It holds a live access token and refresh token, the GitHub login, who
  connected and when, and the repository list. Treat it like a password.
  The tokens are never stored in the database, sent to the browser or
  logged.
- Helm creates the directory with mode 0700 and the file with mode 0600,
  writes through a temporary file and renames it, so a crash never leaves a
  half-written file.
- Helm refuses a state directory that another user owns or that group or
  others can open, and does not change its permissions: fix them yourself
  (`chmod 700`). A symlinked directory is followed.
- A file that is not a regular file, has group or other permission bits, has
  another owner, is over 16 KB, does not parse or has an unknown version is left in place and the
  panel shows **GitHub sign-in needs renewing**. Reconnecting replaces it.

## Token lifetime and refresh

GitHub decides how long tokens last. With **Expire user authorization tokens**
on (the recommended App setting), GitHub
[documents](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens)
8-hour access tokens and 6-month refresh tokens. When Helm next uses GitHub
within 10 minutes of the access token's expiry, or after GitHub rejects it,
Helm exchanges the refresh token for a new pair, saves both, and carries on.
Refreshing needs no client secret and does not disturb verified pull
requests. A failed network call keeps a token that is still valid.

The sign-in ends, and the panel shows **GitHub sign-in needs renewing**, when
GitHub refuses the refresh token (expired, revoked or already used), when
GitHub rejects a token Helm refreshed less than a minute before, or when a token without a
refresh token is rejected. An App without token expiry gets tokens that never
expire and are never refreshed.

## Disconnecting

Press **Disconnect**, then **Disconnect GitHub** to confirm (or **Keep
connected**). Helm deletes the credential file, stops reading repositories at
once, and stops any audit that was running with the old credential.

Helm cannot revoke its access on GitHub: that needs a client secret, which
Helm does not have. To remove it there too, open
[authorized GitHub Apps](https://github.com/settings/apps/authorizations) and
revoke Helm.

## Backup and restore

The credential file is not in any backup Helm's scripts make:

| Backup | Contains the credential file? |
| --- | --- |
| `deploy/helm-backup.sh` (packaged host) | No: the SQLite database and its checksum and metadata only. |
| `deploy/coolify/backup.py` (Coolify lanes) | No: the database, attachments, and a root-only `runtime.tar.gz` of `config`, `codex-users` and `public-endpoints`. In `operator_token` mode a token file kept in `config` is in that archive. |
| Portable exports (v2, v3) | No: they come from the database, which never holds the credential. |
| Snapshots of the whole data directory, volume or machine (made outside Helm) | Yes. Keep them as secret as the file itself. |

Restoring with `deploy/helm-restore.sh` replaces only the database and leaves
the state directory alone, so the connection carries on. A Coolify restore
(`restore_attachments.py`) creates a new lane without the file, so connect
again.

Do not restore an old copy of `connection.json`, or copy it to another Helm.
GitHub gives out a new refresh token each time one is used, so an older copy
holds a refresh token that no longer works: Helm shows **GitHub sign-in needs
renewing** once its access token runs out. Two Helms sharing one file end
each other's sign-in. Press **Reconnect GitHub** instead.

## API

All under `/api/v1`, all answered with `Cache-Control: no-store`. Connect,
cancel and disconnect accept only a signed-in human administrator: a bearer
token gets 403 `human_account_required`. They are never replayed from an
`Idempotency-Key`. Browser requests need Helm's own `Origin`.

| Route | Does |
| --- | --- |
| `GET /integrations/github` | The integration document with its `connection` object. No GitHub call. |
| `POST /integrations/github/check` | In `device_flow` mode, re-reads installations first, then tests. |
| `POST /integrations/github/connect` (`connectGithub`) | Starts sign-in, or returns your pending code. |
| `POST /integrations/github/connect/cancel` | Body `{"attempt_id": "…"}`. A stale ID changes nothing (200). |
| `POST /integrations/github/disconnect` (`disconnectGithub`) | Deletes the credential. Twice is fine. |

The `connection` object: `mode` (`operator_token` or `device_flow`),
`signin_available`, `state` (`disconnected`, `connected`, `no_repositories`,
`reconnect_required`), `login`, `connected_at`, `install_url`,
`authorizations_url`, `repositories_truncated`, and `attempt` when there is
one: `id`, `state` (`pending`, `expired`, `denied`, `failed`),
`verification_uri`, `expires_at`, `message`, and `user_code` for the
administrator who began it while it is pending. In `operator_token` mode the
object reads `operator_token` / `disconnected`.

| Status | `error.code` |
| --- | --- |
| 400 | `invalid_request` (bad cancel body) |
| 403 | `human_account_required`; `forbidden` for a member who is not an administrator |
| 405 | `method_not_allowed` |
| 409 | `github_operator_managed`, `github_signin_unavailable` (no client ID, GitHub disabled, or shutting down), `github_already_connected`, `github_not_connected`, `github_reconnect_required` |
| 424 | `github_unreachable`, `github_refused` |
| 500 | `github_storage_failed`, `internal_error` |

These routes never answer 502 or 504: a proxy in front of Helm may replace
those bodies. Messages are Helm's own; GitHub's text is never passed on.

## Registering the GitHub App

Done once, by whoever publishes the App (for Helm's own App, the KanterLabs
organization owner). On GitHub, create a new GitHub App under the owning
organization's **Developer settings** and set:

- **Enable Device Flow**: ticked. GitHub has no link parameter for it.
- **Webhook → Active**: unticked. Helm uses no webhook.
- Repository permissions, all **Read-only**: **Contents**, **Pull
  requests**, **Checks**, **Commit statuses**, **Metadata**. Nothing else.
- **Expire user authorization tokens**: ticked.
- **Request user authorization (OAuth) during installation**: unticked.
- **Callback URL**: not needed; GitHub ignores it for device flow.
- Make it installable by any account if other organizations' Helms will
  use it.

Then copy the **Client ID** from the App's page and the slug from its public
URL. Set them as `HELM_GITHUB_APP_CLIENT_ID` and `HELM_GITHUB_APP_SLUG`, or
ship them as `DefaultGitHubAppClientID` and `DefaultGitHubAppSlug` in
`internal/config/github.go`. Helm needs no client secret or private key; if
you generate one, keep it out of Helm.
