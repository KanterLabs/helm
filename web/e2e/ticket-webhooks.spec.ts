import { readFileSync } from 'node:fs';
import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

// Proves the "Ticket webhooks failure contract" in docs/E2E_TESTING.md
// against a real Helm process, database and browser.

type Project = { id: string; key: string; slug: string; system_kind?: string };
type Column = { id: string; semantic_state: string };
type Collection<T> = { data: T[] };
type Task = {
  id: string;
  key: string;
  title: string;
  priority: string;
  version: number;
  column_id: string;
  assignee?: string;
  claimed_by?: string;
  ticket?: { origin: string; status: string };
  alert_source?: { integration: string; alert_type: string; resource_name: string; evidence: Record<string, string>; occurrence_count: number; previous_task_key?: string };
};
type HookResult = { disposition: string; occurrence_count: number; ticket: { id: string; key: string; url: string } };
type Webhook = { id: string; name: string; format: string; delivery_count: number; secret_hint: string };

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const origin = new URL(baseURL).origin;

function headers(version?: number): Record<string, string> {
  return {
    Origin: origin,
    'Content-Type': 'application/json',
    'Idempotency-Key': `webhooks-e2e-${crypto.randomUUID()}`,
    ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
  };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}: ${response.ok() ? '' : await response.text()}`).toBeTruthy();
  return await response.json() as T;
}

// Outside apps send plain JSON with no Helm credentials or Origin header.
async function post(request: APIRequestContext, url: string, body: unknown): Promise<APIResponse> {
  return request.post(new URL(url).pathname, { data: body, headers: { 'Content-Type': 'application/json' } });
}

async function getTask(request: APIRequestContext, id: string): Promise<Task> {
  return json<Task>(await request.get(`/api/v1/tasks/${id}`), `task ${id}`);
}

async function projectTaskCount(request: APIRequestContext, project: Project): Promise<number> {
  const page = await json<Collection<Task>>(await request.get(`/api/v1/projects/${project.id}/tasks?limit=200`), 'tasks');
  return page.data.length;
}

test('Admins create readable ticket webhooks that outside apps can post to', async ({ page, request }, testInfo) => {
  test.setTimeout(120_000);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'auth status');
  expect(status.mode).toBe('disabled');
  const me = await json<{ id: string }>(await request.get('/api/v1/auth/me'), 'me');
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', { data: { key: `WH${runID}`.slice(0, 16), name: `Webhooks ${runID}` }, headers: headers() }), 'project');
  const secrets: string[] = [];

  // Agent bearer tokens cannot manage webhooks.
  const agent = await json<{ id: string }>(await request.post('/api/v1/agents', { data: { name: `Webhook probe ${runID}`, project_ids: [project.id] }, headers: headers() }), 'agent');
  const token = await json<{ token: string }>(await request.post(`/api/v1/agents/${agent.id}/tokens`, { data: { name: 'probe', scopes: ['tasks:read', 'tasks:write'], project_ids: [project.id] }, headers: { Origin: origin, 'Content-Type': 'application/json' } }), 'token');
  expect((await request.get('/api/v1/ticket-webhooks', { headers: { Authorization: `Bearer ${token.token}` } })).status()).toBe(403);
  expect((await request.post('/api/v1/ticket-webhooks', { data: { name: 'x', project: project.key }, headers: { Authorization: `Bearer ${token.token}`, 'Content-Type': 'application/json' } })).status()).toBe(403);

  // An admin creates a webhook in Tickets → Connect apps; the URL shows once.
  await page.goto(`/tickets?project=${project.key}`);
  await page.getByRole('button', { name: /Connect apps/ }).click();
  const panel = page.locator('.ticket-integrations');
  await expect(panel.getByRole('heading', { name: 'How to send a ticket' })).toBeVisible();
  await panel.getByLabel('Webhook name', { exact: true }).fill(`Grafana alerts ${runID}`);
  // No project to choose: every ticket lands in the ticket queue.
  await expect(panel.getByLabel('Webhook project')).toHaveCount(0);
  const createResponse = page.waitForResponse((response) => response.url().endsWith('/api/v1/ticket-webhooks') && response.request().method() === 'POST');
  await panel.getByRole('button', { name: 'Create webhook URL' }).click();
  expect((await createResponse).headers()['cache-control']).toBe('no-store');
  const reveal = panel.locator('[data-webhook-secret-reveal]');
  await expect(reveal).toContainText('will not be shown again');
  const url = await reveal.locator('input').inputValue();
  expect(url).toMatch(new RegExp(`^${origin}/api/v1/hooks/tickets/hk_[A-Za-z0-9_-]{43}$`));
  secrets.push(url);
  await expect(panel.locator('[data-webhook-curl]')).toContainText(url);
  await testInfo.attach('connect-apps.png', { contentType: 'image/png', body: await panel.screenshot({ mask: [reveal, panel.locator('[data-webhook-curl]')] }) });
  await reveal.getByRole('button', { name: 'I saved it' }).click();
  await expect(reveal).toHaveCount(0);
  await expect(panel.locator('[data-webhook-curl]')).not.toContainText('hk_');
  const listed = await request.get('/api/v1/ticket-webhooks');
  const listedText = await listed.text();
  expect(listedText).not.toContain(url.split('/').pop() as string);
  const hook = (JSON.parse(listedText) as Collection<Webhook>).data.find((item) => item.name === `Grafana alerts ${runID}`) as Webhook;
  expect(hook).toMatchObject({ format: 'generic', delivery_count: 0 });
  expect(url.endsWith(hook.secret_hint)).toBeTruthy();

  // Creating the webhook created the ticket queue (docs/TICKET_QUEUE_PLAN.md).
  const queueRef = (await json<{ queue?: { project_id: string } }>(await request.get('/api/v1/tickets?limit=1'), 'tickets')).queue;
  const queue = await json<Project>(await request.get(`/api/v1/projects/${queueRef?.project_id}`), 'ticket queue');
  expect(queue.system_kind).toBe('tickets');
  const columns = (await json<Collection<Column>>(await request.get(`/api/v1/projects/${queue.id}/columns?limit=20`), 'columns')).data;
  const backlog = columns.find((column) => column.semantic_state === 'backlog') as Column;

  // A curl-shaped post opens a ticket in Needs triage, assigned as configured.
  const payload = { title: `Disk almost full on db-${runID}`, description: 'Volume at **93%**.', priority: 'high', dedupe_key: `db-${runID}:disk`, source: 'grafana', url: 'https://grafana.example.com/d/disk', fields: { host: `db-${runID}`, usage: '93%', critical: true } };
  const createdResponse = await post(request, url, payload);
  expect(createdResponse.status()).toBe(201);
  const created = await createdResponse.json() as HookResult;
  expect(created).toMatchObject({ disposition: 'created', occurrence_count: 1 });
  expect(created.ticket.url).toBe(`${origin}/p/${queue.slug}/tasks/${created.ticket.key}`);
  const ticket = await getTask(request, created.ticket.id);
  expect(ticket).toMatchObject({ title: payload.title, priority: 'high', column_id: backlog.id, assignee: me.id, ticket: { origin: 'alert', status: 'needs_triage' } });
  expect(ticket.claimed_by).toBeUndefined();
  expect(ticket.alert_source).toMatchObject({ alert_type: 'grafana', resource_name: `Grafana alerts ${runID}`, evidence: { host: `db-${runID}`, usage: '93%', critical: 'true', link: 'https://grafana.example.com/d/disk' } });

  // Invalid payloads name the field and create nothing.
  const before = await projectTaskCount(request, queue);
  for (const [body, field] of [[{ description: 'no title' }, 'title'], [{ title: 'x', priority: 'p1' }, 'priority'], [{ tittle: 'typo' }, 'tittle'], [{ title: 'x', fields: { host: { nested: true } } }, 'fields.host']] as const) {
    const response = await post(request, url, body);
    expect(response.status()).toBe(400);
    const error = (await response.json()).error;
    expect(error.code).toBe('invalid_ticket');
    expect(error.details.field).toBe(field);
  }
  expect(await projectTaskCount(request, queue)).toBe(before);

  // Same dedupe_key while open only repeats; after completion it opens linked work.
  const repeatResponse = await post(request, url, payload);
  expect(repeatResponse.status()).toBe(200);
  expect(await repeatResponse.json()).toMatchObject({ disposition: 'repeated', occurrence_count: 2, ticket: { key: created.ticket.key } });
  expect((await getTask(request, ticket.id)).version).toBe(ticket.version);
  await json(await request.post(`/api/v1/tasks/${ticket.id}/complete`, { data: {}, headers: headers(ticket.version) }), 'complete');
  const reopened = await post(request, url, payload);
  expect(reopened.status()).toBe(201);
  const followUp = await reopened.json() as HookResult;
  expect(followUp.ticket.key).not.toBe(created.ticket.key);
  expect((await getTask(request, followUp.ticket.id)).alert_source?.previous_task_key).toBe(created.ticket.key);
  const without = await (await post(request, url, { title: `One-off ${runID}` })).json() as HookResult;
  const withoutAgain = await (await post(request, url, { title: `One-off ${runID}` })).json() as HookResult;
  expect(withoutAgain.ticket.key).not.toBe(without.ticket.key);

  // The human sees readable evidence on the ticket.
  await page.goto(`/tickets?project=${queue.key}&ticket=${followUp.ticket.key}`);
  const source = page.locator('.ticket-detail .alert-source-section');
  await expect(source).toContainText('Webhook');
  await expect(source).toContainText('grafana');
  await expect(source).toContainText(`db-${runID}`);
  await expect(source).toContainText('https://grafana.example.com/d/disk');

  // Rotation kills the old URL; disabling kills the new one.
  await page.getByRole('button', { name: /Connect apps/ }).click();
  const row = panel.locator(`[data-webhook-name="Grafana alerts ${runID}"]`);
  await expect(row.locator('[data-webhook-deliveries]')).toHaveText('5');
  page.once('dialog', (dialog) => void dialog.accept());
  await row.locator('.row-menu > summary').click();
  await row.getByRole('button', { name: 'Rotate' }).click();
  const rotated = await panel.locator('[data-webhook-secret-reveal] input').inputValue();
  expect(rotated).not.toBe(url);
  secrets.push(rotated);
  expect((await post(request, url, { title: 'old url' })).status()).toBe(404);
  expect((await post(request, rotated, { title: `After rotate ${runID}` })).status()).toBe(201);
  await panel.getByRole('button', { name: 'I saved it' }).click();
  page.once('dialog', (dialog) => void dialog.accept());
  await row.locator('.row-menu > summary').click();
  await row.getByRole('button', { name: 'Disable' }).click();
  await expect(row).toContainText('disabled');
  expect((await post(request, rotated, { title: 'disabled' })).status()).toBe(404);
  await testInfo.attach('connect-apps-list.png', { contentType: 'image/png', body: await panel.screenshot() });

  // A Coolify-format webhook behaves like the built-in Coolify intake.
  const coolify = await json<{ url: string }>(await request.post('/api/v1/ticket-webhooks', { data: { name: `Coolify ${runID}`, format: 'coolify', assignee: 'me' }, headers: { Origin: origin, 'Content-Type': 'application/json' } }), 'coolify webhook');
  secrets.push(coolify.url);
  const coolifyResult = await json<{ disposition: string; alerts: { disposition: string; task_id: string }[] }>(await post(request, coolify.url, {
    event: 'traefik_version_outdated', message: 'Traefik proxy outdated', servers: [{ name: `coolify-${runID}`, uuid: `wh${runID}`, current_version: '3.6.25', latest_version: '3.7.13', update_type: 'minor_upgrade', upgrade_target: 'v3.7' }]
  }), 'coolify post');
  expect(coolifyResult).toMatchObject({ disposition: 'recorded', alerts: [{ disposition: 'created' }] });
  expect((await getTask(request, coolifyResult.alerts[0].task_id))).toMatchObject({ ticket: { origin: 'alert' }, alert_source: { alert_type: 'traefik_version_outdated' } });

  // No webhook secret reaches the retained server log.
  const logPath = process.env.HELM_E2E_SERVER_LOG;
  expect(logPath, 'run.sh must provide HELM_E2E_SERVER_LOG').toBeTruthy();
  const log = readFileSync(logPath as string, 'utf8');
  for (const secretURL of secrets) expect(log).not.toContain(secretURL.split('/').pop() as string);
});
