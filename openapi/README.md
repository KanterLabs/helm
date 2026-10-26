# OpenAPI domains

[`../openapi.yaml`](../openapi.yaml) is the authoring entrypoint: document
metadata and an explicit `x-helm-fragments` manifest. Its listed YAML files
supply disjoint path items and component definitions. Use the assembled
`/openapi.json` endpoint when importing the contract into OpenAPI tools.
The authoring manifest must be assembled before use as a complete document.

- `paths/`: resource routes and actions, including their operation documentation.
- `schemas/`: request, response and shared domain models.
- `parameters/`: resource identities, filters, pagination and mutation guards.
- `responses/`: request errors, mutation conflicts and admission failures.
- `examples/`: examples grouped with their domain.
- `authentication.yaml` and `response-headers.yaml`: shared protocol definitions.

Edit the domain that owns the definition; add its path to the manifest when
adding a domain. Existing internal `$ref` values address the assembled document
and remain unchanged. Metadata belongs in the entrypoint. Duplicate path or
component definitions fail assembly rather than override another domain.
Keep every source and embedded asset at or below 20,000 bytes; split domains
near 16,000 bytes by resource or behavior to leave room for future edits.

From `web/`, run `npm run openapi:generate` after editing. The generator writes
matching, readable JSON fragments under `internal/httpapi/openapi/` and removes
obsolete assets. `npm run openapi:check` detects missing, stale and extra assets.
The Go binary embeds this whole asset tree and assembles the complete document
once at startup; clients still receive one OpenAPI document at `/openapi.json`.
The documentation checker uses the same source assembler as the generator.
Run `node --test scripts/openapi-source.test.mjs` for source assembly checks.
