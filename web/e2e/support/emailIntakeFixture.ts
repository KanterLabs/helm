import { existsSync, readFileSync } from 'node:fs';
import { expect, type APIRequestContext, type APIResponse, type Locator, type Page } from '@playwright/test';

// Proves the "Email intake failure contract" in docs/E2E_TESTING.md against a
// real Helm process and browser. The Worker Helm uploads to the fake
// Cloudflare API is executed here in Node with synthetic messages, posting to
// Helm's real hooks listener, so the Worker contract is tested, not mocked.

type Intake = { id: string; local_part: string; domain: string; worker_name: string; rule_id: string; status: string; cleanup_pending: boolean; subaddress_enabled_by_helm: boolean };
type EmailView = { active?: Intake; history: Intake[]; recent: { subject: string; sender: string; outcome: string; task_key?: string; reason?: string }[]; public_hostname?: string; suggested_domain?: string };
type Binding = { type: string; name: string; text: string };
type FakeState = {
  workers: Record<string, { metadata: { main_module: string; bindings: Binding[] }; source: string }>;
  deleted_workers: string[];
  rules: Record<string, { id: string; matchers: { value: string }[]; actions: { type: string; value: string[] }[] }>;
  deleted_rules: string[];
  email_routing: Record<string, { support_subaddress: boolean }>;
};
type Ticket = { id: string; key: string; title: string; description: string; priority: string; alert_source?: { alert_type: string; evidence: Record<string, string>; occurrence_count: number } };
type Delivery = { rejected?: string; forwarded?: { to: string; headers: Record<string, string> }; threw?: string };
type EmailWorker = { email(message: unknown, env: Record<string, string>): Promise<void> };

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const origin = new URL(baseURL).origin;
const fakeCF = process.env.HELM_E2E_FAKE_CF_URL || '';
const cfToken = process.env.HELM_E2E_FAKE_CF_TOKEN || '';
const noRulesToken = process.env.HELM_E2E_FAKE_CF_NORULES_TOKEN || '';
const hooksURL = process.env.HELM_E2E_HOOKS_URL || '';
const jsonHeaders = { Origin: origin, 'Content-Type': 'application/json' };

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}: ${response.ok() ? '' : await response.text()}`).toBeTruthy();
  return await response.json() as T;
}

const fakeState = async (request: APIRequestContext) => (await json<{ result: FakeState }>(await request.get(`${fakeCF}/__state`), 'fake state')).result;
const emailView = async (request: APIRequestContext) => json<EmailView>(await request.get('/api/v1/email-intake'), 'email view');

async function resetPublicAccess(request: APIRequestContext) {
  // Specs share the fake Cloudflare: start with plus addressing off again.
  await request.patch(`${fakeCF}/client/v4/zones/zone-1/email/routing`, { headers: { Authorization: `Bearer ${cfToken}` }, data: { support_subaddress: false } });
  const email = await emailView(request);
  if (email.active) await json(await request.delete(`/api/v1/email-intake/${email.active.id}`, { headers: jsonHeaders, data: { api_token: cfToken } }), 'reset email');
  const publicView = await json<{ active?: { id: string } }>(await request.get('/api/v1/public-endpoints'), 'public view');
  if (publicView.active) await json(await request.delete(`/api/v1/public-endpoints/${publicView.active.id}`, { headers: jsonHeaders, data: { api_token: cfToken } }), 'reset public URL');
}

async function loadWorker(source: string): Promise<EmailWorker> {
  const module = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`) as { default: EmailWorker };
  return module.default;
}

// A ForwardableEmailMessage as Email Routing hands it to a Worker.
async function deliver(worker: EmailWorker, env: Record<string, string>, raw: string, to: string, rawSize?: number): Promise<Delivery> {
  const bytes = new TextEncoder().encode(raw);
  const headers = new Headers();
  for (const line of raw.split(/\r\n\r\n/)[0].split(/\r\n/)) {
    const colon = line.indexOf(':');
    if (colon > 0) headers.append(line.slice(0, colon).trim(), line.slice(colon + 1).trim());
  }
  const result: Delivery = {};
  const message = {
    from: 'bounces+123@mailer.example', to, headers, raw: new Response(bytes).body, rawSize: rawSize ?? bytes.length,
    setReject(reason: string) { result.rejected = reason; },
    async forward(rcpt: string, extra?: Headers) { result.forwarded = { to: rcpt, headers: Object.fromEntries(extra ? extra.entries() : []) }; }
  };
  try {
    await worker.email(message, env);
  } catch (error) {
    result.threw = String(error);
  }
  return result;
}

