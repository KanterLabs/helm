# Evidence inference transport failure inventory

Recorded before the transport fix and its regression test.

- The isolated evidence subprocess can lose the server's HTTP(S)/SOCKS proxy
  configuration and stall or fail provider requests after GitHub capture succeeds.
- Losing `NO_PROXY` can route local or private endpoints through a proxy. Preserve
  both uppercase and lowercase proxy spellings, including explicitly empty values.
- Losing `SSL_CERT_FILE` or `SSL_CERT_DIR` can reject a deployment's trusted TLS
  chain. These are local TLS-client trust-store paths, not model input or tool
  permissions. Preserve only these explicit certificate settings.
- Broad environment inheritance can expose GitHub/provider credentials, arbitrary
  Helm configuration, or execution hooks such as `NODE_OPTIONS` to evidence code.
  Keep an allowlist; do not inherit the server environment wholesale.
- Server `HOME`/`CODEX_HOME` must not replace the requesting actor's isolated home.
- Transport settings must not enable tools, execution environments, MCP, or change
  the validated `gpt-5.6-luna` / `codex-cli 0.153.0` requirement.
- Verification output must contain synthetic values or boolean attestations only;
  never print the live environment, proxy credentials, or actor credentials.

The regression runs `Manager.DraftEvidence` through a synthetic app-server child
and captures a boolean environment attestation in the test output. It checks
configured, explicitly empty, and absent transport settings, blocked secret and
execution variables, actor-scoped homes, and the existing empty-environment/tool
protocol boundary. It does not send a live inference request.

## Reproducible regression evidence

Use the Go toolchain declared in `go.mod`:

```sh
go test ./internal/codexruntime \
  -run '^(TestDraftEvidenceTransportEnvironment|TestDraftEvidenceIsolatedProtocol)$' \
  -count=1 -v
```

The test emits a captured `evidence transport artifact` JSON map of booleans for
each mode. Before the runtime change, configured and explicitly empty modes each
failed all ten transport checks; absent mode, secret exclusion, actor homes and
completion checks passed. After the allowlist change, all 24 checks pass in each
mode, and the existing isolated protocol test passes.

This proves the subprocess receives only the intended tested transport settings
and excludes the tested secret/execution settings. TLS certificate settings are
forwarded as local client configuration; the fixture does not prove a live
provider connection or validate certificate contents. Tool policies, CLI/model
validation and actor account handling are unchanged.

The beta transport probe made an unauthenticated HTTPS request to the provider's
model-list endpoint. With the deployment settings it established verified TLS
and received the expected HTTP 401. With the evidence process's old stripped
environment it could not connect. No provider credential, inference request or
private response content was used in this probe.
