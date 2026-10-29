import { expect, test, type Page } from '@playwright/test';
import { api, harnessEnabled, saveArtifact, seed, startAuditByApi, waitForJob } from './support/columnAuditHarness';

// Parent-review failure inventory, written before fixes:
// 1. A project-wide counting cap silently omits later cards when narrowing scope.
// 2. Rotating the client session keeps an earlier human's selected corrections.
// 3. The evidence viewer hides the actual outcome of a captured check.
// This supplements the real lifecycle journey. Only the oversized counting
// response is synthetic; session rotation uses the actual app's Vite module.
test.use({ serviceWorkers: 'block', trace: 'off' });
test.skip(!harnessEnabled, 'Requires the real column-audit browser harness');

async function rotateSession(page: Page): Promise<void> {
  await page.evaluate(async () => {
    const modulePath = performance.getEntriesByType('resource').map((entry) => entry.name)
      .find((name) => new URL(name).pathname.endsWith('/src/app/state/session.svelte.ts'));
    if (!modulePath) throw new Error('The real client session module was not loaded');
    const { session } = await import(/* @vite-ignore */ modulePath);
    session.sessionGeneration += 1;
  });
}

test('counts a narrowed column beyond the audit batch limit without truncation', async ({ page }, info) => {
  const data = await seed(page.request, 'unverified');
  const template = await api(page.request, 'GET', `/tasks/${data.tasks.health.id}`);
  const requests: number[] = [];
  const cards = Array.from({ length: 1405 }, (_, i) => ({
    ...template, id: `synthetic-count-${i}`, column_id: i >= 1400 ? data.columns.completed : data.columns.active
  }));
  await page.route(`**/api/v1/projects/${data.project.id}/tasks?**`, async (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.get('limit') !== '200') return route.continue();
    const offset = Number(url.searchParams.get('cursor') || 0);
    requests.push(offset);
    await route.fulfill({ json: {
      data: cards.slice(offset, offset + 200), next_cursor: offset + 200 < cards.length ? String(offset + 200) : null
    } });
  });
  await page.goto(`/p/${data.project.slug}/audits`);
  await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
  const scope = page.locator('[data-column-audit-scope]');
  await scope.getByRole('radio', { name: 'Specific columns' }).check();
  await expect(scope.getByRole('checkbox', { name: 'Done (5)', exact: true })).toBeVisible();
  await scope.getByRole('checkbox', { name: 'Done (5)', exact: true }).check();
  await expect(scope.getByRole('button', { name: 'Audit 5 cards', exact: true })).toBeEnabled();
  saveArtifact(info, 'scope-beyond-batch-limit', { count: cards.length, done: 5, page_offsets: requests });
});

test('session rotation discards selected corrections and their review', async ({ page }, info) => {
  test.skip(Boolean(process.env.HELM_COLUMN_AUDIT_DIST), 'Client-session injection requires the Vite test harness');
  const data = await seed(page.request, 'lifecycle');
  const audit = await startAuditByApi(page.request, data.project.id);
  await waitForJob(page.request, audit);
  await page.goto(`/p/${data.project.slug}/audits/${audit}`);
  const selected = page.getByRole('checkbox', { name: `Select ${data.tasks.complete.key}: Mark done`, exact: true });
  await selected.check();
  await page.getByRole('button', { name: 'Review 1 correction', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Apply 1 correction', exact: true })).toBeEnabled();
  await rotateSession(page);
  await expect(page.locator('[data-column-audit-results]')).toBeVisible();
  await expect(selected).not.toBeChecked();
  await expect(page.locator('[data-correction-review]')).toBeHidden();
  saveArtifact(info, 'session-review-isolation', { selection_discarded: true, review_discarded: true });
});

test('a late card response cannot open a drawer in a new session', async ({ page }, info) => {
  test.skip(Boolean(process.env.HELM_COLUMN_AUDIT_DIST), 'Client-session injection requires the Vite test harness');
  const data = await seed(page.request, 'lifecycle');
  const audit = await startAuditByApi(page.request, data.project.id);
  await waitForJob(page.request, audit);
  const task = await api(page.request, 'GET', `/tasks/${data.tasks.complete.id}`);
  await page.goto(`/p/${data.project.slug}/audits/${audit}`);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route(`**/api/v1/tasks/${task.id}`, async (route) => {
    await gate;
    await route.fulfill({ json: task });
  });
  const requested = page.waitForRequest((request) => request.url().endsWith(`/api/v1/tasks/${task.id}`));
  await page.locator(`[data-audit-row="${task.key}"]`).getByRole('link', { name: task.title, exact: true }).click();
  await requested;
  await rotateSession(page);
  const returned = page.waitForResponse((response) => response.url().endsWith(`/api/v1/tasks/${task.id}`));
  release();
  await (await returned).finished();
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`/audits/${audit}$`));
  saveArtifact(info, 'late-card-session-isolation', { drawer_opened: false });
});

test('check citations show the observed outcome and pinned commit', async ({ page }, info) => {
  const data = await seed(page.request, 'lifecycle');
  const audit = await startAuditByApi(page.request, data.project.id);
  await waitForJob(page.request, audit);
  const findings = await api(page.request, 'GET', `/audits/${audit}/findings`);
  const finding = findings.data.find((entry: { task_id: string }) => entry.task_id === data.tasks.complete.id);
  const citation = finding.assessment.conditions[0].citation_ids[0];
  // Supplemental presentation fixture: preserve the real immutable response
  // shape but show a check citation. No altered assessment is submitted/applied.
  await page.route(`**/api/v1/audits/${audit}/evidence`, async (route) => {
    const response = await route.fetch();
    const evidence = await response.json();
    evidence.items = evidence.items.map((item: { id: string }) => item.id !== citation ? item : {
      ...item, kind: 'check', evidence_class: 'observed_check', title: 'browser', path: '',
      text: JSON.stringify({ name: 'browser', state: 'success', conclusion: 'success', sha: evidence.repositories[0].sha })
    });
    await route.fulfill({ response, json: evidence });
  });
  await page.goto(`/p/${data.project.slug}/audits/${audit}`);
  await page.locator(`[data-audit-row="${data.tasks.complete.key}"]`).getByRole('button', { name: 'Review evidence', exact: true }).click();
  const panel = page.getByRole('dialog', { name: `Evidence for ${data.tasks.complete.key}` });
  await expect(panel.getByText('Observed: Passed', { exact: true })).toBeVisible();
  await expect(panel).toContainText('main @ aaaaaaa');
  saveArtifact(info, 'check-outcome-presentation', { observed: 'Passed', commit: 'aaaaaaa' });
});
