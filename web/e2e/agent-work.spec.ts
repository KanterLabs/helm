import { test } from '@playwright/test';
import { verifyBoardAndDrawerWork } from './support/agentWorkBoard';
import { createAgentWorkFixture } from './support/agentWorkFixture';
import { verifyMyWorkLiveGroups } from './support/agentWorkMyWork';
import { drainAgentWorkRoutes } from './support/agentWorkRoutes';
import { verifyRoadmapAndBoardTimelines } from './support/agentWorkTimelines';

test.afterEach(async ({ page }) => {
  // Drain pending real reads before Playwright disposes request fixtures.
  await drainAgentWorkRoutes(page);
});

test('shows live agent work across the board, drawer, and My Work', async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);

  const fx = await createAgentWorkFixture(page, request);
  await verifyBoardAndDrawerWork(page, fx);
  await verifyMyWorkLiveGroups(page, fx);
  const screenshot = testInfo.outputPath('agent-work-my-work.png');
  await page.screenshot({ path: screenshot, fullPage: true });
  await testInfo.attach('Live agent work in My Work', { path: screenshot, contentType: 'image/png' });
});

test('shows live agent work in roadmap and board timelines', async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);

  const fx = await createAgentWorkFixture(page, request);
  // The timeline journey starts with retained remote progress. The board
  // journey separately verifies its arrival while preserving a drawer draft.
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
  await verifyRoadmapAndBoardTimelines(page, fx);
  const screenshot = testInfo.outputPath('agent-work-timeline.png');
  await page.screenshot({ path: screenshot, fullPage: true });
  await testInfo.attach('Live agent work in board timeline', { path: screenshot, contentType: 'image/png' });
});
