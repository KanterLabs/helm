import { expect, test, type APIRequestContext, type APIResponse, type Page } from '@playwright/test';

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

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

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

async function createRelease(
  request: APIRequestContext,
  project: Project,
  name: string
): Promise<Release> {
  return json<Release>(await request.post(`/api/v1/projects/${project.id}/releases`, {
    data: { name },
    headers: mutationHeaders()
  }), `create ${name}`);
}

async function createBug(
  request: APIRequestContext,
  project: Project,
  column: Column,
  title: string,
  releaseId?: string
): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: {
      title,
      kind: 'bug',
      column_id: column.id,
      priority: 'normal',
      bug: { actual_behavior: `Actual behavior for ${title}.` },
      ...(releaseId ? { release_id: releaseId } : {})
    },
    headers: mutationHeaders()
  }), `create ${title}`);
}

async function expectReleaseFormLayout(page: Page): Promise<void> {
  const dialog = page.getByRole('dialog', { name: 'Create a release' });
  const dialogBox = await dialog.boundingBox();
  if (!dialogBox) throw new Error('The release dialog should have a visible bounding box.');

  const viewport = page.viewportSize();
  if (!viewport) throw new Error('The release form test requires a fixed viewport.');
  expect(dialogBox.x).toBeGreaterThanOrEqual(0);
  expect(dialogBox.x + dialogBox.width).toBeLessThanOrEqual(viewport.width);
  expect(dialogBox.y).toBeGreaterThanOrEqual(0);
  expect(dialogBox.y + dialogBox.height).toBeLessThanOrEqual(viewport.height);

  const labels = dialog.locator('.release-form > label');
  await expect(labels).toHaveCount(3);
  for (let index = 0; index < 3; index += 1) {
    const label = labels.nth(index);
    const heading = label.locator('.release-field-label');
    const control = label.locator('input, textarea');
    const headingBox = await heading.boundingBox();
    const controlBox = await control.boundingBox();
    if (!headingBox || !controlBox) throw new Error(`Release field ${index + 1} should be visible.`);
    expect(controlBox.y).toBeGreaterThanOrEqual(headingBox.y + headingBox.height);
    expect(controlBox.width).toBeGreaterThan(dialogBox.width - 80);
    expect(controlBox.x).toBeGreaterThan(dialogBox.x);
    expect(controlBox.x + controlBox.width).toBeLessThan(dialogBox.x + dialogBox.width);
  }

  const hintBox = await dialog.locator('.release-form-hint').boundingBox();
  const actionsBox = await dialog.locator('.modal-actions').boundingBox();
  if (!hintBox || !actionsBox) throw new Error('Release guidance and actions should be visible.');
  expect(actionsBox.y).toBeGreaterThanOrEqual(hintBox.y + hintBox.height);

  const widths = await page.evaluate(() => ({
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth
  }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
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
  await expectReleaseFormLayout(page);
  await page.setViewportSize({ width: 320, height: 800 });
  await expectReleaseFormLayout(page);
  for (const action of ['Cancel', 'Create release']) {
    const actionBox = await createDialog.getByRole('button', { name: action, exact: true }).boundingBox();
    if (!actionBox) throw new Error(`${action} should be visible in the mobile release dialog.`);
    expect(actionBox.width).toBeGreaterThan(240);
    expect(actionBox.height).toBeGreaterThanOrEqual(44);
  }
  await page.setViewportSize({ width: 1280, height: 720 });
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

test('does not inherit a released board filter when creating tasks or bugs', async ({ page, request }) => {
  test.setTimeout(120_000);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `NEW${suffix}`.slice(0, 16), name: `Release entrypoints ${suffix}` },
    headers: mutationHeaders()
  }), 'create release entrypoint project');
  const columnsResponse = await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list release entrypoint columns'
  );
  const columns = Array.isArray(columnsResponse) ? columnsResponse : columnsResponse.data;
  const ready = columns.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the project should have a Ready column').toBeTruthy();

  const release = await createRelease(request, project, `Frozen ${suffix}`);
  let direct = await createTask(request, project, ready!, `Frozen member ${suffix}`, release.id);
  direct = await json<Task>(await request.post(`/api/v1/tasks/${direct.id}/complete`, {
    headers: mutationHeaders(direct.version)
  }), 'complete frozen release task');
  const currentRelease = await json<Release>(await request.get(`/api/v1/releases/${release.id}`), 'refresh frozen release');
  await json<Release>(await request.post(`/api/v1/releases/${release.id}/complete`, {
    headers: mutationHeaders(currentRelease.version)
  }), 'complete frozen release');

  await page.goto(`/p/${project.slug}?release=${release.id}`);
  await expect(page.locator('section.board')).toBeVisible();
  await expect(page.getByLabel('Filter by release')).toHaveValue(release.id);

  const createdTitles = {
    quick: `Quick unassigned ${suffix}`,
    task: `Task unassigned ${suffix}`,
    bug: `Bug unassigned ${suffix}`
  };
  const readyColumn = page.locator('.board-column').filter({
    has: page.getByRole('heading', { name: 'Ready', exact: true })
  });
  await readyColumn.getByRole('button', { name: /Add task$/ }).click();
  await readyColumn.getByRole('textbox', { name: 'New task in Ready' }).fill(createdTitles.quick);
  await readyColumn.getByRole('button', { name: 'Add task', exact: true }).click();

  await page.getByRole('button', { name: 'New task', exact: true }).click();
  const taskDialog = page.getByRole('dialog', { name: 'Create a task' });
  await expect(taskDialog.getByLabel('Target release')).toHaveValue('');
  await taskDialog.getByLabel('Task title').fill(createdTitles.task);
  await taskDialog.getByRole('button', { name: 'Create task', exact: true }).click();
  await expect(taskDialog).toBeHidden();

  await page.getByRole('button', { name: 'Report bug', exact: true }).click();
  const bugDialog = page.getByRole('dialog', { name: 'Report a bug' });
  await expect(bugDialog.getByLabel('Target release')).toHaveValue('');
  await bugDialog.getByLabel('Bug title').fill(createdTitles.bug);
  await bugDialog.getByLabel('Actual behavior').fill('The release filter must not become a new assignment.');
  await bugDialog.getByRole('button', { name: 'Report bug', exact: true }).click();
  await expect(bugDialog).toBeHidden();

  const taskCollection = await json<Collection<Task> | Task[]>(
    await request.get(`/api/v1/projects/${project.id}/tasks?limit=200`),
    'list release entrypoint tasks'
  );
  const createdTasks = Array.isArray(taskCollection) ? taskCollection : taskCollection.data;
  for (const title of Object.values(createdTitles)) {
    const created = createdTasks.find((task) => task.title === title);
    expect(created, `task ${title} should be created`).toBeTruthy();
    expect(created?.release_id || null, `${title} must not be assigned to the released filter`).toBeNull();
  }
});

