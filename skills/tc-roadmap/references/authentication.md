# Authentication

`scripts/tc_roadmap.py` resolves configuration in this order:

1. Environment variables (`HELM_*` canonical; legacy aliases are accepted when they do not conflict).
2. A JSON file selected by `HELM_CONFIG` or `TC_ROADMAP_CONFIG`.
3. `~/.config/helm/credentials.json`, falling back read-only to `~/.config/tc-roadmap/credentials.json`.

Supported environment variables are:

- `HELM_URL` (canonical)
- `HELM_TOKEN` (canonical)
- `HELM_CF_ACCESS_CLIENT_ID` (canonical)
- `HELM_CF_ACCESS_CLIENT_SECRET` (canonical)
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

Create a dedicated agent identity and grant only the scopes needed for tracking: `projects:read`, `tasks:read`, `tasks:write`, `tasks:claim`, and `events:read`. Restrict the token to relevant projects when practical. Token plaintext is returned only once by Helm and must never be committed, pasted into Helm tasks, or printed in logs. Conflicting canonical and legacy environment values fail closed. Configure the authorized origin explicitly for each installation, using `base_url` in its secure file or `HELM_URL`; requests use `/api/v1`. The old public origin currently remains a deprecated compatibility default for token-only configurations. Do not rely on it for new installations.

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

Run `python3 scripts/tc_roadmap.py config-check` to inspect local setup without
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

For a protected GET-only identity check, use the canonical
`python3 /path/to/helm/scripts/helm.py auth-check` from the same environment as
the actual hook. This compatibility client intentionally retains its existing
command surface beyond the shared `config-check` command.
