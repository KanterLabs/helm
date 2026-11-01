import { createHash, randomBytes } from 'node:crypto';
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { expect, type APIRequestContext, type Browser, type BrowserContext, type Page, type TestInfo } from '@playwright/test';

// Synthetic TC-113 local-auth fixture helpers. Passwords are derived from the
// private fixture database path so Playwright retries reuse the same owner,
// but they are never attached, logged or written to evidence. Every secret
// handed out here is registered for the artifact leak scan below.
export const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
export const origin = new URL(baseURL).origin;
const fixtureSeed = process.env.HELM_E2E_DB || randomBytes(16).toString('hex');
const secrets = new Set<string>();

export function syntheticPassword(label: string): string {
  const digest = createHash('sha256').update(`tc113:${fixtureSeed}:${label}`).digest('base64url').slice(0, 18);
  const value = `Tc113-${digest}-9a`;
  secrets.add(value);
  return value;
}

export function runId(): string {
  return `${Date.now().toString(36)}${randomBytes(3).toString('hex')}`;
}

export const owner = { name: 'Morgan Owner', email: 'owner.tc113@example.test', password: syntheticPassword('owner') };

export type MemberRecord = { id: string; name: string; email?: string; workspace_role?: string; project_ids?: string[]; disabled_at?: string; last_seen_at?: string | null };
export type SessionRecord = { id: string; actor_id: string; created_at: string; last_seen_at: string; expires_at: string; revoked_at?: string | null; ip_address?: string; user_agent?: string };

export const mutationHeaders = () => ({ Origin: origin, 'Content-Type': 'application/json' });

