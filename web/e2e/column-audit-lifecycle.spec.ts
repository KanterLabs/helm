import { expect, test, type Page, type Request } from '@playwright/test';
import { assertLegacyColumnAudit } from './support/legacyColumnAudit';
import {
  api, baseSHA, dbTask, editTask, expectNoHorizontalScroll, harnessEnabled, repository, restart, saveArtifact,
  seed, setState, shot, snapshot, waitForJob
} from './support/columnAuditHarness';

// Decisive TC-310 journey against the real backend harness. Failure inventory:
// docs/AUDIT_COLUMN_UI_FAILURES.md (U-numbers in comments).
test.use({ serviceWorkers: 'block', trace: 'on' });

const row = (page: Page, key: string) => page.locator(`[data-audit-row="${key}"]`);
const results = (page: Page) => page.locator('[data-column-audit-results]');

test.describe('Column audit against the real backend', () => {
  test.skip(!harnessEnabled, 'Requires the TC-310 column-audit harness (HELM_COLUMN_AUDIT_E2E=1)');
  test.setTimeout(180_000);

  test('scope → run → evidence → review → apply → receipt, with stale skip and unknown-outcome retry', async ({ page }, info) => {
    await page.setViewportSize({ width: 1440, height: 960 });
    const data = await seed(page.request, 'lifecycle', 52);
    const { project, tasks } = data;
    const key = (tag: string) => tasks[tag].key;

    // U1/U2: the board Actions entry opens the scope with a server-paginated count.
    await page.goto(`/p/${project.slug}`);
    await page.locator('[data-board-actions-trigger]').click();
    await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/audits`));
    const scope = page.locator('[data-column-audit-scope]');
    await expect(scope.getByText('Cards are judged against committed work here')).toBeVisible();
    await expect(scope).toContainText(repository);
    await expect(scope.getByRole('radio', { name: /All cards/ })).toBeChecked();
    await expect(scope).toContainText('Includes 55 Done and 1 Blocked');
    const startButton = scope.getByRole('button', { name: 'Audit 62 cards', exact: true });
    await expect(startButton).toBeEnabled();
    await shot(page, info, '01-scope-desktop');
    const started = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith(`/projects/${project.id}/github-audits`));
    await startButton.click();
    const startBody = (await started).request().postDataJSON();
    expect(startBody).not.toHaveProperty('states');
    expect(startBody).not.toHaveProperty('baseline');
    const auditId = ((await (await started).json()).audit.id) as string;
    await expect(page).toHaveURL(new RegExp(`/audits/${auditId}$`));
    await waitForJob(page.request, auditId);

    // A card edited after capture becomes stale (U14).
    await editTask(page.request, tasks.stale.id, 'Check prerequisites on completion (edited)');
    await page.reload();

    // U9/U10/U11: the answer first, balanced against the server summary.
    await expect(results(page).getByRole('heading', { name: '6 column corrections suggested' })).toBeVisible({ timeout: 20_000 });
    await expect(results(page)).toContainText('62 cards checked · 54 already correct · 2 couldn\'t be verified');
    await expect(results(page).locator('[data-audit-baseline]')).toContainText(`${repository} · main @ ${baseSHA.slice(0, 7)}`);
    await expect(results(page).getByText(baseSHA, { exact: false })).toHaveCount(0);
    await expect(page.getByRole('tab', { name: /Corrections\s*6/ })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('tab', { name: /Already correct\s*54/ })).toBeVisible();
    await expect(page.getByRole('tab', { name: /Couldn.t verify\s*2/ })).toBeVisible();
    await expect(results(page).getByText(/confidence|0%/i)).toHaveCount(0);

    // U15: a card beyond the board's first page shows its captured title.
    await expect(row(page, key('reopen'))).toContainText('Export board as JSON');
    const complete = row(page, key('complete'));
    await expect(complete).toContainText('Add task dependency checks');
    await expect(complete.locator('[data-audit-route]')).toHaveText(/In progress\s*→\s*Done/);
    await expect(complete).toContainText('Every completion condition is implemented at the audited commit.');
    await expect(complete).toContainText('2 of 2 conditions supported');
    // U20: nothing is preselected and there is no review bar yet.
    await expect(page.locator('[data-review-bar]')).toHaveCount(0);
    await expect(results(page).getByRole('checkbox', { checked: true })).toHaveCount(0);

    // U13: a claimed card keeps its recommendation with an explicit blocker.
    const claimed = row(page, key('claimed'));
    await expect(claimed).toContainText('Done recommended');
    await expect(claimed).toContainText('Can\'t apply while this card is claimed');
    await expect(claimed.getByRole('checkbox')).toHaveCount(0);
    // U14: a stale correction stays visible, muted, with Re-audit and no selection.
    const stale = row(page, key('stale'));
    await expect(stale).toContainText('Card changed after this audit');
    await expect(stale.getByRole('button', { name: /Re-audit/ })).toBeVisible();
    await expect(stale.getByRole('checkbox')).toHaveCount(0);

    // U17/U18: evidence by criterion, literal source, PR-only evidence apart.
    const reviewEvidence = complete.getByRole('button', { name: 'Review evidence' });
    await reviewEvidence.click();
    const panel = page.getByRole('dialog', { name: `Evidence for ${key('complete')}` });
    await expect(panel).toBeVisible();
    await expect(panel.locator('[data-criterion-result="supported"]')).toHaveCount(2);
    await expect(panel).toContainText('Reject completion with unmet prerequisites in internal/deps.go.');
    await expect(panel).toContainText('internal/deps.go:');
    await expect(panel).toContainText(`main @ ${baseSHA.slice(0, 7)}`);
    await expect(panel.locator('pre').filter({ hasText: '<script>alert(1)</script>' })).toHaveCount(1);
    await expect(panel.locator('[data-evidence-pr-only]')).toContainText('#7');
    await expect(panel.locator('[data-evidence-pr-only]')).toContainText('not on main');
    await shot(page, info, '03-evidence-desktop');
    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    await expect(reviewEvidence).toBeFocused();
    await reviewEvidence.click();
    await page.goBack();
    await expect(panel).toBeHidden();
    await expect(page).toHaveURL(new RegExp(`/audits/${auditId}$`));
    await reviewEvidence.click();
    await panel.getByRole('checkbox', { name: /Mark done/ }).check();
    await panel.getByRole('button', { name: 'Close evidence' }).click();
    await expect(complete.getByRole('checkbox', { name: /Mark done/ })).toBeChecked();

    // Select the remaining eligible corrections, including the reopening card
    // beyond the board's first Done page (U2/U15).
    await row(page, key('reopen')).getByRole('checkbox', { name: /Reopen/ }).check();
    await row(page, key('bugfix')).getByRole('checkbox', { name: /Resolve as fixed/ }).check();
    await row(page, key('bugreopen')).getByRole('checkbox', { name: /Reopen bug/ }).check();
    const bar = page.locator('[data-review-bar]');
    await expect(bar.getByRole('button', { name: 'Review 4 corrections' })).toBeVisible();
    await shot(page, info, '02-results-desktop');

    // U21/U22: a read-only preview is the one confirmation screen.
    const applyCalls: Request[] = [];
    page.on('request', (request) => {
      if (request.method() === 'POST' && request.url().endsWith(`/audits/${auditId}/apply`)) applyCalls.push(request);
    });
    const before = await snapshot(page.request, project.id);
    await bar.getByRole('button', { name: 'Review 4 corrections' }).click();
    const review = page.locator('[data-correction-review]');
    await expect(review.getByRole('heading', { name: 'Review 4 corrections' })).toBeVisible();
    await expect.poll(() => applyCalls.length).toBe(1);
    expect(applyCalls[0].postDataJSON()).toMatchObject({ assessment_version: 2, preview: true });
    expect(JSON.stringify(applyCalls[0].postDataJSON())).not.toContain('confirmation_token');
    const resolveItem = review.locator(`[data-review-item="${key('bugfix')}"]`);
    const reopenBugItem = review.locator(`[data-review-item="${key('bugreopen')}"]`);
    await expect(resolveItem).toContainText('Resolve as fixed');
    // Missing bug inputs are not counted; the honest count is 2 until reviewed again.
    await expect(resolveItem).toContainText('Needs a resolution');
    await expect(review.getByRole('button', { name: 'Apply 2 corrections', exact: true })).toBeEnabled();
    await resolveItem.getByLabel('Resolution').selectOption('fixed');
    await reopenBugItem.getByLabel('Reopen reason').fill('The remember-me flag is still dropped after refresh.');
    // Any input change invalidates the earlier preview proof.
    await expect(review.getByRole('button', { name: /^Apply \d+ corrections?$/ })).toBeDisabled();
    await review.getByRole('button', { name: 'Update review' }).click();
    await expect.poll(() => applyCalls.length).toBe(2);
    expect(applyCalls[1].postDataJSON().items).toEqual(expect.arrayContaining([
      expect.objectContaining({ resolution: 'fixed' }),
      expect.objectContaining({ reason: 'The remember-me flag is still dropped after refresh.' })
    ]));
    const apply = review.getByRole('button', { name: 'Apply 4 corrections', exact: true });
    await expect(apply).toBeEnabled();
    await expect(review.getByRole('button', { name: /Approve|Continue/ })).toHaveCount(0);
    await expect(page.getByRole('alertdialog')).toHaveCount(0);
    const afterPreview = await snapshot(page.request, project.id);
    expect(afterPreview.tasks).toEqual(before.tasks);
    expect(afterPreview.receipts).toHaveLength(0);
    await shot(page, info, '04-review-desktop');

    // The bug-reopen card changes after the preview; the transport then loses
    // the final response (U23). The server commits, the browser cannot know.
    await editTask(page.request, tasks.bugreopen.id, 'Remember-me lost on refresh (edited)');
    await setState(page.request, { drop_next_apply: true });
    await apply.click();
    const receipt = page.locator('[data-correction-receipt]');
    await expect(receipt.getByRole('heading', { name: 'Outcome unknown' })).toBeVisible();
    await expect(receipt).toContainText('may already have been applied');
    await expect(receipt).not.toContainText('Nothing changed');
    const finalCalls = applyCalls.filter((call) => call.postDataJSON().preview === false);
    expect(finalCalls).toHaveLength(1);
    await shot(page, info, '05-unknown-outcome');

    // Restart loses preview proofs; only the durable receipt can answer now.
    await restart(page.request);
    await receipt.getByRole('button', { name: 'Retry same request' }).click();
    await expect(receipt.getByRole('heading', { name: '3 of 4 corrections applied' })).toBeVisible();
    const retried = applyCalls.filter((call) => call.postDataJSON().preview === false);
    expect(retried).toHaveLength(2);
    expect(retried[1].postData()).toBe(retried[0].postData());
    expect(retried[1].headers()['idempotency-key']).toBe(retried[0].headers()['idempotency-key']);
    await expect(receipt).toContainText('1 skipped');
    await expect(receipt.locator(`[data-receipt-item="${key('complete')}"]`)).toContainText('Applied');
    await expect(receipt.locator(`[data-receipt-item="${key('reopen')}"]`)).toContainText('Applied');
    await expect(receipt.locator(`[data-receipt-item="${key('bugfix')}"]`)).toContainText('Applied');
    await expect(receipt.locator(`[data-receipt-item="${key('bugreopen')}"]`)).toContainText('Skipped');
    await expect(receipt.locator(`[data-receipt-item="${key('bugreopen')}"]`)).toContainText('re-audit');
    await shot(page, info, '06-receipt');

    // The actual SQLite outcome (U24).
    const after = await snapshot(page.request, project.id);
    expect(dbTask(after, key('complete'))).toMatchObject({ semantic_state: 'completed', completed: true });
    expect(dbTask(after, key('reopen'))).toMatchObject({ semantic_state: 'active', completed: false });
    expect(dbTask(after, key('bugfix'))).toMatchObject({ semantic_state: 'completed', resolution: 'fixed' });
    expect(dbTask(after, key('bugreopen'))).toMatchObject({ semantic_state: 'completed', resolution: 'fixed' });
    expect(dbTask(after, key('claimed'))).toMatchObject({ semantic_state: 'active', claimed: true });
    expect(after.receipts.map((r) => `${r.task}:${r.outcome}`).sort()).toEqual([
      `${key('bugfix')}:applied`, `${key('bugreopen')}:skipped`, `${key('complete')}:applied`, `${key('reopen')}:applied`
    ].sort());
    expect(new Set(after.receipts.map((r) => r.request_key)).size).toBe(1);
    expect(after.events.filter((e) => e.task === key('complete')).map((e) => e.type)).toContain('task.completed');
    expect(after.events.filter((e) => e.task === key('bugfix')).map((e) => e.type)).toContain('bug.resolved');
    saveArtifact(info, 'lifecycle-db-after', { before: before.tasks.filter((t) => !t.title.startsWith('Maintenance')), after: { ...after, tasks: after.tasks.filter((t) => !t.title.startsWith('Maintenance')) } });
    saveArtifact(info, 'lifecycle-apply-request', { body: retried[0].postDataJSON(), same_key_retry: true });

    // U24/U25: results rows show what happened; the board reflects it.
    await receipt.getByRole('button', { name: 'Back to results' }).click();
    await expect(row(page, key('complete'))).toContainText('Applied: In progress → Done');
    await expect(row(page, key('bugreopen'))).toContainText('Card changed after this audit');
    await expectNoHorizontalScroll(page);

    // U15: open a card beyond the first board page without losing the audit.
    await row(page, key('reopen')).getByRole('link', { name: /Export board as JSON/ }).click();
    await expect(page.getByRole('dialog', { name: `${key('reopen')}: Export board as JSON` })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page).toHaveURL(new RegExp(`/audits/${auditId}$`));
    await expect(results(page)).toBeVisible();

    // Couldn't verify: neutral, no selection, the missing condition named.
    await page.getByRole('tab', { name: /Couldn.t verify/ }).click();
    const unknown = row(page, key('unknown'));
    await expect(unknown).toContainText('Couldn\'t verify');
    await expect(unknown).toContainText('This is not a column change.');
    await expect(unknown.getByRole('checkbox')).toHaveCount(0);
    await unknown.getByRole('button', { name: /Review inspected evidence/ }).click();
    const unknownPanel = page.getByRole('dialog', { name: `Evidence for ${key('unknown')}` });
    await expect(unknownPanel.locator('[data-criterion-result="unknown"]')).toContainText('Not verified');
    await expect(unknownPanel).toContainText('No committed implementation of this condition was inspected');
    await unknownPanel.getByRole('button', { name: 'Close evidence' }).click();

    // U19: revoked access hides every protected finding and excerpt; Retry recovers.
    await setState(page.request, { revoked: true });
    await page.reload();
    await expect(page.getByRole('alert').filter({ hasText: /Source access must be restored/ })).toBeVisible();
    await expect(results(page)).toHaveCount(0);
    await expect(page.getByText('Placeholder only; the route is not registered yet.')).toHaveCount(0);
    await expect(page.locator('pre')).toHaveCount(0);
    await shot(page, info, '07-revoked');
    await setState(page.request, { revoked: false });
    // Restored access is trusted only after GitHub is checked again.
    await page.getByRole('button', { name: 'Check GitHub access' }).click();
    await expect(results(page)).toBeVisible();

    // U16: legacy external history keeps its original review flow.
    await assertLegacyColumnAudit(page, info, project, tasks.correct.id);
  });
});
