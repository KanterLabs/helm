# Gitea OAuth sign-in: research record

Status: complete, 2026-11-12. Tracking: TC-387. Companion:
[implementation plan](GITEA_OAUTH_PLAN.md).

## Question

Can Helm connect to a self-hosted Gitea by OAuth, with read-only access and
no secret stored in Helm, and what exactly does Gitea do at each step?

## How this was established

| Level | Source |
| --- | --- |
| Live | The home instance's version and OpenID discovery document, read anonymously. It runs **Gitea 28.3.0**. |
| Tested | The whole flow scripted against throwaway **Gitea 28.3.0** and **Forgejo 17.0.1** containers: registration, sign-in, consent, token exchange, API reads, refresh, revocation and failure cases. |
| Source | Gitea at tag `v28.3.0`: `routers/web/auth/oauth2_provider.go`, `services/oauth2_provider/access_token.go`, `models/auth/oauth2.go`, `services/auth/oauth2.go`, `routers/api/v1/api.go`. |
| Docs | Gitea and Forgejo OAuth2 provider pages and Gitea's configuration reference. |

The scripts and sanitized outputs are kept at
`/var/tmp/agent-artifacts-1000/helm-gitea-oauth-research/`. They hold no
tokens. WP5 of the plan turns them into a repeatable proof.

Gitea renumbered its releases in 2026: `v1.27.x` was followed by `v28.0.0`.

## Findings in brief

1. **It works without a secret.** Gitea supports the authorization-code grant
   with PKCE for public clients. A public client exchanges and refreshes
   tokens with only its client ID.
