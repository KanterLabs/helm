/**
 * Shared real-server fixtures for the agent-token E2E specs.
 *
 * Failure scenarios these specs are written to catch (recorded before the
 * feature implementation, per AGENTS.md):
 *
 * 1. Inventory hides credentials: filters or the compact layout drop an
 *    agent's other tokens, expired tokens vanish, or an alert source's legacy
 *    credential is hidden while it can still authenticate.
 * 2. Status lies: an expired, missing or invalid expiry renders as Active, a
 *    token never used renders a recent use, or a disabled agent looks usable.
 * 3. Presets lie: labels are derived by subset instead of exact set equality,
 *    or Custom silently adds read scopes to write grants.
 * 4. Project access widens: an empty selection becomes workspace-wide, a
 *    replacement keeps a project the agent no longer has, or a forged
 *    outside-ceiling request yields a token.
 * 5. Issuance repeats: a double click sends two POSTs, or a lost response is
 *    retried automatically and creates an unseen second credential.
 * 6. Issuance is misreported: a successful POST whose list refresh fails is
 *    shown as a failed creation and the one-time plaintext is lost.
 * 7. Plaintext leaks: it remains in the DOM after close/logout, reaches
 *    browser storage, caches, URLs, setup snippets, screenshots, traces or
 *    attached metadata.
 * 8. The reveal can be lost by accident: Escape, backdrop, navigation or
 *    a session change silently discards it, or focus is not restored.
 * 9. Revocation/replacement is unsafe: a revoked bearer still works, copy
 *    success revokes the old token, or replacement retires the old token
 *    without its own confirmation.
 *
 * Plaintext handling: issued values stay in test memory only. Traces, videos
 * and automatic failure screenshots are disabled in the specs that render a
 * secret; every retained screenshot masks the reveal field, and every
 * attached response passes through `redact` and `assertNoSecret`.
 */
import { expect, type APIRequestContext, type APIResponse, type Page, type TestInfo } from '@playwright/test';
import { DatabaseSync } from 'node:sqlite';
import { createHash, randomBytes } from 'node:crypto';

export type Project = { id: string; key: string; name: string; slug: string };
export type Agent = { id: string; name: string; project_ids?: string[]; intake_source?: boolean };
export type TokenMeta = {
  id: string;
  name: string;
  scopes: string[];
  project_ids: string[];
  expires_at?: string | null;
  created_at: string;
  last_used_at?: string | null;
};
export type IssuedToken = TokenMeta & { token: string };
type Collection<T> = { data: T[]; next_cursor?: string | null };

export const ALL_SCOPES = [
  'projects:read',
  'projects:write',
  'tasks:read',
  'tasks:write',
  'tasks:claim',
  'events:read',
  'notifications:read',
  'notifications:write'
] as const;
export const TASK_WORKER = ['projects:read', 'tasks:read', 'tasks:write', 'tasks:claim', 'events:read'];
export const READ_ONLY = ['projects:read', 'tasks:read'];
export const MAINTAINER = [...TASK_WORKER, 'projects:write'];

export const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
export const origin = new URL(baseURL).origin;
const DAY = 24 * 60 * 60 * 1000;

export function runId(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
}

let mutation = 0;
function writeHeaders(): Record<string, string> {
  return { Origin: origin, 'Content-Type': 'application/json', 'Idempotency-Key': `agent-token-e2e-${Date.now()}-${++mutation}` };
}

export async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

export async function requireDisabledAuth(request: APIRequestContext): Promise<void> {
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');
}

export async function createProject(request: APIRequestContext, key: string, name: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', { data: { key, name }, headers: writeHeaders() }), `create project ${key}`);
}

export async function createAgent(request: APIRequestContext, name: string, projectIds?: string[]): Promise<Agent> {
  const data: Record<string, unknown> = { name, description: `Synthetic agent for token UX ${name}` };
  if (projectIds) data.project_ids = projectIds;
  return json<Agent>(await request.post('/api/v1/agents', { data, headers: writeHeaders() }), `create agent ${name}`);
}

/** Issuance never sends an Idempotency-Key: the server rejects replay of a one-time secret. */
export async function issueToken(
  request: APIRequestContext,
  agentId: string,
  input: { name: string; scopes: string[]; project_ids?: string[]; expires_at?: string | null }
): Promise<IssuedToken> {
  return json<IssuedToken>(await request.post(`/api/v1/agents/${agentId}/tokens`, {
    data: input,
    headers: { Origin: origin, 'Content-Type': 'application/json' }
  }), `issue token ${input.name}`);
}

