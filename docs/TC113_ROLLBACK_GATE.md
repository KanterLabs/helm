# Membership activation and recovery gate

TC-113's backend and UI can be reviewed before activation. Do not deploy
migration 041 until TC-335 and TC-336 are complete. These are unclaimed Backlog
implementation cards, not completed evidence.

The current `deploy/coolify-deploy.py` restores the preceding image after any
deployment error. Once 041 commits, a schema35/40 binary can ignore reviewed
human grants or reconcile the configured email back to administrator. SQL
shape compatibility and readiness do not establish authorization compatibility.
The legacy beta switcher is retired; filtering its menu cannot fix the active
Coolify recovery path.

## TC-335: enforce recovery selection

Before any deployment mutation, require bounded acceptance evidence tied to
the exact candidate and recovery image digest/source SHA. Persist a monotonic
lane authorization floor outside the selected application revision. Advancing
the floor must not be undone by reverting image/revision coordinates.

After membership activation begins, reject direct or automatic selection of
incompatible images. Use an explicitly accepted membership-aware recovery
pair, rather than the preceding schema35/40 image. If recovery fails, retain
current data and report the failure; never restore an older database.

Establish single-primary replacement: the old binary must stop serving before
the new binary migrates the shared database. Exact health revision checks alone
cannot prove this. Failure after migration, partial environment writes,
interrupted recovery and stale/malformed evidence all need isolated proofs.

Historical images and backups stay intact. Incompatible artifacts become
ineligible for supported rollback; retaining their bytes does not require
allowing them to serve a membership-governed database.

## TC-336: prove exact artifacts on populated copies

Retain a reviewed membership backend with the previous UI as a distinct
immutable recovery baseline. Local source `cc07c75` is a possible starting
point, not an accepted artifact. Build provenance, an immutable digest and
actual runtime acceptance must exist before the first live 041 activation.

Refresh verified lane backups and use isolated copies with disposable identity
credentials and no live integration access. Exercise schema35 and schema40
upgrades separately, preserving stable IDs, relationships, claims, history,
credentials, integrity and foreign keys. Do not upload private database copies.

Run each exact retained binary unchanged on its own upgraded copy. Check
restricted and zero-grant humans, pending/disabled identities, configured-email
demotion, agent and human bearer scopes, aggregate visibility, active/revoked/
reset cookies and disable/reactivation. Record unsafe widening as a failed
eligibility gate, even if readiness and storage reads pass.

Require both the recovery baseline and full candidate to pass. Exercise an
authorized write on the baseline, then roll forward against the same current
database and verify the write, memberships and history remain. Capture only
sanitized status codes and synthetic response excerpts.

## Integration points

Use focused policy modules near the active Coolify deployer. Wire protected
acceptance/fallback coordinates into both deployment lanes, the release
preparation gate and final provenance. Preserve lane locks, branch-tip checks,
private ingress, runtime mounts and secrets. The old LXC broker is outside this
path and must not be revived as an authorization workaround.

No floor, accepted recovery image or runtime authorization proof is implemented
by this document. Membership remains undeployed until those gates are real.
