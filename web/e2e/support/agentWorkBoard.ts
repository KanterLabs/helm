// Board cards, agent-work filters and the drawer work panel, including a live remote update.
import { expect, type Page } from '@playwright/test';
import type { AgentWorkFixture } from './agentWorkFixture';

export async function verifyBoardAndDrawerWork(page: Page, fx: AgentWorkFixture): Promise<void> {
  await page.goto(`/p/${encodeURIComponent(fx.projectA.slug)}`);
  await expect(page.getByRole('heading', { name: fx.projectAName, exact: true })).toBeVisible();
  const board = page.locator('section.board');
  await expect(board).toBeVisible();

  const workingCard = board.locator('.task-card').filter({ hasText: fx.tasks.working.title });
  await expect(workingCard).toBeVisible();
  const compactPulse = workingCard.locator('.agent-pulse');
  const expandToggle = workingCard.getByRole('button', { name: `Show details for ${fx.tasks.working.key}` });
  await expect(workingCard).toHaveCSS('height', '128px');
  await expect(expandToggle).toHaveAttribute('aria-expanded', 'false');
  await expect(compactPulse, 'board cards keep live-work details hidden until expanded').toBeHidden();
  await expect(compactPulse.locator('.agent-pulse-copy')).toHaveCount(0);

  await expandToggle.click();
  await expect(workingCard).toHaveCSS('height', '220px');
  await expect(workingCard.getByRole('button', { name: `Hide details for ${fx.tasks.working.key}` })).toHaveAttribute('aria-expanded', 'true');
  await expect(compactPulse).toBeVisible();
  await expect(compactPulse.locator('.agent-pulse-badge-label')).toHaveText('Working');
  await expect(compactPulse.locator('.agent-pulse-copy')).toHaveCount(0);

  await workingCard.getByRole('button', { name: `Hide details for ${fx.tasks.working.key}` }).click();
  await expect(workingCard).toHaveCSS('height', '128px');
  await expect(compactPulse).toBeHidden();

  const missingCard = board.locator('.task-card').filter({ hasText: fx.tasks.missing.title });
  await expect(missingCard).toBeVisible();
  const completedCard = board.locator('.task-card').filter({ hasText: fx.tasks.completed.title });
  await expect(completedCard).toBeVisible();
  await expect(completedCard.locator('.agent-pulse'), 'completed cards hide retained live-work snapshots').toHaveCount(0);
  const boardWorkFilter = page.getByLabel('Filter by agent work');
  const selectBoardWorkFilter = async (value: string) => {
    await page.locator('[data-board-filter-trigger]').click();
    await boardWorkFilter.selectOption(value);
    await page.locator('.board-filter-panel .filter-panel-done').click();
  };
  await selectBoardWorkFilter('working');
  await expect(workingCard).toBeVisible();
  await expect(missingCard, 'a task without an agent pulse is not live working work').toBeHidden();
  await selectBoardWorkFilter('missing');
  await expect(missingCard).toBeVisible();
  const missingPulse = missingCard.locator('.agent-pulse');
  await expect(missingPulse).toBeHidden();
  await missingCard.getByRole('button', { name: `Show details for ${fx.tasks.missing.key}` }).click();
  await expect(missingPulse).toBeVisible();
  await expect(missingPulse).toHaveAccessibleName('No live pulse');
  await expect(workingCard).toBeHidden();
  await expect(completedCard).toBeHidden();
  await selectBoardWorkFilter('stale');
  await expect(completedCard, 'completed work does not match the stale filter').toBeHidden();
  await selectBoardWorkFilter('action-needed');
  await expect(completedCard, 'completed work does not match the action-needed filter').toBeHidden();
  await selectBoardWorkFilter('all');

  await completedCard.locator('[data-task-trigger]').click();
  const completedDrawer = page.locator('.task-drawer');
  await expect(completedDrawer).toBeVisible();
  await expect(completedDrawer.locator('.agent-work-panel'), 'completed drawers hide live-work details').toHaveCount(0);
  await expect(completedDrawer.getByRole('button', { name: '✓ Completed', exact: true })).toBeDisabled();
  await completedDrawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(completedDrawer).toBeHidden();

  await workingCard.locator('[data-task-trigger]').click();
  const drawer = page.locator('.task-drawer');
  await expect(drawer).toBeVisible();
  await expect(drawer.getByLabel('Task title')).toHaveValue(fx.tasks.working.title);

  const workPanel = drawer.locator('.agent-work-panel');
  await expect(workPanel).toBeVisible();
  await expect(workPanel.getByRole('heading', { name: /Working/ })).toBeVisible();
  await expect(workPanel.locator('.agent-work-details')).toContainText('Implementing the browser workflow');
  await expect(workPanel.locator('.agent-work-details')).toContainText('1 of 2 checkpoints (50%)');
  await expect(workPanel.locator('.agent-work-details')).toContainText('The live working pulse is visible on the board.');
  await expect(workPanel.locator('.agent-work-details')).toContainText('Wait for the remote progress event.');
  await expect(workPanel.locator('.agent-work-details dt')).toHaveText(['Phase', 'Progress', 'Summary', 'Next', 'Started', 'Last check-in', 'Last progress']);
  await expect(workPanel.locator('[data-agent-last-progress] time')).toHaveCount(1);
  await expect(workPanel.locator('.agent-work-details time')).toHaveCount(3);

  // A remote progress write creates task.progressed/comment.created events.
  // The draft must survive the event poll and authoritative detail refresh.
  const draftTitle = `Unsaved title draft ${fx.suffix}`;
  await drawer.getByLabel('Task title').fill(draftTitle);
  fx.tasks.working = await fx.publish(
    fx.tasks.working,
    'working',
    'Remote event received',
    fx.remoteSummary,
    'Keep the title draft until review.',
    ['api', 'poll'],
    2,
    2
  );

  await expect.poll(() => fx.eventReads(), { timeout: 30_000 }).toBeGreaterThan(1);
  await expect(drawer.locator('.agent-work-details')).toContainText(fx.remoteSummary, { timeout: 30_000 });
  await expect(drawer.locator('.agent-work-details')).toContainText('Remote event received');
  await expect(drawer.locator('.agent-work-details')).toContainText('2 of 2 checkpoints (100%)');
  await expect(drawer.locator('.agent-work-details')).toContainText('Keep the title draft until review.');
  await expect(drawer.getByLabel('Task title')).toHaveValue(draftTitle);

  page.once('dialog', (dialog) => dialog.accept());
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(drawer).toBeHidden();
}
