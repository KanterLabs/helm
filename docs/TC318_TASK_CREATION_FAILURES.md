# TC-318 — title-first task creation failure inventory

Recorded on 29 October 2026 before any product change. Scope: the ordinary
New task form, board create actions, global/command/child entry points and
manual draft protection. Retirement of the task-draft API, Codex settings and
private Luna history is TC-325 and is out of scope here.

Regression: `web/e2e/task-creation-title-first.spec.ts`. Existing coverage is
reused rather than duplicated where noted.

## Failures this card must remove (red before implementation)

| ID | Failure | Observable proof |
| --- | --- | --- |
| F1 | Opening New task places focus in a "Rough idea" field instead of the title. | The `Task title` textbox is not focused after the dialog opens. |
| F2 | Ordinary creation shows the Luna assist panel, a model prompt and a Codex/ChatGPT connection detour. | The dialog contains Luna/Rough idea/Codex/ChatGPT copy or an assist button. |
| F3 | Merely opening the form contacts the Codex account endpoint (and the assist path can start a model request). | A `/api/v1/codex/*` or `/projects/*/task-draft` request is observed between opening the form and creating the task. |
| F4 | The board offers two competing actions (heading "New task" and toolbar "Create task") that open the same dialog. | More than one board button named New task/Create task. |
| F5 | A title typed into the form is silently discarded by Escape, the backdrop or Cancel. | The dialog closes without asking and the typed draft is gone. |

## Guards that must stay green

| ID | Risk introduced by the change | Observable proof / existing coverage |
| --- | --- | --- |
| G1 | Removing the toolbar action leaves no reachable create action on a phone. | The heading New task action is visible inside a 390 px viewport (`board-controls-improvements.spec.ts`). |
| G2 | Per-column quick-add disappears or targets the wrong column. | The Ready column still offers `Add task` (`releases.spec.ts` creates through it). |
| G3 | Keyboard creation regresses: Enter submits zero/two times, wrong project/column, focus is lost. | Title + Enter creates exactly one task in the selected project's Ready column; focus returns to New task. |
| G4 | Child creation loses its parent or the parent's planned Focus. | Create child shows the parent note; the created task has that parent and Focus. |
| G5 | Changing project keeps a project-local parent/Focus or a stale column. | After switching project the parent note and Focus clear, the title stays, and the task lands only in the new project's Ready column. |
| G6 | Global Create menu and command palette stop opening the same form. | Topbar `Create new → Task` opens the title-first form on a phone; palette coverage stays in `command-palette.spec.ts`. |
| G7 | The confirmation itself loses data: discarding does not close, keeping does not keep, or an empty form now nags. | Dismissing the prompt keeps the values; accepting closes, persists nothing and reopens empty; an empty form closes without a prompt. |
| G8 | Lost-response retry, duplicate submit, pending dismissal/Back, validation errors and network reconnect regress. | Reused: `creation-safety.spec.ts`, `creation-destinations.spec.ts`. |
| G9 | Update-consent draft protection regresses. | Reused: `pwa-update-safety.spec.ts` task form case. |
| G10 | Removing the UI breaks the retained private Luna history while TC-325 is pending. | `luna-run-history.spec.ts` now creates its synthetic run through the existing API instead of the retired form. |
| G11 | The dialog overflows a phone viewport. | Dialog bounds stay within 390 px and 1280 px viewports (`task-draft.spec.ts`). |

## Deliberate replacements of retired-UI expectations

- `task-draft.spec.ts` "previews and selectively applies a Luna task draft"
  exercised the retired panel. It is replaced by a manual-capture case that
  proves the form issues no Codex/task-draft request and that typed manual
  fields survive a project change.
- `luna-run-history.spec.ts` drove a model run through the retired panel. It
  now uses `POST /projects/{key}/task-draft` (still served until TC-325) and
  keeps every Settings/private-history assertion.
- `board-controls-improvements.spec.ts` measured the duplicate toolbar
  button; it now measures the single heading New task action.

## Limits

No storage, API or migration change. The raw assignee actor-ID input remains
until TC-8. Dormant assist exports stay compiled for existing Settings
consumers and are removed by TC-325.

Parent acceptance uses real service workers for the new unmocked creation
flows and requires no error toast when opening the form. Blocking registration
in the earlier harness produced an unrelated PWA error in its screenshots.
Board-control failure injection keeps its existing blocked-worker setup; its
unchanged API fixtures now live in `e2e/support/boardControlsFixture.ts` so the
spec remains below the file-size warning point.
