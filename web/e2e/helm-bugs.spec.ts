import { expect, test } from '@playwright/test';

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

test.describe('Helm workspace', () => {
  test('reports, triages, resolves, and reopens a bug', async ({ page }) => {
    const statusResponse = await page.request.get('/api/v1/auth/status');
    const status = (await statusResponse.json()) as { mode?: string };
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const projectName = `Issue E2E ${runId}`;
    const projectKey = `ISS${runId}`.slice(0, 16);
    const bugTitle = `Preview session expires ${runId}`;
    const labelName = `regression-${runId.toLowerCase()}`;

    await page.goto('/');
    await page.getByRole('button', { name: 'New project', exact: true }).click();
    const projectDialog = page.getByRole('dialog', { name: 'Create a project' });
    await projectDialog.getByLabel('Project name').fill(projectName);
    await projectDialog.getByLabel('Project key').fill(projectKey);
    await projectDialog.getByRole('button', { name: 'Create project', exact: true }).click();
    await expect(page.getByRole('heading', { name: projectName, exact: true })).toBeVisible();

    await page.getByRole('button', { name: 'Report bug', exact: true }).click();
    const reportDialog = page.getByRole('dialog', { name: 'Report a bug' });
    await expect(reportDialog.getByLabel('Bug title')).toBeFocused();
    await reportDialog.getByLabel('Bug title').fill(bugTitle);
    await reportDialog.getByLabel('Actual behavior').fill('The authenticated preview asks the user to sign in again.');
    await reportDialog.getByLabel('Expected behavior').fill('The preview keeps the authenticated session.');
    await reportDialog.getByLabel(/Reproduction steps/).fill('Open the preview, authenticate, and reload.');
    await reportDialog.getByLabel(/Environment/).fill('Desktop Chrome');
    await reportDialog.getByLabel(/Affected version/).fill('preview');
    await reportDialog.getByLabel(/Labels/).fill(labelName);
    await reportDialog.getByRole('button', { name: 'Report bug', exact: true }).click();
    await expect(reportDialog).toBeHidden();

    await page.getByRole('button', { name: 'Issues', exact: true }).first().click();
    await expect(page.getByRole('region', { name: 'Issue health' })).toBeVisible();
    const severityFilter = page.getByLabel('Filter by severity');
    await severityFilter.selectOption('untriaged');
    await expect(page).toHaveURL(/severity=untriaged/);
    await page.reload();
    await expect(page.getByLabel('Filter by severity')).toHaveValue('untriaged');
    await page.getByLabel('Filter by severity').selectOption('all');
    const issueRow = page.getByRole('button', { name: new RegExp(escapeRegExp(bugTitle)) });
    await expect(issueRow).toContainText('Untriaged');
    await issueRow.click();

    const drawer = page.locator('.task-drawer');
    await expect(drawer.getByRole('heading', { name: 'Bug report' })).toBeVisible();
    await drawer.getByLabel('Bug severity').selectOption('s2');
    await drawer.getByLabel('Triage priority').selectOption('high');
    await drawer.getByRole('button', { name: 'Triage issue', exact: true }).click();
    await expect(drawer.locator('.severity-badge').filter({ hasText: 'S2 · High' })).toBeVisible();

    await drawer.getByLabel('Bug resolution').selectOption('fixed');
    await drawer.getByLabel(/Resolution note/).fill('The preview now retains its authenticated session.');
    await drawer.getByRole('button', { name: 'Resolve issue', exact: true }).click();
    await expect(drawer.getByRole('heading', { name: 'Resolved as Fixed' })).toBeVisible();

    await drawer.getByLabel('Reopen reason').fill('The regression returned in a later preview.');
    await drawer.getByRole('button', { name: 'Reopen issue', exact: true }).click();
    await expect(drawer.getByRole('heading', { name: 'Resolve' })).toBeVisible();
    await expect(drawer.locator('.drawer-save-status')).toContainText('All changes saved');

    await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
    await expect(drawer).toBeHidden();
    await page.getByRole('button', { name: 'Search anything', exact: true }).click();
    const commandDialog = page.getByRole('dialog', { name: 'Search Helm' });
    await commandDialog.getByRole('combobox', { name: 'Search projects and views, tasks, issues, and actions' }).fill(bugTitle);
    await commandDialog.getByRole('option', { name: new RegExp(escapeRegExp(bugTitle)) }).click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(bugTitle);
  });
});