test('clears invalid cross-project release links before loading board pages', async ({ page, request }) => {
  test.setTimeout(120_000);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `LNK${suffix}`.slice(0, 16), name: `Release link target ${suffix}` },
    headers: mutationHeaders()
  }), 'create release link target project');
  const otherProject = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `OTH${suffix}`.slice(0, 16), name: `Release link source ${suffix}` },
    headers: mutationHeaders()
  }), 'create release link source project');
  const otherRelease = await createRelease(request, otherProject, `Other project release ${suffix}`);

  await page.goto(`/p/${project.slug}?release=${encodeURIComponent(otherRelease.id)}`);
  await expect(page.locator('section.board')).toBeVisible();
  await expect(page.getByLabel('Filter by release')).toHaveValue('all');
  await expect(page).toHaveURL(new RegExp(`/p/${escapeRegExp(project.slug)}/?$`));
  await expect(page.locator('.content-alert.error')).toHaveCount(0);

  await page.goto(`/p/${project.slug}?release=missing-${suffix}`);
  await expect(page.locator('section.board')).toBeVisible();
  await expect(page.getByLabel('Filter by release')).toHaveValue('all');
  await expect(page).toHaveURL(new RegExp(`/p/${escapeRegExp(project.slug)}/?$`));
  await expect(page.locator('.content-alert.error')).toHaveCount(0);
});

test('clear issue filters reloads unfiltered results after a stale release request', async ({ page, request }) => {
  test.setTimeout(120_000);
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `CLR${suffix}`.slice(0, 16), name: `Clear release filter ${suffix}` },
    headers: mutationHeaders()
  }), 'create clear filter project');
  const columnsResponse = await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list clear filter columns'
  );
  const columns = Array.isArray(columnsResponse) ? columnsResponse : columnsResponse.data;
  const ready = columns.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the project should have a Ready column').toBeTruthy();
  const release = await createRelease(request, project, `Filtered release ${suffix}`);
  const releasedBug = await createBug(request, project, ready!, `Release issue ${suffix}`, release.id);
  const unassignedBug = await createBug(request, project, ready!, `Unassigned issue ${suffix}`);

  await page.goto('/issues');
  await expect(page.getByRole('region', { name: 'Issue health' })).toBeVisible();
  const releaseFilter = page.getByLabel('Filter issues by release');
  await expect(releaseFilter.locator(`option[value="${release.id}"]`)).toHaveCount(1);

  let releaseRequestBlocked = false;
  let releaseRequestResolve!: () => void;
  const releaseRequestGate = new Promise<void>((resolve) => { releaseRequestResolve = resolve; });
  await page.route('**/api/v1/issues**', async (route) => {
    const url = new URL(route.request().url());
    if (!releaseRequestBlocked && url.searchParams.get('release_id') === release.id) {
      releaseRequestBlocked = true;
      await releaseRequestGate;
    }
    await route.continue();
  });

  try {
    await releaseFilter.selectOption(release.id);
    await expect.poll(() => releaseRequestBlocked).toBe(true);
    await expect(page.getByRole('button', { name: 'Clear filters', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
    await expect(page).toHaveURL(/\/issues\/?$/);
    await expect(page.getByRole('button', { name: new RegExp(escapeRegExp(unassignedBug.title)) })).toBeVisible();
    releaseRequestResolve();
    await expect(page.getByRole('button', { name: new RegExp(escapeRegExp(unassignedBug.title)) })).toBeVisible();
    expect(releasedBug.id).not.toBe(unassignedBug.id);
  } finally {
    releaseRequestResolve();
    await page.unroute('**/api/v1/issues**');
  }
});
