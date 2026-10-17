import { expect, test, type APIRequestContext } from '@playwright/test';

// Failure contract: a board URL loses criteria; drawer navigation loses its
// origin; Back leaves a stale drawer mounted; edited saved views execute their
// old filters; New search retains criteria; Save as overwrites the original.
async function fixture(request: APIRequestContext) {
  const suffix = Date.now().toString(36).toUpperCase();
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const post = async (path: string, data: unknown) => {
    const response = await request.post(path, { data, headers: { Origin: origin, 'Idempotency-Key': crypto.randomUUID() } });
    expect(response.ok()).toBeTruthy();
    return response.json();
  };
  const project = await post('/api/v1/projects', { key: `NV${suffix}`.slice(0, 16), name: `Navigation ${suffix}` });
  const columns = (await (await request.get(`/api/v1/projects/${project.id}/columns`)).json()).data;
  const ready = columns.find((c: { semantic_state: string }) => c.semantic_state === 'ready');
  const task = await post(`/api/v1/projects/${project.id}/tasks`, { title: `Navigation needle ${suffix}`, priority: 'high', column_id: ready.id });
  return { project, task, suffix };
}

test('restores board criteria and task Activity through reload and history', async ({ page, request }, testInfo) => {
  const { project, task } = await fixture(request);
  const board = `/p/${project.slug}?q=Navigation&priority=high&sort=title&order=desc`;
  await page.goto(board);
  await expect(page.getByLabel('Search tasks', { exact: true })).toHaveValue('Navigation');
  await page.locator('.task-card').filter({ hasText: task.key }).locator('[data-task-trigger]').click();
  await expect(page).toHaveURL(/\/tasks\//);
  await page.getByRole('tab', { name: 'Activity', exact: true }).click();
  const activityURL = page.url();
  await page.reload();
  await expect(page.getByRole('tab', { name: 'Activity', exact: true })).toHaveAttribute('aria-selected', 'true');
  await page.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page).toHaveURL(board);
  await expect(page.getByLabel('Search tasks', { exact: true })).toHaveValue('Navigation');
  await page.goBack();
  await expect(page).toHaveURL(activityURL);
  await expect(page.locator('.task-drawer')).toBeVisible();
  await testInfo.attach('restored-activity', { body: await page.screenshot(), contentType: 'image/png' });
});

test('keeps saved search edits explicit and makes New search reset every criterion', async ({ page, request }, testInfo) => {
  const { task, suffix } = await fixture(request);
  await page.goto('/search');
  await page.getByLabel('Search all tasks').fill(task.title);
  await page.getByLabel('Search all tasks').press('Enter');
  await page.getByLabel('Saved view name').fill(`Original ${suffix}`);
  await page.getByRole('button', { name: 'Save view', exact: true }).click();
  await expect(page.getByRole('heading', { name: `Original ${suffix}`, exact: true })).toBeVisible();
  const originalId = new URL(page.url()).searchParams.get('view');
  await page.getByLabel('Filter search by priority').selectOption('low');
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No matching tasks' })).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Filter search by priority')).toHaveValue('low');
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
  await page.getByLabel('Saved view name').fill(`Copy ${suffix}`);
  await page.getByRole('button', { name: 'Save as', exact: true }).click();
  await expect(page.getByRole('heading', { name: `Copy ${suffix}`, exact: true })).toBeVisible();
  expect(new URL(page.url()).searchParams.get('view')).not.toBe(originalId);
  await page.getByLabel('Saved view name').fill(`Renamed ${suffix}`);
  await page.getByRole('button', { name: 'Rename', exact: true }).click();
  await expect(page.getByRole('heading', { name: `Renamed ${suffix}`, exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'New search', exact: true }).click();
  await expect(page.getByLabel('Search all tasks')).toHaveValue('');
  await expect(page.getByLabel('Filter search by priority')).toHaveValue('all');
  await expect(page.getByLabel('Filter search by state')).toHaveValue('all');
  await expect(page.getByLabel('Sort search results')).toHaveValue('updated_at');
  await expect(page).toHaveURL('/search');
  const response = await request.get('/api/v1/views');
  expect(response.ok()).toBeTruthy();
  const views = (await response.json()).data;
  expect(views.find((view: { id: string }) => view.id === originalId).filters.q).toBe(task.title);
  expect(views.find((view: { id: string }) => view.id === originalId).filters.priority).toBeUndefined();
  await testInfo.attach('new-search-reset', { body: await page.screenshot(), contentType: 'image/png' });
});

