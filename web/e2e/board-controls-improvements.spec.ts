import { expect, test, type APIRequestContext, type APIResponse, type Locator, type Page, type TestInfo } from '@playwright/test';

// Keep API failure injection observable in WebKit; PWA behavior has its own suite.
test.use({ hasTouch: true, serviceWorkers: 'block' });

type Project = { id: string; key: string; slug: string };
type Column = { id: string; name: string; semantic_state: string };
type Task = { id: string; key: string; title: string; column_id: string; position: number; version: number; priority: string };
type Collection<T> = { data: T[] };
const origin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
const headers = (version?: number) => ({
  Origin: origin,
  'Content-Type': 'application/json',
  'Idempotency-Key': `board-controls-${crypto.randomUUID()}`,
  ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
});

async function json<T>(response: APIResponse): Promise<T> {
  expect(response.ok(), `Fixture/API request returned ${response.status()}`).toBeTruthy();
  return response.json() as Promise<T>;
}

async function fixture(request: APIRequestContext) {
  const status = await json<{ mode: string }>(await request.get('/api/v1/auth/status'));
  expect(status.mode, 'Use the documented auth-disabled E2E server').toBe('disabled');
  const suffix = Date.now().toString(36).toUpperCase() + Math.random().toString(36).slice(2, 5).toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    headers: headers(), data: { key: `CTRL${suffix}`.slice(0, 16), name: `Board controls ${suffix}`, description: 'Synthetic mobile and keyboard browser evidence.' }
  }));
  const response = await json<Collection<Column> | Column[]>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`));
  const columns = Array.isArray(response) ? response : response.data;
  const ready = columns.find((column) => column.semantic_state === 'ready')!;
  const backlog = columns.find((column) => column.semantic_state === 'backlog')!;
  expect(ready).toBeTruthy();
  expect(backlog).toBeTruthy();
  const tasks: Task[] = [];
  for (let index = 0; index < 3; index += 1) {
    tasks.push(await json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
      headers: headers(),
      data: {
        column_id: ready.id,
        title: index === 1 ? `A long synthetic task title that must leave Actions reachable on every phone ${suffix}` : `Synthetic neighbor ${index} ${suffix}`,
        description: index === 1 ? 'Long synthetic metadata and a detailed description exercise compact uniform card heights without exposing user content.' : '',
        priority: 'normal'
      }
    })));
  }
  return { project, ready, backlog, tasks, moving: tasks[1] };
}

function trigger(page: Page, task: Task) { return page.locator(`[data-card-actions="${task.key}"]`); }
function menu(page: Page, task: Task) { return page.getByRole('menu', { name: `Actions for ${task.key}`, exact: true }); }
async function openMenu(page: Page, task: Task) {
  await trigger(page, task).click();
  await expect(menu(page, task)).toBeVisible();
}
async function targetBounds(locator: Locator) {
  const bounds = await locator.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.width).toBeGreaterThanOrEqual(44);
  expect(bounds!.height).toBeGreaterThanOrEqual(44);
  return bounds!;
}
async function screenshot(page: Page, info: TestInfo, name: string) {
  await info.attach(name, { body: await page.screenshot({ fullPage: false }), contentType: 'image/png' });
}
async function tasksFor(request: APIRequestContext, project: Project) {
  const response = await json<Collection<Task>>(await request.get(`/api/v1/projects/${project.id}/tasks?limit=100`));
  return response.data;
}

const filterNames = ['Filter by state', 'Filter by priority', 'Filter by focus', 'Filter by agent work', 'Filter by dependency readiness', 'Filter by label', 'Filter by assignee', 'Sort tasks', 'Sort direction'];

test('compact card actions and filter sheet stay reachable at all five required widths', async ({ page, request }, info) => {
  test.setTimeout(120_000);
  const state = await fixture(request);
  await page.goto(`/p/${state.project.slug}`);
  const measurements: unknown[] = [];
  for (const width of [320, 390, 768, 1280, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    const actionTrigger = trigger(page, state.moving);
    await expect(actionTrigger).toBeVisible();
    await actionTrigger.scrollIntoViewIfNeeded();
    const cardBounds = await targetBounds(actionTrigger);
    const card = page.locator('.task-card').filter({ has: actionTrigger });
    await expect(card.locator('.card-move')).toHaveCount(0);
    const touchBounds = [];
    if (width <= 700) {
      const outer = await card.boundingBox();
      for (const selector of ['.task-select', '.task-drag-handle', '.card-expand-toggle']) {
        const control = card.locator(selector);
        if (selector === '.card-expand-toggle' && !await control.count()) continue;
        const bounds = await targetBounds(control);
        expect(bounds.x).toBeGreaterThanOrEqual(outer!.x);
        expect(bounds.y).toBeGreaterThanOrEqual(outer!.y);
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(outer!.x + outer!.width);
        expect(bounds.y + bounds.height).toBeLessThanOrEqual(outer!.y + outer!.height);
        touchBounds.push({ selector, ...bounds });
      }
      const selection = touchBounds.find((bounds) => bounds.selector === '.task-select')!;
      const drag = touchBounds.find((bounds) => bounds.selector === '.task-drag-handle')!;
      const key = await card.locator('.task-key').boundingBox();
      expect(key!.width).toBeGreaterThan(0);
      expect(selection.x + selection.width).toBeLessThanOrEqual(key!.x);
      expect(key!.x + key!.width).toBeLessThanOrEqual(drag.x);
      const expander = touchBounds.find((bounds) => bounds.selector === '.card-expand-toggle');
      if (expander) expect(expander.x + expander.width).toBeLessThanOrEqual(cardBounds.x);
    }
    const collapsedHeights = await page.locator('.task-card').evaluateAll((cards) => cards.map((item) => item.getBoundingClientRect().height));
    expect(Math.max(...collapsedHeights) - Math.min(...collapsedHeights)).toBeLessThanOrEqual(1);
    await actionTrigger.focus();
    await page.keyboard.press('ArrowDown');
    const actionMenu = menu(page, state.moving);
    await expect(actionMenu.getByRole('menuitem')).toHaveCount(6);
    await expect(actionMenu.getByRole('menuitem').first()).toBeFocused();
    await page.keyboard.press('End');
    await expect(actionMenu.getByRole('menuitem').last()).toBeFocused();
    await page.keyboard.press('Home');
    await expect(actionMenu.getByRole('menuitem').first()).toBeFocused();
    const menuBounds = await actionMenu.boundingBox();
    expect(menuBounds!.x).toBeGreaterThanOrEqual(0);
    expect(menuBounds!.x + menuBounds!.width).toBeLessThanOrEqual(width);
    expect(menuBounds!.y).toBeGreaterThanOrEqual(0);
    expect(menuBounds!.y + menuBounds!.height).toBeLessThanOrEqual(900);
    const itemBounds = [];
    for (const item of await actionMenu.getByRole('menuitem').all()) itemBounds.push(await targetBounds(item));
    await screenshot(page, info, `card-actions-${width}.png`);
    await page.keyboard.press('Escape');
    await expect(actionMenu).toBeHidden();
    await expect(actionTrigger).toBeFocused();
    await expect(page.locator('.task-drawer')).toHaveCount(0);
    if (width <= 390) {
      await actionTrigger.tap();
      await expect(actionMenu).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(actionMenu).toBeHidden();
    }

    const filtersTrigger = page.locator('[data-board-filter-trigger]');
    await filtersTrigger.scrollIntoViewIfNeeded();
    const filterTriggerBounds = await targetBounds(filtersTrigger);
    const searchBounds = await page.getByRole('textbox', { name: 'Search tasks', exact: true }).boundingBox();
    expect(searchBounds!.x).toBeGreaterThanOrEqual(0);
    expect(searchBounds!.x + searchBounds!.width).toBeLessThanOrEqual(width);
    const create = page.locator('.board-toolbar [data-task-modal-trigger]');
    await expect(create).toBeVisible();
    const createBounds = await targetBounds(create);
    expect(createBounds.x + createBounds.width).toBeLessThanOrEqual(width);
    if (width <= 390) await filtersTrigger.tap();
    else await filtersTrigger.click();
    const panel = page.getByRole('dialog', { name: 'Board filters', exact: true });
    await expect(panel).toBeVisible();
    const filters = [];
    for (const name of filterNames) {
      const control = panel.getByRole('combobox', { name, exact: true });
      await control.scrollIntoViewIfNeeded();
      const bounds = await targetBounds(control);
      expect(bounds.x).toBeGreaterThanOrEqual(0);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
      filters.push({ name, ...bounds });
    }
    await panel.getByRole('button', { name: 'Show results', exact: true }).focus();
    await page.keyboard.press('Tab');
    await expect(panel.getByRole('button', { name: 'Close board filters', exact: true })).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(panel.getByRole('button', { name: 'Show results', exact: true })).toBeFocused();
    await screenshot(page, info, `board-filters-${width}.png`);
    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    await expect(filtersTrigger).toBeFocused();
    measurements.push({ width, cardBounds, touchBounds, collapsedHeights, menuBounds, itemBounds, filterTriggerBounds, searchBounds, createBounds, filters });
  }
  await info.attach('passing-control-bounds.json', { body: JSON.stringify(measurements, null, 2), contentType: 'application/json' });
});

test('all six movement commands persist and rejected moves restore focus', async ({ page, request }, info) => {
  test.setTimeout(120_000);
  const state = await fixture(request);
  await page.setViewportSize({ width: 390, height: 900 });
  await page.goto(`/p/${state.project.slug}`);
  const evidence: unknown[] = [];
  for (const [id, index] of [['previous', 0], ['next', 1], ['first', 0], ['last', 2]] as const) {
    await openMenu(page, state.moving);
    const command = menu(page, state.moving).locator(`[data-card-action="${id}"]`);
    await expect(command).toHaveAttribute('aria-disabled', 'false');
    const saved = page.waitForResponse((response) => response.url().endsWith(`/api/v1/tasks/${state.moving.id}/reorder`) && response.request().method() === 'POST');
    await command.click();
    expect((await saved).ok()).toBeTruthy();
    await expect.poll(async () => (await tasksFor(request, state.project)).filter((task) => task.column_id === state.ready.id).sort((a, b) => a.position - b.position).map((task) => task.id).indexOf(state.moving.id)).toBe(index);
    await expect(trigger(page, state.moving)).toBeFocused();
    evidence.push({ command: id, tasks: await tasksFor(request, state.project) });
  }
  for (const [id, destination] of [['left', state.backlog], ['right', state.ready]] as const) {
    await openMenu(page, state.moving);
    const saved = page.waitForResponse((response) => response.url().endsWith(`/api/v1/tasks/${state.moving.id}/move`) && response.request().method() === 'POST');
    await menu(page, state.moving).locator(`[data-card-action="${id}"]`).click();
    expect((await saved).ok()).toBeTruthy();
    await expect.poll(async () => (await tasksFor(request, state.project)).find((task) => task.id === state.moving.id)?.column_id).toBe(destination.id);
    await expect(trigger(page, state.moving)).toBeFocused();
    evidence.push({ command: id, tasks: await tasksFor(request, state.project) });
  }

  const before = (await tasksFor(request, state.project)).find((task) => task.id === state.moving.id)!;
  await page.route(`**/api/v1/tasks/${state.moving.id}/reorder`, (route) => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'unavailable', message: 'Synthetic movement failure.' } }) }), { times: 1 });
  await openMenu(page, state.moving);
  await menu(page, state.moving).locator('[data-card-action="previous"]').click();
  await expect(page.getByText('Synthetic movement failure.', { exact: false })).toBeVisible();
  await expect(trigger(page, state.moving)).toBeFocused();
  const afterFailure = (await tasksFor(request, state.project)).find((task) => task.id === state.moving.id)!;
  expect(afterFailure.position).toBe(before.position);
  expect(afterFailure.version).toBe(before.version);
  evidence.push({ rejectedMove: { before, afterFailure } });
  await screenshot(page, info, 'rejected-move-focus.png');

  const concurrent = await json<Task>(await request.patch(`/api/v1/tasks/${before.id}`, { headers: headers(before.version), data: { priority: 'high' } }));
  await openMenu(page, state.moving);
  await menu(page, state.moving).locator('[data-card-action="previous"]').click();
  await expect(page.getByText(/changed elsewhere.*refreshed/).first()).toBeVisible();
  await expect(trigger(page, state.moving)).toBeFocused();
  const reconciled = (await tasksFor(request, state.project)).find((task) => task.id === state.moving.id)!;
  expect(reconciled.version).toBe(concurrent.version);
  expect(reconciled.position).toBe(concurrent.position);
  evidence.push({ staleMove: { concurrent, reconciled } });
  await screenshot(page, info, 'stale-move-reconciled.png');
  await info.attach('movement-api-evidence.json', { body: JSON.stringify(evidence, null, 2), contentType: 'application/json' });
});

test('filter count, disabled reasons and clear all preserve real board criteria', async ({ page, request }, info) => {
  const state = await fixture(request);
  await page.goto(`/p/${state.project.slug}`);
  const filtersTrigger = page.locator('[data-board-filter-trigger]');
  await filtersTrigger.click();
  const panel = page.getByRole('dialog', { name: 'Board filters', exact: true });
  await panel.getByRole('combobox', { name: 'Filter by priority', exact: true }).selectOption('normal');
  await panel.getByRole('button', { name: 'Show results', exact: true }).click();
  await expect(filtersTrigger).toHaveAccessibleName(/1 active/);
  await expect(filtersTrigger).toBeFocused();
  await expect(page).toHaveURL(/priority=normal/);
  await openMenu(page, state.moving);
  const first = menu(page, state.moving).locator('[data-card-action="first"]');
  await expect(first).toHaveAttribute('aria-disabled', 'true');
  await expect(first).toContainText(/filter/i);
  const unchanged = await tasksFor(request, state.project);
  // Disabled commands stay focusable so keyboard users can read the reason.
  // Playwright correctly refuses click() on aria-disabled controls; Enter
  // exercises the component's activation guard through the real keyboard.
  await first.focus();
  await page.keyboard.press('Enter');
  await expect(first).toBeFocused();
  await expect(menu(page, state.moving)).toBeVisible();
  expect(await tasksFor(request, state.project)).toEqual(unchanged);
  await page.keyboard.press('Escape');
  await filtersTrigger.click();
  await panel.getByRole('button', { name: 'Clear all', exact: true }).click();
  await expect(panel).toBeVisible();
  await expect(panel.getByRole('combobox', { name: 'Filter by priority', exact: true })).toHaveValue('all');
  await panel.getByRole('button', { name: 'Show results', exact: true }).click();
  await expect(filtersTrigger).toHaveAccessibleName('Filters');
  await expect(page).not.toHaveURL(/priority=normal/);
  await expect(page.locator('.task-card')).toHaveCount(3);
  await screenshot(page, info, 'cleared-filters.png');
  await info.attach('cleared-filter-url.json', { body: JSON.stringify({ url: page.url(), tasks: await tasksFor(request, state.project) }, null, 2), contentType: 'application/json' });
});
