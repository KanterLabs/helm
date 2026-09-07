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

test('selects visible tasks, preserves filtered selections, and reviews bulk results', async ({ page, request }) => {
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
  await expect(page.getByRole('button', { name: 'Select visible', exact: true })).toBeVisible();

  // A filtered selection remains selected when the filter changes.
  await page.getByRole('textbox', { name: 'Search tasks' }).fill(first.title);
  await page.getByRole('button', { name: 'Select visible', exact: true }).click();
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
  await page.getByRole('button', { name: 'Select visible', exact: true }).click();
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Clear selection', exact: true }).click();
  await expect(page.locator('.bulk-selection-count')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Review bulk changes', exact: true })).toHaveCount(0);
});
