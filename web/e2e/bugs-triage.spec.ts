import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

// Real-flow failures: duplicate tickets on classification, lost comments or keys
// after filing, ordinary tasks routed to Bugs, missing severity accepted, stale
// edits overwriting another actor, or a bug disappearing between Tickets/Board.
// Screenshots and captured synthetic API responses are attached for replay.
type Task = { id: string; key: string; kind: string; project_id: string; column_id: string; version: number;
  priority: string; bug?: { actual_behavior: string; reproduction_steps: string; severity?: string };
  ticket?: { status: string } };
type Column = { id: string; name: string; semantic_state: string };
const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
const headers = (version?: number) => ({ Origin: origin, 'Idempotency-Key': crypto.randomUUID(),
  ...(version === undefined ? {} : { 'If-Match': `"v${version}"` }) });
async function json<T>(response: APIResponse): Promise<T> {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}
async function task(request: APIRequestContext, id: string): Promise<Task> {
  return json(await request.get(`/api/v1/tasks/${id}`));
}

test('ticket classification, project Bugs intake, and triage share one record', async ({ page, request }, info) => {
  const suffix = crypto.randomUUID().replaceAll('-', '').slice(0, 9).toUpperCase();
  const project = await json<{ id: string; key: string; name: string }>(await request.post('/api/v1/projects', {
    data: { name: `Bug triage ${suffix}`, key: `BT${suffix}` }, headers: headers()
  }));
  const columns = (await json<{ data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns`))).data;
  const bugs = columns.find(c => c.name === 'Bugs')!;
  const ready = columns.find(c => c.semantic_state === 'ready')!;
  expect(bugs?.semantic_state).toBe('backlog');
  const ticket = await json<Task>(await request.post('/api/v1/tickets', {
    data: { title: `Missing attachment ${suffix}`, description: 'A screenshot disappears after reopening the task.' }, headers: headers()
  }));
  await json(await request.post(`/api/v1/tasks/${ticket.id}/comments`, {
    data: { body: 'Original reporter evidence stays attached.' }, headers: headers()
  }));
  await page.goto(`/tickets?ticket=${ticket.key}`);
  const detail = page.locator('.ticket-detail');
  await detail.getByRole('button', { name: 'Classify as bug', exact: true }).click();
  const form = detail.getByRole('form', { name: 'Classify ticket as bug' });
  await form.getByLabel('Actual behavior', { exact: true }).fill('A screenshot disappears after reopening the task.');
  await form.getByLabel('Reproduction steps', { exact: true }).fill('Attach a screenshot, close the drawer, then reopen.');
  await form.getByRole('button', { name: 'Save as bug', exact: true }).click();
  await expect(detail.getByRole('heading', { name: 'Bug triage', exact: true })).toBeVisible();
  const classified = await task(request, ticket.id);
  expect(classified.id).toBe(ticket.id);
  expect(classified.key).toBe(ticket.key);
  expect(classified.kind).toBe('bug');
  expect(classified.ticket?.status).toBe('needs_triage');
  await detail.getByLabel('File to project').selectOption(project.id);
  await expect(detail.locator('.task-key').first()).toHaveText(new RegExp(`^${project.key}-`));
  const filed = await task(request, ticket.id);
  expect(filed.column_id).toBe(bugs.id);
  expect((await task(request, ticket.key)).id).toBe(ticket.id);
  await expect(detail).toContainText('Original reporter evidence stays attached.');
  const accept = detail.getByRole('button', { name: 'Triage & move to Ready', exact: true });
  await expect(accept).toBeDisabled();
  await detail.getByLabel('Bug severity', { exact: true }).selectOption('s2');
  await detail.getByLabel('Bug priority', { exact: true }).selectOption('high');
  await info.attach('ticket-bug-triage.png', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(accept).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await info.attach('ticket-bug-mobile.png', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
  await accept.click();
  await expect(detail.locator('[data-ticket-status]')).toHaveText('Ready');
  const accepted = await task(request, ticket.id);
  expect(accepted.column_id).toBe(ready.id);
  expect(accepted.bug?.severity).toBe('s2');
  expect(accepted.priority).toBe('high');
  expect(accepted.bug?.reproduction_steps).toBe(classified.bug?.reproduction_steps);
  await page.reload();
  await expect(detail.getByRole('heading', { name: 'Bug triage' })).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole('button', { name: project.name, exact: true }).first().click();
  const readyLane = page.getByRole('article', { name: 'Ready column', exact: true });
  await expect(readyLane.locator(`[data-task-trigger="${ticket.id}"]`)).toBeVisible();
  await expect(page.getByRole('article', { name: 'Bugs column', exact: true }).locator(`[data-task-trigger="${ticket.id}"]`)).toHaveCount(0);
  await info.attach('bug-ready-board.png', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
  await info.attach('same-record.json', { contentType: 'application/json', body: Buffer.from(JSON.stringify({ ticket, classified, filed, accepted }, null, 2)) });
});

test('bug intake defaults to Bugs, ordinary tasks stay Backlog, stale triage cannot overwrite', async ({ request }, info) => {
  const suffix = crypto.randomUUID().replaceAll('-', '').slice(0, 9).toUpperCase();
  const project = await json<{ id: string }>(await request.post('/api/v1/projects', {
    data: { name: `Routing ${suffix}`, key: `BR${suffix}` }, headers: headers()
  }));
  const columns = (await json<{ data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns`))).data;
  const bug = await json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: { kind: 'bug', title: 'Synthetic routing bug', bug: { actual_behavior: 'Test failure' } }, headers: headers()
  }));
  const ordinary = await json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: { title: 'Ordinary task' }, headers: headers()
  }));
  expect(bug.column_id).toBe(columns.find(c => c.name === 'Bugs')?.id);
  expect(ordinary.column_id).toBe(columns.find(c => c.name === 'Backlog')?.id);
  const missingSeverity = await request.post(`/api/v1/tasks/${bug.id}/triage`, {
    data: { column_id: columns.find(c => c.semantic_state === 'ready')?.id }, headers: headers(bug.version)
  });
  expect(missingSeverity.status()).toBe(400);
  const changed = await json<Task>(await request.patch(`/api/v1/tasks/${bug.id}`, { data: { priority: 'urgent' }, headers: headers(bug.version) }));
  const stale = await request.post(`/api/v1/tasks/${bug.id}/triage`, {
    data: { severity: 's4', priority: 'low' }, headers: headers(bug.version)
  });
  expect(stale.status()).toBe(409);
  const current = await task(request, bug.id);
  expect(current.version).toBe(changed.version);
  expect(current.priority).toBe('urgent');
  expect(current.bug?.severity).toBeFalsy();
  await info.attach('routing-and-stale-triage.json', { contentType: 'application/json', body: Buffer.from(JSON.stringify({ bug, ordinary, current, staleStatus: stale.status() }, null, 2)) });
});
