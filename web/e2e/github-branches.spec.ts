import { expect, test, type Page } from '@playwright/test';
import { api, repository, seed, selectRepository, sentinel } from './support/githubBetaFixture';

// Scenarios: test/githubbeta/FAILURES.md, "Branches view". The server, SQLite,
// routes and provider are real; only GitHub's HTTP replies are synthetic.
test.use({ trace: 'on' });

const row = (page: Page, name: string) => page.locator(`[data-branch-row="${name}"]`);
const upstreamRequests = async (page: Page) => (await (await page.request.get('/__githubbeta/state')).json()).upstream_requests as number;

test.describe('Branches view real backend', () => {
  test.skip(process.env.HELM_GITHUB_BETA_E2E !== '1', 'Dedicated synthetic upstream harness required');
  test.setTimeout(120_000);

  test('graphs every branch against main and beta, and hides them when access is revoked', async ({ page }, info) => {
    const leaks: string[] = [];
    page.on('response', async response => {
      if (response.url().includes('/api/')) {
        try { if ((await response.text()).includes(sentinel)) leaks.push(response.url()); } catch { /* navigation can close a response */ }
      }
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
    const { project } = await seed(page.request, `R${Date.now().toString().slice(-7)}`);

    // No selection: an explanation, and GitHub is never contacted.
    const before = await upstreamRequests(page);
    await page.goto(`/p/${project.slug}`);
    const views = page.locator('nav.sidebar').getByRole('group', { name: `${project.name} views` });
    await views.getByRole('button', { name: 'Branches', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/branches$`));
    await expect(views.getByRole('button', { name: 'Branches', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('heading', { name: 'No repository selected' })).toBeVisible();
    await expect(page.locator('[data-branch-graph]')).toHaveCount(0);
    expect(await upstreamRequests(page)).toBe(before);

    await selectRepository(page, project);
    const outside = await page.request.get(`/api/v1/projects/${project.id}/github/branches?repository=elsewhere/unselected`);
    expect(outside.status()).toBe(400);
    expect(await upstreamRequests(page)).toBe(before);

    // A direct URL restores the view; trunks and active branches are drawn.
    await page.goto(`/p/${project.slug}/branches`);
    await expect(page.locator('[data-branch-headline]')).toHaveAttribute('aria-label', 'beta is 2 ahead, 1 behind main');
    await expect(row(page, 'beta')).toContainText('↑2');
    for (const [name, ahead, behind] of [['feat/neglect-signal', '1', '1'], ['feat/branch-graph', '3', '18'], ['fix/audit-timeout', '14', '36']]) {
      await expect(row(page, name)).toHaveAttribute('data-ahead', ahead);
      await expect(row(page, name)).toHaveAttribute('data-behind', behind);
      await expect(row(page, name)).toHaveAttribute('data-kind', 'active');
    }
    await expect(row(page, 'feat/neglect-signal')).toContainText('in beta');
    await expect(row(page, 'feat/branch-graph')).not.toContainText('in beta');
    await expect(row(page, 'feat/branch-graph').getByRole('link')).toHaveAttribute('href', `https://github.com/${repository}/tree/feat/branch-graph`);

    // Stale and merged stay hidden behind counted toggles.
    for (const [bucket, count] of [['active', '3'], ['stale', '2'], ['merged', '2']]) {
      await expect(page.locator(`[data-branch-toggle="${bucket}"] b`)).toHaveText(count);
    }
    await expect(page.locator('[data-branch-toggle="stale"]')).toHaveAttribute('aria-pressed', 'false');
    await expect(row(page, 'docs/old-plan')).toHaveCount(0);
    await expect(row(page, 'release/v0.2.2')).toHaveCount(0);

    // A rewritten tip is reported as not compared, never as merged.
    await expect(page.locator('[data-branch-partial]')).toContainText('1 branch has not been compared yet.');
    await expect(page.locator('[data-branch-uncompared]')).toContainText('wip/force-pushed');
    await info.attach('branches-active.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });

    await page.locator('[data-branch-toggle="stale"]').click();
    await expect(row(page, 'docs/old-plan')).toHaveAttribute('data-kind', 'stale');
    await expect(row(page, 'backlog/search')).toHaveAttribute('data-behind', '255');
    await page.locator('[data-branch-toggle="merged"]').click();
    await expect(row(page, 'release/v0.2.2')).toContainText('merged');
    await expect(row(page, 'fix/settings-column')).toHaveAttribute('data-kind', 'merged');
    await expect(row(page, 'wip/force-pushed')).toHaveCount(0);

    const report = await api(page.request, 'GET', `/projects/${project.id}/github/branches`);
    expect(report).toMatchObject({ state: 'ready', repository, partial: true, base: { name: 'main' }, staging: { name: 'beta', ahead: 2, behind: 1, compared: true } });
    expect(report.branches.find((branch: { name: string }) => branch.name === 'wip/force-pushed')).toMatchObject({ compared: false, ahead: 0 });
    expect(report.branches.find((branch: { name: string }) => branch.name === 'feat/neglect-signal').in_staging).toBe(true);
    // The missing commit must not have cost the repository its other features.
    expect((await api(page.request, 'POST', '/integrations/github/check')).state).toBe('connected');
    await info.attach('branches-report.json', { body: JSON.stringify(report, null, 2), contentType: 'application/json' });

    for (const [name, width, height, scheme] of [
      ['desktop-light', 1440, 1000, 'light'], ['desktop-dark', 1440, 1000, 'dark'], ['phone-dark', 390, 844, 'dark'], ['phone-light', 320, 844, 'light'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await page.emulateMedia({ colorScheme: scheme });
      await page.evaluate((theme) => localStorage.setItem('helm.theme', theme), scheme);
      await page.reload();
      await expect(page.locator('html')).toHaveAttribute('data-theme', scheme);
      await expect(row(page, 'feat/branch-graph')).toBeVisible();
      await page.locator('[data-branch-toggle="stale"]').click();
      await page.locator('[data-branch-toggle="merged"]').click();
      await expect(row(page, 'release/v0.2.2')).toBeVisible();
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: info.outputPath(`${name}.png`), fullPage: true });
    }

    // Revocation: names already on the page disappear at the next refresh.
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.request.put('/__githubbeta/state', { data: { revoked: true } });
    await page.getByRole('button', { name: /Refresh/ }).click();
    await expect(page.locator('[data-branch-state="permission_required"]')).toContainText(/access or token permissions/);
    await expect(page.locator('[data-branch-row]')).toHaveCount(0);
    await expect(page.getByText('feat/branch-graph')).toHaveCount(0);
    await page.screenshot({ path: info.outputPath('revoked-access.png'), fullPage: true });
    await page.request.put('/__githubbeta/state', { data: { revoked: false } });
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(row(page, 'feat/branch-graph')).toBeVisible();
    await api(page.request, 'POST', '/integrations/github/check');

    const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
    expect(storage.includes(sentinel), 'Browser storage must not contain the synthetic credential').toBeFalsy();
    expect(leaks).toEqual([]);
  });

  test('a slow load never paints the previous project', async ({ page }, info) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    const stamp = Date.now().toString().slice(-6);
    const withRepository = (await seed(page.request, `S${stamp}`)).project;
    const withoutRepository = (await seed(page.request, `T${stamp}`)).project;
    await selectRepository(page, withRepository);
    await page.goto(`/p/${withRepository.slug}`);
    const sidebar = page.locator('nav.sidebar');
    try {
      await page.request.put('/__githubbeta/state', { data: { branch_delay_ms: 3000 } });
      await sidebar.getByRole('group', { name: `${withRepository.name} views` }).getByRole('button', { name: 'Branches', exact: true }).click();
      await expect(page.getByRole('status', { name: 'Loading branches' })).toBeVisible();
      await sidebar.getByRole('button', { name: withoutRepository.name, exact: true }).click();
      await sidebar.getByRole('group', { name: `${withoutRepository.name} views` }).getByRole('button', { name: 'Branches', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'No repository selected' })).toBeVisible();
      // Outlast the delayed reply for the first project.
      await page.waitForTimeout(3600);
      await expect(page.locator('[data-branch-row]')).toHaveCount(0);
      await expect(page.getByRole('heading', { name: 'No repository selected' })).toBeVisible();
      await info.attach('project-switch.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
    } finally {
      await page.request.put('/__githubbeta/state', { data: { branch_delay_ms: 0 } });
    }
  });
});
