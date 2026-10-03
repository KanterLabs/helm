# End-to-end testing

Helm treats user-observable workflows against a real Helm server and database
as its primary verification mechanism. New behavior should be proven through
the same HTTP, persistence, and browser boundaries used in production.

Every end-to-end run must leave a repeatable evidence bundle containing:

- the Playwright HTML report and machine-readable JSON result;
- browser attachments produced by the workflow;
- the Helm server log and SQLite database used by the run;
- a small metadata file identifying the tested revision and command; and
- `SHA256SUMS`, covering every other file in the bundle.

The bundle is successful evidence only when the test command exits zero and
`sha256sum --check SHA256SUMS` passes. CI retains the bundle whether the suite
passes or fails so a successful result remains inspectable instead of
discarding all evidence.

Run the same workflow locally with `make test` or `npm test` from `web/`.
The harness uses the installed Go toolchain when available and otherwise builds
the application in the repository's pinned Go container through Docker.

## Replacement rule

Do not add a unit test after implementing behavior. Replace existing isolated
coverage feature by feature:

1. Write the externally visible failure scenarios before test implementation.
2. Add a workflow that exercises the compiled application through real
   process, HTTP, database, and browser boundaries.
3. Make that workflow emit and verify its evidence bundle.
4. Remove the isolated tests superseded by the workflow.

Small pure checks may remain temporarily where no real boundary can express
the invariant yet. They are migration debt, not the default place for new
coverage.

## Agent lifetime allowance removal contract

The E2E workflow must prove these failure cases before changing admission code:

| Failure | Observable proof |
| --- | --- |
| An existing agent has a legacy usage row at the former 256 MiB ceiling | A bearer-authenticated mutation succeeds without deleting or changing that row. |
| Removing the lifetime ceiling accidentally removes burst protection | The same actor's eleventh immediate mutation returns 429 with `Retry-After`; a different token cannot evade it. |
| The migration discards historical usage | The legacy row remains unchanged after the successful mutation. |

## Luna run-history failure contract

The Luna history replacement workflow must prove all of these cases through a
real Helm process:

| Failure | Observable proof |
| --- | --- |
| Helm cannot start with the deterministic Codex fixture | Readiness never succeeds and the server log is retained. |
| The human account is not recognized as connected | The task-draft workflow cannot reach review and the browser report identifies the failed step. |
| A Luna turn does not cross the runtime and HTTP boundaries | No task suggestion appears and no completed run is returned by `/api/v1/codex/runs`. |
| Run metadata is not persisted | Reloading Settings does not show the completed run. |
| History leaks actor IDs, prompts, or model output | The real history response contains one of those forbidden values. |
| The UI hides essential diagnostics | Expanding the run does not show model, effort, run ID, thread ID, and turn ID. |
| The endpoint accepts an unsafe limit | `/api/v1/codex/runs?limit=101` does not return a structured `400`. |
| Successful E2E evidence cannot be independently checked | The evidence bundle is missing or its `SHA256SUMS` verification fails. |

## Coolify alert intake failure contract

The Coolify webhook workflow must prove these failure cases through a real
Helm process configured with a disposable intake secret file. Payloads are
synthetic and use run-unique server identities so a Playwright retry against
the same database starts from a fresh condition.

| Failure | Observable proof |
| --- | --- |
| A wrong or missing secret can create work or reveal that intake exists | The request returns the same `404` as an unknown route and the project task count is unchanged. |
| A Traefik notice creates work in the wrong place or for an agent | The task is `kind=task` in the configured project's Backlog column, assigned to the configured human, with no `claimed_by` and no `agent_work`. |
| Concurrent deliveries of one notice create duplicate tasks | Parallel identical deliveries return one `created` and the rest `repeated`; exactly one task exists and its occurrence count equals the delivery count. |
| A repeat overwrites human triage or notifies again | After a human edits title, priority and description, a repeat leaves those fields and the task version unchanged and adds no assignee notification. |
| A repeat reopens or recreates completed work | After completion, an identical notice reports `retained`; the task stays completed and no new task appears. |
| A new offered version is merged into old work | A different `latest_version` creates a second task whose alert source links the earlier task key. |
| A multi-server notice partially commits | A payload with one valid and one invalid server returns `400` and creates no task; a fully valid two-server payload creates two tasks. |
| Unsupported or test events create work | `test` reports `ignored` and `server_unreachable` reports `unsupported`, both with no task. |
| Oversized or malformed bodies are processed | A body over the intake limit returns `413`; invalid JSON returns `400`; neither creates a task. |
| Misconfigured routing silently drops alerts | A missing target project returns `503` with no task. |
| The secret leaks into logs or responses | The retained server log and every intake response omit the secret. |
| Evidence is invisible to the human | The task drawer shows the Alert source panel with server, installed and offered versions and repeat count, and Activity shows the repeat alert. |

## Tickets workspace failure contract

The Tickets workflow must prove these failure cases through a real Helm
process, real database and browser. Fixtures use run-unique projects so a
retry against the same database starts clean.

