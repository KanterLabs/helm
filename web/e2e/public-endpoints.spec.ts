import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { expect, test, type APIRequestContext, type APIResponse, type Locator } from '@playwright/test';

// Proves the "Public endpoint failure contract" in docs/E2E_TESTING.md
// against a real Helm process with the fake Cloudflare API and cloudflared.

type View = {
  active?: { id: string; hostname: string; tunnel_id: string; dns_record_id: string; created_by_name?: string };
  connector: { state: string; connections: number; locations?: string[]; connected_at?: string };
  last_test?: { endpoint_id: string; ok: boolean };
  public_hook_base?: string;
  history: { id: string; hostname: string; status: string; cleanup_pending: boolean; tunnel_id: string; dns_record_id: string }[];
  connector_available: boolean;
};
type FakeState = {
  tunnels: Record<string, { id: string; name: string }>;
  configs: Record<string, { config: { ingress: Record<string, string>[] } }>;
  dns: Record<string, { id: string; type: string; name: string; content: string; proxied: boolean }>;
  run_tokens: Record<string, string>;
  deleted_tunnels: string[];
  deleted_dns: string[];
};

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const origin = new URL(baseURL).origin;
const fakeCF = process.env.HELM_E2E_FAKE_CF_URL || '';
const cfToken = process.env.HELM_E2E_FAKE_CF_TOKEN || '';
const zonelessToken = process.env.HELM_E2E_FAKE_CF_ZONELESS_TOKEN || '';
const hooksURL = process.env.HELM_E2E_HOOKS_URL || '';
const cloudflaredLog = process.env.HELM_E2E_CLOUDFLARED_LOG || '';

