import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

// Real-backend harness: internal/httpapi/column_audit_browser_test.go. Only the
// GitHub upstream and the model are scripted; auth, router, store, worker and
// correction lifecycle are the production code paths.
export const harnessEnabled = process.env.HELM_COLUMN_AUDIT_E2E === '1';
export const repository = 'example/synthetic';
export const baseSHA = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const origin = () => process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18310';

export type SeedTask = { id: string; key: string; title: string };
export type Seed = {
  project: { id: string; slug: string; key: string; name: string };
  tasks: Record<string, SeedTask>;
  columns: Record<string, string>;
};
export type DbTask = { id: string; key: string; title: string; kind: string; column: string; semantic_state: string; version: number; completed: boolean; claimed: boolean; resolution: string };
export type DbSnapshot = {
  tasks: DbTask[];
  events: { task: string; type: string }[];
  receipts: { task: string; action: string; outcome: string; request_key: string }[];
  correction_requests: number;
};

async function control(request: APIRequestContext, method: 'GET' | 'POST' | 'PUT', route: string, data?: unknown) {
  const response = await request.fetch(`/__columnaudit/${route}`, { method, data, headers: { Origin: origin() } });
  expect(response.ok(), `${method} ${route}: ${response.status()}`).toBeTruthy();
  return response.json();
}

export const seed = (request: APIRequestContext, scenario: 'lifecycle' | 'unverified', fillers = 0): Promise<Seed> =>
  control(request, 'POST', 'seed', { scenario, fillers });

export const setState = (request: APIRequestContext, state: Record<string, boolean | number>) => control(request, 'PUT', 'state', state);
export const restart = (request: APIRequestContext) => control(request, 'POST', 'restart');
export const snapshot = (request: APIRequestContext, projectId: string): Promise<DbSnapshot> => control(request, 'GET', `db?project=${encodeURIComponent(projectId)}`);
export const dbTask = (db: DbSnapshot, key: string) => db.tasks.find((task) => task.key === key)!;

/** Ordinary Helm API call as the disabled-mode synthetic human. */
export async function api(request: APIRequestContext, method: string, route: string, body?: unknown, version?: number) {
  const response = await request.fetch(`/api/v1${route}`, {
    method,
    data: body,
    headers: {
      Origin: origin(),
      ...(method === 'GET' ? {} : { 'Idempotency-Key': `e2e-${Date.now()}-${Math.random().toString(36).slice(2)}` }),
      ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
    }
  });
  const text = await response.text();
  expect(response.ok(), `${method} ${route}: ${response.status()} ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : null;
}

/** Edit a card after capture: the audit's assessment of it becomes stale. */
export async function editTask(request: APIRequestContext, taskId: string, title: string) {
  const current = await api(request, 'GET', `/tasks/${taskId}`);
  return api(request, 'PATCH', `/tasks/${taskId}`, { title }, current.version);
}

export async function waitForJob(request: APIRequestContext, auditId: string, outcome?: string) {
  await expect.poll(async () => (await api(request, 'GET', `/audits/${auditId}/job`)).stage, { timeout: 40_000 }).toBe('finished');
  const job = await api(request, 'GET', `/audits/${auditId}/job`);
  if (outcome) expect(job.outcome).toBe(outcome);
  return job;
}

/** Start through the API for journeys whose subject is review, not scope. */
export async function startAuditByApi(request: APIRequestContext, projectId: string): Promise<string> {
  const started = await api(request, 'POST', `/projects/${projectId}/github-audits`, {});
  return started.audit.id;
}

/** Fail the journey on horizontal overflow at the current viewport. */
export async function expectNoHorizontalScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBeTruthy();
}

/** Sanitized artifacts: never write a confirmation proof to disk. */
export function saveArtifact(info: TestInfo, name: string, value: unknown) {
  const text = JSON.stringify(value, (key, item) => (key === 'confirmation_token' ? '[redacted proof]' : item), 2);
  expect(text).not.toMatch(/"confirmation_token":\s*"(?!\[redacted)/);
  const dir = process.env.HELM_COLUMN_AUDIT_ARTIFACTS || info.outputDir;
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, `${name}.json`), text);
}

export async function shot(page: Page, info: TestInfo, name: string) {
  const dir = process.env.HELM_COLUMN_AUDIT_ARTIFACTS || info.outputDir;
  await page.screenshot({ path: path.join(dir, 'shots', `${name}.png`), fullPage: true });
}

export async function setTheme(page: Page, theme: 'light' | 'dark') {
  await page.evaluate((value) => {
    localStorage.setItem('helm.theme', value);
  }, theme);
  await page.reload();
}
