# TC-336 immutable-runtime acceptance failure inventory

Written before the runtime harness. These are real-container HTTP journeys,
not unit tests and not policy-reader fixture receipts.

- PR head metadata can name a different commit from the checkout used to build;
  verify merge parents and exact Git trees independently through GitHub.
- An artifact download, archive checksum, image config ID, registry manifest
  digest and executable hash are different identities. Never substitute one
  for another. Inspect the executable's embedded Go build VCS metadata.
- Image entrypoints, environment or mounted executables can silently replace
  tested code. Run the retained exact image ID with its unchanged entrypoint;
  inspect actual image identity and prohibit published ports or external network.
- Empty databases do not demonstrate populated migration preservation. Exercise
  schema35 and schema40 independently and compare existing columns/rows before
  migration; preserve IDs, relationships, credentials, claims and event history.
- Runtime last-seen updates are expected; explicitly exclude only those columns
  and verify all other pre-existing rows, allowing additive migrations/rows.
- Readiness or legacy SQL compatibility can hide authorization widening. Check
  granted/hidden projects, zero grants, pending and disabled humans, configured
  email demotion, human/agent bearer ceilings and aggregate results over HTTP.
- Session invalidation may fail for retained legacy cookies, explicit revocation,
  reset or disable/reactivation. Use real login cookies and stale cookie replay.
- Recovery can appear successful after restoring old data. Switch exact images
  on the same current database, write through recovery HTTP, then roll forward
  and verify that write and membership/history preservation.
- Generated populated data is not a live-lane copy. Label the evidence source;
  never issue protected policy acceptance from synthetic-only results.
- Partial runs, unexpected responses and missing scenarios must remain failed
  or incomplete; no passing exchange may be inferred or fabricated.
- Receipts must never include passwords, cookies, bearer values, raw private
  responses, database rows or integration configuration. Capture allowlisted
  projections and status codes; keep databases mode0700/0600 and local only.
