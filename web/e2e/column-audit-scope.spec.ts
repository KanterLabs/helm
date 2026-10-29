import { expect, test, type Page } from '@playwright/test';
import {
  api, expectNoHorizontalScroll, harnessEnabled, repository, saveArtifact, seed, setState, setTheme, shot,
  startAuditByApi, waitForJob
} from './support/columnAuditHarness';

// Scope, running and layout journeys against the real backend harness.
// Failure inventory: docs/AUDIT_COLUMN_UI_FAILURES.md.
test.use({ serviceWorkers: 'block', trace: 'on' });

const scopePanel = (page: Page) => page.locator('[data-column-audit-scope]');
const results = (page: Page) => page.locator('[data-column-audit-results]');

test.describe('Column audit scope and layout against the real backend', () => {
  test.skip(!harnessEnabled, 'Requires the TC-310 column-audit harness (HELM_COLUMN_AUDIT_E2E=1)');
  test.setTimeout(150_000);

  test.afterEach(async ({ request }) => {
    await setState(request, { delay_ms: 0, ai_offline: false, revoked: false });
  });

  test('scope survives interruption, PR-head baseline is explicit, run is resumable, cancel and retry are distinct', async ({ page }, info) => {
    const { project, tasks } = await seed(page.request, 'unverified');
    await setState(page.request, { ai_offline: true });
    await page.goto(`/p/${project.slug}/audits`);
    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    const scope = scopePanel(page);
    // U5: readiness is fixed where the audit starts.
    await expect(scope).toContainText(/Connect your AI account/i);
    await expect(scope.getByRole('link', { name: /Settings/ })).toBeVisible();
    await expect(scope.getByRole('button', { name: /^Audit \d+ cards?$/ })).toBeDisabled();
    await setState(page.request, { ai_offline: false });
    await scope.getByRole('button', { name: 'Check again' }).click();

    // U1/U2: every card, including Blocked and Done, counted by the server.
    await expect(scope.getByRole('radio', { name: /All cards/ })).toBeChecked();
    await expect(scope).toContainText('Includes 0 Done and 1 Blocked');
    await expect(scope.getByRole('button', { name: 'Audit 2 cards', exact: true })).toBeEnabled();
    await scope.getByRole('radio', { name: /Specific columns/ }).check();
    await expect(scope.getByRole('button', { name: /^Audit 0 cards$/ })).toBeDisabled();
    await scope.getByRole('checkbox', { name: /In progress/ }).check();
    await expect(scope.getByRole('button', { name: 'Audit 1 card', exact: true })).toBeEnabled();

    // U3: the PR head is an explicit baseline choice; branches the API cannot honor are absent.
    const baseline = scope.getByLabel(`Baseline for ${repository}`);
    await expect(baseline.locator('option')).toHaveText([/main.*saved branch/, /Pull request head/]);
    await baseline.selectOption({ label: 'Pull request head…' });
    await scope.getByLabel('Pull request number').fill('7');
    await expect(scope).toContainText('Cards are judged against committed work here');

    // U4: settings detour and a reload keep the intended scope.
    await scope.getByRole('button', { name: 'Edit saved branch' }).click();
    await expect(page.locator('#audit-github-repositories')).toBeVisible();
    await page.reload();
    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    await expect(scope.getByRole('radio', { name: /Specific columns/ })).toBeChecked();
    await expect(scope.getByRole('checkbox', { name: /In progress/ })).toBeChecked();
    await expect(scope.getByLabel('Pull request number')).toHaveValue('7');
    await scope.getByRole('radio', { name: /All cards/ }).check();
    await shot(page, info, '10-scope-pr-baseline');

    // U6/U7: real stages, leave/return through the deep link.
    await setState(page.request, { delay_ms: 5000 });
    const started = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/github-audits'));
    await scope.getByRole('button', { name: 'Audit 2 cards', exact: true }).click();
    const start = await started;
    expect(start.request().postDataJSON()).toEqual({ baseline: { repository, pull_request: 7 } });
    const auditId = (await start.json()).audit.id as string;
    const running = page.locator('[data-column-audit-run]');
    await expect(running).toContainText('Leaving this page is safe');
    await expect(running.locator('[aria-current="step"]')).toHaveText(/Reading committed work|Checking card criteria|Preparing corrections/);
    await expect(running).not.toContainText('%');
    await shot(page, info, '11-running');
    await page.goto(`/p/${project.slug}`);
    await page.goto(`/p/${project.slug}/audits/${auditId}`);
    await waitForJob(page.request, auditId);

    // U10: nothing verified is not an all-clear.
    await expect(results(page).getByRole('heading', { name: 'No placement judgments could be verified' })).toBeVisible({ timeout: 20_000 });
    await expect(results(page)).toContainText('2 cards checked · 0 already correct · 2 couldn\'t be verified');
    await expect(page.getByRole('tab', { name: /Corrections/ })).toHaveCount(0);
    await expect(page.getByRole('tab', { name: /Already correct/ })).toHaveCount(0);
    await expect(results(page).locator('[data-audit-baseline]')).toContainText('PR #7 head @ ccccccc');
    await shot(page, info, '12-nothing-verified');

    // U8: Re-audit keeps a focused scope; cancel is distinct and retry keeps scope.
    await page.locator(`[data-audit-row="${tasks.health.key}"]`).getByRole('button', { name: /Re-audit/ }).click();
    await expect(scope.getByRole('radio', { name: /Selected cards \(1\)/ })).toBeChecked();
    const restarted = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/github-audits'));
    await scope.getByRole('button', { name: 'Audit 1 card', exact: true }).click();
    const second = await restarted;
    expect(second.request().postDataJSON()).toMatchObject({ task_ids: [tasks.health.id] });
    const secondId = (await second.json()).audit.id as string;
    await page.locator('[data-column-audit-run]').getByRole('button', { name: 'Cancel audit' }).click();
    await expect(page.locator('[data-column-audit-run]')).toContainText('Audit canceled');
    await expect(page.locator('[data-column-audit-run]')).toContainText('No cards were changed');
    const retried = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith(`/audits/${secondId}/retry`));
    await page.getByRole('button', { name: 'Retry audit' }).click();
    const retryId = (await (await retried).json()).audit.id as string;
    await expect(page).toHaveURL(new RegExp(`/audits/${retryId}$`));
    const job = await waitForJob(page.request, retryId);
    expect(job.task_count).toBe(1);
    saveArtifact(info, 'scope-requests', { start: start.request().postDataJSON(), reaudit: second.request().postDataJSON(), retry_job: job });
  });

  test('mobile 320/390, dark theme and keyboard review @webkit', async ({ page }, info) => {
    const { project, tasks } = await seed(page.request, 'lifecycle');
    const auditId = await startAuditByApi(page.request, project.id);
    await waitForJob(page.request, auditId);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/p/${project.slug}/audits/${auditId}`);
    await expect(results(page).getByRole('heading', { name: '6 column corrections suggested' })).toBeVisible({ timeout: 20_000 });
    await expectNoHorizontalScroll(page);
    await shot(page, info, `20-results-390-light-${info.project.name}`);

    // Keyboard: open evidence, focus stays in the sheet, Escape returns focus.
    const row = page.locator(`[data-audit-row="${tasks.complete.key}"]`);
    const open = row.getByRole('button', { name: 'Review evidence' });
    await open.focus();
    await page.keyboard.press('Enter');
    const panel = page.getByRole('dialog', { name: `Evidence for ${tasks.complete.key}` });
    await expect(panel).toBeVisible();
    for (let i = 0; i < 12; i += 1) {
      await page.keyboard.press('Tab');
      expect(await panel.evaluate((node) => node.contains(document.activeElement))).toBeTruthy();
    }
    await expectNoHorizontalScroll(page);
    await shot(page, info, `21-evidence-390-${info.project.name}`);
    await page.keyboard.press('Escape');
    await expect(open).toBeFocused();

    const select = row.getByRole('checkbox', { name: /Mark done/ });
    await select.focus();
    await page.keyboard.press('Space');
    await expect(select).toBeChecked();
    await expect(page.getByRole('status').filter({ hasText: /1 correction selected/ })).toHaveCount(1);
    const reviewButton = page.locator('[data-review-bar]').getByRole('button', { name: 'Review 1 correction' });
    await reviewButton.focus();
    await page.keyboard.press('Enter');
    const review = page.locator('[data-correction-review]');
    await expect(review.getByRole('button', { name: 'Apply 1 correction', exact: true })).toBeEnabled();
    await expectNoHorizontalScroll(page);
    await shot(page, info, `22-review-390-${info.project.name}`);
    await review.getByRole('button', { name: 'Back to results' }).click();
    // Leaving the review applied nothing.
    expect((await api(page.request, 'GET', `/tasks/${tasks.complete.id}`)).completed_at ?? null).toBeNull();

    await page.setViewportSize({ width: 320, height: 720 });
    await setTheme(page, 'dark');
    await expect(results(page)).toBeVisible({ timeout: 20_000 });
    await expect(results(page).locator('[data-audit-baseline]')).toBeVisible();
    await expectNoHorizontalScroll(page);
    await shot(page, info, `23-results-320-dark-${info.project.name}`);
    await page.getByRole('tab', { name: /Couldn.t verify/ }).click();
    await expectNoHorizontalScroll(page);
    await shot(page, info, `24-unverified-320-dark-${info.project.name}`);

    await page.setViewportSize({ width: 1440, height: 960 });
    await shot(page, info, `25-results-desktop-dark-${info.project.name}`);
    await setTheme(page, 'light');
  });
});
