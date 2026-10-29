import { expect, type Page, type TestInfo } from '@playwright/test';
import { api, saveArtifact } from './columnAuditHarness';

/** Create and review actual external history; setup errors must fail the journey. */
export async function assertLegacyColumnAudit(page: Page, info: TestInfo, project: { id: string; slug: string }, taskId: string): Promise<void> {
  const task = await api(page.request, 'GET', `/tasks/${taskId}`);
  const legacy = await api(page.request, 'POST', `/projects/${project.id}/audits`, { scope: 'board' });
  const finding = await api(page.request, 'POST', `/audits/${legacy.id}/findings`, {
    task_id: taskId, captured_version: task.version, source_column: task.column_id,
    verdict: 'correct', confidence: 0.7, reason: 'Legacy external observation', evidence_refs: []
  });
  await api(page.request, 'POST', `/audits/${legacy.id}/finalize`, { status: 'complete' });
  await page.goto(`/p/${project.slug}/audits/${legacy.id}`);
  await expect(page.getByRole('heading', { name: 'Board audit', exact: true })).toBeVisible();
  await expect(page.locator('[data-column-audit-results]')).toHaveCount(0);
  const row = page.locator('.audit-finding').filter({ hasText: 'Legacy external observation' });
  await expect(row).toHaveCount(1);
  await row.getByRole('button', { name: 'Approve', exact: true }).click();
  await expect(row.locator('.audit-review-state')).toHaveText('Approved');
  saveArtifact(info, 'legacy-history-review', { audit: legacy.id, finding: finding.id, approved: true });
}
