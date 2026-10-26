import { expect, test } from '@playwright/test';
import { columns, evidence, gate, headers, json, origin, project, suffix, taskModal, tasks, type Project, type Task } from './creation-safety-helpers';

// Fault injection must reach the browser; service-worker coverage lives in the PWA suite.
test.use({ serviceWorkers: 'block' });

// Failure scenarios recorded before implementation in /tmp/helm-creation-failures-20261017.md:
// committed submit + dismiss/double click; lost response replay; project and quick-add writes.
test.describe('creation safety', () => {
  test('keeps the exact Search route and history origin when Back is attempted during a committed create', async ({ page, request }, info) => {
    const intended = await project(request);
    const other = await project(request);
    await page.goto(`/p/${intended.slug}`);
    await page.getByRole('button', { name: 'Search anything', exact: true }).click();
    await page.getByRole('option', { name: /^Search everything\./ }).click();
    await expect(page.getByRole('heading', { name: 'Search everything', exact: true })).toBeVisible();
    const query = `Pending navigation ${suffix()}`;
    await page.getByRole('textbox', { name: 'Search all tasks', exact: true }).fill(query);
    await page.getByRole('textbox', { name: 'Search all tasks', exact: true }).press('Enter');
    await expect(page).toHaveURL(/\/search\?q=/);
    const route = await page.evaluate(() => {
      const origin = { ...history.state, helmTaskOrigin: location.pathname + location.search };
      history.replaceState(origin, '', location.href + '#creation-origin');
      return { url: location.href, state: history.state };
    });
    await page.getByRole('button', { name: 'Search anything', exact: true }).click();
    await page.getByRole('option', { name: /^New task\./ }).click();
    const modal = page.getByRole('dialog', { name: 'Create a task', exact: true });
    await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toBeEnabled();
    await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(intended.id);
    const title = `Protected Search creation ${suffix()}`;
    await modal.getByLabel('Task title', { exact: true }).fill(title);
    const persisted = gate();
    const delivery = gate();
    let posts = 0;
    await page.route(`**/api/v1/projects/${intended.id}/tasks`, async (requestRoute) => {
      if (requestRoute.request().method() !== 'POST') return requestRoute.continue();
      posts += 1;
      const response = await requestRoute.fetch();
      persisted.release();
      await delivery.promise;
      await requestRoute.fulfill({ response });
    });
    try {
      await modal.getByRole('button', { name: 'Create task', exact: true }).click();
      await persisted.promise;
      await page.goBack();
      await expect(page).toHaveURL(route.url);
      expect(await page.evaluate(() => history.state)).toEqual(route.state);
      await expect(page.getByRole('heading', { name: 'Search everything', exact: true })).toBeVisible();
      await expect(page.getByRole('textbox', { name: 'Search all tasks', exact: true })).toHaveValue(query);
      await expect(page.locator('section.board')).toHaveCount(0);
      await expect(modal).toBeVisible();
      await expect(modal.getByRole('button', { name: 'Close', exact: true })).toBeDisabled();
      const saved = (await tasks(request, intended)).filter((item) => item.title === title);
      expect(saved).toHaveLength(1);
      expect((await tasks(request, other)).filter((item) => item.title === title)).toHaveLength(0);
      expect(posts).toBe(1);
      await evidence(page, info, 'pending-back-preserves-search-route', { expected: route, actual: { url: page.url(), state: await page.evaluate(() => history.state), view: 'Search everything' }, saved, other: [], posts });
    } finally { delivery.release(); }
    await expect(modal).toBeHidden();
    await expect(page).toHaveURL(route.url);
    await expect(page.getByRole('heading', { name: 'Search everything', exact: true })).toBeVisible();
    expect((await tasks(request, intended)).filter((item) => item.title === title)).toHaveLength(1);
  });

  test('keeps committed task visible, frozen and unique through dismissal and duplicate submission', async ({ page, request }, info) => {
    const intended = await project(request);
    const other = await project(request);
    await page.goto(`/p/${intended.slug}`);
    const modal = await taskModal(page);
    const title = `Committed task ${suffix()}`;
    await modal.getByLabel('Task title', { exact: true }).fill(title);
    const delivery = gate();
    const persisted = gate();
    let posts = 0;
    await page.route(`**/api/v1/projects/${intended.id}/tasks`, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      posts += 1;
      const response = await route.fetch();
      persisted.release();
      await delivery.promise;
      await route.fulfill({ response });
    });
    try {
      await modal.getByRole('button', { name: 'Create task', exact: true }).click();
      await persisted.promise;
      await modal.locator('form').dispatchEvent('submit');
      await page.keyboard.press('Escape');
      await page.locator('.modal-backdrop').dispatchEvent('click');
      await expect(modal).toBeVisible();
      await expect(modal.getByRole('button', { name: 'Close', exact: true })).toBeDisabled();
      await expect(modal.getByRole('button', { name: 'Cancel', exact: true }).last()).toBeDisabled();
      await expect(modal.getByRole('combobox', { name: 'Project', exact: true })).toBeDisabled();
      await expect(modal.getByLabel('Task title', { exact: true })).toBeDisabled();
      const saved = (await tasks(request, intended)).filter((item) => item.title === title);
      expect(saved).toHaveLength(1);
      expect((await tasks(request, other)).filter((item) => item.title === title)).toHaveLength(0);
      expect(posts).toBe(1);
      await evidence(page, info, 'task-pending-single-persisted-record', { posts, intended: saved.map(({ title, project_id, column_id }) => ({ title, project_id, column_id })), other: [] });
    } finally {
      delivery.release();
    }
    await expect(modal).toBeHidden();
    expect((await tasks(request, intended)).filter((item) => item.title === title)).toHaveLength(1);
  });

  test('reconciles a committed task after lost delivery with the original payload and idempotency key', async ({ page, request }, info) => {
    const intended = await project(request);
    const other = await project(request);
    await page.goto(`/p/${intended.slug}`);
    const modal = await taskModal(page);
    const title = `Lost response ${suffix()}`;
    await modal.getByLabel('Task title', { exact: true }).fill(title);
    const attempts: { key: string | undefined; payload: unknown }[] = [];
    await page.route(`**/api/v1/projects/${intended.id}/tasks`, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      attempts.push({ key: route.request().headers()['idempotency-key'], payload: route.request().postDataJSON() });
      const response = await route.fetch();
      if (attempts.length <= 2) {
        expect(response.ok()).toBeTruthy();
        // The server committed; the proxy cannot deliver its response.
        return route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ error: { code: 'response_lost', message: 'Synthetic delivery loss after commit' } }) });
      }
      await route.fulfill({ response });
    });
    await modal.getByRole('button', { name: 'Create task', exact: true }).click();
    await expect(modal.getByRole('alert')).toContainText('The result is not confirmed');
    expect(attempts).toHaveLength(2);
    await page.keyboard.press('Escape');
    await expect(modal).toBeVisible();
    await expect(modal.getByLabel('Task title', { exact: true })).toBeDisabled();
    await expect(modal.getByRole('button', { name: 'Close', exact: true })).toBeDisabled();
    expect((await tasks(request, intended)).filter((item) => item.title === title)).toHaveLength(1);
    await evidence(page, info, 'unconfirmed-operation-protected', { attempts: attempts.map(({ payload }) => ({ payload })), persistedCount: 1 });
    await modal.getByRole('button', { name: 'Retry original submission', exact: true }).click();
    await expect(modal).toBeHidden();
    expect(attempts).toHaveLength(3);
    expect(attempts[0].key).toBeTruthy();
    expect(attempts[1]).toEqual(attempts[0]);
    expect(attempts[2]).toEqual(attempts[0]);
    const saved = (await tasks(request, intended)).filter((item) => item.title === title);
    expect(saved).toHaveLength(1);
    expect((await tasks(request, other)).filter((item) => item.title === title)).toHaveLength(0);
    await evidence(page, info, 'reconciled-original-operation', { attempts: attempts.map(({ payload }) => ({ payload })), sameKey: attempts[0].key === attempts[1].key, intended: saved, other: [] });
  });

  test('preserves an editable draft after a confirmed validation failure', async ({ page, request }, info) => {
    const intended = await project(request);
    await page.goto(`/p/${intended.slug}`);
    const modal = await taskModal(page);
    const title = `Rejected draft ${suffix()}`;
    const revised = `Corrected draft ${suffix()}`;
    await modal.getByLabel('Task title', { exact: true }).fill(title);
    await modal.getByLabel('Description', { exact: false }).fill('Synthetic draft retained after validation.');
    const keys: (string | undefined)[] = [];
    await page.route(`**/api/v1/projects/${intended.id}/tasks`, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      keys.push(route.request().headers()['idempotency-key']);
      if (keys.length === 1) return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: { code: 'validation_failed', message: 'Synthetic validation failure' } }) });
      await route.fulfill({ response: await route.fetch() });
    });
    await modal.getByRole('button', { name: 'Create task', exact: true }).click();
    await expect(modal.getByRole('alert')).toContainText('Synthetic validation failure');
    await expect(modal.getByLabel('Task title', { exact: true })).toHaveValue(title);
    await expect(modal.getByLabel('Description', { exact: false })).toHaveValue('Synthetic draft retained after validation.');
    await expect(modal.getByLabel('Task title', { exact: true })).toBeEnabled();
    await expect(modal.getByRole('button', { name: 'Close', exact: true })).toBeEnabled();
    expect(await tasks(request, intended)).toHaveLength(0);
    await evidence(page, info, 'confirmed-failure-editable-draft', { persisted: [], confirmedFailure: true });
    await modal.getByLabel('Task title', { exact: true }).fill(revised);
    await modal.getByRole('button', { name: 'Create task', exact: true }).click();
    await expect(modal).toBeHidden();
    expect(keys).toHaveLength(2);
    expect(keys[0]).not.toBe(keys[1]);
    const saved = await tasks(request, intended);
    expect(saved).toHaveLength(1);
    expect(saved[0].title).toBe(revised);
    await evidence(page, info, 'confirmed-failure-corrected-create', { saved });
  });

  test('retains the original operation through a real network loss and reconnect', async ({ page, request }, info) => {
    const intended = await project(request);
    await page.goto(`/p/${intended.slug}`);
    const modal = await taskModal(page);
    const title = `Reconnect original ${suffix()}`;
    await modal.getByLabel('Task title', { exact: true }).fill(title);
    const attempts: { key: string | undefined; payload: unknown }[] = [];
    await page.route(`**/api/v1/projects/${intended.id}/tasks`, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      attempts.push({ key: route.request().headers()['idempotency-key'], payload: route.request().postDataJSON() });
      const response = await route.fetch();
      if (attempts.length === 1) {
        expect(response.ok()).toBeTruthy();
        return route.abort('failed');
      }
      await route.fulfill({ response });
    });
    await modal.getByRole('button', { name: 'Create task', exact: true }).click();
    await expect(page.getByTestId('offline-reconnect')).toBeVisible();
    expect((await tasks(request, intended)).filter((item) => item.title === title)).toHaveLength(1);
    await page.getByTestId('offline-reconnect').click();
    await expect(modal).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Retry original submission', exact: true })).toBeEnabled();
    await expect(modal.getByLabel('Task title', { exact: true })).toHaveValue(title);
    await expect(modal.getByLabel('Task title', { exact: true })).toBeDisabled();
    await evidence(page, info, 'reconnected-protected-operation', { persistedCount: 1, deliveryFailedAfterCommit: true });
    await modal.getByRole('button', { name: 'Retry original submission', exact: true }).click();
    await expect(modal).toBeHidden();
    expect(attempts).toHaveLength(2);
    expect(attempts[1]).toEqual(attempts[0]);
    const saved = (await tasks(request, intended)).filter((item) => item.title === title);
    expect(saved).toHaveLength(1);
    await evidence(page, info, 'network-loss-reconciled', { saved, sameOperation: true });
  });

  test('protects project creation and quick add until each committed response is delivered', async ({ page, request }, info) => {
    const existing = await project(request);
    await page.goto(`/p/${existing.slug}`);
    await page.locator('[data-project-modal-trigger]').first().click();
    const modal = page.getByRole('dialog', { name: 'Create a project', exact: true });
    const id = suffix();
    const name = `Pending project ${id}`;
    const key = `PC${id}`.slice(0, 16);
    await modal.getByLabel('Project name', { exact: true }).fill(name);
    await modal.getByLabel('Project key', { exact: true }).fill(key);
    const projectDelivery = gate(); const projectFetched = gate();
    let projectPosts = 0;
    let created!: Project;
    await page.route('**/api/v1/projects', async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      projectPosts += 1;
      const response = await route.fetch(); created = await response.json() as Project;
      projectFetched.release(); await projectDelivery.promise; await route.fulfill({ response });
    });
    try {
      await modal.getByRole('button', { name: 'Create project', exact: true }).click();
      await projectFetched.promise;
      await modal.locator('form').dispatchEvent('submit');
      await page.keyboard.press('Escape');
      await page.locator('.modal-backdrop').dispatchEvent('click');
      await expect(modal).toBeVisible();
      await expect(modal.getByLabel('Project key', { exact: true })).toBeDisabled();
      await expect(modal.getByRole('button', { name: 'Close', exact: true })).toBeDisabled();
      expect(projectPosts).toBe(1);
      const persisted = await json<Project>(await request.get(`/api/v1/projects/${created.id}`));
      expect(persisted.name).toBe(name);
      await evidence(page, info, 'project-pending', { projectPosts, key: persisted.key, name: persisted.name });
    } finally { projectDelivery.release(); }
    await expect(modal).toBeHidden();
    await expect(page).toHaveURL(new RegExp(`/p/${created.slug}$`));
    const ready = (await columns(request, created)).find((column) => column.semantic_state === 'ready')!;
    await page.locator(`[data-quick-add-trigger="${ready.id}"]`).click();
    const quick = page.locator('.quick-add-form');
    const title = `Pending quick add ${suffix()}`;
    await quick.getByRole('textbox').fill(title);
    const quickDelivery = gate(); const quickFetched = gate();
    let quickPosts = 0;
    await page.route(`**/api/v1/projects/${created.id}/tasks`, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      quickPosts += 1;
      const response = await route.fetch(); quickFetched.release(); await quickDelivery.promise; await route.fulfill({ response });
    });
    try {
      await quick.getByRole('button', { name: 'Add task', exact: true }).click();
      await quickFetched.promise;
      await quick.dispatchEvent('submit');
      await expect(quick.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled();
      await expect(quick.getByRole('textbox')).toBeDisabled();
      expect(quickPosts).toBe(1);
      const saved = (await tasks(request, created)).filter((item) => item.title === title);
      expect(saved).toHaveLength(1);
      expect(saved[0].column_id).toBe(ready.id);
      expect((await tasks(request, existing)).filter((item) => item.title === title)).toHaveLength(0);
      await evidence(page, info, 'quick-add-pending', { quickPosts, saved, other: [] });
    } finally { quickDelivery.release(); }
    await expect(quick).toBeHidden();
  });
});
