# TC-113 membership UI handoff

This records the original worker slice. Later parent corrections and current
acceptance are in [TC113_UI_PARENT_ACCEPTANCE.md](TC113_UI_PARENT_ACCEPTANCE.md).

Worker slice on `feat/helm-consolidation` (base `cc07c75`), uncommitted. Parent
owns TC-113, independent diff review, final acceptance and every release gate.
This does not complete TC-113, certify deployment or make rollback safe.
Scope: [port contract](TC113_MEMBERSHIP_PORT.md), [backend handoff](TC113_BACKEND_HANDOFF.md),
[parent acceptance](TC113_PARENT_ACCEPTANCE.md). Failure-first inventories:
[UI](TC113_UI_FAILURES.md) and [runner phase](../test/e2e/MEMBERSHIP_SUITE_FAILURES.md).
No backend, OpenAPI, generated JSON, migration, package or workflow file changed.

## Behavior

- **Settings** now leads with *Your account* for everyone: identity, role and
  granted project names, or an explicit "No projects granted yet". Local mode
  adds password change (current + new + confirmation) and *Signed-in sessions*
  with real metadata (summarized user agent, raw agent in tooltip, IP, sign-in,
  last active, expiry/revocation). No session is labelled current; the server
  has no such flag. Cloudflare/Tailnet/disabled modes explain who manages
  sign-in and offer no password or session controls.
- **Members** (active owner/admin, or the development disabled-auth bypass):
  every page with `disabled=true`, ordered pending → active → disabled. Add
  member (no invitation email; password only in local mode), review/admit a
  pending identity, edit name/description/role/explicit grants, disable or
  reactivate with confirmation, reset another member's password and inspect or
  revoke their sessions. Only changed fields are sent; omitted grants stay
  unchanged and `[]` revokes all, shown as "No projects selected". Grants for
  projects outside the loaded list are preserved. Saved responses replace
  drafts; last-admin `409` reads "Helm needs at least one active owner or
  administrator". Agents & tokens moved to `AgentsSection.svelte`, admin-only;
  `loadAgents` no longer runs for members.
- **Retries**: create/patch keep their Idempotency-Key and exact body while the
  outcome is unknown; "Retry same change" reuses both, a different change is
  refused until "Refresh members". `idempotency_key_reused` asks for a refresh.
  Password routes never send a key. Membership writes use
  `suppressNetworkUnavailable`, so a dropped response is reported in place
  rather than switching the workspace into offline read-only mode.
- **Admission**: outside disabled mode a human without `workspace_role` is
  pending regardless of a stale `admin` bit. `finishAuthentication` stops
  before project/beta/polling reads and App shows *Waiting for workspace access*
  with *Check again* (kept on screen while re-checking). Disabled mode and
  legacy fixtures (all current route fixtures use it) stay admitted/admin.
- **Fencing**: an access generation is bumped on sign-out, actor change and
  panel unmount; late member/session/password responses are discarded. Sign-in
  form password fields are cleared after success. Self role/status changes
  re-read the actor; an ended session returns to sign-in.

## Files and bytes

Measured after the final edit with `check-file-sizes` (no WARN/OVER).

| File | Bytes |
| --- | ---: |
| `web/src/lib/components/membership/access.ts` (new) | 4,842 |
| `…/membership/membersState.svelte.ts` (new) | 6,078 |
| `…/membership/types.ts` (new) | 310 |
| `…/membership/membership.css` (new) | 6,092 |
| `…/membership/AccountSecurity.svelte` (new) | 3,351 |
| `…/membership/PasswordChange.svelte` (new) | 3,040 |
| `…/membership/SessionList.svelte` (new) | 6,447 |
| `…/membership/MembersAdmin.svelte` (new) | 4,969 |
| `…/membership/MemberCreateForm.svelte` (new) | 4,538 |
| `…/membership/MemberEditor.svelte` (new) | 8,706 |
| `…/membership/ProjectGrants.svelte` (new) | 1,640 |
| `…/membership/AdmissionRequired.svelte` (new) | 1,397 |
| `web/src/lib/api/membership.ts` (new) | 2,669 |
| `web/src/lib/types/membership.ts` (new) | 1,259 |
| `web/src/lib/api.ts` / `lib/types.ts` / `lib/types/core.ts` | 2,569 / 502 / 3,709 |
| `web/src/app/session.ts` / `app/settings.ts` / `app/state/session.svelte.ts` | 13,962 / 7,824 / 4,379 |
| `web/src/App.svelte` | 13,519 |
| `web/src/app/views/settings/SettingsView.svelte` / `AgentsSection.svelte` (new) | 6,469 / 4,504 |
| `web/e2e/membership-sessions.spec.ts` (new) | 11,989 |
| `web/e2e/support/membershipFixture.ts` / `membershipSessionJourney.ts` / `membershipSyntheticStates.ts` (new) | 8,202 / 6,579 / 8,878 |
| `test/e2e/run-suites.py` / `test_run_suites.py` / `README.md` | 12,059 / 14,203 / 5,100 |
| `test/e2e/test_membership_suite.py` / `MEMBERSHIP_SUITE_FAILURES.md` (new) | 4,206 / 1,552 |
| `docs/E2E_TESTING.md`; new `TC113_UI_FAILURES.md` / this file | 10,039 / 3,637 / ~8,463 |

