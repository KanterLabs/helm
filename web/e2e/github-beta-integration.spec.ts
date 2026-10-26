import { expect, test } from '@playwright/test';
import { api, repository, seed, selectRepository, sentinel, sourceSHA, startInUI, waitJob } from './support/githubBetaFixture';

test.use({ trace: 'on' });

test.describe('GitHub beta real backend', () => {
  test.skip(process.env.HELM_GITHUB_BETA_E2E !== '1', 'Dedicated synthetic upstream harness required');
  test.setTimeout(120_000);

  test('persists PRs, reviews exact evidence and explicitly applies only a safe unchanged Ready move', async ({ page }, info) => {
    const leaks: string[] = [];
    page.on('response', async response => {
      if (response.url().includes('/api/')) {
        try { if ((await response.text()).includes(sentinel)) leaks.push(response.url()); } catch { /* navigation can close a response */ }
      }
    });
    const { project, tasks } = await seed(page.request, `A${Date.now().toString().slice(-7)}`);
    await selectRepository(page, project);
    await page.goto(`/p/${project.slug}/tasks/${tasks[0].key}`);
    await page.getByLabel(/Pull request URL/i).fill(`https://github.com/${repository}/pull/42`);
    await page.getByRole('button', { name: /Link pull request/i }).click();
    await expect(page.getByText('Synthetic merged implementation', { exact: false })).toBeVisible();
    const beforeRestart = await api(page.request, 'GET', `/tasks/${tasks[0].id}/artifacts`);
    expect(beforeRestart.data).toHaveLength(1);
    expect(beforeRestart.data[0].snapshot.state).toBe('merged');
    await page.request.post('/__githubbeta/restart');
    await page.reload();
    await page.getByRole('button', { name: /Refresh pull request/i }).click();
    await expect(page.getByText('Synthetic merged implementation', { exact: false })).toBeVisible();
    const afterRestart = await api(page.request, 'GET', `/tasks/${tasks[0].id}/artifacts`);
    expect(afterRestart.data[0].id).toBe(beforeRestart.data[0].id);
    expect((await api(page.request, 'GET', `/tasks/${tasks[0].id}`)).column_id).toBe(tasks[0].column_id);
    await page.goto(`/p/${project.slug}`);
    await expect(page.locator('.task-card').filter({ hasText: tasks[0].title })).toContainText('#42');

    const auditID = await startInUI(page, project);
    await page.goto(`/p/${project.slug}`);
    await waitJob(page.request, auditID);
    await page.goto(`/p/${project.slug}/audits/${auditID}`);
    await expect(page.getByRole('heading', { name: 'Board audit', exact: true })).toBeVisible();
    const evidence = await api(page.request, 'GET', `/audits/${auditID}/evidence`);
    expect(evidence.repositories).toEqual([expect.objectContaining({ repository, sha: sourceSHA })]);
    expect(evidence.items.some((item: { kind: string; sha: string }) => item.kind === 'pull_request' && item.sha === sourceSHA)).toBeTruthy();
    await expect(page.getByText(sourceSHA, { exact: false }).first()).toBeVisible();
    const findings = (await api(page.request, 'GET', `/audits/${auditID}/findings`)).data;
    for (const finding of findings) {
      expect(finding.evidence_refs.every((ref: string) => evidence.items.some((item: { id: string }) => item.id === ref))).toBeTruthy();
    }
    const changed = await api(page.request, 'GET', `/tasks/${tasks[1].id}`);
    await api(page.request, 'PATCH', `/tasks/${changed.id}`, { title: 'Changed after capture, preserved' }, changed.version);
    await page.reload();
    const finding = page.locator('.audit-finding').filter({ hasText: tasks[0].title });
    await finding.getByRole('button', { name: /ready.go/ }).click();
    await expect(finding.locator('[data-audit-evidence-viewer]')).toContainText(sourceSHA);
    await finding.getByRole('button', { name: 'Close', exact: true }).click();
    await finding.getByRole('button', { name: 'Approve', exact: true }).click();
    await expect(finding.locator('.audit-review-state')).toHaveText('Approved');
    const approved = await api(page.request, 'GET', `/tasks/${tasks[0].id}`);
    expect(approved.column_id).toBe(tasks[0].column_id);
    await finding.getByRole('button', { name: 'Preview approved finding', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Preview approved finding' })).toBeVisible();
    expect((await api(page.request, 'GET', `/tasks/${tasks[0].id}`)).column_id).toBe(tasks[0].column_id);
    await page.getByRole('button', { name: 'Continue to apply', exact: true }).click();
    await page.getByRole('alertdialog', { name: 'Apply this move?' }).getByRole('button', { name: 'Apply move', exact: true }).click();
    await expect(page.getByRole('alertdialog', { name: 'Apply this move?' })).toBeHidden();
    const columns = (await api(page.request, 'GET', `/projects/${project.id}/columns`)).data;
    expect((await api(page.request, 'GET', `/tasks/${tasks[0].id}`)).column_id).toBe(columns.find((c: { semantic_state: string }) => c.semantic_state === 'ready').id);
    expect((await api(page.request, 'GET', `/tasks/${tasks[1].id}`)).column_id).toBe(tasks[1].column_id);
    expect((await api(page.request, 'GET', `/tasks/${tasks[2].id}`)).column_id).toBe(tasks[2].column_id);
    for (const [name, width, height, scheme] of [
      ['desktop-light', 1440, 1000, 'light'], ['desktop-dark', 1440, 1000, 'dark'], ['phone-dark', 390, 844, 'dark'], ['phone-light', 320, 844, 'light'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await page.emulateMedia({ colorScheme: scheme });
      await page.evaluate((theme) => localStorage.setItem('helm.theme', theme), scheme);
      await page.reload();
      await expect(page.locator('html')).toHaveAttribute('data-theme', scheme);
      await expect(page.locator('[data-audit-evidence-manifest]')).toContainText(sourceSHA);
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: info.outputPath(`${name}.png`), fullPage: true });
    }
    const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
    expect(storage.includes(sentinel), 'Browser storage must not contain the synthetic credential').toBeFalsy();
    expect(leaks).toEqual([]);
    await info.attach('real-backend-evidence', { body: JSON.stringify({ auditID, evidence, findings }, null, 2), contentType: 'application/json' });

    // Refresh after observed upstream revocation must remove already-rendered
    // private findings, captured source and any open citation/preview content.
    await page.request.put('/__githubbeta/state', { data: { revoked: true } });
    await api(page.request, 'POST', '/integrations/github/check');
    await page.getByRole('button', { name: /Refresh/, exact: false }).first().click();
    await expect(page.locator('.audit-finding')).toHaveCount(0);
    await expect(page.locator('[data-audit-evidence-manifest]')).toHaveCount(0);
    await expect(page.getByText(sourceSHA, { exact: false })).toHaveCount(0);
    await expect(page.getByRole('alert')).toContainText(/Source access/);
    await page.screenshot({ path: info.outputPath('revoked-access.png'), fullPage: true });
    await page.request.put('/__githubbeta/state', { data: { revoked: false } });
    await api(page.request, 'POST', '/integrations/github/check');
  });

  test('audits a branch without PRs and fences cancellation before explicit retry', async ({ page }, info) => {
    const { project, tasks } = await seed(page.request, `B${Date.now().toString().slice(-7)}`);
    await selectRepository(page, project);
    await page.request.put('/__githubbeta/state', { data: { delay_ms: 8000 } });
    const auditID = await startInUI(page, project);
    await page.goto(`/p/${project.slug}/audits/${auditID}`);
    await expect.poll(async () => (await api(page.request, 'GET', `/audits/${auditID}/job`)).stage).toBe('analyzing');
    await page.getByRole('button', { name: /Cancel audit/i }).click();
    await waitJob(page.request, auditID, 'cancelled');
    await page.request.put('/__githubbeta/state', { data: { delay_ms: 0 } });
    const retryResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/audits/${auditID}/retry`));
    await page.getByRole('button', { name: /Retry audit/i }).click();
    const retryID = (await (await retryResponse).json()).audit.id;
    const job = await waitJob(page.request, retryID);
    expect(job.retry_of).toBe(auditID);
    const evidence = await api(page.request, 'GET', `/audits/${retryID}/evidence`);
    expect(evidence.items.every((item: { kind: string }) => item.kind === 'file')).toBeTruthy();
    expect(evidence.repositories[0].sha).toBe(sourceSHA);
    for (const task of tasks) expect((await api(page.request, 'GET', `/tasks/${task.id}`)).column_id).toBe(task.column_id);
    await page.goto(`/p/${project.slug}/audits/${retryID}`);
    await page.reload();
    await expect(page.locator('.audit-finding')).toHaveCount(tasks.length);
    await info.attach('branch-only-retry', { body: JSON.stringify({ original: await api(page.request, 'GET', `/audits/${auditID}/job`), job, evidence }, null, 2), contentType: 'application/json' });
  });
});