export async function listAgents(request: APIRequestContext): Promise<Array<Agent & { tokens?: TokenMeta[] }>> {
  const page = await json<Collection<Agent & { tokens?: TokenMeta[] }>>(await request.get('/api/v1/agents?limit=200&disabled=true'), 'list agents');
  return page.data;
}

export function daysFromNow(days: number): string {
  return new Date(Date.now() + days * DAY).toISOString();
}

/** A bearer request made the same way an installed agent would. */
export async function bearer(request: APIRequestContext, token: string, method: 'GET' | 'POST' | 'PATCH', path: string, data?: unknown, headers: Record<string, string> = {}): Promise<number> {
  const response = await request.fetch(path, {
    method,
    data,
    headers: { Authorization: `Bearer ${token}`, ...(data === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers }
  });
  return response.status();
}

export async function backlogColumn(request: APIRequestContext, projectId: string): Promise<string> {
  const page = await json<Collection<{ id: string; semantic_state: string }>>(await request.get(`/api/v1/projects/${projectId}/columns?limit=50`), 'columns');
  const column = page.data.find((item) => item.semantic_state === 'backlog');
  expect(column, 'project has a Backlog column').toBeTruthy();
  return column!.id;
}

function database(): DatabaseSync {
  const dbPath = process.env.HELM_E2E_DB;
  if (!dbPath) throw new Error('HELM_E2E_DB is required for direct fixture rows');
  return new DatabaseSync(dbPath);
}

export const hasDatabase = Boolean(process.env.HELM_E2E_DB);

/**
 * Inserts a token row the API can no longer issue (past expiry, alert-source
 * actor). The hash matches the server's SHA-256 lookup so the credential is
 * genuinely usable or genuinely expired.
 */
export function insertLegacyToken(actorId: string, name: string, input: { scopes: string[]; projectIds?: string[]; expiresAt?: string | null; createdAt?: string }): { id: string; token: string } {
  const token = `helm_e2e_${randomBytes(24).toString('hex')}`;
  const id = `tok-e2e-${randomBytes(6).toString('hex')}`;
  const db = database();
  try {
    db.prepare('INSERT INTO tokens(id, actor_id, name, token_hash, scopes, project_ids, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)')
      .run(id, actorId, name, createHash('sha256').update(token).digest('hex'), JSON.stringify(input.scopes), JSON.stringify(input.projectIds || []), input.expiresAt ?? null, input.createdAt || new Date().toISOString());
  } finally {
    db.close();
  }
  return { id, token };
}

export function setActorDisabled(actorId: string): void {
  const db = database();
  try {
    db.prepare('UPDATE actors SET disabled_at = ? WHERE id = ?').run(new Date().toISOString(), actorId);
  } finally {
    db.close();
  }
}

/** Replace every issued plaintext with a fixed marker before attaching data. */
export function redact(value: unknown, secrets: readonly string[]): unknown {
  let text = JSON.stringify(value, (key, item) => (key === 'token' || key === 'plaintext' || key.toLowerCase() === 'authorization') && typeof item === 'string' ? '[redacted]' : item);
  for (const secret of secrets) if (secret) text = text.split(secret).join('[redacted]');
  return JSON.parse(text);
}

export function assertNoSecret(serialized: string, secrets: readonly string[]): void {
  for (const secret of secrets) if (secret) expect(serialized.includes(secret), 'artifact contains a token plaintext').toBe(false);
}

/** Attach sanitized captured metadata, repeatable across runs (no secrets, no bearer headers). */
export async function attachSanitized(testInfo: TestInfo, name: string, value: unknown, secrets: readonly string[]): Promise<void> {
  const body = JSON.stringify(redact(value, secrets), null, 2);
  assertNoSecret(body, secrets);
  await testInfo.attach(name, { contentType: 'application/json', body });
}

/** Screenshot with every place a plaintext can render masked. */
export async function attachMaskedScreenshot(page: Page, testInfo: TestInfo, name: string): Promise<void> {
  const body = await page.screenshot({
    fullPage: false,
    mask: [page.locator('[data-token-secret]'), page.locator('[data-token-reveal-value]')],
    maskColor: '#7c3aed'
  });
  await testInfo.attach(name, { contentType: 'image/png', body });
}

/** Plaintext must be absent from the DOM, URL and every browser store after close. */
export async function expectPlaintextGone(page: Page, secret: string): Promise<void> {
  expect(secret.length).toBeGreaterThan(16);
  const found = await page.evaluate(async (value) => {
    const places: string[] = [];
    if (document.documentElement.outerHTML.includes(value)) places.push('dom');
    for (const input of Array.from(document.querySelectorAll('input, textarea'))) {
      if ((input as HTMLInputElement).value.includes(value)) places.push('form value');
    }
    if (location.href.includes(value)) places.push('url');
    if (history.state && JSON.stringify(history.state).includes(value)) places.push('history');
    for (const store of [localStorage, sessionStorage]) {
      for (let index = 0; index < store.length; index++) {
        const key = store.key(index) || '';
        if (key.includes(value) || (store.getItem(key) || '').includes(value)) places.push('web storage');
      }
    }
    if ('caches' in self) {
      for (const name of await caches.keys()) {
        const cache = await caches.open(name);
        for (const request of await cache.keys()) {
          if (request.url.includes(value)) places.push('cache key');
          const response = await cache.match(request);
          if (response && (await response.clone().text()).includes(value)) places.push('cache body');
        }
      }
    }
    if (indexedDB.databases) {
      for (const info of await indexedDB.databases()) {
        if (!info.name) continue;
        const contents = await new Promise<string>((resolve) => {
          const open = indexedDB.open(info.name!);
          open.onerror = () => resolve('');
          open.onsuccess = () => {
            const db = open.result;
            const names = Array.from(db.objectStoreNames);
            if (!names.length) { db.close(); resolve(''); return; }
            const tx = db.transaction(names, 'readonly');
            const values: unknown[] = [];
            names.forEach((storeName) => {
              const cursor = tx.objectStore(storeName).openCursor();
              cursor.onsuccess = () => { if (cursor.result) { values.push(cursor.result.value); cursor.result.continue(); } };
            });
            tx.oncomplete = () => { db.close(); resolve(JSON.stringify(values)); };
            tx.onerror = () => { db.close(); resolve(''); };
          };
        });
        if (contents.includes(value)) places.push(`indexeddb:${info.name}`);
      }
    }
    return places;
  }, secret);
  expect(found, 'plaintext persisted after the reveal closed').toEqual([]);
}

export async function openAgentSettings(page: Page): Promise<void> {
  // Use real in-app navigation when a workspace document is already open.
  // A full page.goto would test unload/reload rather than the SPA back guard.
  if (new URL(page.url()).origin === origin && new URL(page.url()).pathname !== '/settings') {
    await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  } else await page.goto('/settings');
  await expect(page.getByRole('heading', { name: 'Agents & tokens', exact: true })).toBeVisible();
}

export function agentGroup(page: Page, agentId: string) {
  return page.locator(`[data-agent-group="${agentId}"]`);
}

export function tokenRow(page: Page, tokenId: string) {
  return page.locator(`[data-token-row="${tokenId}"]`);
}

/** Read the one-time value from the reveal without logging it. */
export async function revealedValue(page: Page): Promise<string> {
  const field = page.getByRole('dialog', { name: 'Save your new token' }).getByRole('textbox', { name: 'Token', exact: true });
  await expect(field).toHaveAttribute('readonly', '');
  return field.inputValue();
}

export async function expectNoHorizontalOverflow(page: Page): Promise<void> {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow, 'page scrolls horizontally').toBeLessThanOrEqual(0);
}

