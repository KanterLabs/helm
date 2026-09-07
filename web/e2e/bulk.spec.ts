import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

type Project = { id: string; key: string; slug: string };
type Column = { id: string; semantic_state: string };
type Task = { id: string; key: string; title: string; priority: string; version: number };

const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

function mutationHeaders(key = `bulk-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return { Origin: e2eOrigin, 'Content-Type': 'application/json', 'Idempotency-Key': key };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

async function createTask(request: APIRequestContext, project: Project, columnID: string, title: string): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: { title, column_id: columnID, priority: 'normal' },
    headers: mutationHeaders()
  }), `create ${title}`);
}

test('selects loaded tasks, preserves filtered selections, and reviews bulk results', async ({ page, request }) => {
  test.setTimeout(90_000);
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `BLK${runID}`.slice(0, 16), name: `Bulk E2E ${runID}` },
    headers: mutationHeaders()
  }), 'create bulk project');
  const columns = await json<Column[] | { data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`), 'list bulk columns');
  const columnList = Array.isArray(columns) ? columns : columns.data;
  const ready = columnList.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the bulk fixture should have a Ready column').toBeTruthy();
  const first = await createTask(request, project, ready!.id, `Bulk first ${runID}`);
  const second = await createTask(request, project, ready!.id, `Bulk second ${runID}`);

  await page.goto(`/p/${project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  await expect(page.getByRole('button', { name: 'Select all loaded filtered tasks', exact: true })).toBeVisible();

  // A filtered selection remains selected when the filter changes.
  await page.getByRole('textbox', { name: 'Search tasks' }).fill(first.title);
  await page.getByRole('button', { name: 'Select all loaded filtered tasks', exact: true }).click();
  await expect(page.getByText('1 selected', { exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: 'Search tasks' }).fill('');
  await expect(page.getByText('1 selected', { exact: true })).toBeVisible();

  const secondCard = board.locator('.task-card').filter({ hasText: second.title });
  await secondCard.getByRole('checkbox', { name: `Select ${second.key}` }).check();
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();

  const dialog = page.getByRole('dialog', { name: 'Review bulk changes' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(first.key, { exact: true })).toBeVisible();
  await expect(dialog.getByText(second.key, { exact: true })).toBeVisible();
  await dialog.getByLabel('Bulk change', { exact: true }).selectOption('priority');
  await dialog.getByLabel('Bulk priority', { exact: true }).selectOption('urgent');
  await dialog.getByRole('button', { name: 'Apply changes to 2 tasks', exact: true }).click();
  await expect(dialog.getByRole('heading', { name: 'Result summary', exact: true })).toBeVisible();
  await expect(dialog.getByRole('status')).toContainText('2 applied · 0 conflicts · 0 skipped');
  await expect(dialog.locator('.bulk-result-status.applied')).toHaveCount(2);

  const firstAfter = await json<Task>(await request.get(`/api/v1/tasks/${first.id}`), 'read first bulk task');
  const secondAfter = await json<Task>(await request.get(`/api/v1/tasks/${second.id}`), 'read second bulk task');
  expect(firstAfter.priority).toBe('urgent');
  expect(secondAfter.priority).toBe('urgent');

  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Select all loaded filtered tasks', exact: true }).click();
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Clear selection', exact: true }).click();
  await expect(page.locator('.bulk-selection-count')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Review bulk changes', exact: true })).toHaveCount(0);
});

test('ignores a delayed bulk response after closing and reopening review', async ({ page, request }) => {
  test.setTimeout(90_000);
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `BLK${runID}`.slice(0, 16), name: `Bulk stale E2E ${runID}` },
    headers: mutationHeaders()
  }), 'create delayed bulk project');
  const columns = await json<Column[] | { data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`), 'list delayed bulk columns');
  const columnList = Array.isArray(columns) ? columns : columns.data;
  const ready = columnList.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the delayed bulk fixture should have a Ready column').toBeTruthy();
  const first = await createTask(request, project, ready!.id, `Delayed first ${runID}`);
  const second = await createTask(request, project, ready!.id, `Delayed second ${runID}`);

  await page.goto(`/p/${project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  const firstCard = board.locator('.task-card').filter({ hasText: first.title });
  const secondCard = board.locator('.task-card').filter({ hasText: second.title });
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toBeVisible();
  await firstCard.getByRole('checkbox', { name: `Select ${first.key}` }).check();
  await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();
  const firstDialog = page.getByRole('dialog', { name: 'Review bulk changes' });
  await expect(firstDialog).toBeVisible();
  await firstDialog.getByLabel('Bulk priority', { exact: true }).selectOption('urgent');

  let bulkReleased = false;
  let releaseBulk!: () => void;
  const bulkHeld = new Promise<void>((resolve) => { releaseBulk = resolve; });
  let finishBulk!: () => void;
  const bulkFinished = new Promise<void>((resolve) => { finishBulk = resolve; });
  const bulkRoute = `**/api/v1/projects/${project.id}/tasks/bulk`;
  await page.route(bulkRoute, async (route) => {
    await bulkHeld;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        mode: 'partial',
        status: 'partial',
        requested: 1,
        applied: 1,
        skipped: 0,
        conflicts: 0,
        results: [{ reference: first.key, task_id: first.id, status: 'applied', version: first.version + 1 }]
      })
    });
    finishBulk();
  });

  try {
    const bulkRequest = page.waitForRequest(
      (browserRequest) => browserRequest.method() === 'POST' && browserRequest.url().endsWith(`/api/v1/projects/${project.id}/tasks/bulk`),
      { timeout: 30_000 }
    );
    await firstDialog.getByRole('button', { name: 'Apply changes to 1 tasks', exact: true }).click();
    await bulkRequest;

    // Closing invalidates the in-flight request. The new review deliberately
    // contains a different task, so an old response would be obvious in its
    // result summary or by changing the current selection.
    await firstDialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(firstDialog).toHaveCount(0);
    await page.getByRole('button', { name: 'Clear selection', exact: true }).click();
    await secondCard.getByRole('checkbox', { name: `Select ${second.key}` }).check();
    await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();
    const secondDialog = page.getByRole('dialog', { name: 'Review bulk changes' });
    await expect(secondDialog).toBeVisible();
    await expect(secondDialog.getByText(second.key, { exact: true })).toBeVisible();
    await expect(secondDialog.getByText(first.key, { exact: true })).toHaveCount(0);

    bulkReleased = true;
    releaseBulk();
    await bulkFinished;
    await expect(secondDialog.getByRole('heading', { name: 'Result summary', exact: true })).toHaveCount(0);
    await expect(secondDialog.getByRole('button', { name: 'Apply changes to 1 tasks', exact: true })).toBeVisible();
  } finally {
    if (!bulkReleased) releaseBulk();
    await page.unroute(bulkRoute);
  }
});
