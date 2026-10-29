import { expect, test, type Page } from '@playwright/test';
import { installGithubMocks, project, SHA_WEB, type GithubMockState } from './support/githubFixture';

// Synthetic GitHub/AI API only; real-API coverage is github-beta-integration.spec.ts.
test.use({ serviceWorkers: 'block' });

const writes = (state: GithubMockState, method: string, fragment: string) =>
  state.writes.filter((write) => write.method === method && write.path.includes(fragment));
const finding = (page: Page, text: string) => page.locator('.audit-finding').filter({ hasText: text });

async function openAudits(page: Page): Promise<void> {
  await page.goto(`/p/${project.slug}/audits`);
  await expect(page.getByRole('heading', { name: 'Board audits', exact: true })).toBeVisible();
}

async function startAudit(page: Page, state: GithubMockState): Promise<string> {
  await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
  const scope = page.locator('[data-column-audit-scope]');
  await expect(scope.getByText('GitHub access is ready.')).toBeVisible();
  await scope.getByRole('button', { name: /^Audit \d+ cards?$/ }).dblclick();
  await expect(page).toHaveURL(/\/audits\/audit-gh-\d+$/);
  const posts = writes(state, 'POST', '/github-audits');
  expect(new Set(posts.map((post) => post.key)).size).toBe(1);
  return decodeURIComponent(page.url().split('/').pop() || '');
}

