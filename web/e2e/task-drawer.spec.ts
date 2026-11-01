import { expect, test } from '@playwright/test';
import { collectionData, columnFor, createProject, createTask, json, mutationHeaders, type Collection, type Column, type Task } from './support/taskDrawerFixture';

// Browser fault injection must bypass SW interception; real SW coverage has its own suite.
test.use({ serviceWorkers: 'block' });

test.describe('task drawer safeguards', () => {
  test.afterEach(async ({ page }) => {
    // Finish task response rewrites before the request context is disposed.
    await page.unrouteAll({ behavior: 'wait' });
  });

  test('keeps bug drafts safe through triage, close guards, focus, and one-scroll layouts', async ({ page, request }) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const project = await createProject(request, suffix);
    const columns = collectionData(await json<Collection<Column> | Column[]>(
      await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
      'list task drawer columns'
    ));
    const ready = columnFor(columns, 'ready');
    const bug = await createTask(request, project, ready, `Draft-safe bug ${suffix}`, {
      kind: 'bug',
      bug: {
        actual_behavior: 'The drawer loses context during triage.',
        expected_behavior: 'Triage keeps all draft context.',
        reproduction_steps: 'Open the bug and edit its title.',
        environment: 'Browser E2E',
        affected_version: 'drawer-test'
      }
    });

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/p/${project.slug}`);
    const board = page.locator('section.board');
    const card = board.locator('.task-card').filter({ hasText: bug.key });
    const trigger = card.locator('[data-task-trigger]');
    await expect(trigger).toBeVisible();
    await trigger.focus();
    await trigger.click();

    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('[data-dialog-initial-focus]')).toBeFocused();
    await expect(drawer.getByLabel('Task title')).not.toBeFocused();

    const saveBar = drawer.locator('.drawer-save-bar');
    await expect(saveBar).toContainText('All changes saved');
    expect(await saveBar.evaluate((element) => getComputedStyle(element).position)).toBe('sticky');

    const editedTitle = `Triage keeps this draft ${suffix}`;
    await drawer.getByLabel('Task title').fill(editedTitle);
    await expect(saveBar).toContainText('Unsaved changes');
    await expect(saveBar.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
    await drawer.getByRole('button', { name: 'Triage issue', exact: true }).click();
    await expect(drawer.getByRole('button', { name: 'Update triage', exact: true })).toBeVisible();
    await expect(drawer.getByLabel('Task title')).toHaveValue(editedTitle);
    const triaged = await json<Task>(await request.get(`/api/v1/tasks/${bug.id}`), 'reload triaged bug');
    expect(triaged.title).toBe(editedTitle);

    const resolvedTitle = `Resolve keeps this draft ${suffix}`;
    await drawer.getByLabel('Task title').fill(resolvedTitle);
    await drawer.getByLabel(/Resolution note/).fill('The drawer draft survives resolution.');
    await drawer.getByRole('button', { name: 'Resolve issue', exact: true }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(resolvedTitle);
    await expect(drawer.getByRole('button', { name: 'Reopen issue', exact: true })).toBeVisible();
    await drawer.getByLabel('Reopen reason').fill('The regression needs another look.');
    await drawer.getByRole('button', { name: 'Reopen issue', exact: true }).click();
    await expect(drawer.getByRole('button', { name: 'Resolve issue', exact: true })).toBeVisible();
    await expect(drawer.getByLabel('Task title')).toHaveValue(resolvedTitle);
    const reopened = await json<Task>(await request.get(`/api/v1/tasks/${bug.id}`), 'reload reopened bug');
    expect(reopened.title).toBe(resolvedTitle);

    // A clean drawer closes immediately and returns focus to its card trigger.
    await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
    await expect(drawer).toBeHidden();
    await expect(trigger).toBeFocused();

    // The backdrop uses the same dirty guard as the close button.
    await trigger.click();
    await expect(drawer).toBeVisible();
    const backdropTitle = `Backdrop keeps this draft ${suffix}`;
    await drawer.getByLabel('Task title').fill(backdropTitle);
    page.once('dialog', (dialog) => dialog.dismiss());
    await page.locator('.drawer-backdrop').click({ position: { x: 10, y: 10 } });
    await expect(drawer.getByLabel('Task title')).toHaveValue(backdropTitle);
    page.once('dialog', (dialog) => dialog.accept());
    await page.locator('.drawer-backdrop').click({ position: { x: 10, y: 10 } });
    await expect(drawer).toBeHidden();

    // Dirty Escape is guarded; dismissing the confirmation leaves both the
    // drawer and the draft intact, while accepting it closes normally.
    await trigger.click();
    await expect(drawer).toBeVisible();
    const dirtyTitle = `Discard confirmation ${suffix}`;
    await drawer.getByLabel('Task title').fill(dirtyTitle);
    page.once('dialog', (dialog) => dialog.dismiss());
    await page.keyboard.press('Escape');
    await expect(drawer).toBeVisible();
    await expect(drawer.getByLabel('Task title')).toHaveValue(dirtyTitle);
    page.once('dialog', (dialog) => dialog.accept());
    await page.keyboard.press('Escape');
    await expect(drawer).toBeHidden();

    // The bug report is in the drawer's main scroll region, not a nested
    // scrollbox. Check both target viewport classes and header stability.
    for (const viewport of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport);
      await trigger.click();
      await expect(drawer).toBeVisible();
      const drawerScroll = drawer.locator('[data-drawer-scroll]');
      await expect(drawerScroll).toHaveCount(1);
      await expect(drawer.locator('.drawer-activity-scroll')).toHaveCount(0);
      const scrollStyle = await drawer.locator('.drawer-bug-controls').evaluate((element) => ({
        overflowY: getComputedStyle(element).overflowY,
        maxHeight: getComputedStyle(element).maxHeight
      }));
      expect(scrollStyle.overflowY).toBe('visible');
      expect(scrollStyle.maxHeight).toBe('none');
      const headerBefore = await drawer.locator('.drawer-header').boundingBox();
      await drawerScroll.evaluate((element) => { element.scrollTop = element.scrollHeight; });
      const headerAfter = await drawer.locator('.drawer-header').boundingBox();
      expect(headerAfter?.y).toBe(headerBefore?.y);
      await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
      await expect(drawer).toBeHidden();
    }
  });

  test('shows claim lease state and preserves drafts through renew, release, block, and conflicts', async ({ page, request }) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const project = await createProject(request, suffix);
    const columns = collectionData(await json<Collection<Column> | Column[]>(
      await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
      'list claim columns'
    ));
    const ready = columnFor(columns, 'ready');
    let claimed = await createTask(request, project, ready, `Claim lifecycle ${suffix}`);
    let conflict = await createTask(request, project, ready, `Claim conflict ${suffix}`);

    claimed = await json<Task>(await request.post(`/api/v1/tasks/${claimed.id}/claim`, {
      data: { lease_seconds: 300 },
      headers: mutationHeaders(claimed.version)
    }), 'claim lifecycle task');
    conflict = await json<Task>(await request.post(`/api/v1/tasks/${conflict.id}/claim`, {
      data: { lease_seconds: 300 },
      headers: mutationHeaders(conflict.version)
    }), 'seed conflict task');

    // Only the foreign task is synthetic; lifecycle actions on the owned task
    // keep using the real API. A captured response avoids racing a live poll
    // through route.fetch while the browser cancels an obsolete request.
    await page.route((url) => url.pathname === `/api/v1/tasks/${conflict.id}`, (route) => {
      if (route.request().method() !== 'GET') return route.continue();
      return route.fulfill({
        json: {
          ...conflict,
          claimed_by: { id: 'foreign-agent', kind: 'agent', name: 'Foreign agent' },
          claim_expires_at: new Date(Date.now() + 240_000).toISOString()
        }
      });
    });

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/p/${project.slug}`);
    const board = page.locator('section.board');
    const claimedCard = board.locator('.task-card').filter({ hasText: claimed.title });
    const claimedPulse = claimedCard.locator('.agent-pulse');
    const expandToggle = claimedCard.getByRole('button', { name: `Show details for ${claimed.key}` });
    await expect(claimedCard).toHaveCSS('height', '160px');
    await expect(expandToggle).toHaveAttribute('aria-expanded', 'false');
    await expect(claimedPulse, 'board cards keep claim detail hidden until expanded').toBeHidden();
    await expect(claimedPulse.locator('.agent-pulse-copy')).toHaveCount(0);

    await expandToggle.click();
    await expect(claimedCard).toHaveCSS('height', '260px');
    await expect(claimedPulse).toBeVisible();
    await expect(claimedPulse.locator('.agent-pulse-badge-label')).toHaveText('No live pulse');
    await expect(claimedPulse.locator('.agent-pulse-copy')).toHaveCount(0);
    await claimedCard.getByRole('button', { name: `Hide details for ${claimed.key}` }).click();
    await expect(claimedCard).toHaveCSS('height', '160px');
    await expect(claimedPulse).toBeHidden();

    await claimedCard.locator('[data-task-trigger]').click();
    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('.claim-lease')).toContainText('Task version v');
    await expect(drawer.locator('.claim-lease')).toContainText('Expires');
    await expect(drawer.locator('.claim-lease')).toContainText(/\d+m left/);
    await expect(drawer.locator('.claim-lease')).toContainText('Expiring soon');

    const draftTitle = `Draft survives claim actions ${suffix}`;
    await drawer.getByLabel('Task title').fill(draftTitle);
    await drawer.getByRole('button', { name: /Renew claim/ }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(draftTitle);
    await drawer.getByRole('button', { name: 'Release claim', exact: true }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(draftTitle);
    await drawer.getByRole('button', { name: /Claim task/ }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(draftTitle);
    await drawer.getByRole('button', { name: 'Release claim', exact: true }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(draftTitle);

    await drawer.getByRole('button', { name: '■ Block', exact: true }).click();
    const reason = `Waiting on a decision ${suffix}`;
    await drawer.getByRole('textbox', { name: 'Why is this task blocked?' }).fill(reason);
    await drawer.getByRole('button', { name: 'Block task', exact: true }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(draftTitle);
    await drawer.getByRole('tab', { name: 'Activity', exact: true }).click();
    await expect(drawer.locator('.task-timeline')).toContainText(reason, { timeout: 20_000 });
    page.once('dialog', (dialog) => dialog.accept());
    await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
    await expect(drawer).toBeHidden();

    const conflictCard = board.locator('.task-card').filter({ hasText: conflict.title });
    await conflictCard.locator('[data-task-trigger]').click();
    await expect(drawer.locator('.claim-lease')).toHaveAttribute('role', 'alert');
    await expect(drawer.locator('.claim-lease')).toContainText('Foreign agent');
    await expect(drawer.locator('.claim-lease')).toContainText('Task version v');
    await expect(drawer.getByRole('button', { name: /Claimed by Foreign agent/ })).toBeDisabled();
    await expect(drawer.getByRole('button', { name: /Renew claim/ })).toHaveCount(0);
  });


});