function mail(lines: string[]): string {
  return lines.join('\r\n');
}

// Inbox mail lands in the ticket queue (docs/TICKET_QUEUE_PLAN.md).
async function queuedTicketsTitled(request: APIRequestContext, title: string): Promise<Ticket[]> {
  const page = await json<{ data: (Ticket & { project_id: string })[]; queue?: { project_id: string } }>(await request.get(`/api/v1/tickets?q=${encodeURIComponent(title)}`), 'tickets');
  const matches = page.data.filter((ticket) => ticket.title === title);
  for (const ticket of matches) expect(ticket.project_id, 'inbox tickets land in the ticket queue').toBe(page.queue?.project_id);
  return matches;
}

// Manual setup forms sit in a collapsed "Set up manually" section.
async function openManual(details: Locator) {
  if (await details.evaluate((element) => !(element as HTMLDetailsElement).open)) await details.locator('summary').first().click();
}

async function openConnectApps(page: Page, projectKey: string) {
  await page.goto(`/tickets?project=${projectKey}`);
  await page.getByRole('button', { name: /Connect apps/ }).click();
  await openManual(page.locator('[data-public-access-advanced]'));
  const card = page.locator('.email-intake');
  await expect(card.getByRole('heading', { name: 'Email', exact: true })).toBeVisible();
  return card;
}

async function verifyAgentDenials(request: APIRequestContext, projectID: string, runID: string) {
  // Agents cannot manage email intake.
  const agent = await json<{ id: string }>(await request.post('/api/v1/agents', { data: { name: `Email probe ${runID}`, project_ids: [projectID] }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'agent');
  const issued = await json<{ token: string }>(await request.post(`/api/v1/agents/${agent.id}/tokens`, { data: { name: 'probe', scopes: ['tasks:read', 'tasks:write'], project_ids: [projectID] }, headers: jsonHeaders }), 'token');
  const bearer = { Authorization: `Bearer ${issued.token}`, 'Content-Type': 'application/json' };
  expect((await request.get('/api/v1/email-intake', { headers: bearer })).status()).toBe(403);
  expect((await request.post('/api/v1/email-intake', { headers: bearer, data: { domain: 'example.test', local_part: 'x', api_token: cfToken } })).status()).toBe(403);
  expect((await request.delete('/api/v1/email-intake/x', { headers: bearer })).status()).toBe(403);
  expect((await request.get('/api/v1/email-inboxes', { headers: bearer })).status()).toBe(403);
  expect((await request.post('/api/v1/email-inboxes', { headers: bearer, data: { name: 'x' } })).status()).toBe(403);
  expect((await request.delete('/api/v1/email-inboxes/x', { headers: bearer })).status()).toBe(403);
  expect((await request.post('/api/v1/email-inboxes/x/address', { headers: bearer })).status()).toBe(403);
}

async function verifyNoSecretLeaks(request: APIRequestContext, intakeSecret: string) {
  // The Worker's intake secret never leaks into responses, the database or
  // the log. (Inbox addresses are shown on purpose.)
  const visible = JSON.stringify([await emailView(request), await json(await request.get('/api/v1/email-inboxes'), 'inboxes')]);
  for (const secret of [intakeSecret]) {
    expect(visible).not.toContain(secret);
    const dbPath = process.env.HELM_E2E_DB as string;
    for (const file of [dbPath, `${dbPath}-wal`]) if (existsSync(file)) expect(readFileSync(file).includes(Buffer.from(secret))).toBe(false);
    expect(readFileSync(process.env.HELM_E2E_SERVER_LOG as string, 'utf8')).not.toContain(secret);
  }
}

export { cfToken, deliver, emailView, fakeCF, fakeState, hooksURL, json, jsonHeaders, loadWorker, mail, noRulesToken, openConnectApps, openManual, queuedTicketsTitled, resetPublicAccess, verifyAgentDenials, verifyNoSecretLeaks };
export type { Intake, Ticket };
