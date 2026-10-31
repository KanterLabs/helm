# TC-335 parent source acceptance

Migration041 is **not approved for rollout**. The membership source at
`19dfdeadfc4706a1c322780ba02e84ccee403daa` passed PR58's six checks; this
document records the subsequent recovery-controller review. Beta remains on
`a414c69` with schema40; production remains on `c0f119cf` with schema35.
No live data, service configuration, host access or image selection was changed
during this review. TC-335, TC-336 and TC-113 are still incomplete.

## Accepted source fixes

Independent review reproduced and corrected these failures:

- An already-current replay could fail policy verification without stopping
  its writer. Replay now requests the exact-target stop-only path on failure.
- An unhealthy service deferred floor validation until a healthy route response.
  Every readiness poll now validates floor and configured coordinates first.
- A different valid floor could produce success with stale provenance. The
  original floor bytes are bound through startup/replay and checked again after
  route verification; drift is fatal.
- Candidate and recovery could refer to the same image or source revision.
  Both the acceptance reader and retained release-provenance reader reject
  either overlap before accepting it as recovery.

The six controller regressions failed before these fixes. Two additional
release-reader regressions failed before the corresponding validation change.
Failure inventories precede implementation; evidence uses synthetic fixtures.

`deploy/test-deployment-security.sh`, already called by the checks job, now
runs the controller, observer contract, parent regressions, release reader,
release commands and existing release pipeline in sequence. Set
`HELM_DEPLOYMENT_POLICY_EVIDENCE_DIR` to retain sanitized JSON receipts; without
it they live in the harness's temporary fixture. This adds no live credentials,
host access or workflow runner changes. CI upload wiring remains open.

## Independent evidence

Local artifacts are gitignored under `.artifacts/tc335/`:

| Evidence | Result |
| --- | --- |
| `parent-red-verified.log` | Six meaningful controller failures before fixes |
| `parent-green.log` | Six controller regressions pass |
| `parent-release-distinct-red.log` | Both overlapping-recovery release cases fail before fix |
| `parent-acceptance/summary.json` | All six independently run suites exit zero |
| `parent-security-integrated.log` | Combined retained protocol and active controller harness |
| `parent-security-integrated/` | Current per-journey receipts from combined harness |

The integrated log is the authoritative result for the latest source. The
original worker's `owned-files.json` identifies its earlier snapshot only.
Current parent hashes are recorded separately in `parent-owned-files.json`.
No membership/browser matrix was repeated for these Python and documentation
changes. Existing restore-auth and backup runtime cases requiring unavailable
sqlite3/root fixtures report skips; those are not runtime preservation proof.

## Installed platform findings

Read-only inspection of the deployed Coolify container confirmed these
behaviors; bounded source excerpts/hashes are retained in
`installed-source-excerpts.json`:

- `StopService` runs Docker stop and removal. `docker_cleanup=false` avoids
  image pruning; it does not promise that stopped containers remain present.
- `StartService` queues work that later queues `CoolifyTask` remote execution.
- A service's activity status can be changed to canceled without proving that
  its queued/reserved remote job cannot execute. `CoolifyTask` invokes remote
  execution without using that status as a cancellation barrier.
- Remote execution starts a process before recording its process ID. A missing
  recorded process ID alone does not establish that no process is running.

Consequently cached `exited`, canceled activities, a single Docker snapshot or
CI concurrency alone cannot certify exclusion after an interrupted controller.
This is a supported asynchronous failure path, not a requirement to constrain
an administrator deliberately bypassing the deployment procedure.

## Remaining activation gates

1. Restricted read-only observer access was approved and verified on2026-10-31;
   the access permission wait is resolved. See the installation update below.
   This access proof does not establish the remaining activation conditions.
2. Implement the actual observer and durable per-lane operation journal. Bind
   pre-stop container/data identity, account for removed containers, and prove
   selected-service queued, delayed, reserved and executing work is quiescent.
   Unknown attribution must block that lane for operator reconciliation, not
   pause all Coolify queues. The journal must retain uncertain operations across
   interruption; a journal does not itself cancel remote jobs.
3. Pin the enforcing controller independently from selected app source and wire
   both protected lanes. Verify the floor survives installed compose parsing,
   provisioning it only after checking actual lane state. Keep supported
   deployment actors serialized and reconcile known restart paths. Do not claim
   a read-only observer creates the continuing exclusion it measures.
4. Complete TC-336 against exact immutable candidate and distinct recovery
   artifacts on populated isolated copies, including authorization, session
   revocation, current-data writes and roll-forward. Refresh and verify live
   backups before any upgrade. Keep all historical images and never restore a
   database automatically.

The [policy contract](TC335_POLICY_CONTRACT.md) and
[worker handoff](TC335_HANDOFF.md) define the source interfaces. Synthetic
receipts are not accepted runtime bundles and cannot open these gates.

## Approved observer access, 2026-10-31

The owner selected restricted CI observer access. The permission wait is closed.
Infrastructure source under `homelab/coolify/helm-observer` implements the fixed
account on the existing Coolify SSH daemon and private Service
`svc:helm-deploy-observer:22`, through the existing edge loopback proxy. Only the
CI tag receives the new grant. A real disposable CI-tagged peer passed pinned
SSH metadata reads and denial of commands, oversized input, TCP forwarding and
root login; its process, device and auth key were removed. No Helm container or
database was changed. Protected beta/production environments now contain the
scoped observer key, known-host pin and endpoint settings; values are not in Git.

`deploy/ssh-observer.py` is the job-private client. Copy it to a private0700
directory with private0600 `client.json` containing only `host`, `port:22` and
`user:helm-observer`, plus `id_ed25519` and `known_hosts` from protected inputs.
Pin the executable independently and pass its path to the existing witness.
The client sends no remote command, clears forwarding/agent configuration,
pins the host, bounds input/output/time and passes no ambient API credentials.

The installed metadata collector intentionally returns **unknown**, not a
stopped-and-fenced receipt. Read access is verified; continuing exclusion,
durable interrupted-operation handling, protected workflow wiring and TC-336
actual runtime/preservation acceptance remain open. Do not enable041 rollout
by treating the read-only access proof as those missing guarantees.
