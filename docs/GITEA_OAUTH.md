# Connect Gitea or Forgejo

Helm can connect to your self-hosted Gitea or Forgejo so it can read your
repositories. You sign in on your forge and approve read-only access; Helm
never sees a password and keeps no client secret.

It lives in **Settings**, under **Code hosting**, in the section titled
**Gitea**. Only administrators see it. Once Helm knows the server is Forgejo,
every label and message says "Forgejo" instead of "Gitea".

This release connects and lists the repositories the account can read.
Showing Gitea pull requests, branches and checks in Helm comes next.

Related: design and decisions in [GITEA_OAUTH_PLAN.md](GITEA_OAUTH_PLAN.md);
what Gitea and Forgejo were tested to do in
[GITEA_OAUTH_RESEARCH.md](GITEA_OAUTH_RESEARCH.md).

## Connect in three steps

Before you start, decide which forge account Helm should use. A dedicated
account that is not a site administrator is the safe choice; see
[Use a dedicated account](#use-a-dedicated-account).

1. **Where is your Gitea?** Type or paste the address you open Gitea at, such
   as `gitea.example.com`. A full link to any page on the forge works too.
   Helm checks it by itself and shows "Found Gitea 28.3.0 at
   gitea.example.com". **Continue** does the same if you prefer a button.
2. **Create Helm's application in Gitea.**
   1. Select **Open Gitea's application page**. It opens your account's
      Applications settings on the forge in a new tab.
   2. Under *Create a new OAuth2 Application*, enter the **Application name**
      (`Helm`) and the **Redirect URI** Helm shows. Each has a copy button.
      **Untick "Confidential Client"**: Gitea ticks it by default, and Helm
      keeps no client secret, so the application must not be confidential.
      Save the application.
   3. Copy the **Client ID** Gitea shows and paste it into **Paste the Client
      ID Gitea shows you**. Pasting a whole line such as "Client ID: …" is
      fine; Helm picks out the ID.
3. **Approve on Gitea, and you come back here connected.** Select **Sign in
   with Gitea**. Gitea asks you to approve Helm's access. Approve it, and the
   browser returns to Settings showing "Connected to gitea.example.com as
   @login".

Gitea approves as whoever is signed in to Gitea in that browser. If that is
not the account Helm should use, sign out of Gitea and sign in as that account
before you select **Sign in with Gitea**.

Once connected, the section shows the server and version, **Read-only
access**, and "Helm can read N repositories". **View** lists them, with a
filter box when there are more than ten. Helm lists at most 500; beyond that
it says "Showing the first 500. Gitea has more." **Test connection** checks
the sign-in and the access again.

## What Helm can and cannot read

Helm always asks for the same access, the scope `read:repository read:user`:

- **Can read:** the signed-in account's profile, and the repositories that
  account can reach (its own, ones it collaborates on, and ones it reaches
  through an organization team): code, branches, pull requests, reviews and
  commit statuses.
- **Cannot:** change anything, create repositories, or read organizations or
  issues.

Helm does not take this on trust. After every sign-in, and whenever you select
**Test connection**, it tries something the scope does not allow (listing the
account's organizations). Gitea refusing it is what earns the **Read-only
access** badge.

The account's own reach is still the real limit: Helm can read every
repository that account can read.

## Use a dedicated account

Sign Helm in with an account made for it, and give that account access to
just the repositories Helm needs (as a collaborator, or through an
organization team).

Do not use a site administrator. A site administrator's sign-in can read
every repository on the server, including other people's private ones, even
with the read-only scope.

## If Helm says it has more than read-only access

You may see: "This server gave Helm more than read-only access to this
account. Helm only reads, but use a dedicated account with access to just the
repositories Helm needs."

Helm checks this after every sign-in and every **Test connection**. It means
one of two things:

- The forge did not enforce the read-only scope, for example a Gitea release
  older than 1.23.
- Helm's authorization on the forge was approved again with a wider scope.
  On Gitea the scope belongs to the authorization, so Helm's existing sign-in
  gains it. Gitea tells Helm the scope it granted, and Helm warns when that
  is more than reading. Forgejo refuses to widen an existing authorization.

Helm stays connected and only ever reads, but the stored sign-in could do
more than read. To fix it:

1. Under **Authorized OAuth2 Applications** on the forge, remove Helm.
2. Upgrade the forge if it is old, or move Helm to a dedicated
   non-administrator account (see [Use a dedicated account](#use-a-dedicated-account)).
3. Select **Disconnect**, then connect again.

If you see "Helm has not yet confirmed that its access is read-only. Test the
connection to check.", the check could not finish. Select **Test connection**.

## Fixing problems

Each message appears where it happened, with the action that fixes it. In
every message, "Gitea" reads "Forgejo" for a Forgejo server.

### Finding the server

| Message | What to do |
| --- | --- |
| That does not look like a server address. Try gitea.example.com. | Enter a host, optionally with `https://` and a path. See [Addresses Helm contacts](#addresses-helm-contacts). |
| This address uses http, so sign-in details could be read on the network. | Use `https` if you can. Otherwise tick **Continue without encryption**; see [Servers on plain http](#servers-on-plain-http). |
| Helm could not reach that server. Check the address, and that the Helm server can reach it on your network. | Helm's server, not your browser, makes this check. Make sure the Helm machine or container can resolve and reach the forge, then select **Try again**. |
| Helm does not trust that server's certificate. If it uses a private certificate authority, see the setup guide. | See [Private certificate authorities](#private-certificate-authorities). |
| That server did not answer like Gitea or Forgejo. If Gitea lives under a path, include it. | Check the address. A forge served under a path needs it, such as `gitea.example.com/git`. |
| This server says its address is …. | The forge's own configured address differs from what you typed. Select **Use that address**. |

### Creating the application and signing in

| Message | What to do |
| --- | --- |
| That does not look like a Client ID. Copy it again from the application page. | Copy the Client ID again. It is not the client secret. |
| Gitea shows *Unregistered Redirect URI* (on the forge) | The redirect URI saved in the application does not match the one Helm shows. Copy it again into the application on Gitea, save, and sign in again. |
| The application is set as a Confidential Client. Edit it in Gitea, untick Confidential Client, save, then sign in again. | Select **Edit the application in Gitea**, untick the box, save, and sign in again. |
| Gitea does not recognise that Client ID. Copy it again from the application page. | The ID is mistyped or the application was deleted. Select **Edit the Client ID** and paste it again. |
| Gitea refused the sign-in. Check the Client ID. | Select **Edit the Client ID** and check it. |
| Sign-in was cancelled on Gitea. Nothing was changed. | Select **Sign in with Gitea** and approve. |
| That sign-in expired or was started somewhere else. | A sign-in must finish within 10 minutes, in the browser and Helm account that started it. Sign in again. |
| Gitea did not complete the sign-in. | Sign in again. If it repeats, check the forge's logs. |
| Gitea still holds an older authorization for Helm. Remove Helm under Authorized OAuth2 Applications, then sign in again. | Forgejo refuses a new approval while an older one exists. Select **Open Authorized OAuth2 Applications**, remove Helm, and sign in again. |
| Helm is already connected. Disconnect first. | Select **Show the connection**. Disconnect it before connecting another server. |
| Helm did not open the sign-in page because it was not on the server you checked. | Try again; if this repeats, check the server address. |

Your address and Client ID are kept in the browser tab until you connect, so
a failed sign-in returns you to step 2 with them filled in. They are not
secrets.

### After connecting

| Message | What to do |
| --- | --- |
| Helm's sign-in to Gitea has ended. | Gitea no longer accepts Helm's sign-in: it was removed on the forge, it lapsed, or Helm's connection file could not be used. Select **Sign in again**; nothing needs to change on Gitea. If the panel names no server, select **Disconnect** and connect again. |
| Helm could not reach gitea.example.com at the last check. | Other Helm work is unaffected. Check the forge is up and reachable from Helm, then **Test connection**. |
| Gitea refused Helm's sign-in at the last check. | **Test connection**. If it keeps failing, disconnect and connect again. |
| Helm could not save the connection. Check that its data directory is writable. | An operator checks [the connection file](#the-connection-file)'s directory. |

### When sign-in is not offered

| Message | What to do |
| --- | --- |
| Sign-in needs Helm's public address. An operator sets HELM_PUBLIC_ORIGIN. | An operator sets `HELM_PUBLIC_ORIGIN` and restarts Helm. |
| Gitea sign-in is turned off on this server. | An operator removes `HELM_GITEA_OAUTH=off` and restarts Helm. |
| Something went wrong connecting to Gitea. | Try again. If it repeats, check that Helm is running normally and can reach the forge. |

## Private certificate authorities

If your forge's `https` certificate comes from your own certificate authority,
Helm will not trust it until you tell it about that authority. There is no
option to skip certificate verification.

Helm trusts the system's certificate authorities plus any in `SSL_CERT_FILE`
and `SSL_CERT_DIR`. The simplest safe route:

1. Save your authority's certificate in PEM format (the text starting
   `-----BEGIN CERTIFICATE-----`) in a directory of its own, such as
   `/etc/helm/ca/home-ca.pem`.
2. Set `SSL_CERT_DIR` to that directory. Several directories can be listed,
   separated by `:`.
3. Restart Helm. It reads certificates once, at startup.

In Docker, mount the directory read-only into the container and set
`SSL_CERT_DIR` to its path there. Leave the image's `SSL_CERT_FILE` as it is:
replacing it with only your authority's certificate would stop other parts of
Helm trusting public servers. On a host install you can instead add the
authority to the system trust store (for example with
`update-ca-certificates`) and restart Helm.

The certificate must also name the address you entered. A certificate for
`gitea.internal` does not cover `gitea.example.com`.

## Servers on plain http

Helm assumes `https` unless you type `http://`. For an `http` address, Helm
warns "This address uses http, so sign-in details could be read on the
network." and continues only after you tick **Continue without encryption**.
Anyone who can watch that network could then read Helm's sign-in to the forge.
The connected view says "without encryption" for as long as it lasts.

Prefer `https`, even with a [private certificate
authority](#private-certificate-authorities).

## Disconnect

Select **Disconnect**, then **Yes, disconnect**. Helm deletes its sign-in and
stops reading repositories.

That does not remove Helm's authorization on the forge, which has no way for
Helm to do it. To remove it too, sign in to the forge as the connected
account and open **Settings → Applications**. Under **Authorized OAuth2
Applications**, remove Helm. The confirmation links to that page. Deleting
the OAuth2 application itself also ends every sign-in made through it.

## Restores, moves and downgrades

The connection is not in Helm's database. It is one file in the forge state
directory (see [For operators](#for-operators)), and Helm's backup scripts do
not copy it:

- **Restoring the database in place** leaves the file alone, so the
  connection carries on.
- **Restoring or moving to a new machine or volume** brings no connection
  file. The section shows **Not connected**: connect again. You can reuse the
  application already on the forge; its page shows the Client ID.
- **Downgrading** to a Helm without this feature leaves the file unused.
  Helm keeps the sign-in alive by refreshing it daily, and Gitea's refresh
  tokens last about 30 days by default. If you come back to this release
  after longer, you will see "Helm's sign-in to Gitea has ended.": select
  **Sign in again**.

If you copy the whole data directory yourself, the copy holds live sign-in
tokens: protect it like a password. A copied file can also hold a sign-in the
forge has since replaced; if so, select **Sign in again**.

## For operators

| Setting | Default | Meaning |
| --- | --- | --- |
| `HELM_GITEA_OAUTH` | `on` | `off` turns the connection off; Settings says sign-in is turned off. |
| `HELM_FORGE_STATE_DIR` | a `forge` directory beside the `HELM_DB` file | Holds the connection file. With the defaults that is `/data/forge` in Docker and `/var/lib/roadmap/data/forge` on a host install, so it follows the data volume. |
| `HELM_PUBLIC_ORIGIN` | see [CONFIGURATION.md](CONFIGURATION.md) | Required. The forge sends the browser back to `<HELM_PUBLIC_ORIGIN>/api/v1/integrations/gitea/oauth/callback`. |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | system authorities | Extra certificate authorities; see [Private certificate authorities](#private-certificate-authorities). |

`HELM_PUBLIC_ORIGIN` must be the address people use to open Helm, and the
person connecting should open Helm at that address: the forge returns the
browser there, and the sign-in finishes only in the same Helm session. The
forge never contacts Helm itself; only the browser goes back and forth.

### The connection file

The file is `gitea.json` in the forge state directory. Helm
creates the directory with mode `0700` and the file with `0600`, owned by the
user Helm runs as, and replaces it atomically. It holds the server address,
Client ID, the account, and the access and refresh tokens. No tokens are
stored in the database, the browser, logs or exports. Helm treats a file it
cannot trust as signed out and leaves it in place: one readable by group or
others, owned by another user, a symbolic link, over 32 KB, or unreadable.
**Disconnect** deletes it. If you set `HELM_FORGE_STATE_DIR` yourself, choose
a directory Helm can write to; the packaged systemd unit only allows writes
under `/var/lib/roadmap/data`. Helm creates the directory if it is missing. A
directory that already exists must belong to Helm's user and be closed to
group and others (mode 0700); it may be a symbolic link to one. Helm refuses
a shared directory, and says why in its log, rather than changing its
permissions.

### Addresses Helm contacts

Helm's server contacts the address an administrator
typed, so it applies these rules:

- The address is `http` or `https`, a host, an optional port and an optional
  path, at most 255 characters. No `user:password@`, query or fragment.
- `http` is used only after the administrator ticks **Continue without
  encryption**.
- Private and loopback addresses are allowed, because self-hosted forges live
  there. Link-local addresses (such as `169.254.169.254`), multicast,
  broadcast and unspecified addresses are refused, checked on the address
  actually dialled.
- Redirects are not followed, each request times out after 10 seconds, and
  responses are size-limited.
- Helm follows the operator's proxy settings (`HTTPS_PROXY`, `HTTP_PROXY`,
  `NO_PROXY`). If the Helm server can only reach the network through a proxy,
  the forge's host must be allowed there; if the forge is reachable directly,
  list it in `NO_PROXY`. Through a proxy, Helm still refuses the addresses
  above before the request leaves, by the address typed or by what the name
  resolves to on the Helm server. "Helm could not reach that server" on a
  server that works from your own machine usually means one of these two
  settings.
- Messages from the forge are never shown or logged; Helm reports one of its
  own codes instead.

### API

Only signed-in human administrators can use these routes; API tokens are
refused. Operations in `/openapi.json`: `getGiteaConnection`,
`probeGiteaServer`, `startGiteaSignIn`, `finishGiteaSignIn`,
`checkGiteaConnection`, `listGiteaRepositories`, `disconnectGitea`.
