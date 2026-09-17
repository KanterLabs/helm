import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; name: string; semantic_state: string; position: number };
type Task = {
  id: string;
  key: string;
  title: string;
  description?: string | null;
  project_id: string;
  column_id: string;
  version: number;
};
type Collection<T> = { data: T[]; next_cursor?: string | null };

const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

function mutationHeaders(key = `routing-drafts-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return {
    Origin: e2eOrigin,
    'Content-Type': 'application/json',
    'Idempotency-Key': key
  };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

async function createProject(request: APIRequestContext, suffix: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `RR${suffix}`.slice(0, 16),
      name: `Routing regression ${suffix}`,
      description: 'Routing and draft regression fixture.'
    },
    headers: mutationHeaders()
  }), 'create routing regression project');
}

async function createTask(request: APIRequestContext, project: Project, column: Column, suffix: string): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: {
      title: `Persisted task title ${suffix}`,
      description: `Persisted task description ${suffix}`,
      column_id: column.id,
      priority: 'normal'
    },
    headers: mutationHeaders()
  }), 'create routing regression task');
}

async function readyColumn(request: APIRequestContext, project: Project): Promise<Column> {
  const payload = await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list routing regression columns'
  );
  const column = collectionData(payload).find((item) => item.semantic_state === 'ready');
  expect(column, 'the routing regression project should have a ready column').toBeTruthy();
  return column as Column;
}

function suffix(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
}

test.describe('routing and draft regressions', () => {
  test('returns from project Progress to the board when browser Back reaches the root route', async ({ page, request }) => {
    const project = await createProject(request, suffix());

    await page.addInitScript((slug) => localStorage.setItem('helm.last-project', slug), project.slug);
    await page.goto('/');
    await expect(page.getByRole('heading', { name: project.name, exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Progress', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/roadmap/?$`));
    await expect(page.getByRole('heading', { name: `${project.name} progress`, exact: true })).toBeVisible();

    await page.goBack();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole('heading', { name: project.name, exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Progress', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: `${project.name} progress`, exact: true })).toHaveCount(0);
  });

  test('canonicalizes an unknown project slug without rendering another project under that URL', async ({ page, request }) => {
    const first = await createProject(request, suffix());
    const second = await createProject(request, suffix());

    // Establish a remembered project so the safe root fallback is observable
    // and cannot accidentally choose the first project for the invalid URL.
    await page.goto(`/p/${second.slug}`);
    await expect(page.getByRole('heading', { name: second.name, exact: true })).toBeVisible();
    await page.evaluate((slug) => localStorage.setItem('helm.last-project', slug), second.slug);

    await page.goto('/p/does-not-exist');
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole('heading', { name: second.name, exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: first.name, exact: true })).toHaveCount(0);
    await expect(page.locator('section.board')).toHaveAttribute('aria-label', `${second.name} board`);
  });

  test('warns before reload and keeps dirty title/description drafts out of persisted API state', async ({ page, request }) => {
    const project = await createProject(request, suffix());
    const task = await createTask(request, project, await readyColumn(request, project), suffix());

    await page.goto(`/p/${project.slug}`);
    const card = page.locator('.task-card').filter({ hasText: task.key });
    await card.locator('[data-task-trigger]').click();
    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();

    const draftTitle = `Unsaved browser title ${task.key}`;
    const draftDescription = `Unsaved browser description ${task.key}`;
    await drawer.getByLabel('Task title').fill(draftTitle);
    await drawer.locator('textarea.description-input').fill(draftDescription);
    await expect(drawer.locator('.drawer-save-bar')).toContainText('Unsaved changes');

    const dialogPromise = page.waitForEvent('dialog');
    const reloadPromise = page.reload({ waitUntil: 'domcontentloaded' });
    const dialog = await dialogPromise;
    expect(dialog.type()).toBe('beforeunload');
    await dialog.accept();
    await reloadPromise;

    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/?$`));
    await expect(page.locator('.task-drawer')).toHaveCount(0);
    const persisted = await json<Task>(await request.get(`/api/v1/tasks/${task.id}`), 'reload dirty-draft task');
    expect(persisted.title).toBe(task.title);
    expect(persisted.description).toBe(task.description);
  });
});
