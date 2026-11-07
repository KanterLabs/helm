import { expect, test, type Page, type Request } from '@playwright/test';
import {
  ALL_SCOPES,
  TASK_WORKER,
  agentGroup,
  attachMaskedScreenshot,
  attachSanitized,
  backlogColumn,
  bearer,
  createAgent,
  createProject,
  listAgents,
  openAgentSettings,
  origin,
  requireDisabledAuth,
  revealedValue,
  runId,
  tokenRow
} from './support/agentTokens';

// These flows render one-time plaintext. Never retain automatic traces,
// videos or failure screenshots; retained screenshots are masked explicitly.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

const DAY = 86_400_000;
const isIssuance = (request: Request) => request.method() === 'POST' && /\/api\/v1\/agents\/[^/]+\/tokens$/.test(new URL(request.url()).pathname);

function issueDialog(page: Page, agentName: string) {
  return page.getByRole('dialog', { name: `New token for ${agentName}` });
}

async function openIssue(page: Page, agentId: string, agentName: string) {
  await agentGroup(page, agentId).getByRole('button', { name: `New token for ${agentName}`, exact: true }).click();
  const dialog = issueDialog(page, agentName);
  await expect(dialog).toBeVisible();
  return dialog;
}

async function finishReveal(page: Page) {
  const reveal = page.getByRole('dialog', { name: 'Save your new token' });
  await reveal.getByRole('checkbox', { name: 'I have saved this token somewhere safe' }).check();
  await reveal.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(reveal).toBeHidden();
}