| Failure | Observable proof |
| --- | --- |
| Every task leaks into Tickets, or a ticket loses membership | An ordinary task in the same project never appears in `/api/v1/tickets` or the UI; a ticket stays listed after its labels change. |
| Alert intake and manual creation produce different kinds of ticket | A Coolify alert and a `New ticket` both appear as `kind=task` tickets with origins `alert` and `manual`, in Backlog, assigned, unclaimed. |
| Queue counts are inferred from the loaded page | With more tickets than one page, server counts for each queue match the full filtered membership while the page returns `limit` rows plus a cursor. |
| Status comes from agent progress or claims | Each queue maps to the column semantic state only (`backlog`, `ready`, `active`, `blocked`, `completed`); `My open` uses assignee only. |
| A triage action applies partially or over a newer edit | Accept moves to Ready; a stale version returns `409`, the UI shows a conflict, reloads the ticket, and applies nothing. |
| Waiting loses its reason, or start/resume creates an agent claim | Waiting records the human reason in Activity; Start and Resume never set `claimed_by` or `agent_work`. |
| Selection, filters or queue are lost on reload or Back | The URL carries queue, project, search and ticket; reload restores them, and phone Back returns from detail to the filtered list. |
| A delayed response for ticket A renders into selected ticket B | Switching tickets while the first detail load is delayed leaves ticket B's header and notes visible. |
| Keyboard users cannot work the queue | Arrow keys move through rows, Enter opens a ticket, `/` focuses search outside text inputs and does nothing while typing. |
| Notes are lost or trigger hidden side effects | An internal note posts as an ordinary task comment and shows in the ticket immediately and after reload. |

## Ticket webhooks failure contract

The ticket webhook workflow must prove these failure cases through a real Helm
process, database and browser. Screenshots mask every revealed webhook URL.

| Failure | Observable proof |
| --- | --- |
| A non-admin or bearer token can create or read webhook secrets | Webhook management returns `403` to agent tokens; list responses never include a secret or full URL. |
| The secret is shown more than once or stored in plaintext | The create and rotate responses (`Cache-Control: no-store`) are the only place the URL appears; later lists show only a hint, and the retained server log omits it. |
| An outside app cannot work out the format | The Connect apps panel shows the real URL, a runnable curl example and a field table; a curl-shaped JSON post creates a ticket. |
| Invalid payloads are opaque or partially applied | A missing `title` or a bad `priority` returns `400` naming the field and creates nothing. |
| Repeats create duplicate tickets | Posts with the same `dedupe_key` while the ticket is open return `repeated` and bump the count; after completion the next post opens a new ticket linked to the previous one. |
| Rotation or disabling leaves old URLs working | After rotate the old URL returns the generic `404` and the new one works; a disabled webhook returns `404`. |
| A webhook routes outside its configured project | Tickets land in the webhook's project Backlog, assigned as configured, with `ticket.origin=alert` and readable evidence fields. |
| A Coolify-format webhook diverges from the built-in Coolify intake | A webhook created with format `coolify` accepts a Coolify `traefik_version_outdated` payload and creates the same kind of ticket. |

## Public endpoint failure contract

The Cloudflare public-endpoint workflow must prove these failure cases through
a real Helm process and browser, using the repository's fake Cloudflare API
and fake `cloudflared` fixtures (no real account is touched in CI).

| Failure | Observable proof |
| --- | --- |
| The Cloudflare API token is stored, logged or echoed | After provisioning, the token is absent from every API response, the SQLite database and the server log. |
| The tunnel exposes more than webhooks | The provisioned ingress routes only `^/api/v1/(hooks/tickets|intake/coolify)/` to the dedicated hook listener with a terminal `http_status:404`; that listener answers dashboard and API paths with `404`. |
| The tunnel run token leaks onto the command line | The fake `cloudflared` records the token only through `TUNNEL_TOKEN` in its environment, never in argv. |
| Webhook URLs keep pointing at the private origin | With an endpoint active, newly created webhook URLs use `https://<public hostname>/api/v1/hooks/tickets/`. |
| A bad token or hostname half-provisions resources | A token without zone access returns `400` naming the problem, and the fake API records no tunnel or DNS record. |
| Disabling leaves public resources behind | Disable with a token deletes the DNS record and tunnel and stops `cloudflared`; webhook URLs fall back to the private origin. |
| Non-admins can expose Helm | Agent tokens receive `403` from every public-endpoint route. |
| The card claims "Connected" without live edge connections | With the fake connector's two registrations, the card shows a Live badge, `2 edge connections · e2e01, e2e02`, the creator's name and the tunnel ID; the API reports `connections: 2` and both locations. |
| The self-test passes without a real round trip, or creates a ticket | **Test public URL** succeeds only by fetching a one-time nonce through the hooks listener with the public `Host`; the ticket count is unchanged, agents get `403`, an inactive endpoint gets `409`, and a guessed nonce gets `404` from the listener and no `200` from the main origin. |
| Resources left behind after a tokenless removal are invisible or unfixable | The removed URL appears under Earlier public URLs as *Needs cleanup* with its tunnel ID, and Admin reports it; a tokenless retry keeps it pending, **Finish cleanup…** with a token deletes both resources in the fake API, and a later tokenless retry does not re-flag it. |
| Admins cannot tell Helm is public outside the card | While active, the Connect apps button shows **● Public**; it disappears after removal, and Admin's **Manage** link opens Connect apps. |
| No way to prove an outside app can open a ticket through the public URL | **Send test** on a webhook files a low-priority ticket through the public path with a nonce bound to that webhook; a second test is a repeat on the same ticket; agents get `403`, a guessed nonce files nothing, and without an active public URL the route returns `409`. |

