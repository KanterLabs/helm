import { expect, test } from '@playwright/test';

test.describe('Helm workspace', () => {
  test('renders the Helm identity and preserves retained browser preferences', async ({ page, request }) => {
    await page.addInitScript(() => {
      localStorage.setItem('roadmap.theme', 'dark');
      localStorage.removeItem('helm.theme');
    });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto('/');

    await expect(page).toHaveTitle('Helm');
    await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', /Helm/);
    await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', '/favicon.svg');
    await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
    await expect(page.locator('svg.helm-mark').filter({ visible: true }).first()).toBeVisible();
    await expect(page.locator('.app-shell')).toHaveClass(/dark-mode/);

    const preferences = await page.evaluate(() => ({
      canonical: localStorage.getItem('helm.theme'),
      retained: localStorage.getItem('roadmap.theme')
    }));
    expect(preferences).toEqual({ canonical: 'dark', retained: 'dark' });
    expect(await page.locator('.new-project-button').evaluate((element) => getComputedStyle(element).transitionDuration)).toBe('0s');

    for (const asset of ['/favicon.svg', '/helm-mark.svg']) {
      const response = await request.get(asset);
      expect(response.status()).toBe(200);
      expect(response.headers()['content-type']).toContain('image/svg+xml');
    }
    expect((await request.get('/assets/missing-helm-asset.svg')).status()).toBe(404);
  });


  test('loads in disabled-auth mode and manages a task from the board', async ({ page, request }) => {
    const statusResponse = await request.get('/api/v1/auth/status');
    expect(statusResponse.ok()).toBeTruthy();
    const status = (await statusResponse.json()) as { mode?: string };
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const projectName = `Browser E2E ${runId}`;
    const projectKey = `E2E${runId}`.slice(0, 16);
    const taskTitle = `Quick task ${runId}`;
    const editedTaskTitle = `Edited task ${runId}`;
    const labelName = `Browser label ${runId}`;

    await page.goto('/');
    await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();

    // Create a unique project so this workflow is repeatable against a
    // persistent development database that may already contain demo data.
    await page.getByRole('button', { name: 'New project', exact: true }).click();
    const projectDialog = page.getByRole('dialog', { name: 'Create a project' });
    await expect(projectDialog).toBeVisible();
    await expect(projectDialog.getByLabel('Project name')).toBeFocused();
    await projectDialog.getByRole('button', { name: 'Close', exact: true }).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(projectDialog.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(projectDialog.getByRole('button', { name: 'Close', exact: true })).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(projectDialog).toBeHidden();
    await expect(page.getByRole('button', { name: 'New project', exact: true })).toBeFocused();

    await page.getByRole('button', { name: 'New project', exact: true }).click();
    await expect(projectDialog).toBeVisible();
    await projectDialog.getByLabel('Project name').fill(projectName);
    await projectDialog.getByLabel('Project key').fill(projectKey);
    await projectDialog.getByRole('button', { name: 'Create project', exact: true }).click();
    await expect(projectDialog).toBeHidden();
    await expect(page.getByRole('heading', { name: projectName, exact: true })).toBeVisible();

    const board = page.locator('section.board');
    await expect(board).toBeVisible();
    const backlogColumn = board.locator('.board-column').filter({
      has: page.getByRole('heading', { name: 'Backlog', exact: true })
    });
    const readyColumn = board.locator('.board-column').filter({
      has: page.getByRole('heading', { name: 'Ready', exact: true })
    });
    await expect(backlogColumn).toBeVisible();
    await expect(readyColumn).toBeVisible();

    // Quick-add through the column's user-facing trigger and form.
    await backlogColumn.getByRole('button', { name: /Add task$/ }).click();
    await backlogColumn.getByRole('textbox', { name: 'New task in Backlog' }).fill(taskTitle);
    await backlogColumn.getByRole('button', { name: 'Add task', exact: true }).click();

    const createdCard = board.locator('.task-card').filter({ hasText: taskTitle });
    await expect(createdCard).toBeVisible();
    const taskKey = (await createdCard.locator('.task-key').textContent())?.trim();
    expect(taskKey, 'The created task should expose its stable task key').toMatch(/^[A-Z0-9_-]+-\d+$/);

    // Move with the accessible next-column action, then verify the card's
    // rendered column changed after the API mutation completed.
    await createdCard.locator('[data-card-actions]').click();
    const moveNext = page.getByRole('menuitem', {
      name: `Move ${taskKey} to next column`, exact: true
    });
    await expect(moveNext).toHaveAttribute('aria-disabled', 'false');
    await moveNext.click();
    await expect(readyColumn.locator('.task-card').filter({ hasText: taskTitle })).toBeVisible();
    await expect(backlogColumn.locator('.task-card').filter({ hasText: taskTitle })).toHaveCount(0);

    const movedCard = readyColumn.locator('.task-card').filter({ hasText: taskKey as string });
    await expect(movedCard).toBeVisible();

    // Open the task with its accessible card button and edit the labeled
    // title/priority controls in the task drawer.
    await movedCard.locator('[data-task-trigger]').click();
    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.getByLabel('Task title')).toHaveValue(taskTitle);
    await expect(drawer.locator('[data-dialog-initial-focus]')).toBeFocused();
    await expect(drawer.getByLabel('Task title')).not.toBeFocused();
    const taskWatch = drawer.locator('[data-task-watch-toggle]');
    await expect(taskWatch).toBeEnabled();
    await drawer.getByRole('button', { name: 'Close task details', exact: true }).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(taskWatch).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(drawer.getByRole('button', { name: 'Delete task', exact: true })).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(taskWatch).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(drawer.getByRole('button', { name: 'Close task details', exact: true })).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(drawer).toBeHidden();
    await movedCard.locator('[data-task-trigger]').click();
    await expect(drawer).toBeVisible();
    await drawer.getByLabel('Task title').fill(editedTaskTitle);
    await drawer.getByLabel('Priority').selectOption('high');
    await drawer.getByLabel(/Labels/).fill(labelName);
    await drawer.getByRole('button', { name: 'Save changes', exact: true }).click();

    await expect(movedCard).toContainText(editedTaskTitle);
    await expect(movedCard.locator('.label-chip')).toHaveText(labelName);

    const deleteLabel = drawer.getByRole('button', { name: `Delete label ${labelName}`, exact: true });
    await expect(deleteLabel).toBeVisible();
    await deleteLabel.click();
    const labelDialog = page.getByRole('alertdialog');
    await expect(labelDialog).toContainText(`Delete label “${labelName}”?`);
    await labelDialog.getByRole('button', { name: 'Delete label', exact: true }).click();
    await expect(deleteLabel).toHaveCount(0);
    await expect(movedCard.locator('.label-chip')).toHaveCount(0);

    await drawer.getByRole('button', { name: 'Delete task', exact: true }).click();
    const taskDialog = page.getByRole('alertdialog');
    await expect(taskDialog).toContainText(`Delete ${taskKey}?`);
    await taskDialog.getByRole('button', { name: 'Delete task', exact: true }).click();
    await expect(drawer).toBeHidden();
    await expect(board.locator('.task-card').filter({ hasText: taskKey as string })).toHaveCount(0);

    // The project progress view is a real API-backed route. At this point the
    // task has been deleted, so the project should report no remaining work.
    await page.getByRole('button', { name: 'Progress', exact: true }).click();
    await expect(page).toHaveURL(/\/p\/[^/]+\/roadmap\/?$/);
    await expect(page.getByRole('heading', { name: `${projectName} progress`, exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Project progress', exact: true })).toBeVisible();
    await expect(page.getByText('0 total tasks', { exact: true })).toBeVisible();
    await expect(page.getByText('0 completed', { exact: true })).toBeVisible();

    const commandTrigger = page.getByRole('button', { name: 'Search anything', exact: true });
    await commandTrigger.click();
    const commandDialog = page.getByRole('dialog', { name: 'Search Helm' });
    const commandInput = commandDialog.getByRole('combobox', { name: 'Search projects and views, tasks, issues, and actions' });
    await expect(commandInput).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(commandDialog.getByRole('option').last()).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(commandInput).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(commandDialog).toBeHidden();
    await expect(commandTrigger).toBeFocused();
  });
});
