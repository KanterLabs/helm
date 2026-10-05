import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

// Proves the "Coolify alert intake failure contract" in docs/E2E_TESTING.md
// against a real Helm process. test/e2e/run.sh starts the server with a
// disposable per-run intake secret. Tickets land in the ticket queue
// (docs/TICKET_QUEUE_PLAN.md); run.sh's HELM_COOLIFY_PROJECT is deprecated
// and must be ignored.

type Project = { id: string; key: string; name: string; slug: string; system_kind?: string };
type Column = { id: string; semantic_state: string };
type Collection<T> = { data: T[]; next_cursor?: string | null };
type AlertSource = {
  integration: string;
  alert_type: string;
  resource_name: string;
  resource_id: string;
  evidence: Record<string, string>;
  occurrence_count: number;
  previous_task_id?: string;
  previous_task_key?: string;
};
type Task = {
  id: string;
  key: string;
  kind: string;
  title: string;
  description: string;
  priority: string;
  column_id: string;
  version: number;
  assignee?: string;
  claimed_by?: string;
  agent_work?: unknown;
  completed_at?: string;
  alert_source?: AlertSource;
};
type AlertResult = {
  event: string;
  disposition: string;
  alerts: { disposition: string; resource_name: string; task_id?: string; task_key?: string; occurrence_count: number }[];
};
type Notification = { id: string; task_id?: string | null };
type Server = Record<string, string>;

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const e2eOrigin = new URL(baseURL).origin;
const secret = process.env.HELM_E2E_COOLIFY_SECRET || '';
const deprecatedProjectKey = process.env.HELM_E2E_COOLIFY_PROJECT || 'COOLIFYE2E';
const intakePath = `/api/v1/intake/coolify/${secret}`;

function headers(version?: number): Record<string, string> {
  return {
    Origin: e2eOrigin,
    'Content-Type': 'application/json',
    'Idempotency-Key': `coolify-e2e-${crypto.randomUUID()}`,
    ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
  };
}

function items<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

function traefik(...servers: Server[]): Record<string, unknown> {
  return { success: false, message: 'Traefik proxy outdated', event: 'traefik_version_outdated', affected_servers_count: servers.length, servers };
}

function server(name: string, uuid: string, latest = 'v3.1.5'): Server {
  return { name, uuid, current_version: 'v3.1.2', latest_version: latest, update_type: 'patch_update' };
}

// Coolify posts JSON with no credentials beyond the URL; mirror that exactly.
async function deliver(request: APIRequestContext, body: unknown, path = intakePath): Promise<APIResponse> {
  const response = await request.post(path, { data: body, headers: { 'Content-Type': 'application/json' } });
  expect(await response.text(), 'intake responses must never echo the secret').not.toContain(secret);
  return response;
}

async function recorded(request: APIRequestContext, body: unknown): Promise<AlertResult> {
  return json<AlertResult>(await deliver(request, body), 'Coolify delivery');
}

async function getTask(request: APIRequestContext, id: string): Promise<Task> {
  return json<Task>(await request.get(`/api/v1/tasks/${id}`), `GET task ${id}`);
}

// The ticket queue exists once any intake path has been set up; creating
// and disabling a throwaway webhook makes sure of that without a ticket.
async function ticketQueue(request: APIRequestContext): Promise<Project> {
  let queue = (await json<{ queue?: { project_id: string } }>(await request.get('/api/v1/tickets?limit=1'), 'tickets')).queue;
  if (!queue) {
    const created = await json<{ webhook: { id: string } }>(await request.post('/api/v1/ticket-webhooks', { data: { name: 'Queue bootstrap' }, headers: headers() }), 'bootstrap webhook');
    expect((await request.delete(`/api/v1/ticket-webhooks/${created.webhook.id}`, { headers: headers() })).status()).toBe(204);
    queue = (await json<{ queue?: { project_id: string } }>(await request.get('/api/v1/tickets?limit=1'), 'tickets')).queue;
  }
  expect(queue, 'the ticket queue exists').toBeTruthy();
  return json<Project>(await request.get(`/api/v1/projects/${queue?.project_id}`), 'ticket queue project');
}

async function projectTasks(request: APIRequestContext, project: Project): Promise<Task[]> {
  return items(await json<Collection<Task> | Task[]>(await request.get(`/api/v1/projects/${project.id}/tasks?limit=200`), 'list project tasks'));
}

async function tasksNamed(request: APIRequestContext, project: Project, name: string): Promise<Task[]> {
  return (await projectTasks(request, project)).filter((task) => task.title === `Review Traefik update on ${name}`);
}

