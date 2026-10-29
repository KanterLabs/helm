import { expect, type APIRequestContext, type Page } from '@playwright/test';

export const repository = 'synthetic/helm-beta';
export const sourceSHA = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
// Keep the complete credential out of Playwright's bundled source snapshots.
export const sentinel = ['HELM_GITHUB_BETA_', 'SENTINEL_DO_NOT_EXPOSE_', '74bf8c'].join('');
export type BetaTask = { id: string; key: string; title: string; version: number; column_id: string };
export type BetaProject = { id: string; slug: string; key: string; name: string };

export async function api(request: APIRequestContext, method: string, path: string, body?: unknown, version?: number, key?: string) {
  const response = await request.fetch(`/api/v1${path}`, {
    method,
    data: body,
    headers: {
      Origin: process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18082',
      ...(version === undefined ? {} : { 'If-Match': `"v${version}"` }),
      ...(key === undefined ? {} : { 'Idempotency-Key': key }),
    },
  });
  const text = await response.text();
  expect(text.includes(sentinel), 'API responses must not expose the synthetic credential').toBeFalsy();
  expect(response.ok(), `${method} ${path}: ${response.status()} ${text}`).toBeTruthy();
  return JSON.parse(text);
}

export async function seed(request: APIRequestContext, suffix: string): Promise<{ project: BetaProject; tasks: BetaTask[] }> {
  const project = await api(request, 'POST', '/projects', { key: `GH${suffix}`, name: `GitHub beta ${suffix}` });
  const tasks = [];
  for (const title of ['Ready proposal from source', 'Changed after capture', 'Merged PR stays open']) {
    tasks.push(await api(request, 'POST', `/projects/${project.id}/tasks`, { title }));
  }
  return { project, tasks };
}

// TC-316 parks the audit-page editor and audit start in the UI; the retained
// API lifecycle is driven directly and the UI is checked read-only.
export async function selectRepository(page: Page, project: BetaProject) {
  const current = await api(page.request, 'GET', `/projects/${project.id}/github`);
  await api(page.request, 'PUT', `/projects/${project.id}/github`, { repositories: [{ repository, ref: 'main' }] }, current.version);
  await expect.poll(async () => (await api(page.request, 'GET', `/projects/${project.id}/github`)).repositories)
    .toEqual([{ repository, ref: 'main' }]);
}

export async function startAudit(request: APIRequestContext, project: BetaProject): Promise<string> {
  const started = await api(request, 'POST', `/projects/${project.id}/github-audits`,
    { repositories: [{ repository, ref: 'main' }], states: ['backlog', 'ready', 'active'] }, undefined, `beta-start-${project.id}`);
  return started.audit.id;
}

export async function waitJob(request: APIRequestContext, auditID: string, outcome = 'complete') {
  await expect.poll(async () => (await api(request, 'GET', `/audits/${auditID}/job`)).outcome, { timeout: 35_000 }).toBe(outcome);
  return api(request, 'GET', `/audits/${auditID}/job`);
}
