import { expect, test } from '@playwright/test';

test.use({ trace: 'on' });

test('retired GitHub automation stays gone while task PR URL linking remains available', async ({ page }, info) => {
  await page.goto('/');
  const origin = new URL(page.url()).origin;
  const headers = { Origin: origin, 'Content-Type': 'application/json' };
  const key = `RM${Date.now().toString(36)}`.toUpperCase();
  const created = await page.request.post('/api/v1/projects', {
    headers, data: { key, name: 'Synthetic integration removal proof' }
  });
  expect(created.status()).toBe(201);
  const project = await created.json();
  const taskResponse = await page.request.post(`/api/v1/projects/${project.id}/tasks`, {
    headers, data: { title: 'Synthetic retained task' }
  });
  expect(taskResponse.status()).toBe(201);
  const task = await taskResponse.json();
  const paths = [
    '/integrations/github', '/integrations/github/check', '/integrations/github/connect',
    '/integrations/github/connect/cancel', '/integrations/github/disconnect',
    `/projects/${project.id}/github`, `/projects/${project.id}/github/branches`,
    `/projects/${project.id}/github/readiness`, `/projects/${project.id}/github-audits`,
    `/projects/${project.id}/github-audit-authorizations`,
    `/projects/${project.id}/github-audit-authorizations/retired`,
    '/artifact-summaries', `/tasks/${task.id}/artifacts`,
    `/tasks/${task.id}/artifacts/retired`, `/tasks/${task.id}/artifacts/retired/refresh`,
    '/audits/retired/evidence', '/audits/retired/job', '/audits/retired/apply',
    '/audits/retired/cancel', '/audits/retired/retry'
  ];
  const exchanges: { method: string; path: string; status: number }[] = [];
  for (const path of paths) {
    for (const method of ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']) {
      const response = await page.request.fetch(`/api/v1${path}`, { method, headers });
      exchanges.push({ method, path: path.replace(project.id, '{project}').replace(task.id, '{task}'), status: response.status() });
      expect(response.status(), `${method} ${path}`).toBe(404);
    }
  }
  await page.goto(`/p/${project.slug}`);
  await expect(page.getByRole('button', { name: 'Branches', exact: true })).toHaveCount(0);
  await page.goto(`/p/${project.slug}/tasks/${task.key}`);
  await expect(page.getByRole('heading', { name: 'Pull requests', exact: true })).toBeVisible();
  await expect(page.getByLabel('Pull request URL', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Link pull request', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /Refresh (pull request|GitHub)|Check GitHub/i })).toHaveCount(0);
  await expect(page.getByLabel('Task title', { exact: true })).toHaveValue('Synthetic retained task');
  await page.goto('/settings');
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: /GitHub/i })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Connect GitHub/i })).toHaveCount(0);
  await info.attach('removed-endpoints.json', { body: JSON.stringify(exchanges, null, 2), contentType: 'application/json' });
  await info.attach('settings-without-github.png', { body: await page.screenshot(), contentType: 'image/png' });
});
