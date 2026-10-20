// My work live groups, filters, sources and the compact mobile layout.
import { expect, type Page } from '@playwright/test';
import { escapeRegExp } from './agentWorkApi';
import type { AgentWorkFixture } from './agentWorkFixture';

export async function verifyMyWorkLiveGroups(page: Page, fx: AgentWorkFixture): Promise<void> {
  const drawer = page.locator('.task-drawer');
  const sidebar = page.locator('nav.sidebar');
  await sidebar.getByRole('button', { name: 'My work', exact: true }).click();
  await expect(page).toHaveURL(/\/my-work\/?$/);
  await expect(page.getByRole('heading', { name: 'Live work', exact: true })).toBeVisible();

  const sourceToggle = page.getByRole('group', { name: 'My work source' });
  const liveToggle = sourceToggle.getByRole('button', { name: 'Live', exact: true });
  const assignedToggle = sourceToggle.getByRole('button', { name: 'Assigned', exact: true });
  await expect(liveToggle).toHaveAttribute('aria-pressed', 'true');
  await expect(assignedToggle).toBeVisible();

  const liveFilters = page.getByRole('group', { name: 'Filter live work' });
  const rowFor = (group: ReturnType<typeof page.locator>, title: string) => {
    const exactTitle = new RegExp(`^${escapeRegExp(title)}$`);
    return group.locator('strong').filter({ hasText: exactTitle }).locator('xpath=ancestor::button[contains(concat(" ", normalize-space(@class), " "), " live-work-row ")][1]');
  };
  const rowIn = async (group: ReturnType<typeof page.locator>, title: string, projectName: string, columnName: string) => {
    const row = rowFor(group, title);
    await expect(row).toHaveCount(1);
    await expect(row).toBeVisible();
    await expect(row.locator('.work-project-name')).toHaveText(`${projectName} · ${columnName}`);
  };

  // Action-needed contains both waiting and stale work. Selecting the filter
  // makes its rows visible even though the default all-groups view collapses
  // the duplicate action-needed rows to avoid showing them twice.
  await liveFilters.getByRole('button', { name: /^Action needed/ }).click();
  const actionGroup = page.locator('section[aria-labelledby="action-needed-heading"]');
  await rowIn(actionGroup, fx.tasks.waiting.title, fx.projectAName, fx.blockedA.name);
  await rowIn(actionGroup, fx.tasks.stale.title, fx.projectBName, fx.activeB.name);
  await rowIn(actionGroup, fx.tasks.staleWaiting.title, fx.projectBName, fx.activeB.name);
  await expect(rowFor(actionGroup, fx.tasks.waiting.title).locator('.agent-pulse')).toContainText('Waiting');
  await expect(rowFor(actionGroup, fx.tasks.stale.title).locator('.agent-pulse')).toHaveAccessibleName('Working Stale check-in');
  await expect(rowFor(actionGroup, fx.tasks.stale.title).locator('.agent-pulse')).toHaveAccessibleDescription(/Stale check-in/);
  await expect(rowFor(actionGroup, fx.tasks.staleWaiting.title).locator('.agent-pulse')).toHaveAccessibleName('Waiting Stale check-in');
  await expect(rowFor(actionGroup, fx.tasks.staleWaiting.title).locator('.agent-pulse')).toHaveAccessibleDescription(/Stale check-in/);

  await liveFilters.getByRole('button', { name: 'Stale', exact: true }).click();
  const staleGroup = page.locator('section[aria-labelledby="stale-heading"]');
  await rowIn(staleGroup, fx.tasks.stale.title, fx.projectBName, fx.activeB.name);
  await rowIn(staleGroup, fx.tasks.staleWaiting.title, fx.projectBName, fx.activeB.name);
  await expect(rowFor(staleGroup, fx.tasks.stale.title).locator('.agent-pulse')).toHaveAccessibleName('Working Stale check-in');
  await expect(rowFor(staleGroup, fx.tasks.stale.title).locator('.agent-pulse')).toHaveAccessibleDescription(/Stale check-in/);
  await expect(rowFor(staleGroup, fx.tasks.staleWaiting.title).locator('.agent-pulse')).toHaveAccessibleName('Waiting Stale check-in');
  await expect(rowFor(staleGroup, fx.tasks.staleWaiting.title).locator('.agent-pulse')).toHaveAccessibleDescription(/Stale check-in/);

  // The fixture's claim is expired only in browser reads. The drawer must
  // offer a fresh claim instead of incorrectly offering claim renewal.
  await rowFor(staleGroup, fx.tasks.stale.title).click();
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole('button', { name: /Claim task/ })).toHaveText(/Claim task/);
  await expect(drawer.getByRole('button', { name: /Renew claim/ })).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(drawer).toBeHidden();

  await liveFilters.getByRole('button', { name: 'Waiting', exact: true }).click();
  const waitingGroup = page.locator('section[aria-labelledby="waiting-heading"]');
  await rowIn(waitingGroup, fx.tasks.waiting.title, fx.projectAName, fx.blockedA.name);
  await expect(rowFor(waitingGroup, fx.tasks.waiting.title).locator('.agent-pulse')).toHaveAccessibleName('Waiting Fresh check-in');

  await liveFilters.getByRole('button', { name: 'Verifying', exact: true }).click();
  const verifyingGroup = page.locator('section[aria-labelledby="verifying-heading"]');
  await rowIn(verifyingGroup, fx.tasks.verifying.title, fx.projectBName, fx.readyB.name);
  await expect(rowFor(verifyingGroup, fx.tasks.verifying.title).locator('.agent-pulse')).toHaveAccessibleName('Verifying Fresh check-in');

  await liveFilters.getByRole('button', { name: 'Working', exact: true }).click();
  const workingGroup = page.locator('section[aria-labelledby="working-heading"]');
  await rowIn(workingGroup, fx.tasks.working.title, fx.projectAName, fx.activeA.name);
  await expect(rowFor(workingGroup, fx.tasks.working.title).locator('.agent-pulse')).toHaveAccessibleName('Working Fresh check-in');

  await liveFilters.getByRole('button', { name: 'All', exact: true }).click();
  for (const heading of ['Action needed', 'Stale', 'Waiting', 'Verifying', 'Working']) {
    await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible();
  }
  await expect(actionGroup.locator('.live-work-row:visible'), 'action-needed work is shown once in its state group').toHaveCount(0);
  await expect(rowFor(staleGroup, fx.tasks.staleWaiting.title)).toHaveCount(1);
  await expect(rowFor(workingGroup, fx.tasks.missing.title), 'missing pulse is excluded from Live Working').toHaveCount(0);

  // Assigned remains a supported source even after the live coordination
  // view has loaded. Switch back so the drawer is opened from live work.
  await assignedToggle.click();
  await expect(assignedToggle).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.work-list')).toBeVisible();
  await expect(page.locator('.work-list .work-row').filter({ hasText: fx.tasks.working.title })).toBeVisible();
  await liveToggle.click();
  await expect(liveToggle).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('heading', { name: 'Live work', exact: true })).toBeVisible();

  const liveWorkingGroup = page.locator('section[aria-labelledby="working-heading"]');
  await liveWorkingGroup.locator('.live-work-row').filter({ hasText: fx.tasks.working.title }).click();
  await expect(drawer).toBeVisible();
  await expect(drawer.locator('.agent-work-panel')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Saved', exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: /Complete/ })).toBeVisible();
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(drawer).toBeHidden();
  await expect(page).toHaveURL(/\/my-work\/?$/);
  await expect(assignedToggle).toBeVisible();

  // The compact mobile layout keeps status and source actions usable and
  // confines the intentionally scrollable filter strip to its own element.
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileNavigation = page.getByRole('navigation', { name: 'Primary navigation' });
  await expect(mobileNavigation).toBeVisible();
  await expect(mobileNavigation.getByRole('button', { name: 'My work', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Live work', exact: true })).toBeVisible();
  await expect(assignedToggle).toBeVisible();
  await expect(rowFor(page.locator('section[aria-labelledby="waiting-heading"]'), fx.tasks.waiting.title).locator('.agent-pulse')).toBeVisible();

  await rowFor(page.locator('section[aria-labelledby="working-heading"]'), fx.tasks.working.title).click();
  await expect(drawer).toBeVisible();
  await expect(drawer.locator('.agent-work-panel')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Saved', exact: true })).toBeVisible();
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth
  }));
  expect(dimensions.documentWidth, 'mobile live work should not overflow horizontally').toBeLessThanOrEqual(dimensions.viewport);
  expect(dimensions.bodyWidth, 'mobile live work should not overflow horizontally').toBeLessThanOrEqual(dimensions.viewport);
  await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(drawer).toBeHidden();
  await expect(page).toHaveURL(/\/my-work\/?$/);
}