## Connect Cloudflare failure contract

Guided setup (`CLOUDFLARE_CONNECT_PLAN.md`) must prove these failure cases
through a real Helm process and browser, the fake Cloudflare API (including
its OAuth endpoints) and the real sign-in relay Worker served by
`test/e2e/relay-server.mjs`.

| Failure | Observable proof |
| --- | --- |
| The token link asks for the wrong permissions | The link opens Cloudflare's account-token page and its `permissionGroupKeys` are exactly zone, dns, zone_settings, email_routing_rule, argotunnel, workers_scripts and email_routing_address; a unit test pins them to the permissions the cards list. |
| A token that sees no domains is accepted | Connecting with it fails with "not allowed to see any domains" and the panel stays disconnected. |
| A weak token half-configures Cloudflare | With a token that cannot manage DNS, every domain shows "No DNS access" and cannot be chosen, the setup API refuses with the fix, and the fake API holds no new tunnel. |
| A failed email step breaks the working URL | When the email rule cannot be created, the public URL, connector and reachability steps are done, the email step says only email was not set up, the Worker is removed and plus addressing restored, and the Public URL card stays Live. |
| The admin has to invent a hostname | Choosing a domain fills `hooks.<domain>`; plus-addressing consent is required before setup can start. |
| Setup claims success without a working path | The run is `done` only after the tunnel, connector, reachability and email steps are done; the Public URL card is Live and Email is On. |
| The credential outlives setup or leaks | After the run Helm reports not connected; neither token appears in status responses, the database or the server log. |
| Sign-in bypasses consent or the relay | Sign-in goes through Cloudflare's authorize endpoint and the relay page, which shows this Helm's origin before continuing; the code is redeemed with PKCE and revoked after setup. |
| A rerun duplicates resources | Running setup again after sign-in shows the existing public URL instead of a hostname field, reports it as already set up, adds email, and leaves one tunnel. |
| A declined or forged sign-in connects anyway | A denied consent returns "Cloudflare sign-in was not completed"; a callback with an unknown state is refused and Cloudflare issues no token. |
| Agents reach setup | Agent tokens get `403` from every guided-setup route. |
| Help is missing or wrong | **Learn more** opens the embedded guide at the guided-setup section; links to other guides open inside the drawer. |

## Email intake failure contract

The email-address workflow (see `EMAIL_ALERT_INTAKE_PLAN.md`) must prove these
failure cases through a real Helm process and browser, using the fake
Cloudflare API and the Worker source Helm actually uploads, executed in Node
against Helm's hooks listener.

| Failure | Observable proof |
| --- | --- |
| Email intake runs without a public path for the Worker | With no active Public URL, setup is refused and the card says why. |
| Setup half-provisions Cloudflare | A zone without Email Routing, an address that is already routed, and a token missing rule access each fail with a message naming the problem; the fake API then holds no Worker, no Helm rule, and plus addressing is unchanged. |
| Plus addressing is turned on silently | With it off, setup without consent is refused; with consent it is on and the intake records that Helm enabled it. |
| The intake secret or webhook tag leaks | Neither appears in API responses after creation, the database, the server log, ticket evidence or the Worker's plain-text bindings; the Worker holds the Helm URL only as a `secret_text` binding. |
| Mail opens tickets for the wrong webhook | Mail to a webhook's address creates a ticket in that webhook's project with the subject as title and the text body as description; an unknown tag is bounced by the Worker with Helm's reason and listed as refused. |
| Repeated or re-delivered mail floods the queue | The same Message-ID twice is `duplicate` with no count change; a new message with the same sender and subject repeats the open ticket; after completion it opens a new ticket. |
| Unreadable or oversized mail is silently lost | Mail without a From header is bounced and listed; mail over 1 MiB is bounced (or forwarded to the fallback) and listed as too large. |
| Helm being down loses mail | With Helm unreachable the Worker retries and then forwards to the fallback address with `X-Helm-Intake-Failed`. |
| Replacing or disabling stops nothing | After **Email address…** replaces a webhook's address, the old address bounces; a disabled webhook's address bounces. |
| Removal leaves Cloudflare resources or breaks delivery paths | The Public URL cannot be removed while email intake is active; removal with a token deletes the Worker and rule; without one the intake shows *Needs cleanup* and **Finish cleanup…** deletes them. |
| Non-admins manage email intake | Agent tokens get `403` from every email-intake management route. |
