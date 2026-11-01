# TC-337 Tickets / Connect apps UX: failure inventory

Written before the change. The Tickets page keeps every existing connection
capability; Connect apps moves from an inline stack above the queue into an
on-demand manager dialog with **Email**, **Webhooks** and **Public access**
tabs. Each row below names how the redesign could fail and the check that
proves it does not. Browser proof lives in `web/e2e/ticket-connections-ux.spec.ts`
plus the adapted `ticket-webhooks`, `public-endpoints`, `email-intake` and
`cloudflare-connect` specs.

## Layout and hierarchy

| Failure | Check |
| --- | --- |
| Opening Connect apps still pushes the queue down the page | The queue tabs, search and first ticket keep their position when the manager opens; the manager is a modal layer, not page flow. |
| Heading competes with the queue | One heading, one short line, one primary **＋ New ticket**; Connect apps is a quiet entry with a compact status. Queue tabs start in the first viewport at 1440, 390 and 320. |
| Manager overflows on phones | At 390 and 320 the manager is full screen, the page and the manager have no horizontal overflow, tabs stay on one row. |
| Dark mode loses contrast or borders | Light and dark screenshots at 1440/390/320 for queue and each tab. |
| Several areas compete inside the manager | Exactly one tab panel is visible; creation forms and the API guide stay hidden until asked for. |

## Function preserved

| Failure | Check |
| --- | --- |
| Inbox create, assign-to-me, copy, mailto, replace, turn off or the retired list become unreachable | Email tab → **New inbox** creates and lists it; Copy/Email it/More → Replace address/Turn off still work (email-intake spec). |
| Webhook create, format, assignment, one-time reveal, copy, “I saved it”, send test + result + ticket link, rotate, disable stop working | Webhooks tab → **New webhook**; ticket-webhooks and public-endpoints specs keep every assertion. |
| Public setup, live/degraded status, retry, details/history/manual setup or disconnect become unreachable | Public access tab renders the unchanged `PublicAccessPanel`; cloudflare-connect and public-endpoints specs keep every assertion. |
| Help and API examples lost | **Learn more** buttons, the **How to send** guide (curl, fields, responses, OpenAPI link) and **Full guide** remain. |
| Status no longer visible | Tabs show inbox count / Off, webhook count and Live / Connecting / Off; the Connect apps entry keeps **● Public**. |
| Setup no longer refreshes inboxes | Guided setup still bumps the inbox list and the heading status. |

## Drafts and secrets

| Failure | Check |
| --- | --- |
| Switching tabs drops a typed inbox/webhook name | Panels stay mounted; the draft survives Email → Webhooks → Email. |
| Closing the manager drops a draft silently | Close/Escape/backdrop with a draft asks first; **Keep editing** returns focus inside. |
| Closing loses the one-time webhook URL | Close with an unacknowledged URL asks first; tab switching keeps it; the Webhooks tab flags it. |
| Secret leaks to storage, URL or evidence | No new storage keys; URL carries no tab or secret; screenshots mask the reveal and curl; the new spec disables trace/video/failure screenshots. |
| Host update guard ignores connection drafts | Workspace `dirty` includes manager drafts and an unsaved URL. |
| Public setup fields disappear without a warning | A synthetic connected Cloudflare account exercises edited hostname, inbox name and email options across tabs; close asks before discarding. Untouched automatic defaults close without a warning. Token drafts also participate in the workspace navigation guard. |

## Keyboard, focus and access

| Failure | Check |
| --- | --- |
| Focus escapes the modal or is lost on close | Tab cycles inside; Escape/✕ close; focus returns to Connect apps. |
| Tabs are not keyboard operable | Arrow/Home/End move selection; `aria-selected`, `aria-controls`, roving tabindex. |
| Help drawer inside the manager breaks Escape or focus | Escape with help open closes only help; focus returns to the opener. |
| Global shortcuts act behind the modal | `/` inside the manager does not jump to ticket search. |
| `?connect=1` deep link breaks | Opens the manager (Public access when not live, otherwise Email) and is removed from the URL; queue URL state (queue/project/q/ticket) and history are untouched. |
| Non-admins see admin controls or trigger admin requests | Non-admins get a read-only explanation and the guide; no webhook/inbox/public requests are made. |