2. **There is no device flow.** The home instance answers 404 on every device
   endpoint and rejects the device grant type. Upstream has only an unmerged
   draft (go-gitea/gitea#36960, last touched 2026-08-30). Forgejo 17 documents
   and advertises the same two grants.
3. **Every forge needs a one-time registration.** Someone creates an OAuth2
   application on that Gitea and gives Helm its client ID. Gitea's built-in
   applications only redirect to `127.0.0.1`, so a server cannot borrow them.
4. **Read-only, limited access works.** The scope `read:repository read:user`
   allowed every read Helm needs and refused everything else tried.
5. **Three traps**, each handled in the plan:
   - The registration form ticks **Confidential Client** by default. A
     confidential application cannot be used without its secret.
   - On Gitea 28.3.0 a mistyped or missing scope silently grants **full
     access**: a token asked for `read:repositry` created a repository.
   - The token response does not say what scope was granted, so Helm must
     test for it.

## Behaviour, as tested

| Behaviour | Gitea 28.3.0 | Forgejo 17.0.1 |
| --- | --- | --- |
| Grants advertised | `authorization_code`, `refresh_token` | Same |
| Public client, PKCE, no secret | Works | Works |
| Authorize without PKCE (public client) | Redirects back with `error=invalid_request` | Same |
| Consent page for a public client | Shown on every sign-in | Same |
| User denies consent | Redirects back with `error=access_denied` | Same |
| Redirect URI not registered | HTTP 400 page on the forge; no redirect back | Same |
| Token response fields | `access_token`, `token_type`, `expires_in`, `refresh_token` | Same |
| Access token lifetime | 3600 s by default | Same |
| Refresh token lifetime | 730 h by default (from the token's `exp`) | Same |
| Scope `read:repository read:user` | Enforced | Enforced |
| Mistyped scope | **Full access** | Unknown part dropped; access stays limited |
| No scope parameter | **Full access** | Not tested |
| Sign in again with a different scope | New consent page | Fails: `server_error`, "a grant exists with different scope", until the user revokes the old grant |
| Old refresh token reused | Accepted by default | Refused by default (`unauthorized_client`, "token was already used") |
| Replay of an old refresh token | With one-time mode on: refused, current token keeps working | Same |
| Refresh within the same second | May return a byte-identical token | New refresh token |
| `Authorization: Bearer` on `/api/v1` | Works (`token` prefix also works) | Works |
| Version string | `28.3.0` | `17.0.1+gitea-1.22.0` |

Forgejo's documentation says OAuth2 scopes are not implemented. Forgejo 17.0.1
enforced them in this test. The plan relies on a runtime check, not on either
claim.

### What `read:repository read:user` reaches

All returned 200 on a private repository: the signed-in user, the repository
list, repository metadata, branches, pull requests and one pull request, pull
request reviews, combined commit status and status list, file contents, the
recursive tree, and branch comparison.

Refused with 403: organizations (`read:organization`), issues (`read:issue`),
and creating a repository.

`GET /api/v1/user/repos` returned repositories the account owns, collaborates
on, and reaches through an organization team. Page size is capped at 50; the
response carries `X-Total-Count` and a `Link` header.

A **site administrator** account could read another user's private
repository. A normal account got 404. The connected account's own reach is
the real boundary, so a dedicated non-administrator account is the safe choice.

## Wire reference

Paths are relative to the forge's base URL.

| Step | Request | Result |
| --- | --- | --- |
| Probe | `GET /.well-known/openid-configuration` | `issuer`, `grant_types_supported`, `code_challenge_methods_supported` (`plain`, `S256`). No revocation or device endpoint. |
| Authorize (browser) | `GET /login/oauth/authorize` with `client_id`, `redirect_uri`, `response_type=code`, `state`, `code_challenge`, `code_challenge_method=S256`, `scope` | Consent page, then 303 to `redirect_uri?code=…&state=…` |
| Exchange | `POST /login/oauth/access_token`, form: `grant_type=authorization_code`, `client_id`, `code`, `redirect_uri`, `code_verifier` | 200 JSON |
| Refresh | Same path, form: `grant_type=refresh_token`, `client_id`, `refresh_token` | 200 JSON with a refresh token every time |
| Identity | `GET /api/v1/user` | `login`, `id` |
| Repositories | `GET /api/v1/user/repos?limit=50&page=N` | List |

Scopes are separated by spaces. `redirect_uri` is compared after lower-casing
and trimming a trailing slash, and must be the same string at authorize and
exchange. Plain `http` redirect URIs are accepted.

Token endpoint errors are HTTP 400 with `error` and `error_description`:

| Situation | `error` | `error_description` |
| --- | --- | --- |
| Confidential application, no secret | `unauthorized_client` (exchange), `invalid_client` (refresh) | "invalid empty client secret" |
| Wrong PKCE verifier | `unauthorized_client` | "failed PKCE code challenge" |
| Code already used | `unauthorized_client` | "client is not authorized" |
| Unparseable or expired refresh token | `unauthorized_client` | "unable to parse refresh token" |
| One-time refresh token reused | `unauthorized_client` | "token was already used" |
| Grant revoked by the user | `invalid_grant` | "grant does not exist" |
| Application deleted | `invalid_client` | "cannot load client with client id: …" |
| Unsupported grant | `unsupported_grant_type` | "Only refresh_token or authorization_code grant type is supported" |

An expired access token, a revoked grant and a deleted application all look
the same on the API: HTTP 401, `{"message": "invalid username, password or
token"}`, with no `WWW-Authenticate` header. Only a refresh attempt tells them
apart.

The tokens are JWTs. The access token carries only `gnt` (grant ID), `tt`,
`exp` and `iat`; permissions are looked up from the grant on each request. So
scope belongs to the grant, and re-authorizing changes what older tokens can do.

## Registering the application

Gitea: Settings → Applications → "Create a new OAuth2 Application", at
`/user/settings/applications`. Fields: Application Name, Redirect URIs (one per
line), **Confidential Client** (ticked by default) and "Skip authorization for
public clients after granting access once". Forgejo has the first three.
Organization and site-administration pages offer the same form.

A user removes Helm's access under "Authorized OAuth2 Applications" on the
same page. There is no revocation endpoint Helm could call.

Relevant server settings (`[oauth2]`): `ENABLED` (default true),
`ACCESS_TOKEN_EXPIRATION_TIME` (3600 s), `REFRESH_TOKEN_EXPIRATION_TIME`
(730 h), `INVALIDATE_REFRESH_TOKENS` (false on Gitea), `DEFAULT_APPLICATIONS`.

## Consequences for the design

1. Use authorization code with PKCE (`S256`) and a public client. Helm stores
   a client ID and no secret.
2. Fix the scope string at `read:repository read:user` and never build it from
   input. Changing it later forces Forgejo users to revoke first.
3. After every sign-in, prove the grant is limited: a request needing a scope
   Helm did not ask for (`GET /api/v1/user/orgs`) must be refused. If it
   succeeds, the grant is full access and Helm must say so. Gitea's refusal
   names the granted scope ("…, token scope=read:repository,read:user", worded
   this way since 1.23), so Helm can also catch a grant widened after sign-in.
4. Do not request `openid`. Identity comes from `/api/v1/user`.
5. Detect a confidential application from the exchange error and tell the
   administrator to untick the box.
6. Always store the newest refresh token, and never assume a refresh changes
   the access token. This is correct in both refresh modes.
7. On a 401, try one refresh. A refresh failure of `invalid_grant`,
   `invalid_client` or `unauthorized_client` means reconnect.
8. Refresh at least daily even when idle, or a 30-day refresh token lapses.
9. A redirect-URI mistake strands the user on the forge's error page, so the
   setup screen must show the exact URI to register and explain that page.
10. Recommend a dedicated non-administrator account.

## Not verified

- Installs under a sub-path (`ROOT_URL` with a path).
- Gitea older than 1.23, where the docs say scopes are ignored. The check in
  consequence 3 covers it.
- Forgejo with no scope parameter, and Forgejo's responses after revocation.
- Sign-in through the forge's own SSO or two-factor step. It happens in the
  browser on the forge and should not affect Helm.

## Sources

- [Gitea OAuth2 provider](https://docs.gitea.com/development/oauth2-provider)
- [Gitea configuration reference](https://docs.gitea.com/administration/config-cheat-sheet)
- [Gitea API usage](https://docs.gitea.com/development/api-usage)
- [Forgejo OAuth2 provider](https://forgejo.org/docs/latest/user/oauth2-provider/)
- [Device flow request, go-gitea/gitea#27309](https://github.com/go-gitea/gitea/issues/27309)
  and [draft, #36960](https://github.com/go-gitea/gitea/pull/36960)
- [Gitea source at v28.3.0](https://github.com/go-gitea/gitea/tree/v28.3.0)
