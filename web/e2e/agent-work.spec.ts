import { test } from '@playwright/test';
import { verifyBoardAndDrawerWork } from './support/agentWorkBoard';
import { createAgentWorkFixture } from './support/agentWorkFixture';
import { verifyMyWorkLiveGroups } from './support/agentWorkMyWork';
import { verifyRoadmapAndBoardTimelines } from './support/agentWorkTimelines';

test('shows live agent work across the board, drawer, and My Work', async ({ page, request }) => {
  test.setTimeout(90_000);

  const fx = await createAgentWorkFixture(page, request);
  await verifyBoardAndDrawerWork(page, fx);
  await verifyMyWorkLiveGroups(page, fx);
  await verifyRoadmapAndBoardTimelines(page, fx);
});