test('clears editable saved-view criteria while preserving hidden scope and every sort term', async ({ page, request }, testInfo) => {
  // Failure contract recorded before this test: hidden project scope broadens;
  // clearing priority still applies the saved priority; reload restores stale
  // criteria; Update drops scope or secondary sorts; clear accepts scope keys.
  const { project, task, suffix } = await fixture(request);
  const outside = await fixture(request);
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const post = async (path: string, data: unknown) => {
    const response = await request.post(path, { data, headers: { Origin: origin, 'Idempotency-Key': crypto.randomUUID() } });
    expect(response.ok()).toBeTruthy();
    return response.json();
  };
  // Equal titles make the second sort observable after priority is cleared.
  const low = await post(`/api/v1/projects/${project.id}/tasks`, {
    title: task.title, priority: 'low', column_id: task.column_id
  });
  const sort = [{ field: 'title', direction: 'asc' }, { field: 'key', direction: 'desc' }];
  const saved = await post('/api/v1/views', {
    name: `Scoped navigation ${suffix}`, filters: { project_ids: [project.id], priority: 'high' }, sort, shared: false
  });

  await page.goto(`/search?view=${encodeURIComponent(saved.id)}`);
  await expect(page.getByRole('heading', { name: saved.name, exact: true })).toBeVisible();
  await expect(page.getByLabel('Filter search by priority')).toHaveValue('high');
  await expect(page.locator('.search-result-row .task-key')).toHaveText([task.key]);
  await expect(page.getByText('Saved', { exact: true })).toBeVisible();

  const clearedResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.pathname === '/api/v1/search' && url.searchParams.get('view') === saved.id
      && url.searchParams.get('clear')?.split(',').includes('priority') === true;
  });
  await page.getByLabel('Filter search by priority').selectOption('all');
  const cleared = await clearedResponse;
  expect(cleared.ok()).toBeTruthy();
  const clearedResults = (await cleared.json()).data as Array<{ id: string; key: string; project_id: string; priority: string }>;
  expect(clearedResults.map((result) => result.id).sort()).toEqual([task.id, low.id].sort());
  expect(clearedResults.every((result) => result.project_id === project.id)).toBeTruthy();
  expect(clearedResults.some((result) => result.id === outside.task.id)).toBeFalsy();
  await expect(page.locator('.search-result-row .task-key')).toHaveText([low.key, task.key]);
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
  expect(new URL(page.url()).searchParams.get('criteria')).toBe('1');
  expect(new URL(page.url()).searchParams.get('priority')).toBeNull();
  expect(new URL(page.url()).searchParams.get('sort')).toBe('title:asc,key:desc');

  await page.reload();
  await expect(page.getByLabel('Filter search by priority')).toHaveValue('all');
  await expect(page.getByLabel('Sort search results')).toHaveValue('title');
  await expect(page.locator('.search-result-row .task-key')).toHaveText([low.key, task.key]);
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Update view', exact: true }).click();
  await expect(page.getByText('Saved', { exact: true })).toBeVisible();
  const persistedResponse = await request.get(`/api/v1/views/${saved.id}`);
  expect(persistedResponse.ok()).toBeTruthy();
  const persisted = await persistedResponse.json();
  expect(persisted.filters.project_ids).toEqual([project.id]);
  expect(persisted.filters.priority).toBeUndefined();
  expect(persisted.sort).toEqual(sort);
  await expect(page.locator('.search-result-row .task-key')).toHaveText([low.key, task.key]);

  const invalid = await request.get(`/api/v1/search?view=${encodeURIComponent(saved.id)}&clear=project_ids`);
  expect(invalid.status()).toBe(400);
  const executed = await request.get(`/api/v1/views/${saved.id}/search`);
  expect(executed.ok()).toBeTruthy();
  const finalResults = (await executed.json()).data as Array<{ id: string; key: string; project_id: string; priority: string }>;
  expect(finalResults.map((result) => result.id)).toEqual([low.id, task.id]);
  expect(finalResults.every((result) => result.project_id === project.id)).toBeTruthy();
  await testInfo.attach('saved-view-scope-and-sort.json', {
    body: JSON.stringify({ filters: persisted.filters, sort: persisted.sort, results: finalResults.map(({ id, key, project_id, priority }) => ({ id, key, project_id, priority })), invalidClearStatus: invalid.status() }, null, 2),
    contentType: 'application/json'
  });
  await testInfo.attach('saved-view-scope-and-sort.png', { body: await page.screenshot(), contentType: 'image/png' });
});

