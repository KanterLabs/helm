import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

// Proves the "Tickets workspace failure contract" in docs/E2E_TESTING.md
// against a real Helm process, database and browser.

type Project = { id: string; key: string; slug: string };
type Column = { id: string; semantic_state: string; position: number };
type Collection<T> = { data: T[]; next_cursor?: string | null };
type Ticket = { origin: string; status: string; created_at: string };
type Task = {
  id: string;
  key: string;
  kind: string;
  title: string;
  version: number;
  column_id: string;
  assignee?: string;
  claimed_by?: string;
  agent_work?: unknown;
  completed_at?: string;
  ticket?: Ticket;
};
type TicketPage = { data: Task[]; next_cursor: string; counts: Record<string, number> };

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const origin = new URL(baseURL).origin;
const secret = process.env.HELM_E2E_COOLIFY_SECRET || '';
const intakeProject = process.env.HELM_E2E_COOLIFY_PROJECT || 'COOLIFYE2E';

function headers(version?: number): Record<string, string> {
  return {
    Origin: origin,
    'Content-Type': 'application/json',
    'Idempotency-Key': `tickets-e2e-${crypto.randomUUID()}`,
    ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
  };
}

function items<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}: ${response.ok() ? '' : await response.text()}`).toBeTruthy();
  return await response.json() as T;
}

async function createProject(request: APIRequestContext, key: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', { data: { key, name: `Tickets ${key}` }, headers: headers() }), `create ${key}`);
}

async function columns(request: APIRequestContext, project: Project): Promise<Column[]> {
  return items(await json<Collection<Column> | Column[]>(await request.get(`/api/v1/projects/${project.id}/columns?limit=50`), 'columns'));
}

async function createTicket(request: APIRequestContext, project: Project, title: string, assignee: string | null, priority = 'normal'): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tickets`, {
    data: { title, description: `Details for ${title}`, priority, assignee },
    headers: headers()
  }), `create ticket ${title}`);
}

async function getTask(request: APIRequestContext, id: string): Promise<Task> {
  return json<Task>(await request.get(`/api/v1/tasks/${id}`), `get ${id}`);
}

async function moveTo(request: APIRequestContext, task: Task, column: Column): Promise<Task> {
  return json<Task>(await request.patch(`/api/v1/tasks/${task.id}`, { data: { column_id: column.id }, headers: headers(task.version) }), `move ${task.key}`);
}

async function ticketPage(request: APIRequestContext, query: string): Promise<TicketPage> {
  return json<TicketPage>(await request.get(`/api/v1/tickets?${query}`), `tickets ${query}`);
}

