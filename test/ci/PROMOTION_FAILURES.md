# Trusted validation and promotion failure inventory

Written before implementation. Proof command: `python3 -m unittest discover -s test/ci -p test_promotion_journeys.py`.
Sanitized subprocess/API captures are retained in `.artifacts/ci-promotion/`.

- A supplied revision can differ from HEAD, or tracked checkout files can be dirty.
  Receipt creation must read Git and refuse both; actual source SHA/tree are recorded.
- Fast/focused/missing/canceled/skipped/failed suites can masquerade as full.
  Require the full checked inventory and real success results; retain the evidence hash.
- Equal trees can use different toolchains, flags, dependencies, workflows or test policy.
  Bind hashes/settings/toolchains and compare destination inputs before reuse.
- VERSION, release metadata, runtime, build, dependency and test-policy changes can
  invalidate a candidate. Initially any tree change falls back to full validation.
  Docker copies the checkout and Go can embed VCS information; metadata is not ignored.
- A PR, manual run, fork, other repository/workflow, incomplete or unsuccessful run
  can produce plausible JSON/images. Only successful push-to-beta API provenance
  and the expected workflow at its recorded source revision can authorize selection.
- A stale rerun, mismatched run attempt, failed required job, missing or ambiguous
  artifact, expired artifact, wrong artifact/run pairing, changed archive digest,
  unsafe archive, missing receipt or substituted source/image must fall back to full.
- Artifact names alone and arbitrary local JSON cannot establish provenance. Resolve
  metadata and download by immutable artifact ID through read-only `gh api` calls.
- Missing source Git objects, malformed API/receipt JSON, unavailable API, excessive
  payloads and command failures must produce an explicit full-validation fallback.
- Promotion commit and artifact source commit differ even for identical trees. Keep
  both identities. Current readiness and admission require the actual source SHA;
  an equal-tree merge SHA therefore records matching inputs but requires full validation
  until a separately proven runtime identity migration is approved.
- Acceptance selects a candidate only. Fresh production image/security, target config,
  readiness/live smoke, migration/backup compatibility and deployment admission remain
  required. Existing pause, authorization, recovery and deployment code are untouched.
- Never put credentials in subprocess argv, errors or proof captures. Fixtures use
  synthetic public repository data and contain no external writes or deployments.

Boundary journeys cover receipt creation, real Git commits/trees, real subprocess CLI,
GitHub API run/jobs/artifact metadata and ZIP bytes via a deterministic `gh` executable.
They prove local contracts; they do not claim live GitHub or production acceptance.

## Main-candidate wrapper boundary failures (written before wrapper implementation)

- Shadow mode, PRs, manual events and every ref except an enforced main push must
  avoid all external reads and select full validation. Environment strings are data.
- Discovery must query successful beta pushes for actual checkout SHA, not a supplied
  revision, release manifest or title. Resolve at most one newest matching run.
- A fresh gate recheck may pin one positive integer run ID. Invalid IDs must never
  become GitHub endpoint syntax or command arguments; failed proof never tries a
  second run or silently accepts stale discovery data.
- Missing/malformed/unavailable discovery, missing receipts, incomplete scope,
  mismatched SHA and toolchain changes must produce a bounded explicit fallback.
- Every accepted output must match the actual source SHA, immutable registry image,
  proof hashes and positive producer identities. Normalize fields; do not forward
  caller/API strings into GitHub output or print potentially private exceptions.
- GitHub output contains only `accepted=true|false` and an immutable `image=` value
  or an empty value. Newlines, command syntax and hostile API strings cannot inject
  output keys, run commands or escape the fixed read-only API endpoints.

## Receipt-job dispatch failures (written before wrapper implementation)

- Full validation keeps the strict existing receipt creator, actual source proof,
  complete suites and binary/frontend inputs. A promotion gate cannot be relabeled full.
- Promotion acceptance is a separate artifact kind, never a new full-validation
  receipt. It records empty current browser inventory and references the original
  trusted full receipt; no binary/frontend outputs are required or invented.
- Promotion acceptance requires an exact trusted main push producer, current actual
  Git SHA/tree, a passed promotion gate with matching aliases and successful current
  checks/container jobs. Beta, manual and PR producers must be refused.
- Reused proof must normalize through the plan's exact-source promotion identity
  checks. Its canonical digest, image, source identities and positive API identities
  must match; a raw digest claim alone cannot certify an altered proof.
- Missing jobs, failed/skipped/cancelled jobs, mismatched source/image/scope,
  incomplete proof, forged full scope, contradictory aliases and dirty checkouts
  must refuse receipt creation. No API reads or deployment actions are performed.