test.describe('Server-run GitHub audits', () => {
  for (const deniedAt of ['preview', 'apply'] as const) {
    test(`clears protected content when ${deniedAt} observes revoked source access`, async ({ page }, testInfo) => {
      const state = await installGithubMocks(page);
      await openAudits(page);
      const auditId = await startAudit(page, state);
      const move = finding(page, 'Show verified PR status');
      await move.getByRole('button', { name: 'Approve', exact: true }).click();
      await expect(move.locator('.audit-review-state')).toHaveText('Approved');
      if (deniedAt === 'apply') {
        await move.getByRole('button', { name: 'Preview approved finding' }).click();
        await page.getByRole('button', { name: 'Continue to apply' }).click();
      }
      await page.route(`**/api/v1/audits/${auditId}/apply`, (route) => route.fulfill({
        status: 409, contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'evidence_unavailable', message: 'Source access must be restored.' } })
      }));
      if (deniedAt === 'preview') await move.getByRole('button', { name: 'Preview approved finding' }).click();
      else await page.getByRole('button', { name: 'Apply move', exact: true }).click();
      await expect(page.getByRole('alert')).toContainText('Source access must be restored.');
      await expect(page.locator('.audit-finding')).toHaveCount(0);
      await expect(page.locator('[data-audit-evidence-manifest]')).toHaveCount(0);
      await expect(page.getByRole('dialog')).toHaveCount(0);
      await expect(page.getByRole('alertdialog')).toHaveCount(0);
      expect(state.tasks[1].column_id).toBe('col-backlog');
      await page.screenshot({ path: testInfo.outputPath(`revoked-at-${deniedAt}.png`), fullPage: true });
    });
  }

  test('selects repositories, starts once, survives leave/return and reviews pinned evidence', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const state = await installGithubMocks(page, { tasksOffBoard: true });
    await openAudits(page);
    await expect(page.getByRole('button', { name: /audit-external/ })).toBeVisible();

    await page.getByRole('button', { name: /GitHub repositor/i }).click();
    const repos = page.locator('[data-github-repositories]');
    await expect(repos.getByRole('checkbox', { name: 'demo-org/helm-web' })).toBeChecked();
    await repos.getByRole('checkbox', { name: 'demo-org/helm-api' }).check();
    await repos.getByLabel('Branch for demo-org/helm-api', { exact: true }).fill('main');
    await repos.getByRole('button', { name: 'Save repositories' }).click();
    await expect(repos).toContainText('Repositories saved');

    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    const scope = page.locator('[data-column-audit-scope]');
    await expect(scope).toContainText('demo-org/helm-api');
    await expect(scope).toContainText('Your AI connection is ready.');
    // Column audits default to every card, including Done and Blocked.
    await expect(scope.getByRole('radio', { name: /All cards/ })).toBeChecked();
    await expect(scope).toContainText(/Includes \d+ Done and \d+ Blocked/);
    await expect(scope.getByText(/terminal|local agent|copy this request/i)).toHaveCount(0);
    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    await expect(scope).toBeHidden();

    const auditId = await startAudit(page, state);
    expect(writes(state, 'POST', '/github-audits')[0].body).toEqual({});
    state.jobs.get(auditId)!.paused = true;
    const job = page.locator('[data-github-audit-job]');
    await expect(job).toContainText(/Waiting to start|Reading committed work/);
    await expect(job.getByRole('button', { name: 'Cancel audit' })).toBeEnabled();

    // Leaving and returning reads server state; navigation never cancels the run.
    await openAudits(page);
    await page.goto(`/p/${project.slug}/audits/${auditId}`);
    await expect(job).toBeVisible();
    expect(writes(state, 'POST', '/cancel')).toHaveLength(0);
    state.jobs.get(auditId)!.paused = false;
    await expect(job).toContainText('Audit finished', { timeout: 20_000 });
    await expect(job).toContainText('One binary file was omitted.');
    await expect(job).toContainText('4 cards');
    await expect(job).toContainText('6 files read');
    await expect(job).toContainText('1 not read');
    await expect(job.getByRole('button', { name: 'Cancel audit' })).toHaveCount(0);

    const manifest = page.locator('[data-audit-evidence-manifest]');
    await expect(manifest).toContainText(SHA_WEB);
    await expect(manifest).toContainText('assets/logo.png: binary file omitted');
    await expect(page.getByText('3 findings to review')).toBeVisible();
    await expect(finding(page, 'Off-page task from page 51')).toBeVisible();
    // Titles and keys come from the captured manifest, not per-task lookups.
    expect(state.requests).not.toContain('GET /tasks/task-77');
    await expect(finding(page, 'Show verified PR status').getByRole('link', { name: /Open GHB-2/ })).toHaveAttribute('href', `/p/${project.slug}/tasks/GHB-2`);

    const attention = finding(page, 'web-e2e fails');
    const citation = attention.getByRole('button', { name: /web-e2e failed/ });
    await citation.click();
    const viewer = attention.locator('[data-audit-evidence-viewer]');
    await expect(viewer).toContainText('expected: PR row');
    await expect(viewer).toContainText(SHA_WEB);
    await expect(viewer.getByRole('link', { name: /Open on GitHub/ })).toHaveAttribute('href', 'https://github.com/demo-org/helm-web/runs/9001');
    await page.keyboard.press('Escape');
    await expect(viewer).toBeHidden();
    await expect(citation).toBeFocused();
    await attention.getByRole('button', { name: /Drawer section/ }).click();
    await expect(viewer).toContainText('src/Drawer.svelte:10–12');
    const move = finding(page, 'Show verified PR status');
    await move.getByRole('button', { name: /helm-api #212 merged/ }).click();
    await expect(move.locator('[data-audit-evidence-viewer]').getByRole('link')).toHaveCount(0);
    await expect(finding(page, 'Off-page task').getByText('legacy:note')).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('github-audit-1440.png'), fullPage: true });

    await move.getByRole('button', { name: 'Approve', exact: true }).click();
    await expect(move.locator('.audit-review-state')).toHaveText('Approved');
    expect(writes(state, 'POST', '/apply')).toHaveLength(0);
    await move.getByRole('button', { name: 'Preview approved finding' }).click();
    const preview = page.getByRole('dialog', { name: 'Preview approved finding' });
    await expect(preview).toContainText('Backlog → Ready is safe.');
    expect(writes(state, 'POST', '/apply')[0].body).toEqual({ finding_ids: ['gf-move'], preview: true });
    expect(state.tasks[1].column_id).toBe('col-backlog');
    await preview.getByRole('button', { name: 'Continue to apply' }).click();
    await page.getByRole('alertdialog', { name: 'Apply this move?' }).getByRole('button', { name: 'Apply move' }).click();
    await expect(page.getByRole('alertdialog', { name: 'Apply this move?' })).toBeHidden();
    expect(writes(state, 'POST', '/apply').at(-1)?.body).toEqual({ finding_ids: ['gf-move'], preview: false });
    expect(state.tasks[1].column_id).toBe('col-ready');
    expect(state.writes.some((write) => write.path.endsWith('/move'))).toBeFalsy();

    const offPage = finding(page, 'Off-page task from page 51');
    await offPage.getByRole('button', { name: 'Approve', exact: true }).click();
    await offPage.getByRole('button', { name: 'Preview approved finding' }).click();
    await expect(preview).toContainText('Needs follow-up');
    await expect(preview).toContainText('Task changed after capture; review it manually.');
    await expect(preview.getByRole('button', { name: 'Continue to apply' })).toBeDisabled();
    await page.keyboard.press('Escape');
    expect(state.tasks.find((item) => item.id === 'task-1')?.column_id).toBe('col-active');
  });

  test('cancels, retries as a linked run and resolves a conflicting start', async ({ page }) => {
    const state = await installGithubMocks(page);
    await openAudits(page);
    const first = await startAudit(page, state);
    state.jobs.get(first)!.paused = true;
    const job = page.locator('[data-github-audit-job]');
    await job.getByRole('button', { name: 'Cancel audit' }).click();
    await expect(job).toContainText('Audit canceled');
    expect(writes(state, 'POST', `/audits/${first}/cancel`)).toHaveLength(1);
    await job.getByRole('button', { name: 'Retry audit' }).click();
    await expect(page).toHaveURL(/\/audits\/audit-gh-2$/);
    await expect(job).toContainText(`Retry of ${first}`);
    state.jobs.get('audit-gh-2')!.paused = true;

    await openAudits(page);
    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    await page.locator('[data-column-audit-scope]').getByRole('button', { name: /^Audit \d+ cards?$/ }).click();
    await expect(page.getByText('An audit is already running for this project.')).toBeVisible();
    await page.getByRole('button', { name: 'Open running audit' }).click();
    await expect(page).toHaveURL(/\/audits\/audit-gh-2$/);
  });

  test('blocks start until AI is connected and keeps external runs on the original flow', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const state = await installGithubMocks(page, { readiness: { github: 'connected', ai: 'not_connected' } });
    await openAudits(page);
    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    const scope = page.locator('[data-column-audit-scope]');
    await expect(scope).toContainText('Connect your AI account in Settings');
    await expect(scope.getByRole('button', { name: /^Audit \d+ cards?$/ })).toBeDisabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();

    await page.getByRole('button', { name: /audit-external/ }).click();
    await expect(page.getByRole('heading', { name: 'Board audit', exact: true })).toBeVisible();
    await expect(page.locator('[data-github-audit-job]')).toHaveCount(0);
    expect(state.requests.some((item) => item.includes('/audits/audit-external/evidence'))).toBeFalsy();
  });

  test('shows a typed evidence error without retained content at 390px', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const state = await installGithubMocks(page, { evidenceError: true });
    await openAudits(page);
    const auditId = await startAudit(page, state);
    await expect(page.getByText('Evidence access was revoked for this repository.')).toBeVisible();
    await expect(page.locator('.audit-finding')).toHaveCount(0);
    await expect(page.locator('[data-audit-evidence-manifest]')).toHaveCount(0);
    await expect(page.getByText('expected: PR row')).toHaveCount(0);
    expect(auditId).toMatch(/^audit-gh-/);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  });
});
