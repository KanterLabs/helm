import { expect, test, type TestInfo } from '@playwright/test';
import { collectionData, columnFor, createProject, createTask, json, mutationHeaders, type Collection, type Column, type Task } from './support/taskDrawerFixture';

// Browser fault injection must bypass SW interception; real SW coverage has its own suite.
test.use({ serviceWorkers: 'block' });

test.describe('task drawer safeguards', () => {
  test('hydrates clean drawer drafts from polling while preserving dirty drafts', async ({ page, request }, testInfo: TestInfo) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const project = await createProject(request, suffix);
    const columns = collectionData(await json<Collection<Column> | Column[]>(
      await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
      'list polling columns'
    ));
    const ready = columnFor(columns, 'ready');
    const task = await createTask(request, project, ready, `Polling source ${suffix}`);

    // Liveness refreshes are intentionally infrequent in production. Shorten
    // only that interval in this browser so the test exercises the real
    // refreshDrawerTask path without waiting a minute.
    await page.addInitScript(() => {
      const nativeSetInterval = window.setInterval.bind(window);
      window.setInterval = ((handler: TimerHandler, timeout?: number, ...args: any[]) =>
        nativeSetInterval(handler, timeout === 60_000 ? 100 : timeout, ...args)) as typeof window.setInterval;
    });
    const cleanRefreshTitle = `Authoritative clean refresh ${suffix}`;
    const dirtyRefreshTitle = `Authoritative dirty refresh ${suffix}`;
    const refreshEvidence: Array<{ key: string; title: string; version: number }> = [];
    const nextPollingRead = (title: string) => page.waitForResponse(async (response) => {
      const url = new URL(response.url());
      if (response.request().method() !== 'GET' || url.pathname !== `/api/v1/tasks/${task.id}` || !response.ok()) return false;
      return (await response.json() as Task).title === title;
    }, { timeout: 10_000 });

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/p/${project.slug}`);
    const board = page.locator('section.board');
    const card = board.locator('.task-card').filter({ hasText: task.key });
    await card.locator('[data-task-trigger]').click();
    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('.drawer-loading')).toBeHidden();
    await expect(drawer.getByLabel('Task title')).toHaveValue(task.title);

    // Persist changes through the real API so the drawer and concurrent board
    // polling receive the same versioned snapshot, regardless of read order.
    const cleanRead = nextPollingRead(cleanRefreshTitle);
    const clean = await json<Task>(await request.patch(`/api/v1/tasks/${task.id}`, {
      data: { title: cleanRefreshTitle }, headers: mutationHeaders(task.version)
    }), 'persist clean polling refresh');
    const cleanResponse = await (await cleanRead).json() as Task;
    refreshEvidence.push({ key: cleanResponse.key, title: cleanResponse.title, version: cleanResponse.version });
    await expect(drawer.getByLabel('Task title')).toHaveValue(cleanRefreshTitle, { timeout: 10_000 });
    await expect(drawer.locator('.drawer-save-bar')).toContainText('All changes saved');
    await expect(card).toContainText(cleanRefreshTitle);

    const dirtyDraftTitle = `Local dirty draft ${suffix}`;
    await drawer.getByLabel('Task title').fill(dirtyDraftTitle);
    const dirtyRead = nextPollingRead(dirtyRefreshTitle);
    const dirty = await json<Task>(await request.patch(`/api/v1/tasks/${task.id}`, {
      data: { title: dirtyRefreshTitle }, headers: mutationHeaders(clean.version)
    }), 'persist dirty polling refresh');
    const dirtyResponse = await (await dirtyRead).json() as Task;
    refreshEvidence.push({ key: dirtyResponse.key, title: dirtyResponse.title, version: dirtyResponse.version });
    await expect(card).toContainText(dirtyRefreshTitle);
    await expect(drawer.getByLabel('Task title')).toHaveValue(dirtyDraftTitle, { timeout: 10_000 });
    await expect(drawer.locator('.drawer-save-bar')).toContainText('Unsaved changes');
    expect(refreshEvidence).toEqual([
      { key: task.key, title: cleanRefreshTitle, version: clean.version },
      { key: task.key, title: dirtyRefreshTitle, version: dirty.version }
    ]);
    const persisted = await json<Task>(await request.get(`/api/v1/tasks/${task.id}`), 'read final polling task');
    expect(persisted.title).toBe(dirtyRefreshTitle);
    expect(persisted.version).toBe(dirty.version);
    await testInfo.attach('task-drawer-polling-responses.json', {
      body: JSON.stringify({ task: task.key, responses: refreshEvidence, persisted: { title: persisted.title, version: persisted.version }, localDraft: dirtyDraftTitle }, null, 2),
      contentType: 'application/json'
    });
    await testInfo.attach('task-drawer-polling.png', {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png'
    });
  });
});
