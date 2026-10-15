# Authentication

`scripts/helm.py` resolves configuration in this order:

1. Environment variables (`HELM_*` canonical; legacy aliases are accepted only when they do not conflict).
2. A JSON file selected by `HELM_CONFIG` or the compatibility alias `TC_ROADMAP_CONFIG`.
3. `~/.config/helm/credentials.json`, falling back read-only to `~/.config/tc-roadmap/credentials.json`.

Supported environment variables are:

- `HELM_URL`
- `HELM_TOKEN`
- `HELM_CF_ACCESS_CLIENT_ID`
- `HELM_CF_ACCESS_CLIENT_SECRET`
- `TC_ROADMAP_URL` (compatibility alias)
- `TC_ROADMAP_TOKEN` (compatibility alias)
- `ROADMAP_TOKEN` (compatibility alias)
- `TC_CF_ACCESS_CLIENT_ID` (compatibility alias)
- `TC_CF_ACCESS_CLIENT_SECRET` (compatibility alias)

The JSON file may contain:

```json
{
  "base_url": "https://helm.example.com",
  "token": "agent-token",
  "cf_access_client_id": "optional-service-token-id",
  "cf_access_client_secret": "optional-service-token-secret"
}
```

The credential file must be owned by the current user and have no group or world permissions; mode `0600` is recommended. The helper refuses a broader mode. Cloudflare fields are optional for deployments whose API is not behind Cloudflare Access.

`HELM_TOKEN` is the Helm application credential issued to an agent actor. It
authorizes API scopes such as `tasks:read`, `tasks:write`, `tasks:claim`, and
`events:read`. `HELM_CF_ACCESS_CLIENT_ID` and
`HELM_CF_ACCESS_CLIENT_SECRET` are separate Cloudflare Access edge credentials
used only to cross the protected tunnel. Supplying Cloudflare credentials
cannot replace a missing Helm token, and the two kinds of credential must not
be copied into one another's fields.

Create a dedicated agent identity and grant only the scopes needed for tracking:
`projects:read`, `tasks:read`, `tasks:write`, `tasks:claim`, and `events:read`.
Add `notifications:read` or `notifications:write` only when the agent uses the
inbox or watch workflows; the existing task/project read and write scopes also
provide the documented compatibility fallbacks. Restrict the token to relevant
projects when practical. Token plaintext is returned only once by Helm and must
never be committed, pasted into Helm tasks, or printed in logs. Configure the authorized origin explicitly for each installation, using
`base_url` in its secure file or `HELM_URL`; requests use `/api/v1`.
The old public origin currently remains a deprecated compatibility default
for token-only configurations. Do not rely on it for new installations.

In `tailnet` mode, human sign-in belongs to the private deployment edge. The
edge derives the real Tailnet peer from tailscaled and sends a short-lived HMAC
assertion bound to the exact origin, HTTP method, and request URI. Do not supply
a local password, trust raw Tailscale/proxy headers, copy the edge signing key
into this credential file, or attempt to synthesize an assertion. The helper
still needs its Helm application bearer token and merely uses the configured
private HTTPS origin; Cloudflare service-token fields are unrelated to Tailnet
identity and should remain unset unless that deployment separately requires
Cloudflare Access.

To verify endpoint reachability and the Helm application identity without
performing a mutation, run `python3 scripts/helm.py auth-check`. The command
returns only bounded actor metadata and emits a sanitized machine-readable
error on failure; credential values never appear in its output.

For portable setup, create `~/.config/helm/credentials.json` with
`"base_url": "https://helm.example.com"` replaced by the operator-authorized
HTTPS origin, and the existing application credential. Keep the file owner-only
(mode `0600`). Canonical and legacy files are never merged: a canonical file
wins when present, unless a config-path environment override selects another
file. Both aliases at the same layer must agree; environment URL values override
file URL values, but do not bypass file validation or alias conflict checks.
Cloudflare ID and secret must be configured together. Remove obsolete edge
aliases from the launch environment as well as the file when an edge no longer
requires them.

Run `python3 scripts/helm.py config-check` to inspect local setup without
making any network request. JSON output contains only the validated `origin`,
`url_source` (`env:HELM_URL`, `env:TC_ROADMAP_URL`, `file:<key>`, or `default`),
`config_source` (`env:HELM_CONFIG`, `env:TC_ROADMAP_CONFIG`, `canonical-file`, or
`legacy-file`), `application_token_present`, and `cloudflare_pair_present`.
The config source names the selected location, even if no file exists there;
source labels never contain file contents or arbitrary paths. All existing
configuration checks apply before output, so a missing token, partial edge pair,
unsafe file, conflicting aliases, or invalid URL fails without diagnostics.
No token values, edge credential values, headers, or raw auth errors are shown.
This local check does not establish reachability or authenticated identity.

Endpoint discovery is explicit configuration. The helper does not scan the
network, fetch `.well-known` endpoint advertisements, or follow redirects.
Credentials remain bound to the configured origin. An old-origin redirect does
not migrate clients. Verify the new authorized endpoint before changing each
installation's secure file. A newly invoked helper or hook reads the file again;
a running agent's inherited URL/config overrides require a corrected launch
environment and restart. A shell export does not update its running parent.
Preserve task, project, claim, operation, and session identities during a move.

After `config-check`, run `python3 scripts/helm.py auth-check` from the actual
hook launch environment to verify the protected identity using GET `/auth/me`.
Compare the actor identity with the pre-migration baseline before resuming work.
