import { expect, test, type Page } from '@playwright/test';
import { installGithubMocks, project, type GithubMockState } from './support/githubFixture';

// Synthetic GitHub API only; real-API coverage is github-beta-integration.spec.ts.
test.use({ serviceWorkers: 'block' });

const card = (page: Page, key: string) => page.locator('section.board .task-card').filter({ hasText: key });
const writes = (state: GithubMockState, method: string, fragment: string) =>
  state.writes.filter((write) => write.method === method && write.path.includes(fragment));
const links = (state: GithubMockState) => state.writes.filter((write) => write.method === 'POST' && write.path === '/tasks/task-2/artifacts');

async function openBoard(page: Page): Promise<void> {
  await page.goto(`/p/${project.slug}`);
  await expect(card(page, 'GHB-1')).toBeVisible();
}

async function openTaskCard(page: Page, key: string): Promise<void> {
  await card(page, key).locator('[data-task-trigger]').click();
  await expect(page.locator('.task-drawer')).toBeVisible();
  await expect(page.getByRole('heading', { name: /Pull requests/ })).toBeVisible();
}

test.describe('GitHub PR links', () => {
  test('board badges stay quiet, batched and honest; drawer separates merged, deployed and task state', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const state = await installGithubMocks(page);
    await openBoard(page);

    await expect(card(page, 'GHB-1').getByRole('button', { name: /^2 PRs, check failing/ })).toBeVisible();
    await expect(card(page, 'GHB-3').getByRole('button', { name: /^#401, check failing/ })).toBeVisible();
    await expect(card(page, 'GHB-4').getByRole('button', { name: /^#300, unavailable/ })).toBeVisible();
    await expect(card(page, 'GHB-2').locator('[data-pr-badge]')).toHaveCount(0);
    // Badges are outside the task's main button and do not change card size.
    await expect(card(page, 'GHB-1').locator('[data-task-trigger] [data-pr-badge]')).toHaveCount(0);
    const withBadge = await card(page, 'GHB-1').boundingBox();
    const without = await card(page, 'GHB-2').boundingBox();
    expect(withBadge?.height).toBe(without?.height);
    const summaryReads = state.requests.filter((item) => item.startsWith('GET /artifact-summaries'));
    expect(summaryReads.length).toBeLessThanOrEqual(2);
    expect(decodeURIComponent(summaryReads.at(-1) || '')).toContain('task-1');
    expect(state.requests.some((item) => /\/tasks\/[^/]+\/artifacts/.test(item))).toBeFalsy();

    await page.screenshot({ path: testInfo.outputPath('pr-board-1440.png') });
    await card(page, 'GHB-1').getByRole('button', { name: /^2 PRs/ }).click();
    const heading = page.getByRole('heading', { name: /Pull requests/ });
    await expect(heading).toBeFocused();
    const section = page.locator('[data-task-pull-requests]');
    const failing = section.locator('[data-pr-row]').filter({ hasText: '#418' });
    await expect(failing).toContainText('Task drawer: Pull requests section');
    await expect(failing).toContainText('Open');
    await expect(failing).toContainText('1 of 2 checks failing');
    await expect(failing).toContainText('Review observation: approved');
    await expect(failing.getByRole('link', { name: /Open PR/ })).toHaveAttribute('href', 'https://github.com/demo-org/helm-web/pull/418');
    const merged = section.locator('[data-pr-row]').filter({ hasText: '#212' });
    await expect(merged).toContainText('Merged');
    await expect(merged).toContainText('Checks passed');
    await expect(merged).toContainText('doesn’t deploy it or complete the task');
    const facts = section.locator('[data-pr-facts]');
    await expect(facts).toContainText('PRs merged');
    await expect(facts).toContainText('1 of 2');
    await expect(facts).toContainText('Not tracked here');
    await expect(facts).toContainText('In progress');
    await expect(page.getByText(/ready to merge/i)).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath('pr-drawer-1440.png') });

    await page.keyboard.press('Escape');
    await card(page, 'GHB-3').getByRole('button', { name: /^#401/ }).click();
    const stale = page.locator('[data-pr-row]').filter({ hasText: '#401' });
    await expect(stale).toContainText('Merged');
    await expect(stale).toContainText('failing');
    await expect(stale).toContainText('Stale');
    await expect(stale).toContainText('Partial');

    await page.keyboard.press('Escape');
    await card(page, 'GHB-4').getByRole('button', { name: /^#300/ }).click();
    const unavailable = page.locator('[data-pr-row]').filter({ hasText: '#300' });
    await expect(unavailable).toContainText('Unavailable');
    await expect(unavailable).toContainText('GitHub access to this repository was revoked.');
    await expect(unavailable.locator('[data-pr-title]')).toHaveCount(0);
  });

  test('links, verifies, refuses bad input, recovers from stale versions and unlinks by keyboard', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const state = await installGithubMocks(page, { slowRefresh: true });
    await openBoard(page);
    await openTaskCard(page, 'GHB-2');
    const input = page.getByLabel(/Pull request URL/i);
    const rows = page.locator('[data-task-pull-requests] [data-pr-row]');
    await expect(page.locator('[data-task-pull-requests]')).toContainText('No pull requests linked');

    await input.fill('https://gitlab.com/demo-org/helm-web/-/merge_requests/1');
    await input.press('Enter');
    await expect(page.locator('#task-pr-error')).toContainText('Only GitHub pull request URLs');
    await expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(writes(state, 'POST', '/artifacts')).toHaveLength(0);

    await input.fill('https://www.github.com/demo-org/helm-api/pull/215/files?w=1#top');
    await expect(page.locator('[data-pr-preview]')).toHaveText('demo-org/helm-api #215');
    await input.press('Enter');
    await expect(rows.filter({ hasText: '#215' })).toContainText('Verifying');
    await expect(rows.filter({ hasText: '#215' }).locator('[data-pr-title]')).toHaveCount(0);
    expect(links(state)[0]).toMatchObject({ ifMatch: '"v2"', body: { url: 'https://github.com/demo-org/helm-api/pull/215' } });
    await expect.poll(() => state.writes.filter((write) => write.path.endsWith('/refresh')).length).toBe(1);
    state.releaseRefresh();
    const verified = rows.filter({ hasText: '#215' });
    await expect(verified.locator('[data-pr-title]')).toHaveText('Expose verified PR status');
    await expect(verified).toContainText('Checks running');
    await expect(verified).toBeFocused();
    await expect(input).toHaveValue('');

    await input.fill('https://github.com/demo-org/helm-api/pull/215');
    await input.press('Enter');
    await expect(page.locator('#task-pr-error')).toHaveText('That pull request is already linked to this task.');
    expect(links(state)).toHaveLength(1);

    // Another actor edits the task: the link fails, keeps the draft, and the retry uses the new version.
    state.tasks[1].version += 1;
    await input.fill('https://github.com/demo-org/helm-web/pull/999');
    await input.press('Enter');
    await expect(page.locator('#task-pr-error')).toContainText('This task changed');
    await expect(input).toHaveValue('https://github.com/demo-org/helm-web/pull/999');
    await expect.poll(() => state.requests.filter((item) => item === 'GET /tasks/task-2').length).toBeGreaterThan(1);
    await page.getByRole('button', { name: 'Link pull request', exact: true }).click();
    await expect(rows.filter({ hasText: '#999' })).toContainText('Verifying');
    await expect.poll(() => state.writes.filter((write) => write.path.endsWith('/refresh')).length).toBe(2);
    state.releaseRefresh();
    const unverified = rows.filter({ hasText: '#999' });
    await expect(unverified).toContainText('Unverified');
    await expect(unverified).toContainText('GitHub did not respond. Try again later.');
    await expect(unverified.locator('[data-pr-title]')).toHaveCount(0);
    expect(links(state).at(-1)?.ifMatch).toBe('"v4"');
    await expect(unverified.getByRole('button', { name: /Refresh pull request #999/ })).toBeEnabled();

    await verified.getByRole('button', { name: /Unlink pull request #215/ }).focus();
    await page.keyboard.press('Enter');
    await expect(rows.filter({ hasText: '#215' })).toHaveCount(0);
    await expect(rows).toHaveCount(1);
    expect(writes(state, 'DELETE', '/tasks/task-2/artifacts/')[0].ifMatch).toBe('"v5"');
    await expect(input).toBeFocused();
  });

  test('keeps per-task URL drafts across drawer switches and fits 390px with keyboard badge access', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await installGithubMocks(page);
    await openBoard(page);
    await openTaskCard(page, 'GHB-2');
    await page.getByLabel(/Pull request URL/i).fill('https://github.com/demo-org/helm-web/pull/77');
    await page.keyboard.press('Escape');
    await expect(page.locator('.task-drawer')).toBeHidden();

    const badge = card(page, 'GHB-1').getByRole('button', { name: /^2 PRs/ });
    await badge.focus();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('heading', { name: /Pull requests/ })).toBeFocused();
    await expect(page.getByLabel(/Pull request URL/i)).toHaveValue('');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath('pr-drawer-390.png') });
    await page.keyboard.press('Escape');

    await openTaskCard(page, 'GHB-2');
    await expect(page.getByLabel(/Pull request URL/i)).toHaveValue('https://github.com/demo-org/helm-web/pull/77');
  });

  test('admin settings show connection state and save allowlisted repositories without any token field', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const state = await installGithubMocks(page);
    await page.goto('/settings');
    const panel = page.locator('[data-github-settings]');
    await expect(panel).toContainText('Connected');
    await expect(panel).toContainText('Token can read the selected repositories.');
    await panel.getByRole('button', { name: 'Test connection' }).click();
    await expect.poll(() => writes(state, 'POST', '/integrations/github/check').length).toBe(1);

    await expect(panel.getByRole('checkbox', { name: 'demo-org/helm-web' })).toBeChecked();
    await panel.getByRole('checkbox', { name: 'demo-org/helm-api' }).check();
    // A branch is explicit: a blank ref is never sent or silently resolved.
    await expect(panel).toContainText('Enter a branch for demo-org/helm-api');
    await expect(panel.getByRole('button', { name: 'Save repositories' })).toBeDisabled();
    await panel.getByLabel('Branch for demo-org/helm-api', { exact: true }).fill('release');
    await panel.getByRole('button', { name: 'Save repositories' }).click();
    await expect(panel).toContainText('Repositories saved');
    expect(writes(state, 'PUT', `/projects/${project.id}/github`)[0]).toMatchObject({
      ifMatch: '"v1"',
      body: { repositories: [{ repository: 'demo-org/helm-web', ref: 'main' }, { repository: 'demo-org/helm-api', ref: 'release' }] }
    });
    await expect(panel.locator('input[type="password"]')).toHaveCount(0);
    await expect(panel.getByText(/token/i).first()).not.toContainText(/ghp_|github_pat_/);
  });

  test('settings explain a missing server configuration and keep other settings usable', async ({ page }) => {
    await installGithubMocks(page, { integrationState: 'not_configured' });
    await page.goto('/settings');
    const panel = page.locator('[data-github-settings]');
    await expect(panel).toContainText('Not configured');
    await expect(panel).toContainText('HELM_GITHUB_TOKEN_FILE');
    await expect(page.getByRole('heading', { name: 'Appearance' })).toBeVisible();
  });
});
