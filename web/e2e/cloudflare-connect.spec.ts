import { existsSync, readFileSync } from 'node:fs';
import { expect, test, type APIRequestContext, type APIResponse, type Page } from '@playwright/test';

// Proves the "Connect Cloudflare failure contract" in docs/E2E_TESTING.md
// against a real Helm process and browser, the fake Cloudflare API (with
// OAuth endpoints) and the real sign-in relay Worker served by
// test/e2e/relay-server.mjs.

type FakeState = {
  tunnels: Record<string, { name: string }>;
  deleted_tunnels: string[];
  workers: Record<string, unknown>;
  rules: Record<string, { matchers: { value: string }[] }>;
  email_routing: Record<string, { support_subaddress: boolean }>;
  oauth_tokens_issued: number;
  oauth_revoked: number;
};
type Status = { connected: boolean; connected_via?: string; token_link: string; oauth_available: boolean; run?: { status: string } };

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const origin = new URL(baseURL).origin;
const fakeCF = process.env.HELM_E2E_FAKE_CF_URL || '';
const cfToken = process.env.HELM_E2E_FAKE_CF_TOKEN || '';
const zonelessToken = process.env.HELM_E2E_FAKE_CF_ZONELESS_TOKEN || '';
const noRulesToken = process.env.HELM_E2E_FAKE_CF_NORULES_TOKEN || '';
const jsonHeaders = { Origin: origin, 'Content-Type': 'application/json' };

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}: ${response.ok() ? '' : await response.text()}`).toBeTruthy();
  return await response.json() as T;
}

const fakeState = async (request: APIRequestContext) => (await json<{ result: FakeState }>(await request.get(`${fakeCF}/__state`), 'fake state')).result;
const status = async (request: APIRequestContext) => json<Status>(await request.get('/api/v1/cloudflare'), 'cloudflare status');

async function resetPublicAccess(request: APIRequestContext) {
  // Specs share the fake Cloudflare: start with plus addressing off again.
  await request.patch(`${fakeCF}/client/v4/zones/zone-1/email/routing`, { headers: { Authorization: `Bearer ${cfToken}` }, data: { support_subaddress: false } });
  await request.delete('/api/v1/cloudflare/session', { headers: jsonHeaders });
  const email = await json<{ active?: { id: string } }>(await request.get('/api/v1/email-intake'), 'email');
  if (email.active) await json(await request.delete(`/api/v1/email-intake/${email.active.id}`, { headers: jsonHeaders, data: { api_token: cfToken } }), 'reset email');
  const endpoint = await json<{ active?: { id: string } }>(await request.get('/api/v1/public-endpoints'), 'public URL');
  if (endpoint.active) await json(await request.delete(`/api/v1/public-endpoints/${endpoint.active.id}`, { headers: jsonHeaders, data: { api_token: cfToken } }), 'reset public URL');
}

async function openSetup(page: Page) {
  await page.goto('/tickets?connect=1');
  await expect(page.getByRole('heading', { name: 'Reach Helm from outside' })).toBeVisible();
  await expect(page.locator('[data-public-access-status]')).toHaveText('Not set up');
  const panel = page.locator('[data-cloudflare-setup]');
  // Sign in is the primary path; the token path is one click away.
  await expect(panel.locator('[data-cloudflare-signin]')).toBeVisible();
  await expect(panel.locator('#cf-token')).toHaveCount(0);
  await panel.locator('[data-token-toggle]').click();
  return panel;
}

test('Admins connect Cloudflare once and Helm sets up public access with a checklist', async ({ page, request }, testInfo) => {
  test.setTimeout(180_000);
  for (const [name, value] of Object.entries({ fakeCF, cfToken, zonelessToken, noRulesToken })) expect(value, `run.sh must provide ${name}`).toBeTruthy();
  await resetPublicAccess(request);
  try {
    // Agents cannot connect Cloudflare or run setup.
    const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    const project = await json<{ id: string }>(await request.post('/api/v1/projects', { data: { key: `CF${runID}`.toUpperCase().slice(0, 12), name: `Connect ${runID}` }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'project');
    const agent = await json<{ id: string }>(await request.post('/api/v1/agents', { data: { name: `Connect probe ${runID}`, project_ids: [project.id] }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'agent');
    const issued = await json<{ token: string }>(await request.post(`/api/v1/agents/${agent.id}/tokens`, { data: { name: 'probe', scopes: ['tasks:read', 'tasks:write'], project_ids: [project.id] }, headers: jsonHeaders }), 'agent token');
    const bearer = { Authorization: `Bearer ${issued.token}`, 'Content-Type': 'application/json' };
    for (const [method, path] of [['get', '/api/v1/cloudflare'], ['post', '/api/v1/cloudflare/session'], ['get', '/api/v1/cloudflare/zones'], ['post', '/api/v1/cloudflare/setup'], ['post', '/api/v1/cloudflare/oauth/start']] as const) {
      expect((await request[method](path, { headers: bearer, data: method === 'post' ? {} : undefined })).status(), path).toBe(403);
    }

    // The token link pre-selects exactly the documented permissions.
    let panel = await openSetup(page);
    const initial = await status(request);
    expect(initial).toMatchObject({ connected: false, oauth_available: true });
    const link = new URL(await panel.locator('[data-cloudflare-token-link]').getAttribute('href') as string);
    expect(link.origin + link.searchParams.get('to')).toBe('https://dash.cloudflare.com/:account/api-tokens');
    expect(JSON.parse(link.searchParams.get('permissionGroupKeys') as string).map((item: { key: string }) => item.key).sort()).toEqual(['argotunnel', 'dns', 'email_routing_address', 'email_routing_rule', 'workers_scripts', 'zone', 'zone_settings']);

    // Help explains every step without leaving Helm.
    await panel.getByRole('button', { name: 'How this works' }).click();
    const drawer = page.locator('[data-help-drawer]');
    await expect(drawer.getByRole('heading', { name: 'Set up with Cloudflare' })).toBeInViewport();
    await drawer.getByRole('link', { name: 'ticket webhooks' }).first().click();
    await expect(drawer.getByRole('heading', { name: 'Ticket webhooks: connect outside apps to Helm Tickets' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(drawer).toHaveCount(0);

    // A token that sees no domains is refused with the fix.
    await panel.locator('#cf-token').fill(zonelessToken);
    await panel.getByRole('button', { name: 'Connect', exact: true }).click();
    await expect(panel.getByRole('alert')).toContainText('not allowed to see any domains');
    expect((await status(request)).connected).toBe(false);

    // A token without DNS access cannot pick a domain, and the API refuses
    // setup too; nothing is created in Cloudflare.
    const before = await fakeState(request);
    await panel.locator('#cf-token').fill(noRulesToken);
    await panel.getByRole('button', { name: 'Connect', exact: true }).click();
    await expect(panel.locator('[data-cloudflare-connected="token"]')).toBeVisible();
    await expect(panel.locator('[data-zone="example.test"] [data-zone-unusable]')).toHaveText('No DNS access');
    await expect(panel.locator('[data-zone="example.test"] input')).toBeDisabled();
    await expect(panel.locator('[data-setup-start]')).toHaveCount(0);
    const refused = await request.post('/api/v1/cloudflare/setup', { headers: jsonHeaders, data: { zone: 'zone-1' } });
    expect(refused.status()).toBe(400);
    expect(await refused.text()).toContain('cannot manage DNS here');
    expect(Object.keys((await fakeState(request)).tunnels)).toEqual(Object.keys(before.tunnels));
    await panel.getByRole('button', { name: 'Disconnect' }).click();
    await expect(panel.getByRole('status')).toContainText('Helm no longer holds the Cloudflare credential');

    // When only email fails, the working public URL stays, the step says
    // why, and the half-created Worker and plus addressing are rolled back.
    await panel.locator('#cf-token').fill(cfToken);
    await panel.getByRole('button', { name: 'Connect', exact: true }).click();
    // The only domain this connection can manage is chosen automatically.
    await expect(panel.locator('[data-zone="example.test"]')).toContainText('Domain example.test');
    await expect(panel.locator('[data-other-zones]')).toContainText('1 other domain');
    await expect(panel.locator('[data-setup-hostname]')).toContainText('https://hooks.example.test');
    await expect(panel.locator('[data-setup-start]')).toBeDisabled();
    await panel.locator('[data-setup-consent] input').check();
    await panel.locator('.cf-email-options > summary').click();
    await panel.getByLabel('Guided email address name').fill(`fail-rule-${runID}`);
    await testInfo.attach('connect-choose.png', { contentType: 'image/png', body: await panel.screenshot() });
    await panel.locator('[data-setup-start]').click();
    await expect(panel.locator('[data-setup-run]')).toHaveAttribute('data-setup-run', 'failed', { timeout: 60_000 });
    for (const step of ['public_url', 'connector', 'reachable', 'forget']) await expect(panel.locator(`[data-setup-step="${step}"]`)).toHaveAttribute('data-setup-status', 'done');
    await expect(panel.locator('[data-setup-step="email"]')).toHaveAttribute('data-setup-status', 'failed');
    await expect(panel.locator('[data-setup-step="email"]')).toContainText('only email was not set up');
    await expect(panel.locator('[data-setup-result]')).toContainText('https://hooks.example.test');
    await testInfo.attach('connect-email-failed.png', { contentType: 'image/png', body: await panel.screenshot() });
    const partial = await fakeState(request);
    expect(Object.values(partial.tunnels).map((tunnel) => tunnel.name)).toContain('helm-hooks.example.test');
    expect(Object.keys(partial.workers)).toEqual(Object.keys(before.workers));
    expect(partial.email_routing['zone-1'].support_subaddress).toBe(false);
    expect((await status(request)).connected).toBe(false);
    await expect(page.locator('[data-public-access-status]')).toHaveText('Live');
    expect((await json<{ active?: unknown }>(await request.get('/api/v1/email-intake'), 'email')).active).toBeUndefined();

    // The token is never stored, logged or returned.
    expect(JSON.stringify(await status(request))).not.toContain(cfToken);
    const dbPath = process.env.HELM_E2E_DB as string;
    for (const secret of [cfToken, noRulesToken]) {
      for (const file of [dbPath, `${dbPath}-wal`]) if (existsSync(file)) expect(readFileSync(file).includes(Buffer.from(secret))).toBe(false);
      expect(readFileSync(process.env.HELM_E2E_SERVER_LOG as string, 'utf8')).not.toContain(secret);
    }

    // Sign in with Cloudflare: consent at Cloudflare, the real relay shows
    // where the browser returns, and Helm redeems the code with PKCE. The
    // rerun keeps the working public URL, adds email, and revokes the
    // sign-in afterwards.
    await panel.getByRole('button', { name: 'Try again' }).click();
    await panel.locator('[data-cloudflare-signin]').click();
    await expect(page.getByRole('heading', { name: 'Finish connecting Cloudflare' })).toBeVisible();
    await expect(page.locator('[data-helm-origin]')).toHaveText(origin);
    await testInfo.attach('relay.png', { contentType: 'image/png', body: await page.screenshot() });
    await page.getByRole('link', { name: 'Continue to Helm' }).click();
    panel = page.locator('[data-cloudflare-setup]');
    await expect(panel.getByRole('status')).toContainText('Signed in with Cloudflare');
    await expect(panel.locator('[data-cloudflare-connected="cloudflare"]')).toBeVisible();
    const revokedBefore = (await fakeState(request)).oauth_revoked;
    // The only domain this connection can manage is chosen automatically.
    await expect(panel.locator('[data-zone="example.test"]')).toContainText('Domain example.test');
    await expect(panel.locator('[data-other-zones]')).toContainText('1 other domain');
    await expect(panel.locator('[data-setup-existing]')).toContainText('https://hooks.example.test');
    await panel.locator('[data-setup-consent] input').check();
    await panel.locator('[data-setup-start]').click();
    await expect(panel.locator('[data-setup-run]')).toHaveAttribute('data-setup-run', 'done', { timeout: 60_000 });
    for (const step of ['public_url', 'connector', 'reachable', 'email', 'forget']) await expect(panel.locator(`[data-setup-step="${step}"]`)).toHaveAttribute('data-setup-status', 'done');
    await expect(panel.locator('[data-setup-step="public_url"]')).toContainText('Already set up: https://hooks.example.test');
    // Setup ends with a working inbox: its address is shown right away.
    await expect(panel.locator('[data-setup-step="email"]')).toContainText('Inbox “Alerts”');
    const inboxAddress = (await panel.locator('[data-setup-inbox]').innerText()).trim();
    expect(inboxAddress).toMatch(/^helm-alerts\+alerts-[a-z2-7]{6}@example\.test$/);
    await testInfo.attach('connect-done.png', { contentType: 'image/png', body: await panel.screenshot() });
    const done = await fakeState(request);
    expect(done.oauth_revoked).toBe(revokedBefore + 1);
    expect(Object.values(done.tunnels).filter((tunnel) => tunnel.name === 'helm-hooks.example.test')).toHaveLength(1);
    expect(Object.values(done.rules).some((rule) => rule.matchers[0]?.value === 'helm-alerts@example.test')).toBe(true);
    expect(done.email_routing['zone-1'].support_subaddress).toBe(true);
    expect((await status(request)).connected).toBe(false);
    // Done shows the short summary; setup can be run again from it.
    await panel.getByRole('button', { name: 'Done' }).click();
    await expect(page.locator('[data-summary-url]')).toHaveText('https://hooks.example.test');
    await expect(page.locator('[data-summary-email]')).toContainText(inboxAddress);
    await expect(page.locator('[data-inbox="Alerts"] [data-inbox-address]')).toHaveText(inboxAddress);
    await expect(page.locator('[data-public-access-status]')).toHaveText('Live');
    await testInfo.attach('summary.png', { contentType: 'image/png', body: await page.locator('[data-public-access]').screenshot() });

    // A declined sign-in and a forged callback are both refused.
    await page.locator('[data-rerun-setup]').click();
    await json(await request.post(`${fakeCF}/__oauth_deny_next`), 'deny next');
    await panel.locator('[data-cloudflare-signin]').click();
    await expect(page.getByRole('heading', { name: 'Cloudflare sign-in was not completed' })).toBeVisible();
    await page.getByRole('link', { name: 'Continue to Helm' }).click();
    await expect(page.locator('[data-cloudflare-setup]').getByRole('alert')).toContainText('Cloudflare sign-in was not completed: The user denied access');
    const issuedBefore = (await fakeState(request)).oauth_tokens_issued;
    await page.goto('/api/v1/cloudflare/oauth/callback?code=forged&state=deadbeefdeadbeefdeadbeefdeadbeef.aHR0cDovLzEyNy4wLjAuMToxODA4MA');
    await expect(page.locator('[data-cloudflare-setup]').getByRole('alert')).toContainText('expired or was started by someone else');
    expect((await fakeState(request)).oauth_tokens_issued).toBe(issuedBefore);
    expect((await status(request)).connected).toBe(false);
  } finally {
    await resetPublicAccess(request);
  }
});
