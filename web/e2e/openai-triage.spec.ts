import { expect, test, type APIResponse } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { AddressInfo } from 'node:net';

// Failure cases before implementation: key echoed or used by another actor;
// failed rotation removes the valid key; restart loses the connection; opening
// a card calls OpenAI; stale, inaccessible or malformed requests send task
// data; provider failures stop manual editing; a suggestion writes a task
// before Apply or replaces an edit made after it was generated.
test.use({ trace: 'off', video: 'off', screenshot: 'off', actionTimeout: 10_000, navigationTimeout: 15_000 });

const firstKey = 'synthetic-decisions-owner-key-A';
const secondKey = 'synthetic-decisions-owner-key-B';
const invalidKey = 'synthetic-decisions-rejected-key';
const connectPath = '/api/v1/openai/connection';

type Packet = {
  model: string;
  input: Array<{ content: Array<{ type: string; data: { title: string; description: string } }> }>;
  questions: Array<{ type: string; name: string; choices: Array<{ value: string }> }>;
};

async function listen(server: Server): Promise<string> {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
}

async function stop(child: ChildProcess | undefined) {
  if (!child?.pid || child.exitCode !== null || child.signalCode !== null) return;
  const finished = once(child, 'exit');
  child.kill();
  await finished;
}

async function status(response: APIResponse, expected = 200) {
  expect(response.status()).toBe(expected);
  return response.json();
}

