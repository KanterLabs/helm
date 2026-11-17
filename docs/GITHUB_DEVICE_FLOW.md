# Connect GitHub by signing in

> Retired in TC-391: this is historical documentation. The GitHub product
> integration and Branches view are removed. See [removal and preservation](GITHUB_REMOVAL.md).

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

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#settings).

## Network access

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#network-access).

## The credential file

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#the-credential-file).

## Token lifetime and refresh

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#token-lifetime-and-refresh).

## Disconnecting

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#disconnecting).

## Backup and restore

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#backup-and-restore).

## API

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#api).

## Registering the GitHub App

Historical details: [operator reference](GITHUB_DEVICE_FLOW_OPERATIONS.md#registering-the-github-app).
