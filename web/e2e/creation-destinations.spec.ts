import { expect, test } from '@playwright/test';
import { columns, evidence, gate, headers, json, origin, project, suffix, taskModal, tasks, type Project, type Task } from './creation-safety-helpers';

// Fault injection must reach the browser; service-worker coverage lives in the PWA suite.
test.use({ serviceWorkers: 'block' });

test.describe('creation destinations', () => {
  for (const kind of ['task', 'bug'] as const) {
    test(`${kind} rejects stale columns across project changes and close/reopen, and keeps failed destinations unavailable`, async ({ page, request }, info) => {
      const first = await project(request);
      const second = await project(request);
      const secondColumns = await columns(request, second);
      const destination = secondColumns.find((item) => kind === 'bug' ? item.name === 'Bugs' : item.semantic_state === 'ready')!;
      await page.goto(`/p/${first.slug}`);
      const open = async () => {
        await page.locator(kind === 'task' ? '[data-task-modal-trigger]' : '[data-report-bug-trigger]').first().click();
        return page.getByRole('dialog', { name: kind === 'task' ? 'Create a task' : 'Report a bug', exact: true });
      };
      let modal = await open();
      const staleDelivery = gate();
      const staleFetched = gate();
      const lateDelivery = gate();
      const lateFetched = gate();
      const staleFinished = gate();
      const lateFinished = gate();
      let reads = 0;
      await page.route(`**/api/v1/projects/${second.id}/columns?*`, async (route) => {
        reads += 1;
        const read = reads;
        if (read === 3) return route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: { message: 'Synthetic columns unavailable' } }) });
        const response = await route.fetch();
        if (read === 1) { staleFetched.release(); await staleDelivery.promise; }
        else if (read === 2) { lateFetched.release(); await lateDelivery.promise; }
        await route.fulfill({ response });
        if (read === 1) staleFinished.release();
        else if (read === 2) lateFinished.release();
      });
      try {
        await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(second.id);
        await staleFetched.promise;
        await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toBeDisabled();
        await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toHaveValue('');
        await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(first.id);
        await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(second.id);
        await lateFetched.promise;
        await modal.getByRole('button', { name: 'Close', exact: true }).click();
        modal = await open();
        await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(second.id);
        await expect(modal.getByRole('alert')).toContainText('Synthetic columns unavailable');
        staleDelivery.release();
        lateDelivery.release();
        await Promise.all([staleFinished.promise, lateFinished.promise]);
        await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toBeDisabled();
        await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toHaveValue('');
        await modal.getByLabel(kind === 'task' ? 'Task title' : 'Bug title', { exact: true }).fill(`Destination safety ${suffix()}`);
        if (kind === 'bug') await modal.getByLabel('Actual behavior', { exact: true }).fill('Synthetic incorrect behavior');
        await expect(modal.getByRole('button', { name: kind === 'task' ? 'Create task' : 'Report bug', exact: true })).toBeDisabled();
        await evidence(page, info, `${kind}-failed-columns`, { reads, first: await tasks(request, first), second: await tasks(request, second) });
        await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(first.id);
        await modal.getByRole('combobox', { name: 'Project', exact: true }).selectOption(second.id);
        await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toHaveValue(destination.id);
        const title = await modal.getByLabel(kind === 'task' ? 'Task title' : 'Bug title', { exact: true }).inputValue();
        await modal.getByRole('button', { name: kind === 'task' ? 'Create task' : 'Report bug', exact: true }).click();
        await expect(modal).toBeHidden();
        expect((await tasks(request, first)).filter((item) => item.title === title)).toHaveLength(0);
        const created = (await tasks(request, second)).filter((item) => item.title === title);
        expect(created).toHaveLength(1);
        expect(created[0].column_id).toBe(destination.id);
        await evidence(page, info, `${kind}-correct-destination`, { first: [], second: created });
      } finally { staleDelivery.release(); lateDelivery.release(); }
    });
  }

  test('freezes bug fields before delayed label resolution and creates one captured report', async ({ page, request }, info) => {
    const intended = await project(request);
    const other = await project(request);
    await page.goto(`/p/${intended.slug}`);
    await page.locator('[data-report-bug-trigger]').first().click();
    const modal = page.getByRole('dialog', { name: 'Report a bug', exact: true });
    await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toBeEnabled();
    const title = `Captured report ${suffix()}`;
    await modal.getByLabel('Bug title', { exact: true }).fill(title);
    await modal.getByLabel('Actual behavior', { exact: true }).fill('Captured behavior before label await');
    await modal.getByLabel('Labels', { exact: false }).fill(`creation-${suffix()}`);
    const delivery = gate();
    const fetched = gate();
    let creates = 0;
    await page.route(`**/api/v1/projects/${intended.id}/labels`, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      const response = await route.fetch(); fetched.release(); await delivery.promise; await route.fulfill({ response });
    });
    await page.route(`**/api/v1/projects/${intended.id}/tasks`, async (route) => {
      if (route.request().method() === 'POST') creates += 1;
      await route.continue();
    });
    try {
      await modal.getByRole('button', { name: 'Report bug', exact: true }).click();
      await fetched.promise;
      await modal.locator('form').dispatchEvent('submit');
      await page.keyboard.press('Escape');
      await expect(modal).toBeVisible();
      await expect(modal.getByLabel('Bug title', { exact: true })).toBeDisabled();
      await expect(modal.getByLabel('Actual behavior', { exact: true })).toBeDisabled();
      await expect(modal.getByRole('combobox', { name: 'Project', exact: true })).toBeDisabled();
      await evidence(page, info, 'bug-pending-label-resolution', { creates, first: await tasks(request, intended), other: await tasks(request, other) });
    } finally { delivery.release(); }
    await expect(modal).toBeHidden();
    const saved = (await tasks(request, intended)).filter((item) => item.title === title);
    expect(saved).toHaveLength(1);
    expect(saved[0].bug?.actual_behavior).toBe('Captured behavior before label await');
    expect(creates).toBe(1);
    expect((await tasks(request, other)).filter((item) => item.title === title)).toHaveLength(0);
    await evidence(page, info, 'bug-captured-persisted-report', { saved, other: [] });
  });

});
