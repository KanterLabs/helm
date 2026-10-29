import { expect, test, type Page } from '@playwright/test';
import { expectNoAuditMutations, expectPausedExplanation } from './support/auditContainment';
import { installGithubMocks, project, SHA_WEB, type GithubMockState } from './support/githubFixture';

// Synthetic GitHub/AI API only; real-API coverage is github-beta-integration.spec.ts.
// TC-316 parks start/retry/cancel/review/apply; recorded server runs stay
// readable. Failure inventory: docs/TC316_UI_CONTAINMENT_FAILURES.md (A1–A11).
test.use({ serviceWorkers: 'block' });

const finding = (page: Page, text: string) => page.locator('.audit-finding').filter({ hasText: text });
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

async function openRun(page: Page, auditId: string): Promise<void> {
  await page.goto(`/p/${project.slug}/audits/${auditId}`);
  await expect(page.getByRole('heading', { name: 'Board audit', exact: true })).toBeVisible();
}

function expectNoWrites(state: GithubMockState): void {
  expect(state.writes.map((write) => `${write.method} ${write.path}`)).toEqual([]);
}

test.describe('Recorded server-run GitHub audits', () => {
  test('a finished run deep link shows job, evidence and findings read-only', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const state = await installGithubMocks(page, { auditHistory: true, tasksOffBoard: true });
    await openRun(page, 'audit-gh-partial');
    await expectPausedExplanation(page);

    const job = page.locator('[data-github-audit-job]');
    await expect(job).toContainText('Partial');
    await expect(job).toContainText('One binary file was omitted.');
    await expect(job).toContainText('4 tasks');
    await expect(job).toContainText('6 files captured');
    await expect(job).toContainText('1 omitted');
    const manifest = page.locator('[data-audit-evidence-manifest]');
    await expect(manifest).toContainText(SHA_WEB);
    await expect(manifest).toContainText('assets/logo.png: binary file omitted');
    await expect(page.getByText('3 findings recorded', { exact: true })).toBeVisible();
    await expect(finding(page, 'Off-page task from page 51')).toBeVisible();
    // Titles and keys come from the captured manifest, not per-task lookups.
    expect(state.requests).not.toContain('GET /tasks/task-77');
    await expect(finding(page, 'Show verified PR status').getByRole('link', { name: /Open GHB-2/ })).toHaveAttribute('href', `/p/${project.slug}/tasks/GHB-2`);
    await expect(finding(page, 'Show verified PR status').locator('.audit-review-state')).toHaveText('Pending review');

    const attention = finding(page, 'web-e2e fails');
    const citation = attention.getByRole('button', { name: /web-e2e failed/ });
    await citation.click();
    const viewer = attention.locator('[data-audit-evidence-viewer]');
    await expect(viewer).toContainText('expected: PR row');
    await expect(viewer.getByRole('link', { name: /Open on GitHub/ })).toHaveAttribute('href', 'https://github.com/demo-org/helm-web/runs/9001');
    await page.keyboard.press('Escape');
    await expect(viewer).toBeHidden();
    await expect(citation).toBeFocused();
    const move = finding(page, 'Show verified PR status');
    await move.getByRole('button', { name: /helm-api #212 merged/ }).click();
    await expect(move.locator('[data-audit-evidence-viewer]').getByRole('link')).toHaveCount(0);
    await expect(finding(page, 'Off-page task').getByText('legacy:note')).toBeVisible();

    await expectNoAuditMutations(page);
    await expect(page.getByText(/to review|Approve a destination|preview and explicitly apply/)).toHaveCount(0);
    expectNoWrites(state);
    await page.screenshot({ path: testInfo.outputPath('recorded-audit-1440.png'), fullPage: true });
  });

  test('running and cancelled runs show progress and outcome without cancel or retry', async ({ page }) => {
    const state = await installGithubMocks(page, { auditHistory: true });
    await openRun(page, 'audit-gh-running');
    const job = page.locator('[data-github-audit-job]');
    await expect(job).toHaveAttribute('data-stage', 'fetching');
    await expect(job.getByRole('list', { name: 'Audit stages' })).toBeVisible();
    await expect.poll(() => state.requests.filter((item) => item === 'GET /audits/audit-gh-running/job').length).toBeGreaterThan(0);
    await expectNoAuditMutations(page);

    await openRun(page, 'audit-gh-cancelled');
    await expect(job).toHaveAttribute('data-outcome', 'cancelled');
    await expect(job).toContainText('Cancelled by Synthetic reviewer.');
    await expectNoAuditMutations(page);
    await expect(page.getByText('Run a fresh audit', { exact: false })).toHaveCount(0);
    expectNoWrites(state);
  });

  test('the list explains the paused capability and offers no start or repository editor at 390px', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const state = await installGithubMocks(page, { auditHistory: true });
    await page.goto(`/p/${project.slug}/audits`);
    await expect(page.getByRole('heading', { name: 'Board audits', exact: true })).toBeVisible();
    await expectPausedExplanation(page);
    await expect(page.getByRole('button', { name: /audit-gh-partial/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /audit-external/ })).toBeVisible();
    await expectNoAuditMutations(page);
    await expect(page.locator('[data-github-audit-scope]')).toHaveCount(0);
    await expect(page.locator('[data-github-repositories]')).toHaveCount(0);
    expect(await noOverflow(page)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath('audit-list-paused-390.png'), fullPage: true });

    await page.getByRole('button', { name: /audit-external/ }).click();
    await expect(page.getByRole('heading', { name: 'Board audit', exact: true })).toBeVisible();
    await expect(page.locator('[data-github-audit-job]')).toHaveCount(0);
    expect(state.requests.some((item) => item.includes('/audits/audit-external/evidence'))).toBeFalsy();
    expect(state.requests.some((item) => item.includes('/github/readiness'))).toBeFalsy();
    expectNoWrites(state);
  });

  test('a refresh that observes revoked access clears rendered findings and captured source', async ({ page }, testInfo) => {
    const state = await installGithubMocks(page, { auditHistory: true });
    await openRun(page, 'audit-gh-partial');
    const attention = finding(page, 'web-e2e fails');
    await attention.getByRole('button', { name: /web-e2e failed/ }).click();
    await expect(attention.locator('[data-audit-evidence-viewer]')).toContainText('expected: PR row');
    await page.route('**/api/v1/audits/audit-gh-partial', (route) => route.fulfill({
      status: 403, contentType: 'application/json',
      body: JSON.stringify({ error: { code: 'evidence_unavailable', message: 'Source access must be restored.' } })
    }));
    await page.getByRole('button', { name: '↻ Refresh', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('Source access must be restored.');
    await expect(page.locator('.audit-finding')).toHaveCount(0);
    await expect(page.locator('[data-audit-evidence-manifest]')).toHaveCount(0);
    await expect(page.getByText('expected: PR row')).toHaveCount(0);
    await expect(page.getByText(SHA_WEB)).toHaveCount(0);
    await expectNoAuditMutations(page);
    expectNoWrites(state);
    await page.screenshot({ path: testInfo.outputPath('revoked-on-refresh.png'), fullPage: true });
  });

  test('a typed evidence error leaves no retained content at 390px', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const state = await installGithubMocks(page, { auditHistory: true, evidenceError: true });
    await openRun(page, 'audit-gh-partial');
    await expect(page.getByText('Evidence access was revoked for this repository.')).toBeVisible();
    await expect(page.locator('.audit-finding')).toHaveCount(0);
    await expect(page.locator('[data-audit-evidence-manifest]')).toHaveCount(0);
    await expect(page.getByText('expected: PR row')).toHaveCount(0);
    expect(await noOverflow(page)).toBeTruthy();
    expectNoWrites(state);
  });
});
