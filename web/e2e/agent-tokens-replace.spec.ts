import { expect, test } from '@playwright/test';
import {
  attachMaskedScreenshot,
  attachSanitized,
  bearer,
  createAgent,
  createProject,
  daysFromNow,
  hasDatabase,
  issueToken,
  listAgents,
  openAgentSettings,
  removeActorProject,
  requireDisabledAuth,
  revealedValue,
  runId,
  tokenRow
} from './support/agentTokens';

// Replacement renders a new plaintext: no automatic traces/videos/screenshots.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

test.describe('revocation and manual replacement', () => {
  test('revocation warns about recent use, focuses Cancel and makes the bearer fail', async ({ page, request }, testInfo) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TK${id}`.slice(0, 16), `Revoke ${id}`);
    const agent = await createAgent(request, `Revoker ${id}`);
    const token = await issueToken(request, agent.id, { name: `Busy ${id}`, scopes: ['projects:read', 'tasks:read'], project_ids: [project.id], expires_at: daysFromNow(30) });
    expect(await bearer(request, token.token, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(200);

    await openAgentSettings(page);
    await tokenRow(page, token.id).getByRole('button', { name: `Revoke Busy ${id}`, exact: true }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toContainText(`Revoke token “Busy ${id}”?`);
    await expect(dialog).toContainText(agent.name);
    await expect(dialog).toContainText(token.id.slice(0, 8));
    await expect(dialog).toContainText(project.name);
    await expect(dialog).toContainText(/used .*ago/i);
    await expect(dialog).toContainText('may fail immediately');
    await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
    await attachMaskedScreenshot(page, testInfo, 'revoke-confirm.png');
    await dialog.getByRole('button', { name: 'Revoke token', exact: true }).click();
    await expect(tokenRow(page, token.id)).toHaveCount(0);
    expect(await bearer(request, token.token, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(401);
    await attachSanitized(testInfo, 'revocation.json', { revoked: true, bearerStatusAfter: 401 }, [token.token]);
  });

  test('replacement keeps both tokens until a separate confirmed revocation and never widens access', async ({ page, request }, testInfo) => {
    test.skip(!hasDatabase, 'narrows the agent ceiling directly in the E2E database');
    test.setTimeout(90_000);
    await requireDisabledAuth(request);
    const id = runId();
    const kept = await createProject(request, `TP1${id}`.slice(0, 16), `Kept ${id}`);
    const dropped = await createProject(request, `TP2${id}`.slice(0, 16), `Dropped ${id}`);
    const agent = await createAgent(request, `Replacer ${id}`, [kept.id, dropped.id]);
    const scopes = ['tasks:read', 'tasks:write', 'notifications:read'];
    const old = await issueToken(request, agent.id, { name: `Laptop ${id}`, scopes, project_ids: [kept.id, dropped.id], expires_at: daysFromNow(10) });
    // The agent lost a project since the old token was issued.
    removeActorProject(agent.id, dropped.id);

    await openAgentSettings(page);
    const oldRow = tokenRow(page, old.id);
    await oldRow.getByRole('button', { name: `Replace Laptop ${id}`, exact: true }).click();
    const dialog = page.getByRole('dialog', { name: `New token for ${agent.name}` });
    await expect(dialog.getByLabel('Token name')).toHaveValue(new RegExp(`^Laptop ${id} .*replacement`, 'i'));
    await expect(dialog.getByRole('radio', { name: 'Custom' })).toBeChecked();
    for (const scope of scopes) await expect(dialog.getByRole('checkbox', { name: scope, exact: true })).toBeChecked();
    await expect(dialog.getByRole('checkbox', { name: 'tasks:claim', exact: true })).not.toBeChecked();
    await expect(dialog.getByRole('radio', { name: 'Selected projects' })).toBeChecked();
    await expect(dialog.getByRole('checkbox', { name: kept.name, exact: true })).toBeChecked();
    await expect(dialog.getByRole('checkbox', { name: dropped.name, exact: true })).toHaveCount(0);
    await expect(dialog.locator('[data-token-access-change]')).toContainText(dropped.name);
    await expect(dialog.getByRole('radio', { name: '90 days' })).toBeChecked();

    const sent = page.waitForRequest((item) => item.method() === 'POST' && item.url().endsWith(`/agents/${agent.id}/tokens`));
    await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
    const body = (await sent).postDataJSON() as { scopes: string[]; project_ids: string[] };
    expect([...body.scopes].sort()).toEqual([...scopes].sort());
    expect(body.project_ids).toEqual([kept.id]);

    const reveal = page.getByRole('dialog', { name: 'Save your new token' });
    const replacement = await revealedValue(page);
    const tasks = `/api/v1/projects/${kept.id}/tasks?limit=1`;
    expect(await bearer(request, old.token, 'GET', tasks)).toBe(200);
    expect(await bearer(request, replacement, 'GET', tasks)).toBe(200);
    await expect(reveal.getByRole('button', { name: 'Revoke old token', exact: true })).toBeDisabled();
    await reveal.getByRole('checkbox', { name: 'I have saved this token somewhere safe' }).check();
    await attachMaskedScreenshot(page, testInfo, 'replacement-reveal.png');

    // Keeping both is explicit and leaves the old token working.
    await reveal.getByRole('button', { name: 'Keep both for now', exact: true }).click();
    await expect(reveal).toBeHidden();
    expect(await bearer(request, old.token, 'GET', tasks)).toBe(200);
    await expect(oldRow).toBeVisible();

    // Retirement is its own confirmation.
    await oldRow.getByRole('button', { name: `Revoke Laptop ${id}`, exact: true }).click();
    await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke token', exact: true }).click();
    await expect(oldRow).toHaveCount(0);
    expect(await bearer(request, old.token, 'GET', tasks)).toBe(401);
    expect(await bearer(request, replacement, 'GET', tasks)).toBe(200);
    const remaining = (await listAgents(request)).find((item) => item.id === agent.id)?.tokens || [];
    expect(remaining.map((token) => token.id)).not.toContain(old.id);
    await attachSanitized(testInfo, 'replacement.json', { scopes: body.scopes, projects: body.project_ids.length, oldRevoked: true, remaining: remaining.length }, [old.token, replacement]);
  });

  test('revoke old token from the reveal opens its own confirmation', async ({ page, request }) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TO${id}`.slice(0, 16), `Retire ${id}`);
    const agent = await createAgent(request, `Retirer ${id}`);
    const old = await issueToken(request, agent.id, { name: `CI ${id}`, scopes: ['projects:read', 'tasks:read'], project_ids: [project.id], expires_at: daysFromNow(10) });
    await openAgentSettings(page);
    await tokenRow(page, old.id).getByRole('button', { name: `Replace CI ${id}`, exact: true }).click();
    await page.getByRole('dialog', { name: `New token for ${agent.name}` }).getByRole('button', { name: 'Generate token', exact: true }).click();
    const reveal = page.getByRole('dialog', { name: 'Save your new token' });
    const replacement = await revealedValue(page);
    await reveal.getByRole('button', { name: 'Copy token', exact: true }).click().catch(() => undefined);
    await reveal.getByRole('checkbox', { name: 'I have saved this token somewhere safe' }).check();
    // Copy or acknowledgement alone never revoked anything.
    expect(await bearer(request, old.token, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(200);
    await reveal.getByRole('button', { name: 'Revoke old token', exact: true }).click();
    await expect(reveal).toBeHidden();
    const confirm = page.getByRole('alertdialog');
    await expect(confirm).toContainText(`Revoke token “CI ${id}”?`);
    await confirm.getByRole('button', { name: 'Cancel', exact: true }).click();
    expect(await bearer(request, old.token, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(200);
    expect(await bearer(request, replacement, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(200);
  });
});