async function notificationsFor(request: APIRequestContext, taskId: string): Promise<Notification[]> {
  return items(await json<Collection<Notification> | Notification[]>(await request.get('/api/v1/notifications?limit=100'), 'list notifications')).filter((item) => item.task_id === taskId);
}

test('Coolify webhooks create one assigned Backlog task per condition and preserve human work', async ({ page, request }, testInfo) => {
  test.setTimeout(120_000);
  expect(secret.length, 'run.sh must provide HELM_E2E_COOLIFY_SECRET').toBeGreaterThanOrEqual(32);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'auth status');
  expect(status.mode).toBe('disabled');
  const me = await json<{ id: string }>(await request.get('/api/v1/auth/me'), 'current actor');

  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`;
  const name1 = `e2e-host-${runID}-a`;
  const uuid1 = `e2e${runID}a`;

  // A wrong secret is indistinguishable from an unknown route.
  const unknown = await request.post('/api/v1/no-such-route', { data: {}, headers: headers() });
  const wrong = await deliver(request, traefik(server(name1, uuid1)), `/api/v1/intake/coolify/${'x'.repeat(40)}`);
  expect(wrong.status()).toBe(404);
  expect(await wrong.json()).toEqual(await unknown.json());

  // Tickets go to the ticket queue whatever HELM_COOLIFY_PROJECT says.
  const project = await ticketQueue(request);
  expect(project.system_kind).toBe('tickets');
  expect(project.key).not.toBe(deprecatedProjectKey);
  expect(await tasksNamed(request, project, name1)).toHaveLength(0);

  const preferences = await json<{ assignments: boolean }>(await request.get('/api/v1/notification-preferences'), 'preferences');
  if (!preferences.assignments) {
    await json(await request.patch('/api/v1/notification-preferences', { data: { assignments: true }, headers: headers() }), 'enable assignments');
  }
  const columns = items(await json<Collection<Column> | Column[]>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`), 'columns'));
  const backlog = columns.find((column) => column.semantic_state === 'backlog');
  expect(backlog).toBeTruthy();

  // Concurrent identical deliveries create exactly one task.
  const burst = await Promise.all(Array.from({ length: 5 }, () => recorded(request, traefik(server(name1, uuid1)))));
  const dispositions = burst.map((result) => result.alerts[0].disposition).sort();
  expect(dispositions).toEqual(['created', 'repeated', 'repeated', 'repeated', 'repeated']);
  expect(new Set(burst.map((result) => result.alerts[0].task_id)).size).toBe(1);
  expect(Math.max(...burst.map((result) => result.alerts[0].occurrence_count))).toBe(5);
  const created = await getTask(request, burst[0].alerts[0].task_id as string);
  expect(await tasksNamed(request, project, name1)).toHaveLength(1);

  // The task is ordinary human work in Backlog, never agent-claimed.
  expect(created.kind).toBe('task');
  expect(created.column_id).toBe(backlog?.id);
  expect(created.assignee).toBe(me.id);
  expect(created.claimed_by).toBeUndefined();
  expect(created.agent_work).toBeUndefined();
  expect(created.priority).toBe('normal');
  expect(created.alert_source).toMatchObject({
    integration: 'coolify',
    alert_type: 'traefik_version_outdated',
    resource_name: name1,
    resource_id: uuid1,
    evidence: { current_version: 'v3.1.2', latest_version: 'v3.1.5', update_type: 'patch_update' },
    occurrence_count: 5
  });
  expect(await notificationsFor(request, created.id)).toHaveLength(1);

  // A repeat after human triage changes neither fields nor version.
  const edited = await json<Task>(await request.patch(`/api/v1/tasks/${created.id}`, {
    data: { title: `Upgrade Traefik on ${name1}`, priority: 'high', description: 'Human triage notes.' },
    headers: headers(created.version)
  }), 'human edit');
  const repeat = await recorded(request, traefik(server(name1, uuid1)));
  expect(repeat.alerts[0]).toMatchObject({ disposition: 'repeated', task_id: created.id, occurrence_count: 6 });
  const afterRepeat = await getTask(request, created.id);
  expect(afterRepeat).toMatchObject({ title: edited.title, priority: 'high', description: 'Human triage notes.', version: edited.version });
  expect(afterRepeat.alert_source?.occurrence_count).toBe(6);
  expect(await notificationsFor(request, created.id)).toHaveLength(1);

  // Completed work is retained, not reopened or recreated.
  const completed = await json<Task>(await request.post(`/api/v1/tasks/${created.id}/complete`, { data: {}, headers: headers(afterRepeat.version) }), 'complete task');
  expect(completed.completed_at).toBeTruthy();
  const retained = await recorded(request, traefik(server(name1, uuid1)));
  expect(retained.alerts[0]).toMatchObject({ disposition: 'retained', task_id: created.id, occurrence_count: 7 });
  const afterRetained = await getTask(request, created.id);
  expect(afterRetained.completed_at).toBe(completed.completed_at);
  expect(afterRetained.version).toBe(completed.version);
  expect((await projectTasks(request, project)).filter((task) => task.alert_source?.resource_id === uuid1)).toHaveLength(1);

  // A new offered version is new work linked to the earlier task.
  const upgraded = await recorded(request, traefik(server(name1, uuid1, 'v3.1.6')));
  expect(upgraded.alerts[0].disposition).toBe('created');
  expect(upgraded.alerts[0].task_key).not.toBe(created.key);
  const followUp = await getTask(request, upgraded.alerts[0].task_id as string);
  expect(followUp.alert_source).toMatchObject({ previous_task_id: created.id, previous_task_key: created.key, occurrence_count: 1 });
  expect(followUp.description).toContain(`Follows earlier alert ${created.key}.`);

  // Multi-server deliveries commit all-or-nothing.
  const name2 = `e2e-host-${runID}-b`;
  const name3 = `e2e-host-${runID}-c`;
  const partial = await deliver(request, traefik(server(name2, `e2e${runID}b`), { name: name3, current_version: 'v3.1.2', latest_version: 'v3.1.5' }));
  expect(partial.status()).toBe(400);
  expect(await tasksNamed(request, project, name2)).toHaveLength(0);
  const pair = await recorded(request, traefik(server(name2, `e2e${runID}b`), server(name3, `e2e${runID}c`)));
  expect(pair.alerts.map((alert) => alert.disposition)).toEqual(['created', 'created']);
  expect(await tasksNamed(request, project, name2)).toHaveLength(1);
  expect(await tasksNamed(request, project, name3)).toHaveLength(1);

  // Test and unsupported events, malformed and oversized bodies create no work.
  const before = (await projectTasks(request, project)).length;
  expect(await recorded(request, { success: true, event: 'test', message: 'This is a test webhook notification from Coolify.' }))
    .toEqual({ event: 'test', disposition: 'ignored', alerts: [] });
  expect(await recorded(request, { success: false, event: 'server_unreachable', message: 'Server unreachable', server_name: name1, server_uuid: uuid1 }))
    .toEqual({ event: 'server_unreachable', disposition: 'unsupported', alerts: [] });
  const malformed = await request.post(intakePath, { data: '{"event": "traefik_version_outdated",', headers: { 'Content-Type': 'application/json' } });
  expect(malformed.status()).toBe(400);
  const oversized = await request.post(intakePath, { data: JSON.stringify({ event: 'test', pad: 'x'.repeat(70 * 1024) }), headers: { 'Content-Type': 'application/json' } });
  expect(oversized.status()).toBe(413);
  expect((await projectTasks(request, project)).length).toBe(before);

  await testInfo.attach('intake-responses.json', {
    contentType: 'application/json',
    body: JSON.stringify({ burst: dispositions, repeat, retained, upgraded, pair }, null, 2)
  });

  // The human sees the evidence and the repeat history in the drawer.
  await page.goto(`/p/${encodeURIComponent(project.slug)}/tasks/${encodeURIComponent(followUp.key)}`);
  const panel = page.locator('.alert-source-section');
  await expect(panel.getByRole('heading', { name: 'Alert source' })).toBeVisible();
  await expect(panel).toContainText(name1);
  await expect(panel).toContainText('v3.1.6');
  await expect(panel).toContainText('Once');
  await panel.getByRole('button', { name: created.key }).click();
  await expect(page).toHaveURL(new RegExp(`/tasks/${created.key}$`));
  await expect(panel).toContainText('7 times');
  await expect(panel).toContainText('v3.1.5');
  await testInfo.attach('alert-source-panel.png', { contentType: 'image/png', body: await page.locator('.task-drawer').screenshot() });
  await page.getByRole('tab', { name: 'Activity' }).click();
  await expect(page.locator('#drawer-activity-panel')).toContainText('received a repeat alert');
  await testInfo.attach('alert-activity.png', { contentType: 'image/png', body: await page.locator('.task-drawer').screenshot() });
});
