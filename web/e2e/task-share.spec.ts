import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; semantic_state: string };
type Task = { id: string; key: string; title: string; project_id: string; column_id: string; version: number };
type Collection<T> = { data: T[]; next_cursor?: string | null };
const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

function mutationHeaders(key = `task-share-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return {
    'Content-Type': 'application/json',
    Origin: e2eOrigin,
    'Idempotency-Key': key
  };
}

async function createProject(request: APIRequestContext, suffix: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `SHARE${suffix}`.slice(0, 16),
      name: `Task share E2E ${suffix}`,
      description: 'Task share acceptance fixture.'
    },
    headers: mutationHeaders()
  }), 'create task share project');
}

test('copies stable task links and keys without changing the current route', async ({ page, request }) => {
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await createProject(request, suffix);
  const columns = await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list task share columns'
  );
  const columnList = Array.isArray(columns) ? columns : columns.data;
  const ready = columnList.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the project should have a ready column').toBeTruthy();
  const task = await json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: { title: `Share me ${suffix}`, column_id: ready?.id, priority: 'normal' },
    headers: mutationHeaders()
  }), 'create task share task');

  await page.addInitScript(() => {
    Object.defineProperty(window, '__taskShareClipboardMode', { value: 'allow', writable: true });
    Object.defineProperty(window, '__taskShareCopied', { value: '', writable: true });
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (value: string) => {
          const state = window as Window & { __taskShareClipboardMode?: string; __taskShareCopied?: string };
          if (state.__taskShareClipboardMode === 'deny') throw new DOMException('Not allowed', 'NotAllowedError');
          state.__taskShareCopied = value;
        }
      }
    });
  });
  await page.goto(`/p/${encodeURIComponent(project.slug)}?filter=secret#not-a-task`);
  const card = page.locator('.task-card').filter({ hasText: task.key });
  await expect(card.locator('[data-task-trigger]')).toBeVisible();
  await card.locator('[data-task-trigger]').click();

  const drawer = page.locator('.task-drawer');
  await expect(drawer).toBeVisible();
  await drawer.getByRole('button', { name: 'Copy task link', exact: true }).click();
  await expect(drawer.getByRole('status')).toContainText('Task link copied');
  const copied = await page.evaluate(() => (window as Window & { __taskShareCopied?: string }).__taskShareCopied);
  const origin = new URL(page.url()).origin;
  expect(copied).toBe(`${origin}/p/${encodeURIComponent(project.slug)}/tasks/${encodeURIComponent(task.key)}`);
  expect(new URL(page.url()).search).toBe('?filter=secret');
  expect(new URL(page.url()).hash).toBe('#not-a-task');

  await page.evaluate(() => {
    (window as Window & { __taskShareClipboardMode?: string }).__taskShareClipboardMode = 'deny';
  });
  await drawer.getByRole('button', { name: 'Copy task key', exact: true }).click();
  await expect(drawer.getByRole('alert')).toContainText('Couldn’t copy task key');
  await expect(drawer.locator('[data-task-share-fallback="key"]')).toHaveValue(task.key);
});
