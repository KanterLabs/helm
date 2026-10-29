import { expect, type Page } from '@playwright/test';

// TC-316: audit history is read-only until TC-311 acceptance restores review.
// See docs/TC316_UI_CONTAINMENT_FAILURES.md (A1–A5).

/** Every control that started, reviewed, corrected or applied an audit. */
export const retiredAuditControls = [
  'Run audit', 'Run first audit', 'Start audit', 'GitHub repositories', 'Save repositories',
  'Approve', 'Approved', 'Dismiss', 'Dismissed', 'Edit destination', 'Save destination',
  'Preview approved finding', 'Continue to apply', 'Apply move', 'Cancel audit', 'Retry audit'
];

export async function expectNoAuditMutations(page: Page): Promise<void> {
  for (const name of retiredAuditControls) {
    await expect(page.getByRole('button', { name, exact: true }), `${name} stays parked`).toHaveCount(0);
  }
  await expect(page.locator('[data-audit-destination]')).toHaveCount(0);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
}

/** The paused capability is explained without claiming results or enforcement. */
export async function expectPausedExplanation(page: Page): Promise<void> {
  const paused = page.locator('[data-audit-paused]');
  await expect(paused).toHaveAttribute('role', 'status');
  await expect(paused).toContainText('New audits and finding review are paused');
  await expect(paused).toContainText('unverified');
  await expect(paused).not.toContainText(/applied|verified correct|disabled on the server/i);
}