test.describe('agent token issuance', () => {
  test('issues an exact Task worker grant for two selected projects with a finite expiry', async ({ page, request }, testInfo) => {
    test.setTimeout(90_000);
    await requireDisabledAuth(request);
    const id = runId();
    const p1 = await createProject(request, `TI1${id}`.slice(0, 16), `Issue One ${id}`);
    const p2 = await createProject(request, `TI2${id}`.slice(0, 16), `Issue Two ${id}`);
    const p3 = await createProject(request, `TI3${id}`.slice(0, 16), `Issue Three ${id}`);
    const agent = await createAgent(request, `Issuer ${id}`);
    const other = await createAgent(request, `Other issuer ${id}`);

    await page.setViewportSize({ width: 1280, height: 900 });
    await openAgentSettings(page);
    let dialog = await openIssue(page, agent.id, agent.name);
    await expect(dialog.getByLabel('Token name')).toBeFocused();
    await expect(dialog.getByRole('radio', { name: 'Task worker' })).toBeChecked();
    for (const preset of ['Read only', 'Maintainer', 'Custom']) await expect(dialog.getByRole('radio', { name: preset })).not.toBeChecked();
    await expect(dialog.getByRole('radio', { name: 'Selected projects' })).toBeChecked();
    await expect(dialog.getByRole('checkbox', { name: p1.name, exact: true })).not.toBeChecked();
    await expect(dialog.getByRole('radio', { name: '90 days' })).toBeChecked();
    await expect(dialog.locator('[data-token-expiry-preview]')).toContainText('Expires');
    const generate = dialog.getByRole('button', { name: 'Generate token', exact: true });
    await dialog.getByLabel('Token name').fill(`Codex on work laptop ${id}`);
    await expect(dialog.getByText('Select at least one project')).toBeVisible();
    await expect(generate).toBeDisabled();

    // Drafts never carry to another agent.
    await dialog.getByRole('checkbox', { name: p1.name, exact: true }).check();
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(agentGroup(page, agent.id).getByRole('button', { name: `New token for ${agent.name}`, exact: true })).toBeFocused();
    const otherDialog = await openIssue(page, other.id, other.name);
    await expect(otherDialog.getByLabel('Token name')).toHaveValue('');
    await expect(otherDialog.getByRole('checkbox', { name: p1.name, exact: true })).not.toBeChecked();
    await otherDialog.getByRole('button', { name: 'Cancel', exact: true }).click();

    dialog = await openIssue(page, agent.id, agent.name);
    await dialog.getByLabel('Token name').fill(`Codex on work laptop ${id}`);
    await dialog.getByRole('checkbox', { name: p1.name, exact: true }).check();
    await dialog.getByRole('checkbox', { name: p2.name, exact: true }).check();
    await dialog.getByRole('radio', { name: '30 days' }).check();
    const summary = dialog.locator('[data-token-issue-summary]');
    await expect(summary).toContainText('Task worker');
    await expect(summary).toContainText(p1.name);
    await expect(summary).toContainText(p2.name);
    await expect(summary).toContainText('claim work');
    await expect(generate).toBeEnabled();
    await attachMaskedScreenshot(page, testInfo, 'issue-form-desktop.png');

    const issued = page.waitForRequest(isIssuance);
    const before = Date.now();
    await generate.click();
    const sent = await issued;
    const body = sent.postDataJSON() as { name: string; scopes: string[]; project_ids?: string[]; expires_at?: string };
    expect(sent.headers()['idempotency-key']).toBeUndefined();
    expect([...body.scopes].sort()).toEqual([...TASK_WORKER].sort());
    expect([...(body.project_ids || [])].sort()).toEqual([p1.id, p2.id].sort());
    const expiry = Date.parse(body.expires_at || '');
    expect(expiry).toBeGreaterThan(before + 30 * DAY - 120_000);
    expect(expiry).toBeLessThan(Date.now() + 30 * DAY + 120_000);

    const plaintext = await revealedValue(page);
    const tasks = (projectId: string) => `/api/v1/projects/${projectId}/tasks?limit=1`;
    expect(await bearer(request, plaintext, 'GET', tasks(p1.id))).toBe(200);
    expect(await bearer(request, plaintext, 'POST', `/api/v1/projects/${p1.id}/tasks`, { title: `Worker task ${id}`, column_id: await backlogColumn(request, p1.id) }, { 'Idempotency-Key': `issue-${id}-task` })).toBe(201);
    expect([403, 404]).toContain(await bearer(request, plaintext, 'GET', tasks(p3.id)));
    expect(await bearer(request, plaintext, 'PATCH', `/api/v1/projects/${p1.id}`, { name: 'Renamed by worker' }, { 'Idempotency-Key': `issue-${id}-project` })).toBe(403);
    await finishReveal(page);

    await page.reload();
    const listed = (await listAgents(request)).find((item) => item.id === agent.id)!.tokens!.find((token) => token.name === body.name)!;
    const row = tokenRow(page, listed.id);
    await expect(row.locator('[data-token-access]')).toContainText('Task worker');
    await expect(row.locator('[data-token-projects]')).toContainText(p1.name);
    await expect(row.locator('[data-token-projects]')).toContainText(p2.name);
    await expect(row.locator('[data-token-expiry]')).toContainText(/(29|30) days/);
    await expect(row.locator('[data-token-status]')).toHaveText('Active');
    await attachSanitized(testInfo, 'task-worker-issuance.json', { request: { ...body, name: '<name>', project_ids: body.project_ids?.length, expires_at: 'now+30d' }, listed: { scopes: listed.scopes, projects: listed.project_ids.length } }, [plaintext]);
  });

  test('custom grants are exact across all eight scopes and the ceiling is enforced', async ({ page, request }, testInfo) => {
    await requireDisabledAuth(request);
    const id = runId();
    const inside = await createProject(request, `TC1${id}`.slice(0, 16), `Ceiling In ${id}`);
    const outside = await createProject(request, `TC2${id}`.slice(0, 16), `Ceiling Out ${id}`);
    const limited = await createAgent(request, `Limited ${id}`, [inside.id]);
    const open = await createAgent(request, `Unrestricted ${id}`);

    // A forged outside-ceiling request is rejected instead of issuing a zero-access token.
    const forged = await request.post(`/api/v1/agents/${limited.id}/tokens`, { data: { name: 'forged', scopes: ['tasks:read'], project_ids: [outside.id] }, headers: { Origin: origin, 'Content-Type': 'application/json' } });
    expect(forged.status()).toBe(400);

    await openAgentSettings(page);
    let dialog = await openIssue(page, limited.id, limited.name);
    await expect(dialog.getByRole('checkbox', { name: inside.name, exact: true })).toBeVisible();
    await expect(dialog.getByRole('checkbox', { name: outside.name, exact: true })).toHaveCount(0);
    await dialog.getByRole('radio', { name: 'All agent projects' }).check();
    await expect(dialog.locator('[data-token-project-coverage]')).toContainText(inside.name);
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();

    dialog = await openIssue(page, open.id, open.name);
    await dialog.getByLabel('Token name').fill(`Inbox watcher ${id}`);
    await dialog.getByRole('radio', { name: 'Custom' }).check();
    for (const scope of ALL_SCOPES) {
      const box = dialog.getByRole('checkbox', { name: scope, exact: true });
      await expect(box).toBeVisible();
      if (await box.isChecked()) await box.uncheck();
    }
    await expect(dialog.getByRole('button', { name: 'Generate token', exact: true })).toBeDisabled();
    await dialog.getByRole('checkbox', { name: 'notifications:write', exact: true }).check();
    await dialog.getByRole('checkbox', { name: 'tasks:write', exact: true }).check();
    // Dependencies are explained, never silently granted.
    await expect(dialog.getByRole('checkbox', { name: 'tasks:read', exact: true })).not.toBeChecked();
    await expect(dialog.getByRole('checkbox', { name: 'notifications:read', exact: true })).not.toBeChecked();
    await expect(dialog.locator('[data-scope-dependency-note]')).toContainText('tasks:read');
    await dialog.getByRole('radio', { name: 'All agent projects' }).check();
    await expect(dialog.locator('[data-token-project-coverage]')).toContainText('future');
    await dialog.getByRole('radio', { name: 'No expiry' }).check();
    await expect(dialog.locator('[data-token-expiry-preview]')).toContainText('never expires');
    await expect(dialog.locator('[data-token-issue-summary]')).toContainText('Custom');

    const issued = page.waitForRequest(isIssuance);
    await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
    const body = (await issued).postDataJSON() as { scopes: string[]; project_ids?: string[]; expires_at?: string | null };
    expect([...body.scopes].sort()).toEqual(['notifications:write', 'tasks:write']);
    expect(body.project_ids ?? []).toEqual([]);
    expect(body.expires_at ?? null).toBeNull();
    const plaintext = await revealedValue(page);
    await finishReveal(page);
    await attachSanitized(testInfo, 'custom-issuance.json', { scopes: body.scopes, projects: 'agent ceiling', forgedStatus: forged.status() }, [plaintext]);
  });

  test('a double click issues exactly one POST', async ({ page, request }) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TD${id}`.slice(0, 16), `Double ${id}`);
    const agent = await createAgent(request, `Double ${id}`);
    let posts = 0;
    page.on('request', (sent) => { if (isIssuance(sent)) posts += 1; });
    await openAgentSettings(page);
    const dialog = await openIssue(page, agent.id, agent.name);
    await dialog.getByLabel('Token name').fill(`Double ${id}`);
    await dialog.getByRole('checkbox', { name: project.name, exact: true }).check();
    await dialog.getByRole('button', { name: 'Generate token', exact: true }).dblclick();
    await expect(page.getByRole('dialog', { name: 'Save your new token' })).toBeVisible();
    await page.waitForTimeout(500);
    expect(posts).toBe(1);
    await finishReveal(page);
    expect((await listAgents(request)).find((item) => item.id === agent.id)?.tokens?.length).toBe(1);
  });

  test('a lost issuance response recovers metadata instead of replaying', async ({ page, request }, testInfo) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TL${id}`.slice(0, 16), `Lost ${id}`);
    const agent = await createAgent(request, `Lost ${id}`);
    let posts = 0;
    let orphanId = '';
    await page.route('**/api/v1/agents/*/tokens', async (route) => {
      posts += 1;
      if (posts > 1) return route.continue();
      const response = await route.fetch();
      orphanId = ((await response.json()) as { id: string }).id;
      await route.abort('failed');
    });
    await openAgentSettings(page);
    const dialog = await openIssue(page, agent.id, agent.name);
    await dialog.getByLabel('Token name').fill(`Lost ${id}`);
    await dialog.getByRole('checkbox', { name: project.name, exact: true }).check();
    await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
    await expect(dialog.getByRole('alert')).toContainText('may have been created');
    await page.waitForTimeout(500);
    expect(posts).toBe(1);
    const candidate = dialog.locator(`[data-issuance-candidate="${orphanId}"]`);
    await expect(candidate).toContainText(`Lost ${id}`);
    await expect(candidate).toContainText(orphanId.slice(0, 8));
    await attachMaskedScreenshot(page, testInfo, 'issuance-uncertain.png');
    await candidate.getByRole('button', { name: `Revoke possible token Lost ${id}`, exact: true }).click();
    await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke token', exact: true }).click();
    await expect(candidate).toHaveCount(0);
    // Empty collections are omitted by the existing Actor JSON contract.
    expect(((await listAgents(request)).find((item) => item.id === agent.id)?.tokens || []).some((token) => token.id === orphanId)).toBe(false);

    await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
    const plaintext = await revealedValue(page);
    expect(posts).toBe(2);
    await finishReveal(page);
    await attachSanitized(testInfo, 'lost-response.json', { posts, orphanRevoked: true }, [plaintext]);
  });

  test('a token issued after the dialog was invalidated is revoked, never revealed', async ({ page, request }) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TO${id}`.slice(0, 16), `Orphan ${id}`);
    const agent = await createAgent(request, `Orphan ${id}`);
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    let issuedId = '';
    const deletes: string[] = [];
    page.on('request', (sent) => { if (sent.method() === 'DELETE' && /\/api\/v1\/tokens\//.test(sent.url())) deletes.push(decodeURIComponent(new URL(sent.url()).pathname.split('/').pop() || '')); });
    await page.route('**/api/v1/agents/*/tokens', async (route) => {
      const response = await route.fetch();
      const body = await response.text();
      issuedId = (JSON.parse(body) as { id: string }).id;
      await held;
      await route.fulfill({ response, body });
    });
    await openAgentSettings(page);
    const dialog = await openIssue(page, agent.id, agent.name);
    await dialog.getByLabel('Token name').fill(`Orphan ${id}`);
    await dialog.getByRole('checkbox', { name: project.name, exact: true }).check();
    await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
    await expect.poll(() => issuedId).not.toBe('');
    await page.evaluate(() => window.dispatchEvent(new Event('helm:auth-invalidated')));
    release();
    await expect(page.getByText('has been revoked')).toBeVisible();
    await expect(page.getByRole('dialog', { name: 'Save your new token' })).toHaveCount(0);
    expect(deletes).toEqual([issuedId]);
    expect(((await listAgents(request)).find((item) => item.id === agent.id)?.tokens || []).some((token) => token.id === issuedId)).toBe(false);
  });

  test('successful issuance survives a failed list refresh', async ({ page, request }) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TR${id}`.slice(0, 16), `Refresh ${id}`);
    const agent = await createAgent(request, `Refresh ${id}`);
    let issuedOnce = false;
    await page.route('**/api/v1/agents?*', async (route) => {
      if (!issuedOnce) return route.continue();
      issuedOnce = false;
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'unavailable', message: 'List unavailable' } }) });
    });
    page.on('requestfinished', (sent) => { if (isIssuance(sent)) issuedOnce = true; });
    await openAgentSettings(page);
    const dialog = await openIssue(page, agent.id, agent.name);
    await dialog.getByLabel('Token name').fill(`Refresh ${id}`);
    await dialog.getByRole('checkbox', { name: project.name, exact: true }).check();
    await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
    const plaintext = await revealedValue(page);
    expect(plaintext.length).toBeGreaterThan(16);
    await expect(page.getByText('could not be refreshed')).toBeVisible();
    await expect(page.getByText('could not be created')).toHaveCount(0);
    await finishReveal(page);
    expect(await bearer(request, plaintext, 'GET', `/api/v1/projects/${project.id}/tasks?limit=1`)).toBe(200);
  });
});
