import { existsSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

// Drives test/e2e/fake-gitea (a synthetic Gitea 28.3.0) and records what the
// real-backend spec needs to prove: entries, Helm API responses, console
// output and leak scans. Failure list: test/e2e/GITEA_FAILURES.md.

export const forgeURL = process.env.HELM_E2E_FAKE_GITEA_URL || '';
/** Per-run random prefix of every secret the fake issues; never in source. */
export const sentinel = process.env.HELM_E2E_FAKE_GITEA_SENTINEL || '';
const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
export const helmOrigin = new URL(baseURL).origin;
export const jsonHeaders = { Origin: helmOrigin, 'Content-Type': 'application/json' };

export type ForgeCounters = Record<
  'applications_created' | 'authorize_requests' | 'authorize_rejected' | 'consent_shown' | 'approvals' | 'denials' | 'exchanges'
  | 'exchange_failures' | 'refreshes' | 'refresh_failures' | 'refresh_reuse_refused' | 'api_calls' | 'api_unauthorized'
  | 'user_calls' | 'limit_checks' | 'repo_pages' | 'access_expired', number>;
export type ForgeState = {
  version: string;
  flags: { token_lifetime: number; one_time_refresh: boolean; full_access: boolean };
  counters: ForgeCounters;
  apps: { name: string; client_id: string; confidential: boolean; redirect_uris: string[] }[];
  grants: { client_id: string; scope: string; revoked: boolean }[];
  last_authorize: null | { client_id: string; redirect_uri: string; response_type: string; scope: string; code_challenge_method: string; has_state: boolean; has_code_challenge: boolean };
  last_exchange_error: string;
  last_refresh_error: string;
};
export type Control = Partial<{ reset: true; token_lifetime: number; expire_access: true; one_time_refresh: boolean; revoke: true; full_access: boolean; confidential: boolean }>;

export async function forge(request: APIRequestContext): Promise<ForgeState> {
  const response = await request.get(`${forgeURL}/__state`);
  expect(response.ok(), 'fake forge state').toBeTruthy();
  return await response.json() as ForgeState;
}

export async function control(request: APIRequestContext, change: Control): Promise<ForgeState> {
  const response = await request.post(`${forgeURL}/__control`, { data: change });
  expect(response.ok(), `fake forge control ${JSON.stringify(change)}`).toBeTruthy();
  return await response.json() as ForgeState;
}

export type HelmStatus = {
  available: boolean; state: string; access: string; repository_count: number; client_id?: string;
  server?: { base_url: string; product: string; version: string; insecure_http: boolean };
  account?: { login: string; id: number }; check: { state: string; code: string };
};

export async function helmStatus(request: APIRequestContext): Promise<HelmStatus> {
  const response = await request.get('/api/v1/integrations/gitea');
  expect(response.ok(), 'Helm gitea status').toBeTruthy();
  return await response.json() as HelmStatus;
}

/** Back to nothing: Helm disconnected, the forge with no applications or grants. */
export async function resetAll(request: APIRequestContext): Promise<void> {
  const response = await request.post('/api/v1/integrations/gitea/disconnect', { headers: jsonHeaders, data: {} });
  expect(response.ok(), 'disconnect Helm from the forge').toBeTruthy();
  await control(request, { reset: true });
}

/** The connection file: beside the database by default (HELM_FORGE_STATE_DIR unset). */
export const stateDir = () => path.join(path.dirname(process.env.HELM_E2E_DB as string), 'forge');
export const stateFile = () => path.join(stateDir(), 'gitea.json');

/**
 * Proves the file holds the issued tokens and is private, comparing booleans
 * only so an assertion message can never print a token.
 */
export function expectPrivateStateFile(): void {
  const dir = statSync(stateDir());
  const file = statSync(stateFile());
  expect(dir.isDirectory() && (dir.mode & 0o777).toString(8), 'forge state directory mode').toBe('700');
  expect(file.isFile() && (file.mode & 0o777).toString(8), 'gitea.json mode').toBe('600');
  const stored = JSON.parse(readFileSync(stateFile(), 'utf8')) as { access_token?: string; refresh_token?: string };
  expect(Boolean(stored.access_token?.startsWith(`${sentinel}-access-`)), 'gitea.json holds the forge access token').toBe(true);
  expect(Boolean(stored.refresh_token?.startsWith(`${sentinel}-refresh-`)), 'gitea.json holds the forge refresh token').toBe(true);
}

/** Helm API responses and browser console text seen by every page in this file. */
export const captured: { responses: { url: string; status: number; headers: Record<string, string>; body: string }[]; console: string[] } = { responses: [], console: [] };

export function watch(page: Page): void {
  page.on('console', (message) => captured.console.push(`${message.type()}: ${message.text()}`));
  page.on('pageerror', (error) => captured.console.push(`pageerror: ${error.message}`));
  page.on('response', async (response) => {
    const url = new URL(response.url());
    if (url.origin !== helmOrigin || !url.pathname.startsWith('/api/v1/integrations/gitea')) return;
    // The callback URL carries the one-time code by design; keep only the path.
    const entry = { url: url.pathname, status: response.status(), headers: response.headers(), body: '' };
    captured.responses.push(entry);
    if (response.status() < 300 || response.status() >= 400) entry.body = await response.text().catch(() => '');
  });
}

/** Names of the sentinel kinds found in text; empty when clean. */
export function leaks(text: string | Buffer, kinds = ['code', 'access', 'refresh', 'clientsecret']): string[] {
  const haystack = typeof text === 'string' ? Buffer.from(text) : text;
  return kinds.filter((kind) => haystack.includes(Buffer.from(`${sentinel}-${kind}-`)));
}

/** Scans the live server log, SQLite files, captured responses and console. */
export async function leakScan(testInfo: TestInfo): Promise<void> {
  const db = process.env.HELM_E2E_DB as string;
  const files = [process.env.HELM_E2E_SERVER_LOG as string, db, `${db}-wal`, `${db}-shm`].filter((file) => file && existsSync(file));
  expect(files.length, 'server log and database exist').toBeGreaterThanOrEqual(2);
  const found: Record<string, string[]> = {};
  for (const file of files) {
    const kinds = leaks(readFileSync(file));
    if (kinds.length) found[path.basename(file)] = kinds;
  }
  const responses = JSON.stringify(captured.responses, null, 2);
  const consoleText = captured.console.join('\n');
  if (leaks(responses).length) found['captured responses'] = leaks(responses);
  if (leaks(consoleText).length) found['browser console'] = leaks(consoleText);
  // Retained evidence: run-worker.sh scans these again (with trace archives) after Helm stops.
  await testInfo.attach('gitea-captured-responses.json', { contentType: 'application/json', body: responses });
  await testInfo.attach('gitea-browser-console.txt', { contentType: 'text/plain', body: consoleText || '(empty)' });
  expect(found, 'no forge secret outside the connection file').toEqual({});
  expect(captured.responses.length, 'Helm gitea responses were captured').toBeGreaterThan(10);
}
