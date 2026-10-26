import { expect, test } from '@playwright/test';
import { collectionData, columnFor, createProject, createTask, escapeRegExp, json, mutationHeaders, type Collection, type Column, type Task } from './support/taskDrawerFixture';

// Browser fault injection must bypass SW interception; real SW coverage has its own suite.
test.use({ serviceWorkers: 'block' });

test.describe('task drawer safeguards', () => {
  test('guards dirty drafts when switching tasks from My Work and the command palette', async ({ page, request }, testInfo) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const project = await createProject(request, suffix);
    const columns = collectionData(await json<Collection<Column> | Column[]>(
      await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
      'list navigation columns'
    ));
    const ready = columnFor(columns, 'ready');
    const source = await createTask(request, project, ready, `Navigation source ${suffix}`);
    const issue = await createTask(request, project, ready, `Navigation issue ${suffix}`, {
      kind: 'bug',
      bug: {
        actual_behavior: 'The command palette can replace a draft.',
        expected_behavior: 'Task switching asks before replacing a draft.',
        reproduction_steps: 'Edit one task, then open another task.',
        environment: 'Browser E2E',
        affected_version: 'drawer-test'
      }
    });
    let work = await createTask(request, project, ready, `Navigation work ${suffix}`);
    work = await json<Task>(await request.post(`/api/v1/tasks/${work.id}/claim`, {
      data: { lease_seconds: 300 },
      headers: mutationHeaders(work.version)
    }), 'claim navigation work task');

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/p/${project.slug}`);
    const board = page.locator('section.board');
    const drawer = page.locator('.task-drawer');
    const sourceCard = board.locator('.task-card').filter({ hasText: source.key });
    await sourceCard.locator('[data-task-trigger]').click();
    await expect(drawer).toBeVisible();

    const myWorkDraft = `My Work draft ${suffix}`;
    await drawer.getByLabel('Task title').fill(myWorkDraft);
    const sourceURL = page.url();
    const myWorkNavigation = page.locator('nav.sidebar').getByRole('button', { name: 'My work', exact: true });
    const dismissMyWorkDialog = page.waitForEvent('dialog').then((dialog) => dialog.dismiss());
    await myWorkNavigation.dispatchEvent('click');
    await dismissMyWorkDialog;
    await expect(page).toHaveURL(sourceURL);
    await expect(drawer).toBeVisible();
    await expect(drawer.getByLabel('Task title')).toHaveValue(myWorkDraft);
    const sourceAfterCancel = await json<Task>(await request.get(`/api/v1/tasks/${source.id}`), 'read source after canceled navigation');
    expect(sourceAfterCancel.title).toBe(source.title);

    const acceptMyWorkDialog = page.waitForEvent('dialog').then((dialog) => dialog.accept());
    await myWorkNavigation.dispatchEvent('click');
    await acceptMyWorkDialog;
    await expect(page).toHaveURL(/\/my-work\/?$/);
    await expect(drawer).toBeHidden();
    const sourceAfterDiscard = await json<Task>(await request.get(`/api/v1/tasks/${source.id}`), 'read source after discarded navigation');
    expect(sourceAfterDiscard.title).toBe(source.title);
    const workSource = page.getByRole('group', { name: 'My work source' });
    const assignedToggle = workSource.getByRole('button', { name: 'Assigned', exact: true });
    await assignedToggle.dispatchEvent('click');
    const workRow = page.locator('.work-list .work-row').filter({ hasText: work.title });
    await expect(workRow).toBeVisible();
    await workRow.click();
    await expect(drawer).toBeVisible();
    await expect(drawer.getByLabel('Task title')).toHaveValue(work.title);

    // Keep the loaded issue collection while returning to the board so the
    // command palette can resolve the issue choice without a second fetch.
    await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
    await page.locator('nav.sidebar').getByRole('button', { name: 'Issues', exact: true }).dispatchEvent('click');
    await expect(page).toHaveURL(/\/issues\/?$/);
    await expect(page.getByRole('button', { name: new RegExp(escapeRegExp(issue.title)) })).toBeVisible();
    await page.locator('nav.sidebar').getByRole('button', { name: 'Go to current project', exact: true }).dispatchEvent('click');
    await expect(page).toHaveURL(new RegExp(`/p/${escapeRegExp(project.slug)}/?$`));
    await sourceCard.locator('[data-task-trigger]').click();
    await expect(drawer).toBeVisible();
    const commandSourceURL = page.url();
    const commandDraft = `Command palette draft ${suffix}`;
    await drawer.getByLabel('Task title').fill(commandDraft);
    await page.keyboard.press('Control+k');
    const commandDialog = page.getByRole('dialog', { name: 'Search Helm' });
    await expect(commandDialog).toBeVisible();
    const commandInput = commandDialog.getByRole('combobox', { name: 'Search projects and views, tasks, issues, and actions' });
    await commandInput.fill(issue.title);
    const issueChoice = commandDialog.getByRole('option', { name: new RegExp(escapeRegExp(issue.title)) });
    await expect(issueChoice).toBeVisible();
    const dismissCommandDialog = page.waitForEvent('dialog').then((dialog) => dialog.dismiss());
    await issueChoice.click();
    await dismissCommandDialog;
    await expect(page).toHaveURL(commandSourceURL);
    await expect(drawer.getByLabel('Task title')).toHaveValue(commandDraft);

    await page.keyboard.press('Control+k');
    const secondCommandDialog = page.getByRole('dialog', { name: 'Search Helm' });
    await secondCommandDialog.getByRole('combobox', { name: 'Search projects and views, tasks, issues, and actions' }).fill(issue.title);
    const secondIssueChoice = secondCommandDialog.getByRole('option', { name: new RegExp(escapeRegExp(issue.title)) });
    await expect(secondIssueChoice).toBeVisible();
    const acceptCommandDialog = page.waitForEvent('dialog').then((dialog) => dialog.accept());
    await secondIssueChoice.click();
    await acceptCommandDialog;
    await expect(drawer.getByLabel('Task title')).toHaveValue(issue.title);
    const persistedSource = await json<Task>(await request.get(`/api/v1/tasks/${source.id}`), 'read source after command navigation');
    expect(persistedSource.title).toBe(source.title);
    await testInfo.attach('discard-navigation-persisted-state.json', {
      body: JSON.stringify({ source: { key: source.key, title: persistedSource.title }, openedTask: issue.key, canceledNavigationURL: new URL(commandSourceURL).pathname }),
      contentType: 'application/json'
    });
    await testInfo.attach('discard-navigation-opened-issue.png', { body: await page.screenshot(), contentType: 'image/png' });
  });


});
