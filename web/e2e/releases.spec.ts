import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; name: string; semantic_state: string };
type Release = {
  id: string;
  project_id: string;
  name: string;
  status: 'planned' | 'released';
  version: number;
};
type Task = {
  id: string;
  key: string;
  title: string;
  project_id: string;
  column_id: string;
  version: number;
  release_id?: string | null;
};
type Collection<T> = { data: T[]; next_cursor?: string | null };

const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

function mutationHeaders(version?: number): Record<string, string> {
  return {
    Origin: e2eOrigin,
    'Content-Type': 'application/json',
    'Idempotency-Key': `releases-e2e-${crypto.randomUUID()}`,
    ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
  };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

async function createTask(
  request: APIRequestContext,
  project: Project,
  column: Column,
  title: string,
  releaseId?: string
): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: {
      title,
      column_id: column.id,
      priority: 'normal',
      ...(releaseId ? { release_id: releaseId } : {})
    },
    headers: mutationHeaders()
  }), `create ${title}`);
}

test('plans, filters, completes, and reopens a dependency-aware release', async ({ page, request }) => {
  test.setTimeout(120_000);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const suffix = runID.slice(-8);
  const releaseName = `Autumn launch ${suffix}`;
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `REL${suffix}`.slice(0, 16),
      name: `Release E2E ${suffix}`,
      description: 'Release planning browser acceptance fixture.'
    },
    headers: mutationHeaders()
  }), 'create release project');
  const columnsResponse = await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list release project columns'
  );
  const columns = Array.isArray(columnsResponse) ? columnsResponse : columnsResponse.data;
  const ready = columns.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the project should have a Ready column').toBeTruthy();

  await page.goto(`/p/${project.slug}`);
  await expect(page.locator('section.board')).toBeVisible();
  await page.getByRole('button', { name: /Releases/ }).click();
  await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/releases`));

  await page.getByRole('button', { name: /New release|Create a release/ }).first().click();
  const createDialog = page.getByRole('dialog', { name: 'Create a release' });
  await createDialog.getByLabel('Release name').fill(releaseName);
  await createDialog.getByLabel('Description').fill('Everything required for the autumn launch.');
  await createDialog.getByLabel('Target date').fill('2026-10-15');
  await createDialog.getByRole('button', { name: 'Create release' }).click();
  await expect(page.getByText(`${releaseName} created.`)).toBeVisible();

  const releaseCollection = await json<Collection<Release>>(
    await request.get(`/api/v1/projects/${project.id}/releases?limit=20`),
    'list created releases'
  );
  const release = releaseCollection.data.find((item) => item.name === releaseName);
  expect(release, 'the release created in the browser should be returned by the API').toBeTruthy();

  let prerequisite = await createTask(request, project, ready!, `Prepare rollout ${suffix}`);
  let direct = await createTask(request, project, ready!, `Ship launch ${suffix}`, release!.id);
  direct = await json<Task>(await request.post(`/api/v1/tasks/${direct.id}/dependencies`, {
    data: { prerequisite: prerequisite.id },
    headers: mutationHeaders(direct.version)
  }), 'link the release prerequisite');

  await page.getByRole('button', { name: 'Refresh release work queue' }).click();
  const queue = page.getByRole('region', { name: 'Work queue' });
  await expect(queue.getByText(direct.key, { exact: true })).toBeVisible();
  await expect(queue.getByText('Direct member', { exact: true })).toBeVisible();
  await expect(queue.getByText(prerequisite.key, { exact: true })).toBeVisible();
  await expect(queue.getByText('Prerequisite', { exact: true })).toBeVisible();
  await expect(page.getByText('Not ready', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Open filtered board' }).click();
  await expect(page.getByLabel('Filter by release')).toHaveValue(release!.id);
  const board = page.locator('section.board');
  await expect(board.getByText(direct.title)).toBeVisible();
  await expect(board.getByText(prerequisite.title)).toHaveCount(0);
  await expect(board.locator('.task-card').filter({ hasText: direct.title }).locator('.release-chip'))
    .toHaveAttribute('title', `Target release: ${releaseName}`);

  prerequisite = await json<Task>(await request.post(`/api/v1/tasks/${prerequisite.id}/complete`, {
    headers: mutationHeaders(prerequisite.version)
  }), 'complete the release prerequisite');
  await json<Task>(await request.post(`/api/v1/tasks/${direct.id}/complete`, {
    headers: mutationHeaders(direct.version)
  }), 'complete the direct release task');

  await page.getByRole('button', { name: /Releases/ }).click();
  await page.locator('.release-page-heading').getByRole('button', { name: /Refresh/ }).click();
  await expect(page.getByText('Ready', { exact: true })).toBeVisible();

  const releaseRow = page.locator(`[data-release-id="${release!.id}"]`);
  await releaseRow.getByRole('button', { name: 'Complete', exact: true }).click();
  const completeDialog = page.getByRole('alertdialog', { name: `Release ${releaseName}?` });
  await completeDialog.getByRole('button', { name: 'Complete release' }).click();
  await expect(releaseRow.getByText('Released', { exact: true })).toBeVisible();

  await releaseRow.getByRole('button', { name: 'Reopen', exact: true }).click();
  const reopenDialog = page.getByRole('dialog', { name: `Reopen ${releaseName}` });
  await reopenDialog.getByLabel('Reason').fill('One final validation item needs to join the release.');
  await reopenDialog.getByRole('button', { name: 'Reopen release' }).click();
  await expect(releaseRow.getByText('Planned', { exact: true })).toBeVisible();
});