const jsonHeaders = { Origin: origin, 'Content-Type': 'application/json' };

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}: ${response.ok() ? '' : await response.text()}`).toBeTruthy();
  return await response.json() as T;
}

async function fakeState(request: APIRequestContext): Promise<FakeState> {
  return (await json<{ result: FakeState }>(await request.get(`${fakeCF}/__state`), 'fake state')).result;
}

// Manual setup forms sit in a collapsed "Set up manually" section.
async function openManual(details: Locator) {
  if (await details.evaluate((element) => !(element as HTMLDetailsElement).open)) await details.locator('summary').first().click();
}

async function view(request: APIRequestContext): Promise<View> {
  return json<View>(await request.get('/api/v1/public-endpoints'), 'public endpoints');
}

test('Admins publish only webhook routes through a Cloudflare tunnel without storing the token', async ({ page, request }, testInfo) => {
  test.setTimeout(120_000);
  for (const [name, value] of Object.entries({ fakeCF, cfToken, zonelessToken, hooksURL, cloudflaredLog })) expect(value, `run.sh must provide ${name}`).toBeTruthy();
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  const hostname = `hooks-${runID}.example.test`;

  // A retry against the same server starts from no active endpoint.
  const initial = await view(request);
  expect(initial.connector_available, 'the cloudflared fixture must be runnable').toBe(true);
  if (initial.active) await json(await request.delete(`/api/v1/public-endpoints/${initial.active.id}`, { headers: jsonHeaders, data: { api_token: cfToken } }), 'reset');

  // Agent tokens cannot touch public endpoints.
  const project = await json<{ id: string; key: string }>(await request.post('/api/v1/projects', { data: { key: `PE${runID}`.toUpperCase().slice(0, 16), name: `Public endpoints ${runID}` }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'project');
  const agent = await json<{ id: string }>(await request.post('/api/v1/agents', { data: { name: `Endpoint probe ${runID}`, project_ids: [project.id] }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'agent');
  const issued = await json<{ token: string }>(await request.post(`/api/v1/agents/${agent.id}/tokens`, { data: { name: 'probe', scopes: ['tasks:read', 'tasks:write'], project_ids: [project.id] }, headers: jsonHeaders }), 'token');
  const bearer = { Authorization: `Bearer ${issued.token}`, 'Content-Type': 'application/json' };
  expect((await request.get('/api/v1/public-endpoints', { headers: bearer })).status()).toBe(403);
  expect((await request.post('/api/v1/public-endpoints', { headers: bearer, data: { hostname, api_token: cfToken } })).status()).toBe(403);
  expect((await request.delete('/api/v1/public-endpoints/x', { headers: bearer })).status()).toBe(403);

  await page.goto(`/tickets?project=${project.key}`);
  await page.getByRole('button', { name: /Connect apps/ }).click();
  // Detailed cards live under "Details, history and manual setup".
  await openManual(page.locator('[data-public-access-advanced]'));
  const card = page.locator('.public-endpoint');
  await expect(card.getByRole('heading', { name: 'Public URL' })).toBeVisible();

  // A token without zone access and an apex hostname fail cleanly, creating nothing.
  const tunnelsBefore = Object.keys((await fakeState(request)).tunnels).length;
  await openManual(card.locator('[data-manual-public]'));
  await card.getByLabel('Public hostname').fill(hostname);
  await card.getByLabel('Cloudflare API token').fill(zonelessToken);
  await card.getByRole('button', { name: 'Create public URL' }).click();
  await expect(card.getByRole('alert')).toContainText('cannot see a Cloudflare zone');
  await card.getByLabel('Public hostname').fill('example.test');
  await card.getByLabel('Cloudflare API token').fill(cfToken);
  await card.getByRole('button', { name: 'Create public URL' }).click();
  await expect(card.getByRole('alert')).toContainText('hostname must be a subdomain');
  await expect(card.locator('[data-hostname-suggestion]')).toHaveText('Use hooks.example.test instead');
  expect(Object.keys((await fakeState(request)).tunnels).length).toBe(tunnelsBefore);

  // A valid token provisions a webhook-only tunnel and starts the connector.
  await openManual(card.locator('[data-manual-public]'));
  await card.getByLabel('Public hostname').fill(hostname);
  await card.getByLabel('Cloudflare API token').fill(cfToken);
  await card.getByRole('button', { name: 'Create public URL' }).click();
  await expect(card.locator('[data-public-hostname]')).toHaveText(`https://${hostname}`);
  await expect(card.locator('[data-connector-state]')).toHaveText('Connected', { timeout: 15_000 });
  const active = (await view(request)).active!;

  // The live view shows the edge connections, who created it and the
  // Cloudflare resource IDs, and keeps the Connect apps button flagged.
  await expect(card.locator('[data-public-badge]')).toHaveText('Live');
  await expect(card.locator('[data-connector-edges]')).toHaveText('2 edge connections · e2e01, e2e02');
  const live = await view(request);
  expect(live.connector).toMatchObject({ state: 'connected', connections: 2, locations: ['e2e01', 'e2e02'] });
  expect(live.connector.connected_at).toBeTruthy();
  expect(live.active!.created_by_name).toBeTruthy();
  await expect(card).toContainText(`by ${live.active!.created_by_name}`);
  await expect(card.locator('[data-public-hook-base]')).toHaveText(`https://${hostname}/api/v1/hooks/tickets/…`);
  await card.getByText('Cloudflare resources and exposed paths').click();
  await expect(card.locator('[data-tunnel-id]')).toHaveText(active.tunnel_id);
  await expect(page.locator('[data-public-pill]')).toHaveText('● Public');

  // The self-test round-trips a one-time nonce through the hooks listener
  // without creating a ticket. Agents cannot run it; guessed or reused
  // nonces are not answered, and the main origin never answers probes.
  const ticketsBefore = (await json<{ data: unknown[] }>(await request.get(`/api/v1/tickets?project=${project.key}`), 'tickets before test')).data.length;
  await expect(card.getByText('Not tested yet')).toBeVisible();
  await card.getByRole('button', { name: 'Test public URL' }).click();
  await expect(card.locator('[data-test-result]')).toHaveAttribute('data-test-result', 'ok');
  await expect(card.locator('[data-test-detail]')).toContainText('ms round trip');
  expect((await view(request)).last_test).toMatchObject({ endpoint_id: active.id, ok: true });
  expect((await json<{ data: unknown[] }>(await request.get(`/api/v1/tickets?project=${project.key}`), 'tickets after test')).data.length).toBe(ticketsBefore);
  expect((await request.post(`/api/v1/public-endpoints/${active.id}/test`, { headers: bearer })).status()).toBe(403);
  expect((await request.post(`/api/v1/public-endpoints/${'0'.repeat(32)}/test`, { headers: jsonHeaders })).status()).toBe(409);
  const guess = `/api/v1/hooks/tickets/probe/${'a'.repeat(32)}`;
  expect((await request.get(`${hooksURL}${guess}`)).status()).toBe(404);
  expect((await request.get(guess)).status()).not.toBe(200);
  await testInfo.attach('public-url-live.png', { contentType: 'image/png', body: await card.screenshot() });
  const state = await fakeState(request);
  expect(state.tunnels[active.tunnel_id].name).toBe(`helm-${hostname}`);
  expect(state.configs[active.tunnel_id].config.ingress).toEqual([
    { hostname, path: '^/api/v1/(hooks/tickets|intake/coolify)/', service: hooksURL.replace(/\/$/, '') },
    { service: 'http_status:404' }
  ]);
  expect(state.dns[active.dns_record_id]).toMatchObject({ type: 'CNAME', name: hostname, content: `${active.tunnel_id}.cfargotunnel.com`, proxied: true });
  const runToken = state.run_tokens[active.tunnel_id];
  const launches = readFileSync(cloudflaredLog, 'utf8').trim().split('\n').map((line) => JSON.parse(line) as { argv: string; token_env: string; token_sha256: string });
  const launch = launches[launches.length - 1];
  expect(launch).toEqual({ argv: 'tunnel --no-autoupdate run', token_env: 'yes', token_sha256: createHash('sha256').update(runToken).digest('hex') });
  await testInfo.attach('public-url-active.png', { contentType: 'image/png', body: await card.screenshot() });

  // The hooks listener serves only webhook routes.
  for (const path of ['/', '/api/v1', '/api/v1/projects', '/tickets', '/api/v1/auth/me']) {
    expect((await request.get(`${hooksURL}${path}`)).status(), path).toBe(404);
  }
  expect((await request.get(`${hooksURL}/healthz`)).status()).toBe(200);
  const webhook = await json<{ url: string; webhook: { id: string } }>(await request.post('/api/v1/ticket-webhooks', { data: { name: `Public ${runID}`, project: project.key }, headers: jsonHeaders }), 'webhook');
  expect(webhook.url).toMatch(new RegExp(`^https://${hostname.replace(/\./g, '\\.')}/api/v1/hooks/tickets/hk_`));
  const viaTunnel = await request.post(`${hooksURL}${new URL(webhook.url).pathname}`, { data: { title: `Through the tunnel ${runID}` }, headers: { 'Content-Type': 'application/json' } });
  expect(viaTunnel.status()).toBe(201);
  expect((await request.post(`${hooksURL}/api/v1/hooks/tickets/${'x'.repeat(40)}`, { data: { title: 'x' }, headers: { 'Content-Type': 'application/json' } })).status()).toBe(404);

  // "Send test" makes Helm post a real ticket for the webhook to its own
  // public hostname (here routed to the hooks listener with the public Host)
  // with a one-time nonce bound to that webhook. Repeats count on one
  // ticket; agents cannot send tests and a guessed nonce files nothing.
  const testTitle = 'Test ticket via public URL';
  const testTickets = async () => (await json<{ data: { key: string; title: string; priority: string }[] }>(await request.get(`/api/v1/tickets?project=${project.key}&q=${encodeURIComponent(testTitle)}`), 'test tickets')).data.filter((item) => item.title === testTitle);
  await page.reload();
  await page.getByRole('button', { name: /Connect apps/ }).click();
  const panel = page.locator('.ticket-integrations');
  await expect(panel.locator('[data-webhook-test-guide]')).toContainText(`https://${hostname}`);
  await panel.locator(`[data-webhook-name="Public ${runID}"]`).getByRole('button', { name: 'Send test' }).click();
  const testRow = panel.locator(`[data-webhook-test-result="Public ${runID}"]`);
  await expect(testRow.locator('[data-webhook-test-status]')).toHaveAttribute('data-webhook-test-status', 'ok');
  const [testTicket] = await testTickets();
  expect(testTicket).toMatchObject({ title: testTitle, priority: 'low' });
  await expect(testRow).toContainText(`Opened ${testTicket.key} through the public URL.`);
  await expect(testRow.getByRole('link', { name: `Open ${testTicket.key}` })).toBeVisible();
  await testInfo.attach('webhook-send-test.png', { contentType: 'image/png', body: await panel.locator('.webhook-table').first().screenshot() });
  const again = await json<{ ok: boolean; disposition: string; occurrence_count: number; ticket_key: string; hostname: string }>(await request.post(`/api/v1/ticket-webhooks/${webhook.webhook.id}/test`, { headers: jsonHeaders }), 'second test');
  expect(again).toMatchObject({ ok: true, disposition: 'repeated', occurrence_count: 2, ticket_key: testTicket.key, hostname });
  expect(await testTickets()).toHaveLength(1);
  expect((await request.post(`/api/v1/ticket-webhooks/${webhook.webhook.id}/test`, { headers: bearer })).status()).toBe(403);
  expect((await request.post(`${hooksURL}/api/v1/hooks/tickets/probe/${'b'.repeat(32)}`, { data: { title: 'guessed' }, headers: { 'Content-Type': 'application/json' } })).status()).toBe(404);
  expect((await request.post(`/api/v1/hooks/tickets/probe/${'b'.repeat(32)}`, { data: { title: 'guessed' }, headers: { 'Content-Type': 'application/json' } })).status()).not.toBe(201);
  expect(await testTickets()).toHaveLength(1);

  // The Cloudflare API token is not in responses, the database or the log.
  expect(JSON.stringify(await view(request))).not.toContain(cfToken);
  const dbPath = process.env.HELM_E2E_DB as string;
  for (const file of [dbPath, `${dbPath}-wal`]) {
    if (existsSync(file)) expect(readFileSync(file).includes(Buffer.from(cfToken))).toBe(false);
  }
  expect(readFileSync(process.env.HELM_E2E_SERVER_LOG as string, 'utf8')).not.toContain(cfToken);

  // Disabling with the token deletes DNS and tunnel and falls back to the origin.
  await card.getByRole('button', { name: 'Remove public URL…' }).click();
  await card.locator('input[type="password"]').fill(cfToken);
  await card.getByRole('button', { name: 'Remove public URL' }).click();
  await expect(card.getByRole('status')).toContainText(`https://${hostname} removed`);
  const after = await fakeState(request);
  expect(after.deleted_dns).toContain(active.dns_record_id);
  expect(after.deleted_tunnels).toContain(active.tunnel_id);
  const finalView = await view(request);
  expect(finalView.active).toBeUndefined();
  expect(finalView.connector.state).toBe('stopped');
  expect((await request.post(`/api/v1/ticket-webhooks/${webhook.webhook.id}/test`, { headers: jsonHeaders })).status()).toBe(409);
  await expect(card.locator(`[data-history-row="${hostname}"]`)).toContainText('Removed');

  // Removing without a token leaves Cloudflare resources behind; the history
  // flags them, a tokenless retry changes nothing, and "Finish cleanup"
  // deletes them with a token. A finished cleanup is never re-flagged.
  const orphanHost = `orphan-${runID}.example.test`;
  await openManual(card.locator('[data-manual-public]'));
  await card.getByLabel('Public hostname').fill(orphanHost);
  await card.getByLabel('Cloudflare API token').fill(cfToken);
  await card.getByRole('button', { name: 'Create public URL' }).click();
  await expect(card.locator('[data-connector-state]')).toHaveText('Connected', { timeout: 15_000 });
  const orphan = (await view(request)).active!;
  await card.getByRole('button', { name: 'Remove public URL…' }).click();
  await card.getByRole('button', { name: 'Remove public URL' }).click();
  await expect(card.getByRole('status')).toContainText(`Delete DNS record ${orphan.dns_record_id} and tunnel ${orphan.tunnel_id}`);
  const orphanRow = card.locator(`[data-history-row="${orphanHost}"]`);
  await expect(orphanRow).toContainText('Needs cleanup');
  await expect(orphanRow).toContainText(orphan.tunnel_id);
  await expect(card).toContainText('1 needs Cloudflare cleanup');
  await testInfo.attach('public-url-needs-cleanup.png', { contentType: 'image/png', body: await card.screenshot() });
  expect((await fakeState(request)).tunnels[orphan.tunnel_id]).toBeTruthy();
  const tokenless = await json<{ endpoint: { cleanup_pending: boolean } }>(await request.delete(`/api/v1/public-endpoints/${orphan.id}`, { headers: jsonHeaders }), 'tokenless retry');
  expect(tokenless.endpoint.cleanup_pending).toBe(true);
  await page.goto('/admin');
  await expect(page.locator('[data-admin-public-access]')).toContainText('1 removed public URL still has resources in Cloudflare.');
  await testInfo.attach('admin-public-access.png', { contentType: 'image/png', body: await page.locator('[data-admin-public-access]').screenshot() });
  await page.locator('[data-admin-public-access]').getByRole('link', { name: 'Manage' }).click();
  await expect(page).toHaveURL(/\/tickets$/);
  await expect(card.locator(`[data-history-row="${orphanHost}"]`)).toContainText('Needs cleanup');
  await orphanRow.getByRole('button', { name: 'Finish cleanup…' }).click();
  await orphanRow.getByLabel(`Cloudflare API token for ${orphanHost}`).fill(cfToken);
  await orphanRow.getByRole('button', { name: 'Delete in Cloudflare' }).click();
  await expect(card.getByRole('status')).toContainText(`Deleted the tunnel and DNS record for ${orphanHost} in Cloudflare.`);
  await expect(orphanRow).toContainText('Removed');
  const cleaned = await fakeState(request);
  expect(cleaned.deleted_tunnels).toContain(orphan.tunnel_id);
  expect(cleaned.deleted_dns).toContain(orphan.dns_record_id);
  const settled = await json<{ endpoint: { cleanup_pending: boolean } }>(await request.delete(`/api/v1/public-endpoints/${orphan.id}`, { headers: jsonHeaders }), 'tokenless after cleanup');
  expect(settled.endpoint.cleanup_pending).toBe(false);
  await expect(page.locator('[data-public-pill]')).toHaveCount(0);
  await testInfo.attach('public-url-history.png', { contentType: 'image/png', body: await card.screenshot() });
  const fallback = await json<{ url: string }>(await request.post('/api/v1/ticket-webhooks', { data: { name: `Private ${runID}`, project: project.key }, headers: jsonHeaders }), 'fallback webhook');
  expect(fallback.url.startsWith(`${origin}/api/v1/hooks/tickets/`)).toBe(true);
  await testInfo.attach('public-url-setup.png', { contentType: 'image/png', body: await card.screenshot() });
});