test('connect, review and apply triage through the real Helm server', async ({ browser }, info) => {
  const binary = process.env.HELM_E2E_FIXTURE_BINARY;
  test.skip(!binary, 'requires the browser coordinator’s Helm executable');
  test.setTimeout(120_000);
  const dir = await mkdtemp(join(tmpdir(), 'helm-decisions-e2e-'));
  const requests: Array<{ key: string; body: Packet }> = [];
  let behavior: 'normal' | 'uncertain' | 'malformed' | 'timeout' = 'normal';
  const provider = createServer(async (req, res) => {
    let raw = '';
    for await (const chunk of req) raw += chunk;
    if (req.method !== 'POST' || req.url !== '/v1/decisions') {
      res.writeHead(404).end();
      return;
    }
    const key = req.headers.authorization?.replace(/^Bearer /, '') || '';
    const body = JSON.parse(raw) as Packet;
    requests.push({ key, body });
    if (![firstKey, secondKey].includes(key)) {
      res.writeHead(401).end(JSON.stringify({ error: { message: key } }));
      return;
    }
    if (behavior === 'timeout') return;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({
      answers: body.questions.map(({ name, choices }) => ({
        name, type: 'choice',
        choice: name === 'connection' ? choices[0].value : name === 'readiness' ? 'needs_details' :
          behavior === 'malformed' ? 'unexpected' : behavior === 'uncertain' ? 'insufficient_context' : 'high'
      }))
    }));
  });
  const providerOrigin = await listen(provider);
  const reservation = createServer();
  const origin = await listen(reservation);
  await new Promise<void>((done) => reservation.close(() => done()));
  const mutation = { Origin: origin };
  const version = (v: number) => ({ ...mutation, 'If-Match': `"v${v}"` });
  let child: ChildProcess | undefined;
  const contexts: Array<Awaited<ReturnType<typeof browser.newContext>>> = [];
  async function start() {
    const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
      !/^(HELM_|ROADMAP_|OPENAI_|CODEX_)/.test(key)));
    child = spawn(binary!, [], {
      cwd: dir,
      stdio: 'ignore',
      env: {
        ...env, HELM_ADDR: new URL(origin).host, HELM_DB: join(dir, 'db.sqlite'),
        HELM_AUTH_MODE: 'local', HELM_PUBLIC_ORIGIN: origin, HELM_SECURE_COOKIES: 'false',
        HELM_DEMO_SEED: 'false', HELM_OPENAI_DECISIONS_TEST_ORIGIN: providerOrigin
      }
    });
    await expect.poll(async () => fetch(`${origin}/readyz`).then((r) => r.ok).catch(() => false)).toBe(true);
  }
  async function context() {
    const value = await browser.newContext({ baseURL: origin, viewport: { width: 390, height: 844 } });
    contexts.push(value);
    return value;
  }

  try {
    await start();
    const owner = await context();
    const account = { name: 'Synthetic Owner', email: 'owner@example.test', password: 'Synthetic-owner-pass-42!' };
    await status(await owner.request.post('/api/v1/auth/setup', { headers: mutation, data: account }), 201);
    await status(await owner.request.post('/api/v1/auth/login', {
      headers: mutation, data: { email: account.email, password: account.password }
    }));
    const project = await status(await owner.request.post('/api/v1/projects', {
      headers: mutation, data: { key: 'TRIAGE', name: 'Synthetic Inbox' }
    }), 201);
    const task = await status(await owner.request.post(`/api/v1/projects/${project.id}/tasks`, {
      headers: mutation, data: { title: 'Export omits attachments', description: 'Synthetic symptom in Firefox.' }
    }), 201);
    const taskPath = `/api/v1/tasks/${task.id}`;
    const suggestionPath = `${taskPath}/triage-suggestion`;
    const taskPage = `/p/${project.slug}/tasks/${task.key}`;
    const page = await owner.newPage();
    await page.goto(taskPage);
    const panel = page.getByRole('region', { name: 'Triage suggestion' });
    const suggest = panel.getByRole('button', { name: 'Suggest triage', exact: true });
    await expect(panel.getByRole('link', { name: 'Connect an OpenAI key' })).toBeVisible();
    expect(requests).toHaveLength(0);
    expect((await owner.request.post(suggestionPath, { headers: version(task.version) })).status()).toBe(409);

    await panel.getByRole('link', { name: 'Connect an OpenAI key' }).click();
    const settings = page.getByRole('region', { name: 'OpenAI' });
    const keyField = settings.getByLabel('OpenAI API key', { exact: true });
    const connected = settings.getByText('Connected', { exact: true });
    await expect(keyField).toBeVisible();
    await keyField.fill(firstKey);
    await settings.getByRole('button', { name: 'Save key' }).click();
    await expect(connected).toBeVisible();
    expect(requests).toHaveLength(1);
    expect(JSON.stringify(requests[0].body)).not.toContain(task.title);
    expect(requests[0].body.model).toBe('gpt-6-luna');
    expect(await status(await owner.request.get(connectPath))).toEqual({ connected: true });

    await settings.getByRole('button', { name: 'Replace key' }).click();
    await keyField.fill(invalidKey);
    await settings.getByRole('button', { name: 'Save key' }).click();
    await expect(settings.getByRole('alert')).toContainText('OpenAI rejected the API key');
    await expect(connected).toBeVisible();
    expect(await settings.innerText()).not.toContain(invalidKey);

    const member = await context();
    const other = { name: 'Synthetic Member', email: 'member@example.test', password: 'Synthetic-member-pass-42!' };
    await status(await owner.request.post('/api/v1/members', {
      headers: mutation, data: { ...other, role: 'member', project_ids: [] }
    }), 201);
    await status(await member.request.post('/api/v1/auth/login', {
      headers: mutation, data: { email: other.email, password: other.password }
    }));
    expect(await status(await member.request.get(connectPath))).toEqual({ connected: false });
    const denied = await member.request.post(suggestionPath, { headers: version(task.version) });
    expect([403, 404]).toContain(denied.status());
    expect(requests).toHaveLength(2);

    await stop(child);
    await start();
    await page.goto(taskPage);
    await expect(suggest).toBeEnabled();
    expect(requests).toHaveLength(2);
    await suggest.click();
    await expect(panel.getByText('Needs details', { exact: true })).toBeVisible();
    await expect(panel.getByLabel('Suggested priority')).toHaveValue('high');
    expect(requests.at(-1)?.key === firstKey).toBe(true);
    expect(requests.at(-1)?.body.input[0].content[0].data).toEqual({
      title: task.title, description: 'Synthetic symptom in Firefox.'
    });
    expect((await status(await owner.request.get(taskPath))).version).toBe(task.version);
    await panel.getByRole('button', { name: 'Apply priority' }).evaluate((button) => button.scrollIntoView({ block: 'center' }));
    await info.attach('triage-review.png', { body: await panel.screenshot(), contentType: 'image/png' });
    await panel.getByLabel('Suggested priority').selectOption('low');
    await panel.getByRole('button', { name: 'Apply priority' }).click();
    await expect.poll(async () => (await status(await owner.request.get(taskPath))).priority).toBe('low');

    const latest = await status(await owner.request.get(taskPath));
    await suggest.click();
    const apply = panel.getByRole('button', { name: 'Apply priority' });
    await expect(apply).toBeEnabled();
    await page.route(`**${taskPath}`, async (route) => {
      if (route.request().method() === 'PATCH') {
        await status(await owner.request.patch(taskPath, {
          headers: version(latest.version), data: { priority: 'urgent' }
        }));
      }
      await route.continue();
    });
    await apply.click();
    await expect(panel.getByRole('alert')).toBeVisible();
    await page.unroute(`**${taskPath}`);
    expect((await status(await owner.request.get(taskPath))).priority).toBe('urgent');

    await page.reload();
    behavior = 'uncertain';
    await suggest.click();
    await expect(panel.getByText('Insufficient context', { exact: true })).toBeVisible();
    await expect(apply).toHaveCount(0);
    for (const failure of ['malformed', 'timeout'] as const) {
      behavior = failure;
      await suggest.click();
      await expect(panel.getByRole('alert')).toBeVisible({ timeout: 15_000 });
      await expect(apply).toHaveCount(0);
    }
    behavior = 'normal';
    await page.goto('/settings');
    await settings.getByRole('button', { name: 'Replace key' }).click();
    await keyField.fill(secondKey);
    await settings.getByRole('button', { name: 'Save key' }).click();
    await expect(keyField).toHaveCount(0);
    const encryptedDir = join(dir, 'db.sqlite.openai-decisions');
    for (const entry of await readdir(encryptedDir)) {
      const bytes = await readFile(join(encryptedDir, entry));
      expect(bytes.includes(firstKey) || bytes.includes(secondKey)).toBe(false);
    }
    await settings.getByRole('button', { name: 'Remove key' }).click();
    await expect(keyField).toBeVisible();
    expect(await status(await owner.request.get(connectPath))).toEqual({ connected: false });
    const finalTask = await status(await owner.request.get(taskPath));
    expect(finalTask.priority).toBe('urgent');
    await info.attach('triage-receipt.json', {
      body: JSON.stringify({ connectedAcrossRestart: true, foreignProject: denied.status(),
        staleApplyPreserved: true, activeCredentialRemoved: true, taskPriority: finalTask.priority }),
      contentType: 'application/json'
    });
  } finally {
    await Promise.allSettled(contexts.map((context) => context.close()));
    await stop(child);
    provider.closeAllConnections();
    await new Promise<void>((done) => provider.close(() => done()));
    await rm(dir, { recursive: true, force: true });
  }
});
