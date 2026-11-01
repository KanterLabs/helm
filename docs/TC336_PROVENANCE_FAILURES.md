# Runtime artifact provenance failure inventory

Recorded before generalizing runtime_acceptance_provenance.py, 2026-11-01.

- Fixed historical run IDs accidentally certify the wrong candidate or recovery.
  Each role accepts an explicit successful run ID, expected source SHA and actual
  checkout SHA; historical defaults remain available.
- Malformed role inputs reach GitHub or the shell. Require positive decimal run
  IDs and complete 40-character Git SHA values before any network operation.
- A failed/incomplete authenticated run, mismatched source SHA or different Git
  object is accepted. Validate run completion/success and object identities.
- Direct push source/checkout equality is incorrectly required to have a parent
  edge. Verify the same authenticated Git object and tree instead.
- A PR merge unrelated to the source or with a different tree is accepted.
  Require the source as a checkout parent and equal independently resolved trees.
- Missing, duplicate, expired or mismatched artifacts are accepted. Require one
  image and one binary artifact for the actual checkout; verify both ZIP digests.
- Local extracted image/binary files differ from the authenticated ZIP payload.
  Compare exact archive/executable bytes and image checksum sidecar.
- Manifest/config/layer or other OCI blob corruption is overlooked. Verify all
  OCI blob names, bytes and descriptor sizes/digests before using an image.
- A checksum-valid checks binary embeds the wrong VCS revision. Require the
  actual checkout; also reject a conflicting image VCS revision when present.
- Log labels or a shortened commit accidentally substitute for source evidence.
  Require exact full SHA output from authenticated checkout git-log, successful
  container job, Docker build/save chain, exact written config digest, exact
  uploaded artifact name and its authenticated ZIP digest. Push logs do not
  contain the PR-only `HEAD is now at ... Merge ...` message.
- Partial verification emits a reusable success receipt. Write provenance.json
  only after both complete role chains pass.

Focused isolation tests construct tiny OCI/ZIP fixtures and fake only the
GitHub/Docker/Go process boundary. They assert complete verification outcomes
and refusal of corrupted evidence; no credentials, real user data, published
images or live deployment services are needed. The parent independently runs
this verifier against authenticated real CI after the candidate is available.
