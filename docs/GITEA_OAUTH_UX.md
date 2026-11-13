# Gitea connection: experience specification

Status: planned, 2026-11-12. Tracking: TC-388. Part of the
[implementation plan](GITEA_OAUTH_PLAN.md); the API it uses is contract C5.

Shane's requirement: connecting a Gitea must be "super straightforward,
responsive and easy". This document is the acceptance standard for that.

## Principles

1. **One card, one thing to do at a time.** Never more than one field to fill
   in per step. Finished steps collapse to a one-line summary with **Change**.
2. **Forgive the input.** Accept `gitea.example.com`, a full URL, or any page
   link copied from the forge. The server works out the base address.
3. **Say what Helm found before asking for more.** "Found Gitea 28.3.0 at
   gitea.example.com."
4. **Copy, don't type.** Every value the person enters on the forge has a
   copy button.
5. **Every failure names its fix**, in the place it happened.
6. **It works on a phone.** One column at 640 px and below, 44 px touch
   targets, no horizontal scrolling, long values wrap.
7. **It feels instant.** Checks start by themselves, nothing reloads except
   the trip to the forge, and the layout does not jump while loading.

## Where it lives

Settings, administrators only, a section titled **Gitea** beside the GitHub
section. Once the server is known to be Forgejo, every label says "Forgejo".

## The flow

### Step 1: "Where is your Gitea?"

- One text field, placeholder `gitea.example.com`, with `inputmode="url"`,
  `autocapitalize="off"`, `autocomplete="off"`, `spellcheck="false"`.
- The check starts on paste, on Enter, on leaving the field, or after 400 ms
  without typing once the value looks like a host. A new check cancels the
  one before it. A **Continue** button does the same for people who expect one.
- Success shows a row: a tick, the product and version, and the address the
  server settled on. Step 2 opens and takes focus.
- An `http` address shows an inline warning with a checkbox, "Continue
  without encryption". Nothing proceeds until it is ticked.
- Failure shows the message for its code under the field and keeps focus
  there.

### Step 2: "Create Helm's application in Gitea"

Shown as three short numbered actions:

1. **Open Gitea's application page** (button, new tab, `register_url`).
2. "Under *Create a new OAuth2 Application*, enter these:" followed by
   - Application name: `Helm` (copy button)
   - Redirect URI: the `redirect_uri` value (copy button; wraps on narrow
     screens)
   - **Untick "Confidential Client"**, drawn as a small unticked checkbox so
     it is not missed.
3. "Paste the Client ID Gitea shows you" with one field.

Behaviour:

- Pasting anything that contains an ID (for example the whole line "Client
  ID: 1a2b…") extracts the ID. Whitespace is trimmed.
- **Sign in with Gitea** is the only primary button. It enables when the ID
  has a valid shape, and disables with a spinner after the first press so a
  double click cannot start two sign-ins.
- A quiet note sits under the button: "If Gitea shows *Unregistered Redirect
  URI*, the redirect URI saved in the application does not match the one
  above. Copy it again."
- The address and client ID are kept in `sessionStorage` (they are not
  secrets) so a failed sign-in returns the person to this step with their
  values intact. They are cleared on success and on disconnect.
- Navigate to `authorize_url` only when it starts with the probed address.

### Step 3: back in Helm

The forge sends the browser to `/settings?gitea=connected` or
`/settings?gitea_error=<code>`. The panel reads the parameter, removes it from
the address bar with `history.replaceState`, scrolls itself into view and
announces the result.

Connected shows:

- "Connected to gitea.example.com as @login", with the product and version.
- An access badge: **Read-only access** (`scoped`), or a warning block for
  `full`: "This server gave Helm more than read-only access to this account.
  Helm only reads, but use a dedicated account with access to just the
  repositories Helm needs."
- "Helm can read N repositories" with **View**, which loads the list on
  demand and offers a filter box when there are more than ten.
- **Test connection**, and **Disconnect**. Disconnect asks for confirmation
  and links to `revoke_url`: "To remove Helm's authorization on Gitea as well,
  delete it under Authorized OAuth2 Applications."

`reconnect_required` shows one button, **Sign in again**, which reuses the
stored server and client ID.

## Messages

| Code | Message | Action offered |
| --- | --- | --- |
| `invalid_server_url` | That does not look like a server address. Try `gitea.example.com`. | Focus the field |
| `insecure_http` | This address uses http, so sign-in details could be read on the network. | The checkbox |
| `server_unreachable` | Helm could not reach that server. Check the address, and that the Helm server can reach it on your network. | Try again |
| `tls_error` | Helm does not trust that server's certificate. If it uses a private certificate authority, see the setup guide. | Link to the guide |
| `not_a_forge` | That server did not answer like Gitea or Forgejo. If Gitea lives under a path, include it. | Focus the field |
| `issuer_mismatch` | This server says its address is {issuer}. | **Use that address** |
| `already_connected` | Helm is already connected. Disconnect first. | Show the connection |
| `state_invalid` | That sign-in expired or was started somewhere else. | **Sign in with Gitea** |
| `denied` | Sign-in was cancelled on Gitea. Nothing was changed. | **Sign in with Gitea** |
| `scope_conflict` | Gitea still holds an older authorization for Helm. Remove Helm under Authorized OAuth2 Applications, then sign in again. | Link to `revoke_url` |
| `authorize_failed` | Gitea refused the sign-in. Check the Client ID. | Focus the Client ID |
| `client_confidential` | The application is set as a Confidential Client. Edit it in Gitea, untick Confidential Client, save, then sign in again. | Link to `register_url` |
| `client_unknown` | Gitea does not recognise that Client ID. Copy it again from the application page. | Focus the Client ID |
| `exchange_failed` | Gitea did not complete the sign-in. | **Sign in with Gitea** |
| `storage_failed` | Helm could not save the connection. Check that its data directory is writable. | None |
| `reconnect_required` | Helm's sign-in to Gitea has ended. | **Sign in again** |
| `unavailable` (`public_origin_missing`) | Sign-in needs Helm's public address. An operator sets `HELM_PUBLIC_ORIGIN`. | None |
| `unavailable` (`disabled`) | Gitea sign-in is turned off on this server. | None |
| Anything else | Something went wrong connecting to Gitea. | Try again |

Never show an upstream message or a raw code alone.

## Layout and responsiveness

- Use the existing settings section styles and tokens; add no new colours.
- Above 640 px: label and value side by side where it helps. At 640 px and
  below: one column, buttons full width, copy buttons stay beside their value.
- Long values (redirect URI, addresses) use `overflow-wrap: anywhere` and
  never cause horizontal scrolling at 320 px.
- Reserve space for the result row and for step 2 so content below does not
  jump when a check finishes. Use a skeleton for the first status load.
- Honour `prefers-reduced-motion`.

## Accessibility

- A polite live region announces check results, copy confirmations and state
  changes. Errors are tied to their field with `aria-describedby`.
- Focus moves to the step heading when a step opens, and to the result after
  returning from the forge.
- Every control is reachable and operable by keyboard; copy buttons have
  names that include what they copy.

## Proof

The WP4 spec screenshots every state at 1280 px and at 390 px, and asserts no
horizontal overflow at 320 px. WP5 measures, on the real binary with the fake
forge, that a person can go from an empty panel to "Connected" with three
entries: the address, the client ID, and the forge's consent button.
