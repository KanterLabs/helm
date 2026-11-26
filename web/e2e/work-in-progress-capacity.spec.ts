import { expect, test, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import { columns, headers, json, suffix, taskModal, type Project } from './creation-safety-helpers';
import { expireTaskClaimInPayload } from './support/agentWorkApi';

// Record these journeys before their implementation. A filter must not invent
// an available slot, a denied drag/keyboard/form action must leave the source
// intact, and an operator must be able to recover by finishing existing work.
// The phone journey catches expired leases that still look like active owners.
// The service worker bypasses API reads. Keep registration enabled so the
// screenshots show the same status surfaces people see on an online board.
test.use({ serviceWorkers: 'allow' });

type Task = { id: string; key: string; title: string; version: number; column_id: string };

async function fixture(request: APIRequestContext) {
  const run = suffix();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    headers: headers(),
    data: { key: `CAP${run}`.slice(0, 16), name: `Capacity browser fixture ${run}` }
  }));
  const projectColumns = await columns(request, project);
  const active = projectColumns.find((column) => column.semantic_state === 'active')!;
  const ready = projectColumns.find((column) => column.semantic_state === 'ready')!;
  const create = async (title: string, columnId: string) => json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    headers: headers(), data: { title, column_id: columnId }
  }));
  const occupied = [
    await create('Publish a synthetic report', active.id),
    await create('Check report links', active.id),
    await create('Review report accessibility', active.id),
    await create('Update synthetic changelog', active.id),
    await create('Verify report formatting', active.id)
  ];
  const queued = await create('Draft the next report', ready.id);
  return { project, active, ready, occupied, queued };
}

function taskButton(page: Page, task: Task) {
  return page.locator(`[data-task-trigger="${task.id}"]`);
}

async function screenshot(page: Page, info: TestInfo, name: string) {
  await info.attach(`${name}.png`, { body: await page.screenshot(), contentType: 'image/png' });
}

