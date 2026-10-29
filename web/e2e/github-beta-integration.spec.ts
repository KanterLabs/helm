import { expect, test } from '@playwright/test';
import { api, attachReceipt, repository, seed, selectRepository, sentinel, sourceSHA, startInUI, waitJob, waitJobStage } from './support/githubBetaFixture';

// v2 preview responses contain short-lived confirmation proofs; keep those out
// of trace archives and retain sanitized receipts plus explicit screenshots.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

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
    await expect(page.getByRole('heading', { name: 'Column audit', exact: true })).toBeVisible();
    const evidence = await api(page.request, 'GET', `/audits/${auditID}/evidence`);
    expect(evidence.repositories).toEqual([expect.objectContaining({ repository, sha: sourceSHA })]);
    expect(evidence.items.some((item: { kind: string; sha: string }) => item.kind === 'pull_request' && item.sha === sourceSHA)).toBeTruthy();
    await expect(page.locator('[data-audit-baseline]')).toContainText(`${repository} · main @ ${sourceSHA.slice(0, 7)}`);
    const findings = (await api(page.request, 'GET', `/audits/${auditID}/findings`)).data;
    expect(findings).toHaveLength(tasks.length);
    for (const finding of findings) {
      expect(finding.assessment).toMatchObject({ assessment_version: 2, placement: 'correction_recommended', target_semantic_state: 'ready', lifecycle_action: 'move' });
      const citations = [...finding.assessment.conditions, ...finding.assessment.basis].flatMap((item: { citation_ids: string[] }) => item.citation_ids);
      expect(citations.length).toBeGreaterThan(0);
      expect(citations.every((ref: string) => evidence.items.some((item: { id: string }) => item.id === ref))).toBeTruthy();
    }
    const changed = await api(page.request, 'GET', `/tasks/${tasks[1].id}`);
    await api(page.request, 'PATCH', `/tasks/${changed.id}`, { title: 'Changed after capture, preserved' }, changed.version);
    await page.reload();
    const finding = page.locator(`[data-audit-row="${tasks[0].key}"]`);
    const stale = page.locator(`[data-audit-row="${tasks[1].key}"]`);
    await expect(stale).toContainText('Card changed after this audit');
    await expect(stale.getByRole('checkbox')).toHaveCount(0);
    const stalePreview = await api(page.request, 'POST', `/audits/${auditID}/apply`, {
      assessment_version: 2, preview: true, items: [{ finding_id: findings.find((f: { task_id: string }) => f.task_id === tasks[1].id).id }],
    });
    expect(stalePreview.data[0]).toMatchObject({ outcome: 'skipped' });
    expect(stalePreview.data[0].confirmation_token).toBeUndefined();
    await finding.getByRole('button', { name: 'Review evidence', exact: true }).click();
    const panel = page.getByRole('dialog', { name: `Evidence for ${tasks[0].key}` });
    await expect(panel.locator('pre').filter({ hasText: 'const Ready = true' })).toBeVisible();
    await expect(panel).toContainText(`main @ ${sourceSHA.slice(0, 7)}`);
    await panel.getByText('Technical details', { exact: true }).click();
    await expect(panel).toContainText(sourceSHA);
    await panel.getByRole('button', { name: 'Close evidence', exact: true }).click();
    const beforeReview = await Promise.all(tasks.map(task => api(page.request, 'GET', `/tasks/${task.id}`)));
    await finding.getByRole('checkbox', { name: new RegExp(`Select ${tasks[0].key}: Move to Ready`) }).check();
    expect(await Promise.all(tasks.map(task => api(page.request, 'GET', `/tasks/${task.id}`)))).toEqual(beforeReview);
    const previewResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/audits/${auditID}/apply`) && r.request().postDataJSON().preview === true);
    await page.locator('[data-review-bar]').getByRole('button', { name: 'Review 1 correction', exact: true }).click();
    const preview = await (await previewResponse).json();
    expect(preview.data[0]).toMatchObject({ outcome: 'eligible', task_id: tasks[0].id });
    const review = page.locator('[data-correction-review]');
    await expect(review.getByRole('heading', { name: 'Review 1 correction', exact: true })).toBeVisible();
    await expect(review.getByRole('button', { name: 'Apply 1 correction', exact: true })).toBeEnabled();
    expect(await Promise.all(tasks.map(task => api(page.request, 'GET', `/tasks/${task.id}`)))).toEqual(beforeReview);
    const appliedResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/audits/${auditID}/apply`) && r.request().postDataJSON().preview === false);
    await review.getByRole('button', { name: 'Apply 1 correction', exact: true }).click();
    const applied = await (await appliedResponse).json();
    expect(applied.data).toEqual([expect.objectContaining({ finding_id: findings.find((f: { task_id: string }) => f.task_id === tasks[0].id).id, task_id: tasks[0].id, outcome: 'applied', outcome_known: true })]);
    const receipt = page.locator('[data-correction-receipt]');
    await expect(receipt.locator(`[data-receipt-item="${tasks[0].key}"]`)).toContainText('Applied');
    await page.screenshot({ path: info.outputPath('explicit-apply-receipt.png'), fullPage: true });
    await receipt.getByRole('button', { name: 'Back to results', exact: true }).click();
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
      await expect(page.locator('[data-audit-baseline]')).toContainText(sourceSHA.slice(0, 7));
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: info.outputPath(`${name}.png`), fullPage: true });
    }
    const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
    expect(storage.includes(sentinel), 'Browser storage must not contain the synthetic credential').toBeFalsy();
    expect(leaks).toEqual([]);
    await attachReceipt(info, 'real-backend-evidence-findings-and-apply.json', { auditID, evidence, findings, stalePreview, preview, applied });

    // Keep an eligible proof in memory so revocation is checked at final apply,
    // even when a human already reviewed the untouched third card.
    const untouchedFinding = findings.find((f: { task_id: string }) => f.task_id === tasks[2].id);
    const beforeRevocation = await api(page.request, 'POST', `/audits/${auditID}/apply`, {
      assessment_version: 2, preview: true, items: [{ finding_id: untouchedFinding.id }],
    });
    expect(beforeRevocation.data[0].outcome).toBe('eligible');
    await page.locator(`[data-audit-row="${tasks[2].key}"]`).getByRole('button', { name: 'Review evidence', exact: true }).click();
    await expect(page.getByRole('dialog', { name: `Evidence for ${tasks[2].key}` })).toBeVisible();

    // Refresh after observed upstream revocation must remove already-rendered
    // private findings, captured source and any open citation/preview content.
    try {
      await page.request.put('/__githubbeta/state', { data: { revoked: true } });
      await api(page.request, 'POST', '/integrations/github/check');
      await page.reload();
      await expect(page.locator('[data-column-audit-results]')).toHaveCount(0);
      await expect(page.getByRole('dialog', { name: /Evidence for/ })).toHaveCount(0);
      await expect(page.locator('[data-audit-evidence-manifest]')).toHaveCount(0);
      await expect(page.getByText(sourceSHA, { exact: false })).toHaveCount(0);
      await expect(page.getByRole('alert')).toContainText(/Source access/);
      const deniedReads: { resource: string; status: number; body: unknown }[] = [];
      for (const resource of ['evidence', 'findings']) {
        const response = await page.request.get(`/api/v1/audits/${auditID}/${resource}`);
        expect(response.status()).toBe(409);
        const body = await response.json();
        expect(body.error.code).toBe('evidence_unavailable');
        deniedReads.push({ resource, status: response.status(), body });
      }
      const revokedApplyResponse = await page.request.post(`/api/v1/audits/${auditID}/apply`, {
        headers: { Origin: process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18082', 'Idempotency-Key': `revoked-${auditID}` },
        data: { assessment_version: 2, preview: false, items: [{ finding_id: untouchedFinding.id, confirmation_token: beforeRevocation.data[0].confirmation_token }] },
      });
      expect(revokedApplyResponse.status()).toBe(200);
      const revokedApply = await revokedApplyResponse.json();
      expect(revokedApply.data[0]).toMatchObject({ outcome: 'skipped', outcome_known: true });
      expect((await api(page.request, 'GET', `/tasks/${tasks[2].id}`)).column_id).toBe(tasks[2].column_id);
      await attachReceipt(info, 'revoked-source-read-and-apply.json', { deniedReads, revokedApply });
      await page.screenshot({ path: info.outputPath('revoked-access.png'), fullPage: true });
    } finally {
      await page.request.put('/__githubbeta/state', { data: { revoked: false } });
      await api(page.request, 'POST', '/integrations/github/check');
    }
  });

  test('audits a branch without PRs and fences cancellation before explicit retry', async ({ page }, info) => {
    const { project, tasks } = await seed(page.request, `B${Date.now().toString().slice(-7)}`);
    await selectRepository(page, project);
    await page.request.put('/__githubbeta/state', { data: { delay_ms: 8000 } });
    const auditID = await startInUI(page, project);
    await page.goto(`/p/${project.slug}/audits/${auditID}`);
    await waitJobStage(page.request, auditID, 'analyzing');
    await page.getByRole('button', { name: /Cancel audit/i }).click();
    await waitJob(page.request, auditID, 'cancelled');
    await page.request.put('/__githubbeta/state', { data: { delay_ms: 0 } });
    const retryResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/audits/${auditID}/retry`));
    await page.getByRole('button', { name: /Retry audit/i }).click();
    const retryID = (await (await retryResponse).json()).audit.id;
    const job = await waitJob(page.request, retryID);
    expect(job.retry_of).toBe(auditID);
    const evidence = await api(page.request, 'GET', `/audits/${retryID}/evidence`);
    expect(evidence.items.some((item: { kind: string; source_role: string; evidence_class: string; path: string; sha: string }) =>
      item.kind === 'file' && item.source_role === 'baseline' && item.path === 'ready.go' && item.sha === sourceSHA)).toBeTruthy();
    for (const task of tasks) expect(evidence.items.some((item: { source_role: string; evidence_class: string; task_ids: string[] }) =>
      item.source_role === 'task_context' && item.evidence_class === 'task_fact' && item.task_ids.includes(task.id))).toBeTruthy();
    expect(evidence.items.some((item: { kind: string }) => item.kind === 'pull_request')).toBeFalsy();
    expect(evidence.repositories[0].sha).toBe(sourceSHA);
    for (const task of tasks) expect((await api(page.request, 'GET', `/tasks/${task.id}`)).column_id).toBe(task.column_id);
    await page.goto(`/p/${project.slug}/audits/${retryID}`);
    await page.reload();
    await expect(page.locator('[data-audit-row]')).toHaveCount(tasks.length);
    const original = await api(page.request, 'GET', `/audits/${auditID}/job`);
    expect(original.outcome).toBe('cancelled');
    const oldFindings = (await api(page.request, 'GET', `/audits/${auditID}/findings`)).data;
    expect(oldFindings).toHaveLength(0);
    const findings = (await api(page.request, 'GET', `/audits/${retryID}/findings`)).data;
    await page.getByRole('button', { name: 'All audits', exact: true }).click();
    await expect(page.locator('.audit-run-row').filter({ hasText: auditID })).toBeVisible();
    await expect(page.locator('.audit-run-row').filter({ hasText: retryID })).toBeVisible();
    await attachReceipt(info, 'branch-only-retry.json', { original, oldFindings, job, evidence, findings });
  });
});
