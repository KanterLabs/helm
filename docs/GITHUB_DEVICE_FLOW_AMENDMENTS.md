# GitHub device-flow sign-in: amendments to the plan

Status: active, 2026-11-14. Read with [the plan](GITHUB_DEVICE_FLOW_PLAN.md).
Where the two differ, this file wins. These come from running the removed
Gitea connection on beta.

## Base

`feat/github-device-flow` starts from `revert/gitea-connection`: `beta` with
the Gitea connection reverted. Its tree equals `eadcd32`. The plan's file
references still hold.

## Binding additions

**A1. GitHub calls go through the operator's proxy.** Beta reaches the network
only through an allowlisting HTTP proxy named by `HTTPS_PROXY`. A client that
ignores it times out. Every call in C4 uses the standard proxy settings
(`http.ProxyFromEnvironment`). The existing provider client gets this from
Go's default transport; a custom transport must set it explicitly.
Reachable from beta today: `github.com` and `api.github.com` on 443 (checked
2026-11-14). Call no other host. A test proves the connection client sends
its requests through a configured proxy.

**A2. No 502 or 504 for an expected failure.** A proxy in front of Helm
(Cloudflare does this) replaces the body of those statuses with its own page,
so the browser loses `error.code` and the panel cannot say what to do. New
routes answer 409 for a state problem and 424 when GitHub cannot be reached or
refuses. A handler test asserts that no code of the new routes maps to 502 or
504.

**A3. The panel never blames GitHub for an answer that is not Helm's.** When a
failed request carries no Helm `error.code` (a replaced page, a network
failure), the panel says: "Helm did not get a proper answer from its own
server. Try again in a moment."

**A4. Log each failed sign-in once**, at warn level, with the outcome code and
nothing else.

**A5. The standard for WP4.** Shane asked for a connection that is "super
straightforward, responsive and easy". That means:

- One primary action per state. Connecting is: press **Connect GitHub**, enter
  the code on GitHub, come back connected. No form fields.
- The code is large, selectable and copied with one press. **Open GitHub**
  opens the verification page in a new tab.
- Usable at 320 px wide with no sideways scrolling, in light and dark, by
  keyboard alone. State changes are announced to assistive technology.
- Every message says what to do next in plain words. Never show a raw code or
  text that came from GitHub.
- The panel updates by itself when GitHub approves; nobody presses refresh.
- Screenshots of every C6 row at 1280 and 390 px wide.

## Changes to the contracts after review

These replace the matching words in C1, C2, C4 and C5.

**A6. A refused token from a renewing source is not a lost permission (C1).**
C1 said the provider calls `Rejected` and then takes its deny path. That moved
the generation on a routine refresh whenever a request was in flight with the
token just rotated away. For a source that implements `Rejected`, a 401 is
reported, the fetch is retried once with the current token, and nothing is
denied. 403 and 404 still deny. The token file keeps its deny path.

**A7. Ending and retrying (C2, C4).** One 401 for a token fresh from a refresh
renews once more; two in a row, or `bad_refresh_token`, end the sign-in. A
failed refresh is not repeated for 30 seconds. `Fresh` answers for the
connection held when it returns, token and identity together. A sign-in that
ends by the clock alone still notifies the allowlist listeners.

**A8. Limits (C2, C4, C5).** Installation and repository pages may be up to
2 MB (a page of 100 repositories does not fit in 64 KB); every other reply
stays at 64 KB. Concurrent syncs share one run. At most 6 device-code requests
a minute; beyond that connect answers 429 `rate_limited`. Only the
administrator who began an attempt may cancel it; anyone may replace it.
Connect and disconnect take no body.

## Gates

**G1.** Unchanged, and the only step that needs Shane before sign-in works
anywhere. GitHub has no URL parameter for device flow, so one box is ticked by
hand. Open
`https://github.com/organizations/KanterLabs/settings/apps/new?name=KanterLabs%20Helm&url=https%3A%2F%2Fgithub.com%2FKanterLabs%2Fhelm&public=true&webhook_active=false&contents=read&pull_requests=read&checks=read&statuses=read&metadata=read`,
tick **Enable Device Flow**, leave **Expire user authorization tokens** ticked,
and create the App. The orchestrator needs its client ID and slug. A callback
URL is not needed: GitHub ignores it for device flow.

**G2.** GitHub's documentation now says the refresh request needs a client
secret "unless the user access token was generated using the device flow". G2
confirms that on a live run; it is no longer an open question.

**G3.** The beta rollout is approved: Shane's instruction is to get this onto
beta. `main` still waits for his explicit approval. Beta runs in
`operator_token` mode today, so device sign-in appears there only once
`HELM_GITHUB_TOKEN_FILE` is unset. That is a configuration change to make at
rollout, with Shane's agreement.