test('restores project Progress after reloading task Activity and traversing history', async ({ page, request }, testInfo) => {
  const { project, task, suffix } = await fixture(request);
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const mutate = async (path: string, data: unknown, version: number) => {
    const response = await request.post(path, {
      data,
      headers: { Origin: origin, 'Idempotency-Key': crypto.randomUUID(), 'If-Match': `"v${version}"` }
    });
    expect(response.ok()).toBeTruthy();
    return response.json();
  };
  const claimed = await mutate(`/api/v1/tasks/${task.id}/claim`, { lease_seconds: 3600 }, task.version);
  const summary = `Progress navigation milestone ${suffix}`;
  await mutate(`/api/v1/tasks/${task.id}/progress`, {
    operation_id: `navigation/${suffix}`, state: 'working', phase: 'Verifying navigation', summary,
    next_action: 'Review browser history', checkpoint_refs: ['navigation'], checkpoint_completed: 0, checkpoint_total: 1
  }, claimed.version);

  const progressURL = `/p/${project.slug}/roadmap`;
  const activityURL = `/p/${project.slug}/tasks/${task.key}?view=activity`;
  const drawer = page.locator('.task-drawer');
  const progressHeading = page.getByRole('heading', { name: `${project.name} progress`, exact: true });
  const liveTask = page.locator('.roadmap-live-work').getByRole('button', { name: `Open ${task.key}: ${task.title} activity`, exact: true });
  const visits: string[] = [];
  const recordVisit = () => visits.push(new URL(page.url()).pathname + new URL(page.url()).search);
  const expectProgress = async () => {
    await expect(page).toHaveURL(progressURL);
    await expect(drawer).toBeHidden();
    await expect(progressHeading).toBeVisible();
    await expect(liveTask).toBeVisible();
    recordVisit();
  };
  const expectActivity = async () => {
    await expect(page).toHaveURL(activityURL);
    await expect(drawer).toBeVisible();
    await expect(drawer.getByRole('tab', { name: 'Activity', exact: true })).toHaveAttribute('aria-selected', 'true');
    await expect(drawer.locator('.task-timeline-item[data-kind="agent_progress"]').filter({ hasText: summary })).toBeVisible();
    recordVisit();
  };

  await page.goto(`/p/${project.slug}`);
  await page.getByRole('button', { name: 'Progress', exact: true }).click();
  await expectProgress();
  await liveTask.click();
  await expectActivity();
  await page.reload();
  await expectActivity();
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expectProgress();
  await page.goBack();
  await expectActivity();
  await page.goBack();
  await expectProgress();
  await page.goForward();
  await expectActivity();
  await page.goForward();
  await expectProgress();
  expect(visits).toEqual([progressURL, activityURL, activityURL, progressURL, activityURL, progressURL, activityURL, progressURL]);
  await testInfo.attach('project-progress-history.json', { body: JSON.stringify({ visits, task: task.key, milestone: summary }, null, 2), contentType: 'application/json' });
  await testInfo.attach('project-progress-restored.png', { body: await page.screenshot(), contentType: 'image/png' });
});

test('overlays legacy saved-view URL criteria on omitted saved query and state', async ({ page, request }, testInfo) => {
  const { project, task, suffix } = await fixture(request);
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const post = async (path: string, data: unknown) => {
    const response = await request.post(path, { data, headers: { Origin: origin, 'Idempotency-Key': crypto.randomUUID() } });
    expect(response.ok()).toBeTruthy();
    return response.json();
  };
  const columnsResponse = await request.get(`/api/v1/projects/${project.id}/columns`);
  expect(columnsResponse.ok()).toBeTruthy();
  const columns = (await columnsResponse.json()).data;
  const backlog = columns.find((column: { semantic_state: string }) => column.semantic_state === 'backlog');
  expect(backlog).toBeTruthy();
  const matching = await post(`/api/v1/projects/${project.id}/tasks`, { title: `${task.title} low`, priority: 'low', column_id: task.column_id });
  const wrongQuery = await post(`/api/v1/projects/${project.id}/tasks`, { title: `Unrelated low ${suffix}`, priority: 'low', column_id: task.column_id });
  const wrongState = await post(`/api/v1/projects/${project.id}/tasks`, { title: `${task.title} backlog`, priority: 'low', column_id: backlog.id });
  const saved = await post('/api/v1/views', {
    name: `Legacy override ${suffix}`, filters: { project_ids: [project.id], q: task.title, priority: 'high', state: 'ready' },
    sort: [{ field: 'title', direction: 'asc' }], shared: false
  });
  const legacyURL = `/search?view=${encodeURIComponent(saved.id)}&priority=low`;
  const executions: Array<{ query: string | null; priority: string | null; state: string | null; resultKeys: string[] }> = [];
  for (const reload of [false, true]) {
    const execution = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === '/api/v1/search' && url.searchParams.get('view') === saved.id;
    });
    if (reload) await page.reload();
    else await page.goto(legacyURL);
    const response = await execution;
    expect(response.ok()).toBeTruthy();
    const url = new URL(response.url());
    const results = (await response.json()).data as Array<{ id: string; key: string }>;
    expect(url.searchParams.get('q')).toBe(task.title);
    expect(url.searchParams.get('priority')).toBe('low');
    expect(url.searchParams.get('state')).toBe('ready');
    expect(results.map((result) => result.id)).toEqual([matching.id]);
    expect(results.some((result) => [task.id, wrongQuery.id, wrongState.id].includes(result.id))).toBeFalsy();
    await expect(page.getByLabel('Search all tasks')).toHaveValue(task.title);
    await expect(page.getByLabel('Filter search by priority')).toHaveValue('low');
    await expect(page.getByLabel('Filter search by state')).toHaveValue('ready');
    await expect(page.locator('.search-result-row .task-key')).toHaveText([matching.key]);
    await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
    await expect(page).toHaveURL(legacyURL);
    executions.push({ query: url.searchParams.get('q'), priority: url.searchParams.get('priority'), state: url.searchParams.get('state'), resultKeys: results.map((result) => result.key) });
  }
  await testInfo.attach('legacy-saved-view-overlay.json', { body: JSON.stringify({ baseline: saved.filters, executions }, null, 2), contentType: 'application/json' });
  await testInfo.attach('legacy-saved-view-overlay.png', { body: await page.screenshot(), contentType: 'image/png' });
});
