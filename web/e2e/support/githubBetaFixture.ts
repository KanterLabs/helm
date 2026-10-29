import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

export const repository = 'synthetic/helm-beta';
export const sourceSHA = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
// Keep the complete credential out of Playwright's bundled source snapshots.
export const sentinel = ['HELM_GITHUB_BETA_', 'SENTINEL_DO_NOT_EXPOSE_', '74bf8c'].join('');
export type BetaTask = { id: string; key: string; title: string; version: number; column_id: string };
export type BetaProject = { id: string; slug: string; key: string; name: string };

export async function api(request: APIRequestContext, method: string, path: string, body?: unknown, version?: number) {
  const response = await request.fetch(`/api/v1${path}`, {
    method,
    data: body,
    headers: {
      Origin: process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18082',
      ...(version === undefined ? {} : { 'If-Match': `"v${version}"` }),
    },
  });
  const text = await response.text();
  expect(text.includes(sentinel), 'API responses must not expose the synthetic credential').toBeFalsy();
  const result = JSON.parse(text);
  expect(response.ok(), `${method} ${path}: ${response.status()} ${safeJSON(result)}`).toBeTruthy();
  return result;
}

export async function seed(request: APIRequestContext, suffix: string): Promise<{ project: BetaProject; tasks: BetaTask[] }> {
  const project = await api(request, 'POST', '/projects', { key: `GH${suffix}`, name: `GitHub beta ${suffix}` });
  const tasks = [];
  for (const title of ['Ready proposal from source', 'Changed after capture', 'Merged PR stays open']) {
    tasks.push(await api(request, 'POST', `/projects/${project.id}/tasks`, { title, description: 'Goal: Implement ready.go synthetic workflow.\nWork status: unstarted\nReadiness: ready' }));
  }
  return { project, tasks };
}

export async function selectRepository(page: Page, project: BetaProject) {
  await page.goto(`/p/${project.slug}/audits`);
  await page.getByRole('button', { name: /GitHub repositor/i }).click();
  await page.getByRole('checkbox', { name: repository, exact: true }).check();
  await page.getByRole('textbox', { name: `Branch for ${repository}`, exact: true }).fill('main');
  await page.getByRole('button', { name: /Save repositor/i }).click();
  await expect.poll(async () => (await api(page.request, 'GET', `/projects/${project.id}/github`)).repositories)
    .toEqual([{ repository, ref: 'main' }]);
}

// Poll explicitly: expect.poll retries thrown errors and would hide a terminal
// failure until timeout. Emit only bounded, redacted server diagnostics.
async function waitForJob(request: APIRequestContext, auditID: string, field: 'outcome' | 'stage', target: string) {
  const deadline = Date.now() + 35_000;
  for (;;) {
    const job = await api(request, 'GET', `/audits/${auditID}/job`);
    if (job[field] === target) return job;
    const diagnostic = safeJSON({ outcome: job.outcome, stage: job.stage, error_code: job.error_code, message: String(job.message || '').slice(0, 1000) });
    if (job.outcome || job.stage === 'finished') throw new Error(`Audit ended before ${field}=${target}: ${diagnostic}`);
    if (Date.now() >= deadline) throw new Error(`Audit timed out waiting for ${field}=${target}: ${diagnostic}`);
    await new Promise(resolve => setTimeout(resolve, 200));
  }
}

export function waitJob(request: APIRequestContext, auditID: string, outcome = 'complete') {
  return waitForJob(request, auditID, 'outcome', outcome);
}

export function waitJobStage(request: APIRequestContext, auditID: string, stage: string) {
  return waitForJob(request, auditID, 'stage', stage);
}

// Preview proofs and credentials stay in memory, including on failure paths.
export function safeJSON(value: unknown) {
  return JSON.stringify(value, (key, item) => /token|authorization/i.test(key) && typeof item === 'string' && key !== 'authorization_id'
    ? '[redacted]' : item, 2).replaceAll(sentinel, '[redacted]')
    .replace(/\b(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|Bearer\s+[^\s"\\]+)/g, '[redacted]');
}

export async function attachReceipt(info: TestInfo, name: string, value: unknown) {
  await info.attach(name, { body: safeJSON(value), contentType: 'application/json' });
}

export async function startInUI(page: Page, project: BetaProject): Promise<string> {
  await page.goto(`/p/${project.slug}/audits`);
  const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/projects/${project.id}/github-audits`));
  await page.getByRole('button', { name: 'Audit columns', exact: true }).click();
  await page.locator('[data-column-audit-scope]').getByRole('button', { name: /^Audit \d+ cards?$/ }).click();
  const started = await response;
  expect(started.status()).toBe(202);
  return (await started.json()).audit.id;
}
