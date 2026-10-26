import { expect, type APIRequestContext, type Page } from '@playwright/test';

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

export async function selectRepository(page: Page, project: BetaProject) {
  await page.goto(`/p/${project.slug}/audits`);
  await page.getByRole('button', { name: /GitHub repositor/i }).click();
  await page.getByRole('checkbox', { name: repository, exact: true }).check();
  await page.getByRole('textbox', { name: `Branch for ${repository}`, exact: true }).fill('main');
  await page.getByRole('button', { name: /Save repositor/i }).click();
  await expect.poll(async () => (await api(page.request, 'GET', `/projects/${project.id}/github`)).repositories)
    .toEqual([{ repository, ref: 'main' }]);
}

export async function waitJob(request: APIRequestContext, auditID: string, outcome = 'complete') {
  await expect.poll(async () => (await api(request, 'GET', `/audits/${auditID}/job`)).outcome, { timeout: 35_000 }).toBe(outcome);
  return api(request, 'GET', `/audits/${auditID}/job`);
}

export async function startInUI(page: Page, project: BetaProject): Promise<string> {
  await page.goto(`/p/${project.slug}/audits`);
  const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/projects/${project.id}/github-audits`));
  await page.getByRole('button', { name: 'Run audit', exact: true }).click();
  await page.getByRole('button', { name: /Start audit|Run audit now/i }).click();
  const started = await response;
  expect(started.status()).toBe(202);
  return (await started.json()).audit.id;
}
