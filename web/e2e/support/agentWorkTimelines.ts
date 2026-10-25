// Roadmap live work, drawer activity tabs and the board-wide timeline.
import { expect, type Page } from '@playwright/test';
import { postJSON } from './agentWorkApi';
import type { AgentWorkFixture } from './agentWorkFixture';

export async function verifyRoadmapAndBoardTimelines(page: Page, fx: AgentWorkFixture): Promise<void> {
  const drawer = page.locator('.task-drawer');
  await page.goto(`/p/${encodeURIComponent(fx.projectA.slug)}/roadmap/`);
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/roadmap/?$`));
  await expect(page.getByRole('heading', { name: `${fx.projectAName} progress`, exact: true })).toBeVisible();

  const roadmapLiveWork = page.locator('.roadmap-live-work');
  await expect(roadmapLiveWork.getByRole('heading', { name: 'Agent work', exact: true })).toBeVisible();
  await expect(roadmapLiveWork.getByLabel(`Open ${fx.tasks.working.key}: ${fx.tasks.working.title} activity`)).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Recent activity', exact: true })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Filter recent activity' })).toBeVisible();

  await roadmapLiveWork.getByLabel(`Open ${fx.tasks.working.key}: ${fx.tasks.working.title} activity`).click();
  await expect(drawer).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/tasks/${fx.tasks.working.key}\\?view=activity$`));
  const activityTab = drawer.getByRole('tab', { name: 'Activity', exact: true });
  const detailsTab = drawer.getByRole('tab', { name: 'Details', exact: true });
  await expect(activityTab).toHaveAttribute('aria-selected', 'true');
  await expect(drawer.getByRole('heading', { name: 'Activity', exact: true })).toBeVisible();
  await expect(drawer.locator('.task-timeline-item[data-kind="agent_progress"]').first()).toContainText(fx.remoteSummary);

  await activityTab.focus();
  await activityTab.press('ArrowLeft');
  await expect(detailsTab).toBeFocused();
  await expect(detailsTab).toHaveAttribute('aria-selected', 'true');
  await detailsTab.press('ArrowRight');
  await expect(activityTab).toBeFocused();
  await expect(activityTab).toHaveAttribute('aria-selected', 'true');

  const timelineFilters = drawer.getByRole('group', { name: 'Filter task activity' });
  await timelineFilters.getByRole('button', { name: 'Agent', exact: true }).click();
  await expect(drawer.locator('.task-timeline-item[data-kind="agent_progress"]')).not.toHaveCount(0);
  await expect(drawer.locator('.task-timeline-item[data-kind="comment"]')).toHaveCount(0);
  await timelineFilters.getByRole('button', { name: 'All', exact: true }).click();
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/roadmap/?$`));

  const boardTimelineComment = `Board timeline comment ${fx.suffix}`;
  await postJSON(fx.request, `/api/v1/tasks/${fx.tasks.working.id}/comments`, { body: boardTimelineComment }, {
    'Idempotency-Key': fx.mutationKey()
  });
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`/p/${encodeURIComponent(fx.projectA.slug)}/timeline`);
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/timeline/?$`));
  const projectViews = page.locator('nav.sidebar').getByRole('group', { name: `${fx.projectAName} views` });
  const timelineView = projectViews.getByRole('button', { name: 'Timeline', exact: true });
  const boardView = projectViews.getByRole('button', { name: 'Board', exact: true });
  await expect(timelineView).toHaveAttribute('aria-current', 'page');
  await boardView.click();
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/?$`));
  await expect(boardView).toHaveAttribute('aria-current', 'page');
  await timelineView.click();
  await expect(timelineView).toHaveAttribute('aria-current', 'page');
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/timeline/?$`));
  const boardTimeline = page.locator('.board-timeline');
  await expect(boardTimeline.getByRole('heading', { name: 'Recent work', exact: true })).toBeVisible();
  await expect(boardTimeline.locator('.board-timeline-row').filter({ hasText: fx.tasks.working.title }).first()).toBeVisible();
  await expect(boardTimeline).not.toContainText(fx.tasks.verifying.title);

  const boardTimelineFilters = boardTimeline.getByRole('group', { name: 'Filter board timeline' });
  await boardTimelineFilters.getByRole('button', { name: 'Comments', exact: true }).click();
  await expect(boardTimeline.locator('.board-timeline-row').filter({ hasText: boardTimelineComment })).toBeVisible();
  await expect(boardTimeline.locator('.board-timeline-item[data-kind="agent_progress"]')).toHaveCount(0);
  await boardTimelineFilters.getByRole('button', { name: 'Agent', exact: true }).click();
  await expect(boardTimeline.locator('.board-timeline-row').filter({ hasText: fx.remoteSummary })).toBeVisible();

  const liveTimelineSummary = `Board timeline refreshed live ${fx.suffix}.`;
  fx.tasks.working = await fx.publish(
    fx.tasks.working,
    'working',
    'Board timeline live refresh',
    liveTimelineSummary,
    'Review the board-wide history.',
    ['timeline', 'polling'],
    2,
    2
  );
  await expect(boardTimeline.locator('.board-timeline-row').filter({ hasText: liveTimelineSummary })).toBeVisible({ timeout: 30_000 });

  await boardTimelineFilters.getByRole('button', { name: 'All', exact: true }).click();
  const timelineTaskRow = boardTimeline.locator('.board-timeline-row').filter({ hasText: liveTimelineSummary });
  await timelineTaskRow.click();
  await expect(drawer).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/tasks/${fx.tasks.working.key}\\?view=activity$`));
  await expect(drawer.getByRole('tab', { name: 'Activity', exact: true })).toHaveAttribute('aria-selected', 'true');
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/p/${encodeURIComponent(fx.projectA.slug)}/timeline/?$`));

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(boardTimeline).toBeVisible();
  const timelineDimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth
  }));
  expect(timelineDimensions.documentWidth, 'mobile board timeline should not overflow horizontally').toBeLessThanOrEqual(timelineDimensions.viewport);
  expect(timelineDimensions.bodyWidth, 'mobile board timeline should not overflow horizontally').toBeLessThanOrEqual(timelineDimensions.viewport);
}
