import { expect, test } from '@playwright/test';

test.describe('Helm workspace', () => {
  test('lets an administrator manage project metadata and columns with confirmations', async ({ page, request }) => {
    const statusResponse = await request.get('/api/v1/auth/status');
    const status = (await statusResponse.json()) as { mode?: string };
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const projectName = `Admin E2E ${runId}`;
    const projectKey = `ADM${runId}`.slice(0, 16);

    await page.goto('/');
    await page.getByRole('button', { name: 'New project', exact: true }).click();
    const projectDialog = page.getByRole('dialog', { name: 'Create a project' });
    await projectDialog.getByLabel('Project name').fill(projectName);
    await projectDialog.getByLabel('Project key').fill(projectKey);
    await projectDialog.getByRole('button', { name: 'Create project', exact: true }).click();
    await expect(page.getByRole('heading', { name: projectName, exact: true })).toBeVisible();

    await page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('button', { name: 'Settings', exact: true }).first().click();
    const administration = page.getByRole('heading', { name: 'Projects & columns', exact: true });
    await expect(administration).toBeVisible();
    const adminSection = page.locator('section[aria-labelledby="project-admin-heading"]');
    await expect(adminSection.locator('#admin-project-select')).toHaveValue(/.+/);

    await adminSection.getByLabel('Project name').fill(`${projectName} renamed`);
    await adminSection.getByLabel('Description').fill('Managed through the administrator flow.');
    await adminSection.getByLabel('Checklist completion policy').selectOption('require');
    await adminSection.getByRole('button', { name: 'Save project', exact: true }).click();
    const projectSaveConfirmation = page.getByRole('alertdialog', { name: `Save ${projectName} renamed settings?` });
    await expect(projectSaveConfirmation).toBeVisible();
    await expect(projectSaveConfirmation).toContainText('stable project key and URL stay unchanged');
    await projectSaveConfirmation.getByRole('button', { name: 'Save project', exact: true }).click();
    await expect(projectSaveConfirmation).toBeHidden();
    const savedProjectResponse = await request.get(`/api/v1/projects/${encodeURIComponent(projectKey)}`);
    expect(savedProjectResponse.ok()).toBeTruthy();
    await expect(savedProjectResponse.json()).resolves.toMatchObject({
      name: `${projectName} renamed`,
      description: 'Managed through the administrator flow.',
      checklist_completion_policy: 'require'
    });

    await adminSection.getByLabel('New column name').fill('Intake');
    await adminSection.locator('.admin-column-create select').selectOption('backlog');
    await adminSection.getByRole('button', { name: /Add column/ }).click();
    const addConfirmation = page.getByRole('alertdialog', { name: 'Add Intake?' });
    await expect(addConfirmation).toBeVisible();
    await addConfirmation.getByRole('button', { name: 'Add column', exact: true }).click();
    await expect(addConfirmation).toBeHidden();
    const intakeRow = adminSection.locator('.admin-column-row').filter({ has: page.locator('input[aria-label^="Column name for Intake"]') });
    await expect(intakeRow).toBeVisible();

    await intakeRow.getByRole('button', { name: 'Move Intake up', exact: true }).click();
    const moveConfirmation = page.getByRole('alertdialog', { name: 'Move Intake?' });
    await expect(moveConfirmation).toBeVisible();
    await moveConfirmation.getByRole('button', { name: 'Move column', exact: true }).click();
    await expect(moveConfirmation).toBeHidden();

    await intakeRow.getByLabel('Column name for Intake').fill('Intake renamed');
    await intakeRow.getByRole('button', { name: 'Save', exact: true }).click();
    const columnSaveConfirmation = page.getByRole('alertdialog', { name: 'Save Intake renamed?' });
    await expect(columnSaveConfirmation).toBeVisible();
    await columnSaveConfirmation.getByRole('button', { name: 'Save column', exact: true }).click();
    await expect(columnSaveConfirmation).toBeHidden();
    const renamedIntakeRow = adminSection.locator('.admin-column-row').filter({ has: page.locator('input[aria-label="Column name for Intake renamed"]') });
    await expect(renamedIntakeRow).toBeVisible();

    await renamedIntakeRow.getByRole('button', { name: 'Archive', exact: true }).click();
    const archiveConfirmation = page.getByRole('alertdialog', { name: 'Archive Intake renamed?' });
    await expect(archiveConfirmation).toBeVisible();
    await expect(archiveConfirmation).toContainText('Tasks in this column will move');
    await archiveConfirmation.getByRole('button', { name: 'Archive column', exact: true }).click();
    await expect(archiveConfirmation).toBeHidden();
    await expect(renamedIntakeRow).toHaveClass(/archived/);

    for (const name of ['After archive first', 'After archive second']) {
      await adminSection.getByLabel('New column name').fill(name);
      await adminSection.locator('.admin-column-create select').selectOption('backlog');
      await adminSection.getByRole('button', { name: /Add column/ }).click();
      const createConfirmation = page.getByRole('alertdialog', { name: `Add ${name}?` });
      await expect(createConfirmation).toBeVisible();
      await createConfirmation.getByRole('button', { name: 'Add column', exact: true }).click();
      await expect(createConfirmation).toBeHidden();
      await expect(adminSection.locator('.admin-column-row').filter({ has: page.locator(`input[aria-label="Column name for ${name}"]`) })).toBeVisible();
    }
    const postArchiveRows = adminSection.locator('.admin-column-row');
    const renderedColumnNames = await postArchiveRows.evaluateAll((rows) => rows.map((row) => row.querySelector('input')?.value || ''));
    expect(renderedColumnNames.indexOf('After archive first')).toBeLessThan(renderedColumnNames.indexOf('Intake renamed'));
    expect(renderedColumnNames.indexOf('After archive second')).toBeLessThan(renderedColumnNames.indexOf('Intake renamed'));
    const firstPostArchiveRow = adminSection.locator('.admin-column-row').filter({ has: page.locator('input[aria-label="Column name for After archive first"]') });
    const secondPostArchiveRow = adminSection.locator('.admin-column-row').filter({ has: page.locator('input[aria-label="Column name for After archive second"]') });
    await expect(firstPostArchiveRow.getByRole('button', { name: 'Move After archive first up', exact: true })).toBeEnabled();
    await expect(firstPostArchiveRow.getByRole('button', { name: 'Move After archive first down', exact: true })).toBeEnabled();
    await expect(secondPostArchiveRow.getByRole('button', { name: 'Move After archive second up', exact: true })).toBeEnabled();
    await expect(secondPostArchiveRow.getByRole('button', { name: 'Move After archive second down', exact: true })).toBeDisabled();

    await adminSection.getByRole('button', { name: 'Archive project', exact: true }).click();
    const projectConfirmation = page.getByRole('alertdialog', { name: `Archive ${projectName} renamed?` });
    await expect(projectConfirmation).toBeVisible();
    await projectConfirmation.getByRole('button', { name: 'Archive project', exact: true }).click();
    await expect(projectConfirmation).toBeHidden();
    await expect(adminSection.getByText('Archived', { exact: true })).toBeVisible();

    await adminSection.getByRole('button', { name: 'Restore project', exact: true }).click();
    const restoreConfirmation = page.getByRole('alertdialog', { name: `Restore ${projectName} renamed?` });
    await expect(restoreConfirmation).toBeVisible();
    await restoreConfirmation.getByRole('button', { name: 'Restore project', exact: true }).click();
    await expect(restoreConfirmation).toBeHidden();
    await expect(adminSection.getByText('Active', { exact: true })).toBeVisible();
  });
});
