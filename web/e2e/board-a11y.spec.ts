import { expect, test } from '@playwright/test';
import { columns, fixture, installFixture, openBoard } from './support/boardA11yFixture';

test.describe('board accessibility regressions', () => {
  test('keeps 3, 5, and 8 columns in one horizontal flow on desktop and mobile', async ({ page }) => {
    const state = fixture(3);
    await installFixture(page, state);
    await page.setViewportSize({ width: 1280, height: 900 });
    for (const count of [3, 5, 8]) {
      state.columns = columns(count, state.project.id);
      state.task.column_id = state.columns[0].id;
      state.task.version = 1;
      state.deleted = false;
      // Re-enter the app for each fixture size so the board loader starts
      // from a clean client state while the route fixture remains installed.
      await page.goto('/');
      const board = page.locator('section.board');
      await expect(board.locator('.board-column')).toHaveCount(count);
      const desktopMetrics = await board.evaluate((element) => {
        const style = getComputedStyle(element);
        return { flow: style.gridAutoFlow, template: style.gridTemplateColumns, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth };
      });
      expect(desktopMetrics.flow).toBe('column');
      expect(desktopMetrics.template).not.toContain('repeat');
      if (count === 8) expect(desktopMetrics.scrollWidth).toBeGreaterThan(desktopMetrics.clientWidth);
    }

    await page.setViewportSize({ width: 390, height: 844 });
    for (const count of [3, 5, 8]) {
      state.columns = columns(count, state.project.id);
      state.task.column_id = state.columns[0].id;
      await page.goto('/');
      const board = page.locator('section.board');
      await expect(board.locator('.board-column')).toHaveCount(count);
      const mobileMetrics = await board.evaluate((element) => {
        const style = getComputedStyle(element);
        return { flow: style.gridAutoFlow, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth };
      });
      expect(mobileMetrics.flow).toBe('column');
      if (count === 8) expect(mobileMetrics.scrollWidth).toBeGreaterThan(mobileMetrics.clientWidth);
    }
  });

  test('uses a dedicated drag handle, keeps keyboard movement, and exposes drop insertion feedback', async ({ page }) => {
    const state = fixture(5);
    await installFixture(page, state);
    await page.setViewportSize({ width: 1280, height: 900 });
    await openBoard(page, state);
    const card = page.locator('.task-card').filter({ hasText: state.task.title });
    const handle = card.locator('.task-drag-handle');
    await expect(card).not.toHaveAttribute('draggable', 'true');
    await expect(handle).toHaveAttribute('draggable', 'true');
    await expect(handle).toHaveAttribute('aria-label', /Drag A11Y-1/);

    await page.evaluate(() => {
      const source = document.querySelector<HTMLButtonElement>('.task-drag-handle');
      const target = document.querySelectorAll<HTMLElement>('.board-column')[1];
      const dataTransfer = new DataTransfer();
      dataTransfer.setData('text/plain', 'task-a11y');
      source?.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer }));
      target?.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, dataTransfer }));
    });
    const targetColumn = page.locator('.board-column').nth(1);
    await expect(targetColumn).toHaveClass(/drop-target/);
    await expect(targetColumn.locator('.drop-placeholder')).toContainText('Drop task in Column 2');
    await targetColumn.dispatchEvent('dragleave');
    await expect(targetColumn).not.toHaveClass(/drop-target/);

    await card.locator('.task-main').focus();
    await page.keyboard.press('Alt+ArrowRight');
    await expect(page.locator('.board-column').nth(1).locator('.task-card')).toContainText(state.task.title);
    await expect(page.getByRole('button', { name: 'Undo' })).toBeVisible();
  });

  test('keeps destructive confirmations named and supports cancel, Undo, and conflict-safe retry', async ({ page }) => {
    const state = fixture(3);
    await installFixture(page, state);
    await page.setViewportSize({ width: 1280, height: 900 });
    await openBoard(page, state);
    const card = page.locator('.task-card').filter({ hasText: state.task.title });
    await card.locator('.task-main').click();
    const drawer = page.locator('.task-drawer');

    const deleteLabel = drawer.getByRole('button', { name: 'Delete label Accessibility', exact: true });
    await deleteLabel.click();
    const labelDialog = page.getByRole('alertdialog');
    await expect(labelDialog).toContainText('Delete label “Accessibility”?');
    await labelDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(deleteLabel).toBeVisible();
    await deleteLabel.click();
    await labelDialog.getByRole('button', { name: 'Delete label', exact: true }).click();
    await expect(deleteLabel).toHaveCount(0);
    await expect(page.locator('#drawer-title')).toBeFocused();

    await drawer.getByRole('button', { name: 'Delete task', exact: true }).click();
    const taskDialog = page.getByRole('alertdialog');
    await expect(taskDialog).toContainText('Delete A11Y-1?');
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('dialog', { name: 'Search Helm' })).toHaveCount(0);
    await expect(taskDialog).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(taskDialog).toBeHidden();
    await expect(drawer).toBeVisible();
    await drawer.getByRole('button', { name: 'Delete task', exact: true }).click();
    await taskDialog.getByRole('button', { name: 'Delete task', exact: true }).click();
    await expect(drawer).toBeHidden();
    await expect(card).toHaveCount(0);
    await expect(page.locator('main.content')).toBeFocused();
    await expect(page.getByRole('button', { name: 'Undo', exact: true })).toBeVisible();

    state.conflictNextRestore = true;
    await page.getByRole('button', { name: 'Undo', exact: true }).click();
    await expect(page.locator('.toast.error')).toContainText('Task changed elsewhere', { timeout: 10_000 });
    await expect(card).toHaveCount(0);

    // A fresh delete/restore cycle verifies the successful action path after
    // the conflict, while the original task snapshot remains version-guarded.
    state.conflictNextRestore = false;
    await page.reload();
    await expect(page.locator('.task-card')).toHaveCount(0);
  });

  test('keeps AgentPulse progress, exact/relative update, and claim expiry in reachable text', async ({ page }) => {
    const state = fixture(3);
    await installFixture(page, state);
    await openBoard(page, state);
    const card = page.locator('.task-card').filter({ hasText: state.task.title });
    const pulse = card.locator('[data-agent-pulse]');
    const expandToggle = card.getByRole('button', { name: `Show details for ${state.task.key}` });
    await expect(card).toHaveCSS('height', '184px');
    await expect(expandToggle).toHaveAttribute('aria-expanded', 'false');
    await expect(pulse, 'board cards keep live-work details hidden until expanded').toBeHidden();
    await expect(pulse.locator('.agent-pulse-copy')).toHaveCount(0);

    await expandToggle.click();
    await expect(card).toHaveCSS('height', '260px');
    await expect(card.getByRole('button', { name: `Hide details for ${state.task.key}` })).toHaveAttribute('aria-expanded', 'true');
    await expect(pulse).toBeVisible();
    await expect(pulse).toHaveAttribute('role', 'group');
    await expect(pulse).not.toHaveAttribute('role', 'img');
    await expect(pulse).not.toHaveAttribute('aria-label');
    await expect(pulse).not.toHaveAttribute('aria-describedby');
    await expect(pulse.locator('.agent-pulse-badge-label')).toHaveText('Working');
    await expect(pulse.locator('.agent-pulse-freshness')).toContainText('Stale check-in');
    await expect(pulse.locator('.agent-pulse-copy')).toHaveCount(0);

    await card.locator('[data-task-trigger]').click();
    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();
    const workPanel = drawer.locator('.agent-work-panel');
    await expect(workPanel).toBeVisible();
    await expect(workPanel.locator('.agent-work-details')).toContainText('Regression coverage');
    await expect(workPanel.locator('.agent-work-details')).toContainText('1 of 2 checkpoints (50%)');
    await expect(workPanel.locator('.agent-work-details')).toContainText('Testing board accessibility.');
    await expect(workPanel.locator('.agent-work-details')).toContainText('Review the result.');

    const detailedPulse = workPanel.locator('.agent-pulse');
    await expect(detailedPulse).toContainText('Last check-in');
    await expect(detailedPulse).toContainText('2026-09-03T23:00:00.000Z');
    await expect(detailedPulse).toContainText('Claimed by Build bot');
    await expect(detailedPulse).toContainText('2030-01-02T03:04:05.000Z');
  });

  test('keeps drag and movement menu controls at least 44px on mobile', async ({ page }, testInfo) => {
    const state = fixture(3);
    await installFixture(page, state);
    await page.setViewportSize({ width: 390, height: 844 });
    await openBoard(page, state);
    const card = page.locator('.task-card').filter({ hasText: state.task.title });
    for (const selector of ['.task-drag-handle', '[data-card-actions]']) {
      const boxes = await card.locator(selector).evaluateAll((elements) => elements.map((element) => {
        const box = element.getBoundingClientRect();
        return { width: box.width, height: box.height };
      }));
      expect(boxes.length, `${selector} must expose a touch target`).toBeGreaterThan(0);
      for (const box of boxes) {
        expect(box.width).toBeGreaterThanOrEqual(44);
        expect(box.height).toBeGreaterThanOrEqual(44);
      }
    }
    await card.locator('[data-card-actions]').click();
    const movementMenu = page.getByRole('menu', { name: `Actions for ${state.task.key}`, exact: true });
    await expect(movementMenu.getByRole('menuitem')).toHaveCount(6);
    const bounds = await movementMenu.getByRole('menuitem').evaluateAll((elements) => elements.map((element) => {
      const box = element.getBoundingClientRect();
      return { width: box.width, height: box.height };
    }));
    for (const box of bounds) {
      expect(box.width).toBeGreaterThanOrEqual(44);
      expect(box.height).toBeGreaterThanOrEqual(44);
    }
    await testInfo.attach('mobile-movement-target-bounds.json', { body: JSON.stringify(bounds, null, 2), contentType: 'application/json' });
    await testInfo.attach('mobile-movement-menu.png', { body: await page.screenshot(), contentType: 'image/png' });
    await page.keyboard.press('Escape');
    await expect(card.locator('[data-card-actions]')).toBeFocused();
  });

  test('keeps meaningful board text at a readable minimum', async ({ page }) => {
    const state = fixture(3);
    state.task.dependency_summary = {
      prerequisite_count: 1,
      unmet_prerequisite_count: 1,
      dependent_count: 0,
      blocked: true
    };
    state.task.dependency_relations = {
      prerequisites: [{
        id: 'task-dependency-a11y',
        key: 'A11Y-2',
        title: 'Readable dependency fixture',
        completed_at: null,
        satisfied: false
      }],
      dependents: []
    };
    await installFixture(page, state);
    await page.setViewportSize({ width: 1280, height: 900 });
    await openBoard(page, state);
    const sizes = await page.locator('.task-key, .task-excerpt, .column-count, .agent-pulse-copy strong, .agent-pulse-phase, .agent-pulse-claim, .dependency-badge').evaluateAll((elements) =>
      elements.map((element) => ({ selector: element.className, size: Number.parseFloat(getComputedStyle(element).fontSize) }))
    );
    expect(sizes.length).toBeGreaterThan(0);
    expect(sizes.every(({ size }) => size >= 11)).toBe(true);
    await expect(page.locator('.task-drag-handle')).toHaveCSS('font-size', '16px');

    await page.locator('.task-card').filter({ hasText: state.task.title }).locator('.task-main').click();
    const drawer = page.locator('.task-drawer');
    await expect(drawer.locator('.dependency-notice')).toBeVisible();
    await expect(drawer.locator('.relationship-grid h3')).toHaveCount(2);
    const dependencySizes = await drawer.locator([
      '.dependency-notice strong',
      '.dependency-notice small',
      '.dependency-heading p',
      '.dependency-blocked',
      '.dependency-ready',
      '.relationship-grid h3',
      '.relationship-grid h3 span',
      '.relationship-key',
      '.relationship-title',
      '.relationship-state',
      '.relationship-empty',
      '.dependency-add > label',
      '.dependency-add > small'
    ].join(', ')).evaluateAll((elements) => elements.map((element) => ({
      selector: element.className,
      size: Number.parseFloat(getComputedStyle(element).fontSize)
    })));
    expect(dependencySizes.length).toBeGreaterThan(0);
    expect(dependencySizes.every(({ size }) => size >= 11)).toBe(true);
    await expect(drawer.locator('.notice-icon')).toHaveCSS('font-size', '13px');
    await expect(drawer.locator('.remove-relationship')).toHaveCSS('font-size', '16px');
  });

  test('replaces token revoke with the same named confirm dialog', async ({ page }) => {
    const state = fixture(3);
    await installFixture(page, state);
    await page.goto('/');
    await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
    await expect(page.getByRole('heading', { name: 'Agents & tokens', exact: true })).toBeVisible();
    const revoke = page.getByRole('button', { name: 'Revoke Browser token', exact: true });
    await revoke.click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toContainText('Revoke token “Browser token”?');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(revoke).toBeVisible();
    await revoke.click();
    await dialog.getByRole('button', { name: 'Revoke token', exact: true }).click();
    await expect(revoke).toHaveCount(0);
    await expect(page.locator('main.content')).toBeFocused();
  });

  test('restores a deleted bug in the current Issues view', async ({ page }) => {
    const state = fixture(3);
    await installFixture(page, state);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto('/issues');
    await expect(page.locator('.issue-row')).toHaveCount(1);
    await page.locator('.issue-row').click();
    const drawer = page.locator('.task-drawer');
    await drawer.getByRole('button', { name: 'Delete task', exact: true }).click();
    const dialog = page.getByRole('alertdialog');
    await dialog.getByRole('button', { name: 'Delete task', exact: true }).click();
    await expect(drawer).toBeHidden();
    await expect(page.locator('.issue-row')).toHaveCount(0);
    const restoreResponse = page.waitForResponse((response) => response.url().includes(`/api/v1/tasks/${state.task.id}/restore`));
    await page.getByRole('button', { name: 'Undo', exact: true }).click();
    await expect((await restoreResponse).status()).toBe(200);
    expect(state.restoreCalls).toBe(1);
    expect(state.deleted).toBe(false);
    await expect(page.locator('.issue-row')).toHaveCount(1);
    await expect(page.locator('.issue-row')).toContainText(state.task.title);
  });

  test('does not put Undo from another project on the active board', async ({ page }) => {
    const state = fixture(3);
    state.otherProject = {
      id: 'project-other',
      key: 'OTHER',
      slug: 'other-board',
      name: 'Other board',
      description: 'A second mocked project.',
      color: '#35b88a',
      favorite: false
    };
    state.otherColumns = columns(3, state.otherProject.id);
    await installFixture(page, state);
    await page.setViewportSize({ width: 1280, height: 900 });
    await openBoard(page, state);
    const card = page.locator('.task-card').filter({ hasText: state.task.title });
    await card.locator('.task-main').click();
    const drawer = page.locator('.task-drawer');
    await drawer.getByRole('button', { name: 'Delete task', exact: true }).click();
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete task', exact: true }).click();
    await expect(drawer).toBeHidden();
    await page.getByRole('button', { name: state.otherProject.name, exact: true }).click();
    await expect(page.locator('section.board')).toBeVisible();
    await expect(page.locator('section.board .task-card')).toHaveCount(0);
    await page.getByRole('button', { name: 'Undo', exact: true }).click();
    await expect(page.locator('section.board .task-card')).toHaveCount(0);
  });
});