test('Tickets keep durable membership, server counts and guarded human triage', async ({ page, request }, testInfo) => {
  test.setTimeout(150_000);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'auth status');
  expect(status.mode).toBe('disabled');
  const me = await json<{ id: string }>(await request.get('/api/v1/auth/me'), 'me');
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
  const project = await createProject(request, `TK${runID}`.slice(0, 16));
  const bulk = await createProject(request, `TB${runID}`.slice(0, 16));
  const projectColumns = await columns(request, project);
  const column = (state: string, cols = projectColumns) => cols.find((item) => item.semantic_state === state) as Column;

  // Membership: ordinary tasks never become tickets; tickets survive relabeling.
  const plain = await json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, { data: { title: `Plain task ${runID}` }, headers: headers() }), 'plain task');
  const manual = await createTicket(request, project, `Renew certificate ${runID}`, me.id, 'high');
  expect(manual).toMatchObject({ kind: 'task', assignee: me.id, column_id: column('backlog').id, ticket: { origin: 'manual', status: 'needs_triage' } });
  expect(manual.claimed_by).toBeUndefined();
  expect(plain.ticket).toBeUndefined();
  await json(await request.post(`/api/v1/projects/${project.id}/labels`, { data: { name: `ops-${runID}` }, headers: headers() }), 'label');
  const labeled = await json<Task>(await request.patch(`/api/v1/tasks/${manual.id}`, { data: { labels: [`ops-${runID}`] }, headers: headers(manual.version) }), 'label ticket');
  const unlabeled = await json<Task>(await request.patch(`/api/v1/tasks/${manual.id}`, { data: { labels: [] }, headers: headers(labeled.version) }), 'unlabel ticket');
  const membership = await ticketPage(request, `project=${project.key}`);
  expect(membership.data.map((task) => task.id)).toEqual([manual.id]);
  expect(unlabeled.ticket?.origin).toBe('manual');
  expect((await ticketPage(request, `project=${project.key}&q=${encodeURIComponent(manual.key)}`)).data.map((task) => task.key)).toEqual([manual.key]);
  expect((await request.post(`/api/v1/projects/${project.id}/tickets`, { data: { title: 'bad', kind: 'bug' }, headers: headers() })).status()).toBe(400);

  // Alert intake creates the same kind of ticket.
  expect(secret.length, 'run.sh must provide HELM_E2E_COOLIFY_SECRET').toBeGreaterThanOrEqual(32);
  const projectList = items(await json<Collection<Project> | Project[]>(await request.get('/api/v1/projects?limit=200'), 'projects'));
  if (!projectList.some((item) => item.key === intakeProject)) await createProject(request, intakeProject);
  const delivered = await json<{ alerts: { task_id: string }[] }>(await request.post(`/api/v1/intake/coolify/${secret}`, {
    data: { event: 'traefik_version_outdated', servers: [{ name: `tickets-${runID}`, uuid: `tk${runID}`, current_version: 'v3.6.1', latest_version: 'v3.6.4' }] },
    headers: { 'Content-Type': 'application/json' }
  }), 'deliver alert');
  const alertTicket = await getTask(request, delivered.alerts[0].task_id);
  expect(alertTicket).toMatchObject({ kind: 'task', ticket: { origin: 'alert', status: 'needs_triage' } });
  expect(alertTicket.claimed_by).toBeUndefined();

  // Server counts cover full membership, not the loaded page; claims never change status or "mine".
  const bulkColumns = await columns(request, bulk);
  const created: Task[] = [];
  for (let index = 0; index < 7; index += 1) created.push(await createTicket(request, bulk, `Bulk ticket ${index} ${runID}`, index < 4 ? me.id : null));
  await moveTo(request, created[0], column('ready', bulkColumns));
  await moveTo(request, created[1], column('active', bulkColumns));
  await json(await request.post(`/api/v1/tasks/${created[2].id}/block`, { data: { reason: 'Vendor reply' }, headers: headers(created[2].version) }), 'block');
  await json(await request.post(`/api/v1/tasks/${created[3].id}/complete`, { data: {}, headers: headers(created[3].version) }), 'complete');
  const claimed = await json<Task>(await request.post(`/api/v1/tasks/${created[5].id}/claim`, { data: {}, headers: headers(created[5].version) }), 'claim');
  expect(claimed.claimed_by).toBe(me.id);
  const firstPage = await ticketPage(request, `project=${bulk.key}&limit=3`);
  expect(firstPage.counts).toEqual({ open: 6, mine: 3, needs_triage: 3, ready: 1, in_progress: 1, waiting: 1, completed: 1 });
  expect(firstPage.data).toHaveLength(3);
  expect(firstPage.next_cursor).toBeTruthy();
  const secondPage = await ticketPage(request, `project=${bulk.key}&limit=3&cursor=${firstPage.next_cursor}`);
  const allKeys = [...firstPage.data, ...secondPage.data].map((task) => task.key);
  expect(new Set(allKeys).size).toBe(6);
  expect(secondPage.next_cursor).toBe('');
  expect((await ticketPage(request, `project=${bulk.key}&queue=mine`)).data.map((task) => task.id).sort()).toEqual([created[0].id, created[1].id, created[2].id].sort());
  expect((await getTask(request, created[5].id)).ticket?.status).toBe('needs_triage');

  // Browser: open the queue with URL-backed filters and triage the manual ticket.
  await page.goto(`/tickets?project=${project.key}`);
  await expect(page.getByRole('heading', { name: 'Tickets', exact: true })).toBeVisible();
  await expect(page.locator('[data-queue-count="open"]')).toHaveText('1');
  const row = page.locator(`[data-ticket-key="${manual.key}"]`);
  await expect(row).toBeVisible();
  await expect(page.locator(`[data-ticket-key="${plain.key}"]`)).toHaveCount(0);
  await row.click();
  const detail = page.locator('.ticket-detail');
  await expect(detail.getByRole('heading', { name: manual.title })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`ticket=${manual.key}`));
  await detail.getByRole('button', { name: 'Accept to Ready' }).click();
  await expect(detail.locator('[data-ticket-status]')).toHaveText('Ready');
  expect((await getTask(request, manual.id)).column_id).toBe(column('ready').id);

  // A newer edit elsewhere makes the next action conflict and apply nothing.
  const current = await getTask(request, manual.id);
  await json(await request.patch(`/api/v1/tasks/${manual.id}`, { data: { title: `Renew certificate (edited) ${runID}` }, headers: headers(current.version) }), 'concurrent edit');
  await detail.getByRole('button', { name: 'Start', exact: true }).click();
  await expect(detail.locator('.ticket-action-status')).toContainText('changed elsewhere');
  await expect(detail.getByRole('heading', { name: `Renew certificate (edited) ${runID}` })).toBeVisible();
  expect((await getTask(request, manual.id)).column_id).toBe(column('ready').id);
  await detail.getByRole('button', { name: 'Start', exact: true }).click();
  await expect(detail.locator('[data-ticket-status]')).toHaveText('In progress');
  const started = await getTask(request, manual.id);
  expect(started.claimed_by).toBeUndefined();
  expect(started.agent_work).toBeUndefined();

  // Waiting records the human reason; Resume returns to Ready without a claim.
  await detail.getByRole('button', { name: 'Waiting…' }).click();
  await detail.getByLabel('What is this waiting on?').fill(`Maintenance window ${runID}`);
  await detail.getByRole('button', { name: 'Mark waiting' }).click();
  await expect(detail.locator('[data-ticket-status]')).toHaveText('Waiting');
  const timeline = await json<{ data: { change?: { payload?: Record<string, unknown> } }[] }>(await request.get(`/api/v1/tasks/${manual.id}/timeline?limit=20`), 'timeline');
  expect(JSON.stringify(timeline.data)).toContain(`Maintenance window ${runID}`);
  await detail.getByRole('button', { name: 'Resume' }).click();
  await expect(detail.locator('[data-ticket-status]')).toHaveText('Ready');
  expect((await getTask(request, manual.id)).claimed_by).toBeUndefined();

  // Notes post as ordinary comments and survive reload with filters restored.
  await detail.getByPlaceholder('Add an internal note').fill(`Checked the renewal job ${runID}`);
  await detail.getByRole('button', { name: 'Add note' }).click();
  await expect(detail.locator('.ticket-note-list')).toContainText(`Checked the renewal job ${runID}`);
  await page.getByRole('tab', { name: /Ready/ }).click();
  await expect(page).toHaveURL(/queue=ready/);
  await page.reload();
  await expect(page.getByRole('tab', { name: /Ready/ })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByLabel('Project filter')).toHaveValue(project.key);
  await expect(detail.getByRole('heading', { name: `Renew certificate (edited) ${runID}` })).toBeVisible();
  await expect(detail.locator('.ticket-note-list')).toContainText(`Checked the renewal job ${runID}`);
  await testInfo.attach('tickets-desktop.png', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });

  // Completing removes it from the open queue with an explicit notice.
  await page.getByRole('tab', { name: /All open/ }).click();
  await detail.getByRole('button', { name: '✓ Complete' }).click();
  await expect(page.locator('.ticket-moved')).toContainText(`moved to Completed`);
  await expect(page.locator(`[data-ticket-key="${manual.key}"]`)).toHaveCount(0);

  // A delayed detail response for ticket A never renders into selected ticket B.
  await page.goto(`/tickets?project=${bulk.key}`);
  const [slow, fast] = [created[4], created[6]];
  await page.route(`**/api/v1/tasks/${slow.key}`, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.fulfill({ response: await route.fetch() });
  });
  await page.locator(`[data-ticket-key="${slow.key}"]`).click();
  await page.locator(`[data-ticket-key="${fast.key}"]`).click();
  await expect(detail.getByRole('heading', { name: fast.title })).toBeVisible();
  await page.waitForTimeout(2000);
  await expect(detail.getByRole('heading', { name: fast.title })).toBeVisible();
  await page.unroute(`**/api/v1/tasks/${slow.key}`);

  // Keyboard: arrows move rows, Enter opens, "/" searches only outside inputs.
  const rows = page.locator('[data-ticket-key]');
  await rows.first().focus();
  await page.keyboard.press('ArrowDown');
  const secondKey = await rows.nth(1).getAttribute('data-ticket-key');
  await expect(rows.nth(1)).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(detail.locator('h2')).toBeFocused();
  await expect(detail.locator('.task-key')).toHaveText(secondKey as string);
  const note = detail.getByPlaceholder('Add an internal note');
  await note.focus();
  await page.keyboard.type('a/b');
  await expect(note).toHaveValue('a/b');
  await note.fill('');
  await page.locator('h1#tickets-heading').click();
  await page.keyboard.press('/');
  await expect(page.getByPlaceholder(/Search tickets/)).toBeFocused();

  // Phone: list and detail are separate screens and Back restores the list.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/tickets?project=${bulk.key}`);
  await expect(page.locator('.mobile-nav').getByRole('button', { name: 'Tickets', exact: true })).toBeVisible();
  await page.locator(`[data-ticket-key="${fast.key}"]`).click();
  await expect(detail.getByRole('heading', { name: fast.title })).toBeVisible();
  await expect(page.locator('.tickets-list-pane')).toBeHidden();
  await testInfo.attach('tickets-mobile-detail.png', { contentType: 'image/png', body: await page.screenshot() });
  await page.goBack();
  await expect(page.locator('.tickets-list-pane')).toBeVisible();
  await expect(page.locator(`[data-ticket-key="${fast.key}"]`)).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await testInfo.attach('tickets-mobile-list.png', { contentType: 'image/png', body: await page.screenshot() });
});
