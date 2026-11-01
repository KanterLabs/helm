import { expect, test } from '@playwright/test';
import { evidence, project, tasks } from './creation-safety-helpers';

test('keeps Connect Codex usable during a background account refresh', async ({ page }) => {
  let accountReads = 0;
  await page.route('**/api/v1/codex/account*', async (route) => {
    accountReads += 1;
    if (accountReads > 1) await new Promise((resolve) => setTimeout(resolve, 1_500));
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ connected: false, requires_openai_auth: true })
    });
  });
  await page.route('**/api/v1/codex/login', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      login_id: 'login-refresh-test',
      verification_url: 'https://auth.openai.com/codex/device',
      user_code: 'TEST-CODE'
    })
  }));

  await page.goto('/settings');
  const connect = page.getByRole('button', { name: 'Connect Codex', exact: true });
  await expect(connect).toBeEnabled();

  const navigation = page.getByRole('navigation', { name: 'Primary navigation' });
  await navigation.getByRole('button', { name: 'Issues', exact: true }).click();
  await navigation.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect.poll(() => accountReads).toBeGreaterThan(1);
  await expect(connect).toBeEnabled();
  await connect.click();
  await expect(page.getByText('Finish connecting in ChatGPT')).toBeVisible();
  await expect(page.getByText('TEST-CODE')).toBeVisible();
});

type SavedTask = Awaited<ReturnType<typeof tasks>>[number] & { description: string; priority: string; due_at?: string | null };

// TC-318 retired the Luna panel from ordinary creation. This replaces the old
// preview/apply journey: typed manual fields survive a destination change and
// are persisted together without any Codex account or task-draft request.
test('captures a manual draft across a project change without contacting Luna', async ({ page, request }, info) => {
  const first = await project(request);
  const second = await project(request);
  const retired: string[] = [];
  page.on('request', (sent) => {
    const path = new URL(sent.url()).pathname;
    if (/^\/api\/v1\/(codex\/|projects\/[^/]+\/task-draft)/.test(path)) retired.push(`${sent.method()} ${path}`);
  });

  await page.goto(`/p/${first.slug}`);
  await page.getByRole('button', { name: 'New task', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create a task' });
  const title = dialog.getByLabel('Task title', { exact: true });
  await expect(title).toBeFocused();
  const typedTitle = `Manual capture ${Date.now().toString(36)}`;
  await title.fill(typedTitle);
  await dialog.getByLabel('Description', { exact: false }).fill('Typed by hand.\n\n- [ ] Persisted with the task');
  await dialog.getByRole('combobox', { name: 'Priority', exact: true }).selectOption('high');
  await dialog.getByLabel('Due date', { exact: true }).fill('2026-12-01');
  await dialog.getByRole('combobox', { name: 'Project', exact: true }).selectOption(second.id);
  await expect(dialog.getByRole('combobox', { name: 'Column', exact: true })).toBeEnabled();
  await expect(title).toHaveValue(typedTitle);
  await expect(dialog.getByRole('combobox', { name: 'Priority', exact: true })).toHaveValue('high');
  await dialog.getByRole('button', { name: 'Create task', exact: true }).click();
  await expect(dialog).toBeHidden();

  const saved = ((await tasks(request, second)) as SavedTask[]).filter((task) => task.title === typedTitle);
  expect(saved).toHaveLength(1);
  expect(saved[0]).toMatchObject({ description: 'Typed by hand.\n\n- [ ] Persisted with the task', priority: 'high' });
  expect(saved[0].due_at).toContain('2026-12-01');
  expect((await tasks(request, first)).filter((task) => task.title === typedTitle)).toHaveLength(0);
  expect(retired).toEqual([]);
  await evidence(page, info, 'manual-capture-no-luna', { saved: saved.map(({ priority, due_at, project_id }) => ({ priority, due_at, project_id })), retiredRequests: retired });
});

test('keeps the task modal within the viewport on desktop and mobile', async ({ page, request }, info) => {
  const destination = await project(request);
  for (const viewport of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await page.goto(`/p/${destination.slug}`);
    const trigger = page.getByRole('button', { name: 'New task', exact: true });
    await trigger.click();
    const dialog = page.getByRole('dialog', { name: 'Create a task' });
    await expect(dialog).toBeVisible();
    const geometry = await dialog.evaluate((element) => {
      const box = element.getBoundingClientRect();
      return {
        left: box.left,
        right: box.right,
        viewportWidth: window.innerWidth,
        hasHorizontalOverflow: element.scrollWidth > element.clientWidth + 1
      };
    });
    expect(geometry.left).toBeGreaterThanOrEqual(0);
    expect(geometry.right).toBeLessThanOrEqual(geometry.viewportWidth + 1);
    expect(geometry.hasHorizontalOverflow).toBeFalsy();
    await evidence(page, info, `task-draft-${viewport.width}`, { project_id: destination.id, ...geometry });
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
  }
});
