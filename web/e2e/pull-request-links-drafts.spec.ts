import { expect, test } from '@playwright/test';
import { currentTask, endpoint, fixture, link, links, receipt } from './support/pullRequestLinks';

// Failure scenarios: mutation refresh discards title/URL drafts; switching
// tabs unmounts and clears typed URLs; stale-version retry loses the draft;
// pending navigation abandons a write; another task inherits the old URL.
test.use({ trace: 'on', serviceWorkers: 'block' });

test('tab changes and link refresh preserve unsaved title and URL drafts', async ({ page, request }, info) => {
  const { project, task } = await fixture(request, 'draft-tabs');
  await page.goto(`/p/${project.slug}/tasks/${task.key}`);
  const title = page.getByLabel('Task title', { exact: true });
  const url = page.getByLabel('Pull request URL', { exact: true });
  const titleDraft = 'Synthetic title draft must survive PR refresh';
  const urlDraft = 'https://github.com/synthetic-helm/drafts/pull/1';
  await title.fill(titleDraft);
  await url.fill(urlDraft);
  await page.getByRole('tab', { name: 'Activity', exact: true }).click();
  await page.getByRole('tab', { name: 'Details', exact: true }).click();
  await expect(title).toHaveValue(titleDraft);
  await expect(url).toHaveValue(urlDraft);
  const concurrent = await link(request, task, 'https://github.com/synthetic-helm/drafts/pull/2');
  // The browser still has task.version from before the API mutation.
  await page.getByRole('button', { name: 'Link pull request', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'This task changed elsewhere' })).toBeVisible();
  await expect(url).toHaveValue(urlDraft);
  await expect(title).toHaveValue(titleDraft);
  await page.getByRole('button', { name: 'Refresh task and links', exact: true }).click();
  await expect(page.locator(`a[href="${concurrent.link.url}"]`)).toBeVisible();
  await expect(url).toHaveValue(urlDraft);
  await expect(title).toHaveValue(titleDraft);
  await page.getByRole('button', { name: 'Link pull request', exact: true }).click();
  await expect(page.locator(`a[href="${urlDraft}"]`)).toBeVisible();
  await expect(title).toHaveValue(titleDraft);
  expect((await currentTask(request, task)).title).toBe(task.title);
  expect(await links(request, task)).toHaveLength(2);
  await receipt(info, 'unsaved-title-and-url', { savedTitle: (await currentTask(request, task)).title, titleDraft, savedLinks: await links(request, task) });
  await info.attach('draft-preservation.png', { body: await page.screenshot(), contentType: 'image/png' });
});

test('pending link blocks close and changed drafts require explicit discard before switching tasks', async ({ page, request }, info) => {
  const { project, task, createTask } = await fixture(request, 'pending');
  const other = await createTask('Synthetic other task must have no inherited draft');
  await page.goto(`/p/${project.slug}/tasks/${task.key}`);
  let releaseWrite!: () => void;
  let markIntercepted!: () => void;
  const gate = new Promise<void>((resolve) => { releaseWrite = resolve; });
  const intercepted = new Promise<void>((resolve) => { markIntercepted = resolve; });
  await page.route(`**${endpoint(task)}`, async (route) => {
    if (route.request().method() === 'POST') {
      markIntercepted();
      await gate;
    }
    await route.continue();
  });
  const url = page.getByLabel('Pull request URL', { exact: true });
  const savedURL = 'https://github.com/synthetic-helm/pending/pull/1';
  const nextDraft = 'https://git.synthetic.invalid/synthetic-helm/pending/pulls/2';
  try {
    await url.fill(savedURL);
    await url.press('Enter');
    await intercepted;
    await page.getByRole('button', { name: 'Close task details', exact: true }).click();
    await expect(page.locator('.task-drawer')).toBeVisible();
    await expect(page.getByText('Wait for the pull request link to finish saving.', { exact: true })).toBeVisible();
    await url.fill(nextDraft);
  } finally {
    releaseWrite();
  }
  await expect(page.locator(`a[href="${savedURL}"]`)).toBeVisible();
  await expect(url).toHaveValue(nextDraft);
  await page.unrouteAll({ behavior: 'wait' });
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page.locator('.task-drawer')).toBeVisible();
  await expect(url).toHaveValue(nextDraft);
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page.locator('.task-drawer')).toHaveCount(0);
  await page.goto(`/p/${project.slug}/tasks/${other.key}`);
  await expect(page.getByLabel('Pull request URL', { exact: true })).toHaveValue('');
  expect(await links(request, other)).toEqual([]);
  expect((await links(request, task)).map((item) => item.url)).toEqual([savedURL]);
  await receipt(info, 'pending-navigation-guard', { sourceLinks: await links(request, task), otherTaskLinks: await links(request, other), discardedURL: nextDraft });
  await info.attach('other-task-empty-pr-draft.png', { body: await page.screenshot(), contentType: 'image/png' });
});
