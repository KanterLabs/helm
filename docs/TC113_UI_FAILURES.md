# Parent review regressions

- Successful self-demotion must immediately apply the returned role, clear
  privileged projects/boards/offline snapshots, and fence outstanding reads,
  even when a subsequent status request fails.
- A late revocation status probe must not sign out a newer login. Revoking
  this browser's cookie must return to sign-in even if that probe is unavailable.
- Initial session-list failures need retry; pending admission rechecks need
  visible errors.
- Required local membership validation must fail on a nonlocal fixture,
  rather than passing with every case skipped. Final reports and copied
  evidence need credential checks after their writers have finished.

# TC-113 membership UI failure inventory

Recorded before the browser journeys and before any membership UI source.
Scope: [the port contract](TC113_MEMBERSHIP_PORT.md) and the reviewed backend
in [the backend handoff](TC113_BACKEND_HANDOFF.md). Every scenario below is a
way the Settings, admission or account surfaces could fail a real person.

## Administration

- Owner/admin cannot see pending or disabled identities because the list reads
  only the first page or omits `disabled=true`.
- Create sends an empty `project_ids`, `null` or an unexpected field, or offers
  an invitation email that does not exist. Admin-created password fields leak
  into URLs, browser storage, logs or screenshots.
- Grants are shown from the local draft instead of the saved response; an
  empty grant list looks like "all projects" instead of "no projects".
  Omitting grants on a profile-only edit accidentally revokes them.
- Role and grant edits show success while the server rejected them. Last-admin
  `409` denials for demotion or self-disable are swallowed or look generic.
- Disable/reactivate or password reset runs without confirmation, or reactivation
  implies the member's old sessions came back.
- An uncertain create/patch (network loss after commit) is retried with a fresh
  Idempotency-Key and executes twice, or a `409 idempotency_key_reused` is
  presented as an ordinary failure instead of "refresh to see the saved state".
- Password routes receive an Idempotency-Key and are rejected.

## Ordinary members and isolation

- Members see the Members/Agents/token forms or trigger admin-only `/members`
  and `/agents` fetches whose `403` appears as an error.
- A member sees ungranted projects, or a member with zero grants sees a blank
  board/spinner instead of an explicit "no projects granted" state.
- After sign-out, sign-in as another actor, or demotion, a late `/members` or
  session response repopulates private state for the wrong viewer.

## Account and sessions

- Local password change lacks current password or confirmation, validates only
  on the server, or signs the requesting browser out. Other sessions survive.
- Admin reset leaves the member's cookies working; revoked sessions are listed
  as active; a "current session" badge is invented without server support.
- Session descriptors expose or label raw identity/session keys as names, or
  hide real metadata (created, last seen, expiry, revoked, IP, user agent).
- Revoking the session in use leaves a broken shell rather than sign-in.
- Cloudflare/Tailnet users are offered local password or session controls that
  cannot work in that mode.

## Admission and compatibility

- A verified pending identity (no `workspace_role`) sees an empty board,
  infinite spinner or `membership_required` toast instead of an explicit
  admission-required state, and the client keeps polling workspace routes.
- Development auth-disabled mode or legacy fixtures without `workspace_role`
  are treated as pending; conversely an explicitly pending production user is
  treated as an administrator because of a stale `admin` bit.

## Presentation

- Controls lack labels, keyboard order or focus return after confirmation;
  saving/loading/error states are absent; 320/390px or dark mode clips text.

Real local-auth browser journeys must prove administration, isolation,
password/session invalidation, last-admin denial and non-revival. Synthetic
route fixtures cover pending admission, non-local modes, unavailable/forbidden
reads and late responses, and are labeled synthetic in test titles and
evidence. Artifacts must never contain passwords, cookies or tokens.