export async function setTheme(page: Page, theme: 'light' | 'dark'): Promise<void> {
  await page.evaluate((value) => localStorage.setItem('helm.theme', value), theme);
}

/** Ceiling changes have no public API; the replacement spec narrows one directly. */
export function removeActorProject(actorId: string, projectId: string): void {
  const db = database();
  try {
    db.prepare('DELETE FROM actor_projects WHERE actor_id = ? AND project_id = ?').run(actorId, projectId);
  } finally {
    db.close();
  }
}

/** Creates a webhook alert source and returns its server-derived intake actor. */
export async function createAlertSource(request: APIRequestContext, name: string, projectKey: string): Promise<{ actorId: string; taskId: string }> {
  const created = await json<{ webhook: { id: string }; url: string }>(await request.post('/api/v1/ticket-webhooks', {
    data: { name, project: projectKey },
    // The response carries the webhook secret, so it cannot be idempotent.
    headers: { Origin: origin, 'Content-Type': 'application/json' }
  }), 'create ticket webhook');
  const delivered = await request.post(new URL(created.url).pathname, {
    data: { title: `Synthetic alert ${name}`, dedupe_key: `${name}:token-ux` },
    headers: { 'Content-Type': 'application/json' }
  });
  expect(delivered.status(), 'webhook delivery').toBe(201);
  const result = await delivered.json() as { ticket: { id: string } };
  // Server-derived provenance: intakeActorID("webhook-" + id).
  const actorId = `actor-webhook-${created.webhook.id}-intake`;
  const agents = await listAgents(request);
  expect(agents.some((agent) => agent.id === actorId), 'intake actor is listed with agents').toBe(true);
  return { actorId, taskId: result.ticket.id };
}