## Evidence (`.artifacts/tc113-membership-ui/`)

1. `harness-red.log`: 5 phase proofs failed before `run-suites.py` changed;
   `harness-green.log` passes. The existing full-run proof now uses 4 workers
   so all four ancillaries can overlap; the 3-worker queueing is asserted in
   the new proof. Every coordinator run re-passed all 65 supervisor proofs.
2. `e2e-red/` (before any UI source): `membership` required and failed, 7/7
   journeys failed; main/onboarding passed. Two test bugs found there were
   fixed before implementing: the viewport login posted `name` to the strict
   login schema (400), and a substring `getByLabel` was made exact.
3. `e2e-green-1/`: 6/7 passed. The revoke journey checked a cookie before the
   asynchronous DELETE finished; it now waits for the visible "Session
   revoked" status. Screenshot review drove summarized device labels, a
   five-session cap and keeping the admission screen up during re-checks.
4. `e2e-green-2/`: 7/7 passed under `-race`. A synthetic admission journey
   and an admission-screen spacing fix followed. `e2e-green-3/` is the final
   receipt: 8/8 membership, main and onboarding passed, exit 0, 65 proofs.
5. `screenshots/` (from green-3): owner/member at 320/390/1280 in light and dark, member
   settings, last-admin denial, admin sessions, Cloudflare/Tailnet accounts,
   pending admission, admitted identity, fenced member view, plus sanitized
   JSON captures (member responses, session descriptors, retry receipt).

Commands: native Go 1.26.10 with `GOMODCACHE`, `GOFLAGS=-mod=readonly`,
`GOPROXY=off`; `HELM_E2E_GOFLAGS=-race HELM_E2E_ARTIFACT_DIR=… ./test/e2e/run.sh
e2e/membership-sessions.spec.ts`; `npm run check` (0 errors/warnings, docs
pass); `npx vitest run` (230 pass); `python3 test/e2e/test_membership_suite.py`.

**Real backend** (local auth, real cookies/database): create, grant swap,
`[]` revoke, member isolation and absent admin fetches, last-admin demotion and
self-disable denial, disable ends cookies, reactivation plus restored grants do
not revive them, self change keeps only the requesting cookie, wrong/mismatched
password, admin reset expires all and rejects the old password, admin and self
revocation by device, self-revocation returns to sign-in, injected response
loss retried with the same key, 12 viewport/theme captures without overflow.
**Synthetic** (labelled): pending admission and Check again, admitting a
pending identity, disabled-mode legacy fixture, Cloudflare/Tailnet modes,
directory outage/retry and a held owner response arriving after sign-out.

Artifacts use synthetic identities; traces/video are off for this file. An
in-test scan and a post-run scan of all runs (including copied SQLite files and
server logs) found no synthetic password, cookie header or bearer value.

## Decisions and open risks

- Runner: membership binds `+50..+80` of the onboarding block so `run.sh`
  range checks stay untouched. Alternative: a full fifth block, which needs a
  `run.sh` range change outside this slice. Parent should pick.
- The pre-existing `/admin/beta/builds` probe still runs for members (silently
  hidden 403); `app/beta.ts` is outside ownership.
- Not exercised with real servers: Cloudflare/Tailnet ingress, pending identities,
  self-demotion while another admin exists, paginated directories over 100.
- Worker evidence still copies SQLite (bcrypt and token hashes, no plaintext).
- **Unsafe old-image downgrade remains OPEN.** Retained binaries ignore grants
  and can re-elevate configured email. A passing UI proves nothing about that.
  Populated migration, fresh backup, retained-image authorization and rollout
  gates remain with the parent; none were run or changed here.
