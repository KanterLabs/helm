import { expect, test } from '@playwright/test';
import {
  MAINTAINER,
  READ_ONLY,
  TASK_WORKER,
  agentGroup,
  attachMaskedScreenshot,
  attachSanitized,
  bearer,
  createAgent,
  createAlertSource,
  createProject,
  daysFromNow,
  expectNoHorizontalOverflow,
  hasDatabase,
  insertLegacyToken,
  issueToken,
  listAgents,
  origin,
  openAgentSettings,
  requireDisabledAuth,
  runId,
  setActorDisabled,
  setTheme,
  tokenRow
} from './support/agentTokens';

// Inventory pages never render plaintext, but these specs share fixtures with
// tokens held in memory; keep automatic traces/videos off for consistency.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

test.describe('agent token inventory', () => {
  test.skip(!hasDatabase, 'requires the real-process E2E database for expired and legacy rows');

  test('groups tokens by agent with honest status, access, usage and expiry', async ({ page, request }, testInfo) => {
    test.setTimeout(90_000);
    await requireDisabledAuth(request);
    const id = runId();
    const alpha = await createProject(request, `TIA${id}`.slice(0, 16), `Inventory Alpha ${id}`);
    const beta = await createProject(request, `TIB${id}`.slice(0, 16), `Inventory Beta ${id}`);
    const worker = await createAgent(request, `Inventory worker ${id}`);
    const idle = await createAgent(request, `Inventory idle ${id}`, [alpha.id]);
    const retired = await createAgent(request, `Inventory retired ${id}`);

    const active = await issueToken(request, worker.id, { name: `Codex laptop ${id}`, scopes: TASK_WORKER, project_ids: [alpha.id, beta.id], expires_at: daysFromNow(90) });
    const soon = await issueToken(request, worker.id, { name: `CI runner ${id}`, scopes: READ_ONLY, project_ids: [alpha.id], expires_at: daysFromNow(5) });
    const forever = await issueToken(request, worker.id, { name: `Duplicate ${id}`, scopes: MAINTAINER, expires_at: null });
    const duplicate = await issueToken(request, worker.id, { name: `Duplicate ${id}`, scopes: ['tasks:write'], project_ids: [beta.id], expires_at: daysFromNow(30) });
    const expired = insertLegacyToken(worker.id, `Old deploy ${id}`, { scopes: TASK_WORKER, projectIds: [alpha.id], expiresAt: new Date(Date.now() - 2 * 86_400_000).toISOString(), createdAt: new Date(Date.now() - 40 * 86_400_000).toISOString() });
    const invalid = insertLegacyToken(worker.id, `Unknown expiry ${id}`, { scopes: READ_ONLY, expiresAt: 'not-a-date' });
    const disabledToken = await issueToken(request, retired.id, { name: `Retired bot ${id}`, scopes: READ_ONLY, expires_at: daysFromNow(60) });
    setActorDisabled(retired.id);
    const secrets = [active.token, soon.token, forever.token, duplicate.token, expired.token, invalid.token, disabledToken.token];

    // Real authentication: the used token records last use, the expired one fails.
    expect(await bearer(request, active.token, 'GET', `/api/v1/projects/${alpha.id}/tasks?limit=1`)).toBe(200);
    expect(await bearer(request, expired.token, 'GET', `/api/v1/projects/${alpha.id}/tasks?limit=1`)).toBe(401);

    await page.setViewportSize({ width: 1280, height: 900 });
    await openAgentSettings(page);
    const section = page.getByRole('region', { name: 'Agents & tokens' });
    const search = section.getByRole('searchbox', { name: 'Search agents and tokens' });
    const status = section.getByRole('combobox', { name: 'Token status' });
    const project = section.getByRole('combobox', { name: 'Token project' });
    await expect(status.locator('option')).toHaveText(['All current tokens', 'Active', 'Expiring soon', 'Expired', 'Never used']);
    await expect(status).toHaveValue('all');
    await expect(project.locator('option').first()).toHaveText('All projects');

    const group = agentGroup(page, worker.id);
    await expect(group.getByRole('heading', { name: worker.name, exact: true })).toBeVisible();
    await expect(group.getByRole('button', { name: `New token for ${worker.name}`, exact: true })).toBeVisible();
    // No bare cross icon: every action is named.
    await expect(group.getByRole('button', { name: '×', exact: true })).toHaveCount(0);

    const activeRow = tokenRow(page, active.id);
    await expect(activeRow).toContainText(active.name);
    await expect(activeRow).toContainText(active.id.slice(0, 8));
    await expect(activeRow.locator('[data-token-status]')).toHaveText('Active');
    await expect(activeRow.locator('[data-token-access]')).toContainText('Task worker');
    await expect(activeRow.locator('[data-token-projects]')).toContainText(alpha.name);
    await expect(activeRow.locator('[data-token-projects]')).toContainText(beta.name);
    await expect(activeRow.locator('[data-token-last-used]')).not.toContainText('Never');
    await expect(activeRow.locator('[data-token-last-used] time')).toHaveAttribute('datetime', /\d{4}-\d{2}-\d{2}T/);

    await expect(tokenRow(page, soon.id).locator('[data-token-status]')).toHaveText('Expiring soon');
    await expect(tokenRow(page, soon.id).locator('[data-token-access]')).toContainText('Read only');
    await expect(tokenRow(page, soon.id).locator('[data-token-last-used]')).toContainText('Never');
    await expect(tokenRow(page, forever.id).locator('[data-token-expiry]')).toContainText('No expiry');
    await expect(tokenRow(page, forever.id).locator('[data-token-access]')).toContainText('Maintainer');
    await expect(tokenRow(page, forever.id).locator('[data-token-projects]')).toContainText('All agent projects');
    await expect(tokenRow(page, duplicate.id).locator('[data-token-access]')).toContainText('Custom');
    await expect(tokenRow(page, expired.id).locator('[data-token-status]')).toHaveText('Expired');
    await expect(tokenRow(page, invalid.id).locator('[data-token-status]')).toHaveText('Unknown');
    // Duplicate names stay distinguishable by immutable ID metadata.
    await expect(tokenRow(page, forever.id)).toContainText(forever.id.slice(0, 8));
    await expect(tokenRow(page, duplicate.id)).toContainText(duplicate.id.slice(0, 8));

    // Default sort: expiring soon, then active, then expired/unknown.
    const order = await group.locator('[data-token-row]').evaluateAll((rows) => rows.map((row) => row.getAttribute('data-token-row')));
    expect(order.indexOf(soon.id)).toBeLessThan(order.indexOf(active.id));
    expect(order.indexOf(active.id)).toBeLessThan(order.indexOf(expired.id));

    // A token-less automation agent is one short row that can still issue.
    const idleGroup = agentGroup(page, idle.id);
    await expect(idleGroup).toContainText('No tokens yet');
    await expect(idleGroup.getByRole('button', { name: `New token for ${idle.name}`, exact: true })).toBeEnabled();

    // A disabled agent is a separate access condition, not an Active token.
    const retiredGroup = agentGroup(page, retired.id);
    await expect(retiredGroup).toContainText('Disabled');
    await expect(retiredGroup.getByRole('button', { name: `New token for ${retired.name}`, exact: true })).toHaveCount(0);
    await expect(tokenRow(page, disabledToken.id)).toContainText('Agent disabled');

    // Filtering never hides that an agent has other tokens.
    await search.fill(`CI runner ${id}`);
    await expect(tokenRow(page, soon.id)).toBeVisible();
    await expect(tokenRow(page, active.id)).toHaveCount(0);
    await expect(group.locator('[data-token-hidden-count]')).toHaveText(/5 more tokens hidden by filters/);
    await search.fill('');
    await status.selectOption('expired');
    await expect(tokenRow(page, expired.id)).toBeVisible();
    await expect(tokenRow(page, soon.id)).toHaveCount(0);
    await status.selectOption('never-used');
    await expect(tokenRow(page, soon.id)).toBeVisible();
    await expect(tokenRow(page, active.id)).toHaveCount(0);
    await status.selectOption('all');
    await project.selectOption({ label: beta.name });
    await expect(tokenRow(page, duplicate.id)).toBeVisible();
    await expect(tokenRow(page, soon.id)).toHaveCount(0);
    await project.selectOption({ label: 'All projects' });

    // No matches keeps filters visible and offers a reset.
    await search.fill(`no such token ${id}`);
    await expect(section.getByText('No tokens match these filters')).toBeVisible();
    await expect(search).toBeVisible();
    await section.getByRole('button', { name: 'Clear filters', exact: true }).click();
    await expect(search).toHaveValue('');
    await expect(tokenRow(page, active.id)).toBeVisible();

    await expect(section.locator('[data-token-summary]')).toContainText(/\d+ tokens?/);
    await search.fill(id);
    await attachMaskedScreenshot(page, testInfo, 'inventory-desktop-light.png');
    await setTheme(page, 'dark');
    await page.reload();
    await section.getByRole('searchbox', { name: 'Search agents and tokens' }).fill(id);
    await attachMaskedScreenshot(page, testInfo, 'inventory-desktop-dark.png');

    for (const theme of ['dark', 'light'] as const) {
      await setTheme(page, theme);
      await page.setViewportSize({ width: 320, height: 800 });
      await page.reload();
      await section.getByRole('searchbox', { name: 'Search agents and tokens' }).fill(id);
      await expect(tokenRow(page, active.id)).toBeVisible();
      await expectNoHorizontalOverflow(page);
      const revoke = tokenRow(page, active.id).getByRole('button', { name: `Revoke ${active.name}`, exact: true });
      const box = await revoke.boundingBox();
      expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
      await tokenRow(page, active.id).scrollIntoViewIfNeeded();
      await attachMaskedScreenshot(page, testInfo, `inventory-320-${theme}.png`);
    }

    const listed = (await listAgents(request)).filter((agent) => [worker.id, idle.id, retired.id].includes(agent.id));
    await attachSanitized(testInfo, 'inventory-metadata.json', listed.map((agent) => ({
      agent: agent.name.replace(id, '<run>'),
      tokens: (agent.tokens || []).map((token) => ({ name: token.name.replace(id, '<run>'), scopes: token.scopes, projects: token.project_ids.length, expires: token.expires_at ? 'set' : 'none', used: Boolean(token.last_used_at) }))
    })), secrets);
  });

  test('separates server-classified alert sources and keeps legacy source credentials reviewable', async ({ page, request }, testInfo) => {
    test.setTimeout(90_000);
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TAS${id}`.slice(0, 16), `Alert source ${id}`);
    const automation = await createAgent(request, `Automation ${id}`);
    const source = await createAlertSource(request, `Grafana ${id}`, project.key);
    const sourceAgent = (await listAgents(request)).find((agent) => agent.id === source.actorId);
    expect(sourceAgent?.intake_source, 'server marks the passive source').toBe(true);
    expect(automation.intake_source ?? false).toBe(false);

    // The server rejects new issuance to the source identity.
    const denied = await request.post(`/api/v1/agents/${source.actorId}/tokens`, {
      data: { name: 'forged source token', scopes: ['tasks:read'] },
      headers: { 'Content-Type': 'application/json', Origin: origin }
    });
    expect(denied.status()).toBeGreaterThanOrEqual(400);
    expect(denied.status()).toBeLessThan(500);

    // A credential that predates classification still authenticates and must stay visible.
    const legacy = insertLegacyToken(source.actorId, `Legacy source key ${id}`, { scopes: ['tasks:read'], projectIds: [project.id] });
    expect(await bearer(request, legacy.token, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(200);

    await page.setViewportSize({ width: 1280, height: 900 });
    await openAgentSettings(page);
    await expect(agentGroup(page, source.actorId)).toHaveCount(0);
    await expect(agentGroup(page, automation.id).getByRole('button', { name: `New token for ${automation.name}`, exact: true })).toBeEnabled();

    const sources = page.locator('[data-alert-sources]');
    await expect(sources.getByText(/Needs review/)).toBeVisible();
    const sourceRow = sources.locator(`[data-alert-source="${source.actorId}"]`);
    await expect(sourceRow).toContainText(`Grafana ${id}`);
    await expect(sourceRow.getByRole('button', { name: /New token/ })).toHaveCount(0);
    await expect(sources.getByRole('link', { name: 'Manage integrations' }).first()).toHaveAttribute('href', '/tickets?connect=1');
    const legacyRow = tokenRow(page, legacy.id);
    await expect(legacyRow).toBeVisible();
    await expect(legacyRow).toContainText('Needs review');
    await expect(legacyRow.getByRole('button', { name: `Replace ${`Legacy source key ${id}`}`, exact: true })).toHaveCount(0);
    await attachMaskedScreenshot(page, testInfo, 'alert-sources-review.png');

    await legacyRow.getByRole('button', { name: `Revoke Legacy source key ${id}`, exact: true }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toContainText(`Revoke token “Legacy source key ${id}”?`);
    await dialog.getByRole('button', { name: 'Revoke token', exact: true }).click();
    await expect(legacyRow).toHaveCount(0);
    expect(await bearer(request, legacy.token, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(401);

    // Identity and history are preserved: the actor and its alert task remain.
    expect((await listAgents(request)).some((agent) => agent.id === source.actorId)).toBe(true);
    expect((await request.get(`/api/v1/tasks/${source.taskId}`)).status()).toBe(200);
    await attachSanitized(testInfo, 'alert-source-metadata.json', { source: source.actorId.replace(/webhook-[^-]+/, 'webhook-<id>'), issuanceDenied: denied.status(), legacyRevoked: true }, [legacy.token]);
  });
});