export async function authMode(request: APIRequestContext): Promise<{ mode?: string; setup_required?: boolean }> {
  const response = await request.get('/api/v1/auth/status');
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/** Complete first-run setup once per fixture; later tests and retries reuse it. */
export async function ensureOwner(request: APIRequestContext): Promise<void> {
  const status = await authMode(request);
  if (!status.setup_required) return;
  const response = await request.post('/api/v1/auth/setup', { headers: mutationHeaders(), data: owner });
  expect(response.status(), 'synthetic owner setup').toBe(201);
}

/** A fresh browser context authenticated through the real local login route. */
export async function signedInContext(browser: Browser, email: string, password: string, userAgent?: string): Promise<BrowserContext> {
  const context = await browser.newContext({ baseURL, ...(userAgent ? { userAgent } : {}) });
  const response = await context.request.post('/api/v1/auth/login', { headers: mutationHeaders(), data: { email, password } });
  expect(response.status(), `local login for ${email}`).toBe(200);
  await recordContextCredentials(context);
  return context;
}

/** Include cookies minted by every real login in the final evidence gate. */
export async function recordContextCredentials(context: BrowserContext): Promise<void> {
  for (const cookie of await context.cookies()) secrets.add(cookie.value);
}

export async function loginThroughUi(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await Promise.all([
    page.waitForResponse((response) => new URL(response.url()).pathname === '/api/v1/auth/login'),
    page.getByRole('button', { name: 'Sign in', exact: true }).click()
  ]);
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
  await recordContextCredentials(page.context());
}

export async function createProject(context: BrowserContext, label: string): Promise<{ id: string; name: string; key: string }> {
  const suffix = runId().toUpperCase();
  const response = await context.request.post('/api/v1/projects', {
    headers: mutationHeaders(),
    data: { key: `M${label}${suffix}`.replace(/[^A-Z0-9]/g, '').slice(0, 12), name: `${label} ${suffix}`, description: 'Synthetic TC-113 membership project.' }
  });
  expect(response.status(), 'owner creates project').toBe(201);
  return response.json();
}

export async function createMemberViaApi(context: BrowserContext, input: { email: string; name: string; password: string; project_ids: string[] }): Promise<MemberRecord> {
  const response = await context.request.post('/api/v1/members', { headers: mutationHeaders(), data: { ...input, role: 'member' } });
  expect(response.status(), 'owner creates member').toBe(201);
  return response.json();
}

export async function visibleProjectIds(context: BrowserContext): Promise<string[]> {
  const response = await context.request.get('/api/v1/projects?limit=200');
  expect(response.status()).toBe(200);
  const body = await response.json() as { data: Array<{ id: string }> };
  return body.data.map((project) => project.id).sort();
}

/** Whether a stored browser cookie still authenticates a real request. */
export async function cookieWorks(context: BrowserContext): Promise<boolean> {
  const response = await context.request.get('/api/v1/auth/status');
  const body = await response.json() as { authenticated?: boolean };
  return Boolean(body.authenticated);
}

export async function listSessions(context: BrowserContext, actorId?: string): Promise<SessionRecord[]> {
  const query = actorId ? `?actor_id=${encodeURIComponent(actorId)}` : '';
  const response = await context.request.get(`/api/v1/auth/sessions${query}`);
  expect(response.status()).toBe(200);
  return (await response.json() as { data: SessionRecord[] }).data;
}

/** Response shapes only: synthetic names/emails, statuses and session metadata. */
export async function attachCapture(info: TestInfo, name: string, payload: unknown): Promise<void> {
  const body = JSON.stringify(payload, null, 2);
  for (const secret of secrets) expect(body.includes(secret), `${name} must not contain credentials`).toBe(false);
  await info.attach(name, { body: Buffer.from(body), contentType: 'application/json' });
}

export async function attachScreenshot(info: TestInfo, page: Page, name: string): Promise<void> {
  await info.attach(name, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
}

function files(root: string): string[] {
  try {
    return readdirSync(root).flatMap((name) => {
      const full = path.join(root, name);
      const stat = statSync(full);
      return stat.isDirectory() ? files(full) : stat.size < 25_000_000 ? [full] : [];
    });
  } catch {
    return [];
  }
}

/** Fail if any synthetic password or observed cookie value reached evidence. */
export function assertNoSecretsInEvidence(info: TestInfo): void {
  // Reporters and database copies finish later. A final coordinator gate uses
  // these one-way fingerprints from private fixture state, never raw secrets.
  const database = process.env.HELM_E2E_DB;
  if (database) {
    const target = path.join(path.dirname(database), 'membership-credential-fingerprints.json');
    const previous = existsSync(target) ? JSON.parse(readFileSync(target, 'utf8')) as Array<{ length: number; sha256: string }> : [];
    const records = new Map(previous.map((record) => [record.sha256, record]));
    for (const secret of secrets) {
      const sha256 = createHash('sha256').update(secret).digest('hex');
      records.set(sha256, { length: Buffer.byteLength(secret), sha256 });
    }
    writeFileSync(target, JSON.stringify([...records.values()]), { mode: 0o600 });
  }
  const roots = [info.outputDir, process.env.HELM_E2E_ARTIFACT_DIR].filter(Boolean) as string[];
  // Includes copied SQLite files: sessions store only token hashes and
  // accounts only bcrypt hashes, so a plaintext match is a real leak.
  const leaked = roots.flatMap(files).filter((file) => {
    const text = readFileSync(file).toString('latin1');
    return [...secrets].some((secret) => text.includes(secret));
  });
  expect(leaked, 'evidence files containing synthetic credentials').toEqual([]);
}

// Shared Settings locators for the membership journeys.
export const isPath = (url: string, path: string | RegExp) => typeof path === 'string' ? new URL(url).pathname === path : path.test(new URL(url).pathname);
export const membersRegion = (page: Page) => page.getByRole('region', { name: 'Members', exact: true });
export const memberRow = (page: Page, email: string) => membersRegion(page).getByRole('listitem').filter({ hasText: email });

export async function openEditor(page: Page, name: string, email: string) {
  await memberRow(page, email).getByRole('button', { name: `Manage ${name}` }).click();
  const editor = page.getByRole('region', { name: `Edit ${name}` });
  await expect(editor).toBeVisible();
  return editor;
}

export async function confirm(page: Page, label: string) {
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: label, exact: true }).click();
}
