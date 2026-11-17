# GitHub device-flow operations (historical)

> Retired in TC-391: this is historical documentation. The GitHub product
> integration and Branches view are removed. See [removal and preservation](GITHUB_REMOVAL.md).

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
requests. A failed network call keeps a token that is still valid, and Helm
waits 30 seconds before asking GitHub again.

The sign-in ends, and the panel shows **GitHub sign-in needs renewing**, when
GitHub refuses the refresh token (expired, revoked or already used), when
GitHub twice in a row rejects a token Helm had just refreshed, or when a
token without a refresh token is rejected. An App without token expiry gets tokens that never
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
| `POST /integrations/github/connect` (`connectGithub`) | Starts sign-in, or returns your pending code. No body. At most 20 new attempts a minute. |
| `POST /integrations/github/connect/cancel` | Body `{"attempt_id": "…"}`. Only the administrator who began the attempt cancels it; a stale ID or someone else's attempt changes nothing (200). |
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
| 400 | `invalid_request` (bad cancel body, or a body on connect or disconnect) |
| 403 | `human_account_required`; `forbidden` for a member who is not an administrator |
| 405 | `method_not_allowed` |
| 409 | `github_operator_managed`, `github_signin_unavailable` (no client ID, GitHub disabled, or shutting down), `github_already_connected`, `github_not_connected`, `github_reconnect_required` |
| 424 | `github_unreachable`, `github_refused` |
| 429 | `rate_limited` (too many sign-in attempts in a minute; see `Retry-After`) |
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
