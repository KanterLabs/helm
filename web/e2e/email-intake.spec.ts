import { existsSync, readFileSync } from 'node:fs';
import { expect, test, type APIRequestContext, type APIResponse, type Locator, type Page } from '@playwright/test';

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

async function ticketsTitled(request: APIRequestContext, projectKey: string, title: string): Promise<Ticket[]> {
  const page = await json<{ data: Ticket[] }>(await request.get(`/api/v1/tickets?project=${projectKey}&q=${encodeURIComponent(title)}`), 'tickets');
  return page.data.filter((ticket) => ticket.title === title);
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

test('Admins create email inboxes whose mail becomes tickets through the Cloudflare email Worker', async ({ page, request }, testInfo) => {
  test.setTimeout(180_000);
  for (const [name, value] of Object.entries({ fakeCF, cfToken, noRulesToken, hooksURL })) expect(value, `run.sh must provide ${name}`).toBeTruthy();
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  const projectKey = `EM${runID}`.toUpperCase().slice(0, 12);
  const localPart = `alerts-${runID}`;
  await resetPublicAccess(request);
  try {
    const project = await json<{ id: string; key: string }>(await request.post('/api/v1/projects', { data: { key: projectKey, name: `Email intake ${runID}` }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'project');

    // Agents cannot manage email intake.
    const agent = await json<{ id: string }>(await request.post('/api/v1/agents', { data: { name: `Email probe ${runID}`, project_ids: [project.id] }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } }), 'agent');
    const issued = await json<{ token: string }>(await request.post(`/api/v1/agents/${agent.id}/tokens`, { data: { name: 'probe', scopes: ['tasks:read', 'tasks:write'], project_ids: [project.id] }, headers: jsonHeaders }), 'token');
    const bearer = { Authorization: `Bearer ${issued.token}`, 'Content-Type': 'application/json' };
    expect((await request.get('/api/v1/email-intake', { headers: bearer })).status()).toBe(403);
    expect((await request.post('/api/v1/email-intake', { headers: bearer, data: { domain: 'example.test', local_part: 'x', api_token: cfToken } })).status()).toBe(403);
    expect((await request.delete('/api/v1/email-intake/x', { headers: bearer })).status()).toBe(403);
    expect((await request.get('/api/v1/email-inboxes', { headers: bearer })).status()).toBe(403);
    expect((await request.post('/api/v1/email-inboxes', { headers: bearer, data: { name: 'x', project: project.key } })).status()).toBe(403);
    expect((await request.delete('/api/v1/email-inboxes/x', { headers: bearer })).status()).toBe(403);
    expect((await request.post('/api/v1/email-inboxes/x/address', { headers: bearer })).status()).toBe(403);
    // Inboxes need email to be on.
    expect((await request.post('/api/v1/email-inboxes', { headers: jsonHeaders, data: { name: 'Too early', project: project.key } })).status()).toBe(409);

    // Without a Public URL the Worker has no way to reach Helm.
    let card = await openConnectApps(page, project.key);
    await expect(card.locator('[data-email-needs-public]')).toBeVisible();
    expect((await request.post('/api/v1/email-intake', { headers: jsonHeaders, data: { domain: 'example.test', local_part: localPart, enable_subaddressing: true, api_token: cfToken } })).status()).toBe(409);

    const publicHost = `mail-${runID}.example.test`;
    await json(await request.post('/api/v1/public-endpoints', { headers: jsonHeaders, data: { hostname: publicHost, api_token: cfToken } }), 'public URL');
    card = await openConnectApps(page, project.key);
    await openManual(card.locator('[data-manual-email]'));
    await expect(card.getByLabel('Email domain')).toHaveValue('example.test');
    await expect(card.locator('[data-email-preview]')).toHaveText('helm-alerts+<tag>@example.test');
    const before = await fakeState(request);
    const workersBefore = Object.keys(before.workers).length;
    const rulesBefore = Object.keys(before.rules).length;
    const nothingCreated = async () => {
      const state = await fakeState(request);
      expect(Object.keys(state.workers).length).toBe(workersBefore);
      expect(Object.keys(state.rules).length).toBe(rulesBefore);
      expect(state.email_routing['zone-1'].support_subaddress).toBe(false);
    };
    const submit = async (domain: string, name: string, token: string, fallback = '') => {
      await openManual(card.locator('[data-manual-email]'));
      await card.getByLabel('Email domain').fill(domain);
      await card.getByLabel('Email address name').fill(name);
      await card.getByLabel('Fallback email address').fill(fallback);
      await card.getByLabel('Cloudflare API token for email').fill(token);
      await card.getByRole('button', { name: 'Turn on email' }).click();
    };

    // A zone without Email Routing is refused before anything is created.
    await submit('plain.test', localPart, cfToken);
    await expect(card.getByRole('alert')).toContainText('Email Routing is not enabled for plain.test');
    await nothingCreated();

    // Plus addressing is never turned on without consent.
    await submit('example.test', localPart, cfToken);
    await expect(card.locator('[data-email-consent]')).toContainText('plus addressing is off for example.test');
    await expect(card.getByRole('button', { name: 'Turn on email' })).toBeDisabled();
    await nothingCreated();
    await card.locator('[data-email-consent]').getByRole('checkbox').check();

    // An address someone already routes, a token without rule access, an
    // unverified fallback and a failed rule all leave Cloudflare unchanged.
    await submit('example.test', 'taken', cfToken);
    await expect(card.getByRole('alert')).toContainText('Cloudflare already routes taken@example.test');
    await submit('example.test', localPart, noRulesToken);
    await expect(card.getByRole('alert')).toContainText('Authentication error');
    await submit('example.test', localPart, cfToken, 'unverified@example.org');
    await expect(card.getByRole('alert')).toContainText('unverified@example.org is not a verified Email Routing destination');
    await nothingCreated();
    await submit('example.test', `fail-rule-${runID}`, cfToken);
    await expect(card.getByRole('alert')).toContainText('Invalid rule');
    await nothingCreated();
    expect((await fakeState(request)).deleted_workers.some((name) => name.startsWith('helm-email-'))).toBe(true);

    // Setup deploys the Worker with its Helm URL only as a secret binding.
    await submit('example.test', localPart, cfToken, 'fallback@example.org');
    await expect(card.locator('[data-email-badge]')).toHaveText('On');
    await expect(card.locator('[data-email-base]')).toHaveText(`${localPart}+<inbox>@example.test`);
    const view = await emailView(request);
    const intake = view.active!;
    expect(intake).toMatchObject({ local_part: localPart, domain: 'example.test', subaddress_enabled_by_helm: true });
    const state = await fakeState(request);
    expect(state.email_routing['zone-1'].support_subaddress).toBe(true);
    const deployed = state.workers[intake.worker_name];
    const intakeBinding = deployed.metadata.bindings.find((binding) => binding.name === 'HELM_INTAKE_URL')!;
    expect(intakeBinding.type).toBe('secret_text');
    expect(intakeBinding.text).toMatch(new RegExp(`^https://${publicHost.replace(/\./g, '\\.')}/api/v1/hooks/tickets/email/em_[A-Za-z0-9_-]{43}$`));
    expect(deployed.metadata.bindings.find((binding) => binding.name === 'FALLBACK_TO')).toEqual({ type: 'plain_text', name: 'FALLBACK_TO', text: 'fallback@example.org' });
    expect(deployed.metadata.bindings.filter((binding) => binding.type !== 'secret_text').map((binding) => binding.text).join(' ')).not.toContain('em_');
    expect(state.rules[intake.rule_id]).toMatchObject({ matchers: [{ value: `${localPart}@example.test` }], actions: [{ type: 'worker', value: [intake.worker_name] }] });
    const intakeSecret = intakeBinding.text.split('/').pop()!;

    // The Public URL cannot be removed while email delivers through it.
    const publicID = (await json<{ active: { id: string } }>(await request.get('/api/v1/public-endpoints'), 'public')).active.id;
    const blocked = await request.delete(`/api/v1/public-endpoints/${publicID}`, { headers: jsonHeaders, data: { api_token: cfToken } });
    expect(blocked.status()).toBe(409);
    expect(await blocked.text()).toContain('email addresses deliver mail through this Public URL');

    // An inbox created in Helm shows its address right away, permanently.
    const inboxes = page.locator('[data-email-inboxes]');
    await inboxes.getByLabel('Inbox name').fill(`Backups ${runID}`);
    await inboxes.getByLabel('Inbox project').selectOption(project.key);
    await inboxes.getByRole('button', { name: 'Create inbox' }).click();
    const inboxRow = inboxes.locator(`[data-inbox="Backups ${runID}"]`);
    const address = (await inboxRow.locator('[data-inbox-address]').innerText()).trim();
    expect(address).toMatch(new RegExp(`^${localPart}\\+backups-[a-z0-9-]+-[a-z2-7]{6}@example\\.test$`));
    await expect(inboxes.getByRole('status')).toContainText(`Send email to ${address}`);
    await expect(inboxRow.locator('[data-inbox-mailto]')).toHaveAttribute('href', new RegExp(`^mailto:${address.replace('+', '\\+')}\\?subject=`));
    await expect(inboxRow.locator('[data-inbox-count]')).toHaveText('No email yet: send one to try it');
    await page.goto(`/tickets?connect=1&project=${project.key}`);
    await expect(page.locator(`[data-inbox="Backups ${runID}"] [data-inbox-address]`)).toHaveText(address);
    await testInfo.attach('email-inbox.png', { contentType: 'image/png', body: await page.locator('[data-email-inboxes]').screenshot() });

    const worker = await loadWorker(deployed.source);
    const env = { HELM_INTAKE_URL: intakeBinding.text.replace(`https://${publicHost}`, hooksURL.replace(/\/$/, '')), FALLBACK_TO: 'fallback@example.org' };
    const title = `Nightly backup failed ${runID}`;
    const first = mail([
      'From: Backup Bot <backup@nas.example>', `To: ${address}`, `Subject: ${title}`, `Message-ID: <first-${runID}@nas.example>`,
      'X-Priority: 1', 'Content-Type: text/plain; charset=utf-8', '', 'Volume 1 is degraded.', '', 'Replace disk 2.'
    ]);

    // Mail to the inbox opens a ticket in its project.
    expect(await deliver(worker, env, first, address)).toEqual({});
    const [ticket] = await ticketsTitled(request, project.key, title);
    expect(ticket).toMatchObject({ title, description: 'Volume 1 is degraded.\n\nReplace disk 2.', priority: 'high' });
    const detail = await json<Ticket>(await request.get(`/api/v1/tasks/${ticket.id}`), 'ticket');
    expect(detail.alert_source).toMatchObject({ alert_type: 'email', occurrence_count: 1, evidence: { from: 'Backup Bot <backup@nas.example>', message_id: `first-${runID}@nas.example`, authentication: 'not reported by Cloudflare' } });

    // Re-delivery of the same message changes nothing; a new message with
    // the same sender and subject repeats the open ticket.
    expect(await deliver(worker, env, first, address)).toEqual({});
    expect((await json<Ticket>(await request.get(`/api/v1/tasks/${ticket.id}`), 'ticket')).alert_source?.occurrence_count).toBe(1);
    const repeat = first.replace(`Subject: ${title}`, `Subject: Re: ${title}`).replace(`<first-${runID}@`, `<second-${runID}@`);
    expect(await deliver(worker, env, repeat, address)).toEqual({});
    expect((await json<Ticket>(await request.get(`/api/v1/tasks/${ticket.id}`), 'ticket')).alert_source?.occurrence_count).toBe(2);
    expect(await ticketsTitled(request, project.key, title)).toHaveLength(1);

    // Unknown addresses and unreadable mail bounce with Helm's reason.
    const stranger = `${localPart}+backups-aaaaaa@example.test`;
    expect(await deliver(worker, env, first.replace(`<first-${runID}@`, `<third-${runID}@`), stranger)).toEqual({ rejected: 'No Helm inbox uses this address' });
    expect(await deliver(worker, env, mail([`Subject: No sender ${runID}`, `Message-ID: <nofrom-${runID}@x>`, '', 'body']), address)).toEqual({ rejected: 'the message has no valid From address' });

    // Oversized mail goes to the fallback and is listed; Helm being down
    // falls back too, after the Worker's retries.
    const oversize = await deliver(worker, env, mail(['From: big@nas.example', `Subject: Huge report ${runID}`, `Message-ID: <big-${runID}@x>`, '', 'x']), address, 3 * 1024 * 1024);
    expect(oversize).toEqual({ forwarded: { to: 'fallback@example.org', headers: { 'x-helm-intake-failed': 'too-large' } } });
    const down = await deliver(worker, { ...env, HELM_INTAKE_URL: 'http://127.0.0.1:9/api/v1/hooks/tickets/email/x' }, first, address);
    expect(down).toEqual({ forwarded: { to: 'fallback@example.org', headers: { 'x-helm-intake-failed': 'Helm unreachable' } } });

    // Recent emails shows every outcome, including bounces.
    const recent = (await emailView(request)).recent;
    expect(recent.map((item) => item.outcome)).toEqual(expect.arrayContaining(['created', 'repeated', 'unknown_recipient', 'unreadable', 'too_large']));
    expect(recent.find((item) => item.outcome === 'too_large')?.reason).toContain('Helm accepts email up to 1 MiB');
    expect(recent.find((item) => item.outcome === 'unknown_recipient')?.sender).toBe('Backup Bot <backup@nas.example>');
    card = await openConnectApps(page, project.key);
    await expect(card.locator(`[data-email-receipt="${title}"] [data-email-outcome="created"]`)).toHaveText('Opened');
    await expect(card.locator(`[data-email-receipt="Re: ${title}"] [data-email-outcome="repeated"]`)).toHaveText('Repeat ×2');
    await expect(card.locator('[data-email-outcome="unknown_recipient"]').first()).toHaveText('Bounced: unknown address');
    await testInfo.attach('email-intake-active.png', { contentType: 'image/png', body: await card.screenshot() });

    // The inbox counts what it received.
    await page.goto(`/tickets?connect=1&project=${project.key}`);
    await expect(page.locator(`[data-inbox="Backups ${runID}"] [data-inbox-count]`)).toContainText('2 emails');

    // Replacing the address stops the old one; turning the inbox off stops
    // the new one. Tickets stay.
    page.once('dialog', (dialog) => dialog.accept());
    await page.locator(`[data-inbox="Backups ${runID}"] .row-menu > summary`).click();
    await page.locator(`[data-inbox="Backups ${runID}"]`).getByRole('button', { name: 'Replace address' }).click();
    await expect(page.locator('[data-email-inboxes]').getByRole('status')).toContainText('now receives at');
    const replaced = (await page.locator(`[data-inbox="Backups ${runID}"] [data-inbox-address]`).innerText()).trim();
    expect(replaced).not.toBe(address);
    const fresh = first.replace(`<first-${runID}@`, `<fourth-${runID}@`);
    expect(await deliver(worker, env, fresh, address)).toEqual({ rejected: 'No Helm inbox uses this address' });
    expect(await deliver(worker, env, fresh, replaced)).toEqual({});
    expect((await json<Ticket>(await request.get(`/api/v1/tasks/${ticket.id}`), 'ticket')).alert_source?.occurrence_count).toBe(3);
    page.once('dialog', (dialog) => dialog.accept());
    await page.locator(`[data-inbox="Backups ${runID}"] .row-menu > summary`).click();
    await page.locator(`[data-inbox="Backups ${runID}"]`).getByRole('button', { name: 'Turn off' }).click();
    await expect(page.locator(`[data-inbox="Backups ${runID}"]`)).toHaveCount(0);
    expect(await deliver(worker, env, first.replace(`<first-${runID}@`, `<fifth-${runID}@`), replaced)).toEqual({ rejected: 'No Helm inbox uses this address' });
    expect((await json<Ticket>(await request.get(`/api/v1/tasks/${ticket.id}`), 'ticket')).id).toBe(ticket.id);

    // The Worker's intake secret never leaks into responses, the database or
    // the log. (Inbox addresses are shown on purpose.)
    const visible = JSON.stringify([await emailView(request), await json(await request.get('/api/v1/email-inboxes'), 'inboxes')]);
    for (const secret of [intakeSecret]) {
      expect(visible).not.toContain(secret);
      const dbPath = process.env.HELM_E2E_DB as string;
      for (const file of [dbPath, `${dbPath}-wal`]) if (existsSync(file)) expect(readFileSync(file).includes(Buffer.from(secret))).toBe(false);
      expect(readFileSync(process.env.HELM_E2E_SERVER_LOG as string, 'utf8')).not.toContain(secret);
    }

    // Admin shows email is on.
    await page.goto('/admin');
    await expect(page.locator('[data-admin-email-access]')).toContainText(`${localPart}+…@example.test`);

    // Removal without a token leaves the Worker listed for cleanup; finishing
    // with a token deletes it and the rule. Plus addressing stays on.
    card = await openConnectApps(page, project.key);
    await card.getByRole('button', { name: 'Turn off email…' }).click();
    await card.getByRole('button', { name: 'Turn off email' }).click();
    await expect(card.getByRole('status')).toContainText(`Delete routing rule ${intake.rule_id} and Worker ${intake.worker_name}`);
    const history = card.locator(`[data-email-history="${intake.id}"]`);
    await expect(history).toContainText('Needs cleanup');
    expect((await fakeState(request)).workers[intake.worker_name]).toBeTruthy();
    expect(await deliver(worker, env, fresh.replace(`<fourth-${runID}@`, `<sixth-${runID}@`), replaced)).toEqual({ rejected: 'Helm is not accepting email at this address' });
    await history.getByRole('button', { name: 'Finish cleanup…' }).click();
    await history.getByLabel(`Cloudflare API token for ${localPart}@example.test`).fill(cfToken);
    await history.getByRole('button', { name: 'Delete in Cloudflare' }).click();
    await expect(card.getByRole('status')).toContainText(`Deleted the Worker and routing rule for ${localPart}@example.test`);
    await expect(history).toContainText('Removed');
    const cleaned = await fakeState(request);
    expect(cleaned.deleted_workers).toContain(intake.worker_name);
    expect(cleaned.deleted_rules).toContain(intake.rule_id);
    expect(cleaned.email_routing['zone-1'].support_subaddress).toBe(true);
    await testInfo.attach('email-intake-removed.png', { contentType: 'image/png', body: await card.screenshot() });
  } finally {
    await resetPublicAccess(request);
  }
});
