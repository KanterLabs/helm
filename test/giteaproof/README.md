# Real-forge proof for the Gitea connection

Opt-in proof for WP5 of [the plan](../../docs/GITEA_OAUTH_PLAN.md): it starts
real Gitea and Forgejo containers and drives Helm's real binary through OAuth
sign-in against each, through Helm's HTTP API only, as the Settings panel
would. It needs Docker and pulls images, so it is **not** part of routine CI or
`test/e2e/run.sh`.

```sh
test/giteaproof/run.sh
```

Exit status 0 means every assertion passed for every forge. Artifacts go to
`.artifacts/giteaproof/<run-id>/`: `result.json` (per forge: version, each
assertion and its outcome, timings), each forge's console log, and Helm's
stdout and stderr (copied only after the leak scan found them clean).

## Settings

| Variable | Default | Meaning |
| --- | --- | --- |
| `HELM_GITEAPROOF_IMAGES` | `gitea/gitea:28.3.0 codeberg.org/forgejo/forgejo:17` | Space-separated images. An image whose name contains `forgejo` is driven as Forgejo. |
| `HELM_GITEAPROOF_ARTIFACT_DIR` | `.artifacts/giteaproof/<run-id>` | Where results go. |
| `HELM_GITEAPROOF_GO_WORK` | private, removed | Reuse a Go module and build cache directory between runs. |
| `TMPDIR` | | Scratch root; one private directory is made under it and removed. |

## What it does

For each forge: a throwaway certificate authority signs a certificate for
`127.0.0.1`, so the forge speaks https on a free loopback port (and Helm gets
the CA through `SSL_CERT_FILE`). The forge runs with an in-memory `/data`,
`INSTALL_LOCK`, a 5-second access-token lifetime and, on Gitea,
`INVALIDATE_REFRESH_TOKENS=true` (Forgejo is one-time by default). An
administrator creates fixtures by API: a non-administrator `helm-reader`, a
private repository it owns, one it collaborates on, one reached through an
organization team, two private repositories it must not see, and a public and
a confidential OAuth2 application whose redirect URI is Helm's callback.

Helm runs from a fresh build with a fresh database in local auth mode: the
owner is created by first-run setup and a second administrator by the members
API, each with its own session. The proof then scripts the forge's login and
consent pages with plain HTTP and a cookie jar, and asserts: the probe with a
full URL, a bare host and a deep page link; `client_confidential`, `denied`
and `state_invalid` (other administrator, forged state, no Helm session);
sign-in as the reader with `scoped` access and exactly the three reachable
repositories; refresh after expiry, four times, with one-time mode proven at
the forge and a Helm restart in the middle; revocation on the forge's settings
page giving `reconnect_required`, then signing in again; disconnect removing
the 0600 file from its 0700 directory; and no token or authorization code in
Helm's output, its SQLite files or the artifacts.

Everything is removed on exit, including on failure or interruption: Helm,
the containers (named and labelled with the run ID), the private directory,
and any image this run pulled. Images already present are left alone. See
[FAILURES.md](FAILURES.md) for what each guard is for.
