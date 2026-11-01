# Runtime admission failure inventory and DEV journeys

Written before main integration and journey implementation, 2026-10-31.
This bounded stage proves actual Helm startup behavior on synthetic data. It
does not implement bootstrap, authorize an artifact pair, inspect a real
Coolify queue, or accept rollout. The independent review's remaining host
bootstrap, drain, profile, recovery and observation gates stay open.

1. A new binary under the historical Compose profile could migrate protected
   data before the identity barrier. Mount the private directory with its old
   owner and no authority; startup must fail with admission error before DB
   bytes change, regardless of default image entrypoint or enable flags.
2. A missing, closed or foreign authority could be mistaken for public use.
   With the private mount present, missing authority, prepared phase, wrong
   executable hash and a writable authority mount all fail before DB writes.
3. A user override could satisfy the private UID without enforcing admission.
   The actual binary's early check must run without an external guard helper.
   An old UID with a valid marker still fails before database access.
4. A lease may be dropped before the application starts or by healthcheck.
   Run admitted Helm, verify readiness with its actual healthcheck command,
   then start a duplicate against the same data and lock: the duplicate must
   fail admission while the first remains healthy. Stop the first and verify
   a subsequent process can acquire the same permanent lock and serve.
5. Schema/audit CLI commands could bypass startup. With private data and no
   authority, migration-apply, schema-preflight and audit-access must reject
   before their command-specific database handling. Healthcheck and
   migration-info are nonmutating and remain usable without a lease.
6. Public Docker usage could become dependent on private authority. Run the
   same binary without the reserved private mount, at the image's original
   UID/default data path, and verify actual HTTP readiness and populated-data
   preservation.
7. Tests could accidentally target live data, expose a server, or claim that a
   hybrid image is an accepted immutable artifact. Use exclusively created
   synthetic fixtures, network=none, no ports/secrets, unique owned container
   names and bounded calls. Mount the built binary into a retained image only
   as a labeled DEV hybrid fixture. Record base image, actual executable hash
   and effective process profile; this is explicitly not TC-336 acceptance.
8. Root-only fixture setup or failures could leak processes and hide state.
   A bounded setup container mounts only the unique fixture. Finally remove
   only recorded owned containers and restore fixture ownership. Record the
   exact remaining-container list. Preserve every image artifact.
9. An error could corrupt a populated database or hide changed writes. Invalid
   startup cases compare DB hashes; admitted cases verify synthetic stable IDs,
   relationships, integrity and foreign keys after orderly stop. No live
   restore/reset is used. Keep artifacts bounded and free of credentials.

Run `python3 test/deployment-policy/runtime_admission_e2e.py --binary PATH`
after building `./cmd/helm` with cached Go. The harness records a repeatable
receipt under `.artifacts/tc335/runtime-journey/`. Initial RED runs use the
unmodified main; GREEN requires the integrated runtime admission primitive.