test('shares capacity across filtered board actions and recovers by finishing a card', async ({ page, request }, info) => {
  test.setTimeout(75_000);
  const { project, active, ready, occupied, queued } = await fixture(request);
  const capacity = page.getByRole('region', { name: 'In Progress capacity' });
  const activeColumn = page.getByRole('article', { name: `${active.name} column`, exact: true });
  const card = taskButton(page, queued);
  await page.goto(`/p/${project.slug}`);
  await expect(capacity).toContainText('5 of 5');
  await expect(capacity).toContainText('Full');
  const filteredPage = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.pathname === `/api/v1/projects/${project.id}/tasks` && url.searchParams.get('q') === queued.title;
  });
  await page.getByRole('textbox', { name: 'Search tasks' }).fill(queued.title);
  expect((await filteredPage).ok()).toBeTruthy();
  await expect(page.locator('.board-loading, .board-loading-note')).toHaveCount(0);
  await expect(activeColumn.locator('.task-card')).toHaveCount(0);
  await expect(capacity).toContainText('5 of 5');

  await card.press('Alt+ArrowRight');
  await expect(page.getByRole('alert').filter({ hasText: /Finish or move an In Progress task/ })).toBeVisible();
  await expect(card).toBeFocused();
  await capacity.getByRole('button', { name: 'Review In Progress tasks' }).click();
  await expect(taskButton(page, occupied[0])).toBeFocused();
  await expect(activeColumn.locator('.task-card')).toHaveCount(5);
  await screenshot(page, info, 'full-board');

  const drag = page.getByRole('button', { name: `Drag ${queued.key}, ${queued.title}` });
  const rejectDrop = page.waitForResponse((response) => response.url().endsWith(`/tasks/${queued.id}/reorder`) && response.status() === 409);
  await drag.dragTo(activeColumn.getByRole('heading', { name: active.name, exact: true }));
  expect((await (await rejectDrop).json()).error.code).toBe('project_capacity_reached');
  await expect(page.getByRole('article', { name: `${ready.name} column`, exact: true }).locator(`[data-task-trigger="${queued.id}"]`)).toBeVisible();

  await activeColumn.getByRole('button', { name: /Add task/ }).click();
  const quickTitle = activeColumn.getByRole('textbox', { name: `New task in ${active.name}` });
  await quickTitle.fill('Keep this quick add draft');
  await quickTitle.press('Enter');
  await expect(activeColumn.getByRole('alert')).toContainText(/Finish or move an In Progress task/);
  await expect(quickTitle).toHaveValue('Keep this quick add draft');
  await activeColumn.getByRole('button', { name: 'Cancel', exact: true }).click();

  const modal = await taskModal(page);
  await modal.getByLabel('Task title', { exact: true }).fill('Keep this draft after refusal');
  await modal.getByRole('combobox', { name: 'Column', exact: true }).selectOption(active.id);
  await modal.getByRole('button', { name: 'Create task', exact: true }).click();
  await expect(modal.getByRole('alert')).toContainText(/Finish or move an In Progress task/);
  await expect(modal.getByLabel('Task title', { exact: true })).toHaveValue('Keep this draft after refusal');
  await screenshot(page, info, 'create-refusal-desktop');
  await modal.getByRole('combobox', { name: 'Column', exact: true }).selectOption(ready.id);
  await modal.getByRole('button', { name: 'Create task', exact: true }).click();
  await expect(modal).toBeHidden();

  await page.getByRole('checkbox', { name: `Select ${occupied[0].key}`, exact: true }).check();
  await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();
  const review = page.getByRole('dialog', { name: 'Review bulk changes', exact: true });
  await review.getByLabel('Bulk change', { exact: true }).selectOption('complete');
  await review.getByRole('button', { name: 'Apply changes to 1 tasks', exact: true }).click();
  await expect(review.getByRole('status')).toContainText('1 applied · 0 conflicts · 0 skipped');
  await expect(review.getByRole('status')).toContainText(occupied[0].key);
  await review.getByRole('button', { name: 'Close', exact: true }).click();

  await expect(capacity).toContainText('4 of 5');
  // Creating the queued draft and completing the occupied card changed both
  // neighboring column revisions. This append must acquire the current tail.
  const acceptedMove = page.waitForResponse((response) => response.request().method() === 'POST'
    && response.url().endsWith(`/tasks/${queued.id}/reorder`));
  await card.press('Alt+ArrowRight');
  expect((await acceptedMove).status(), 'the first move into the freed slot succeeds').toBe(200);
  await expect(activeColumn.locator(`[data-task-trigger="${queued.id}"]`)).toBeVisible();
  await expect(capacity).toContainText('5 of 5');
  await screenshot(page, info, 'slot-reused');

  // Simulate the authoritative count returned for a project carried over
  // from before enforcement. Creating that state through writes is refused.
  await page.route(`**/api/v1/projects/${project.id}`, async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), active_task_count: 7 } });
  });
  await page.reload();
  await expect(capacity).toContainText('7 of 5');
  await expect(capacity).toContainText('Over limit');
  await expect(capacity).toContainText('Finish or move 3 In Progress tasks');
  await screenshot(page, info, 'legacy-over-limit');
});

test('keeps occupied work and expired claims actionable on a phone', async ({ page, request }, info) => {
  const { project, occupied } = await fixture(request);
  const task = occupied[2];
  await json<Task>(await request.post(`/api/v1/tasks/${task.id}/claim`, {
    headers: { ...headers(), 'If-Match': `"v${task.version}"` }, data: { lease_seconds: 3600 }
  }));
  // Age only the read boundary. The server has no clock-setting route, and
  // this case verifies what the owner sees when a shown lease has expired.
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== 'GET' || ![`/api/v1/tasks/${task.id}`, `/api/v1/projects/${project.id}/tasks`].includes(path)) {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const payload: unknown = await response.json();
    expireTaskClaimInPayload(payload, task.id, '2020-01-01T00:00:00Z');
    await route.fulfill({ response, json: payload });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/p/${project.slug}`);
  const capacity = page.getByRole('region', { name: 'In Progress capacity' });
  await expect(capacity).toContainText('5 of 5');
  await screenshot(page, info, 'full-board-phone');
  await capacity.getByRole('button', { name: 'Review In Progress tasks' }).click();
  await taskButton(page, task).click();
  const details = page.getByRole('dialog', { name: `${task.key}: ${task.title}`, exact: true });
  await expect(details.getByText(/Previous claim by/)).toBeVisible();
  await expect(details.getByRole('button', { name: '⚑ Claim task', exact: true })).toBeEnabled();
  const work = details.getByRole('region', { name: 'Active task details' });
  await expect(work).toContainText('Unassigned');
  await expect(work).toContainText('Next action');
  await expect(work).toContainText('Finish condition');
  await screenshot(page, info, 'expired-claim-phone');
});
