import { expect, test } from '@playwright/test';

test('first local administrator setup signs into the workspace', async ({ page, request }, info) => {
  const statusResponse = await request.get('/api/v1/auth/status');
  expect(statusResponse.ok()).toBeTruthy();
  const status = (await statusResponse.json()) as { mode?: string; setup_required?: boolean };
  test.skip(status.mode !== 'local' || !status.setup_required, 'requires a fresh local-auth database');

  await page.goto('/');
  await expect(page).toHaveTitle('Helm');
  await expect(page.getByText('Helm keeps projects focused', { exact: false })).toBeVisible();
  await expect(page.locator('svg.helm-mark')).toBeVisible();
  await page.getByLabel('Full name').fill('Onboarding Admin');
  await page.getByLabel('Email').fill('onboarding@example.test');
  await page.getByLabel('Password').fill('LocalOnboarding-2026!');
  // Setup hashes the password and the subsequent login verifies it. Under
  // race instrumentation those real requests can outlast a visibility wait.
  // Observe both responses before asserting the signed-in shell; keep the
  // normal request and test deadlines instead of extending UI timeouts.
  const authResponse = (path: string) => page.waitForResponse(response =>
    new URL(response.url()).pathname === path && response.request().method() === 'POST');
  const statuses: Record<string, number> = {};
  await Promise.all([
    authResponse('/api/v1/auth/setup').then(response => {
      statuses.setup = response.status();
      expect(response.status(), 'workspace setup succeeds').toBe(201);
    }),
    authResponse('/api/v1/auth/login').then(response => {
      statuses.login = response.status();
      expect(response.status(), 'new administrator signs in').toBe(200);
    }),
    page.getByRole('button', { name: 'Create workspace', exact: true }).click()
  ]);
  await info.attach('onboarding-auth-results.json', {
    body: Buffer.from(JSON.stringify(statuses)), contentType: 'application/json'
  });

  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'New project', exact: true })).toBeVisible();
  await expect(page.getByText('Onboarding Admin', { exact: true })).toBeVisible();

  await page.reload();
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
  await expect(page.getByText('Onboarding Admin', { exact: true })).toBeVisible();
  await info.attach('onboarding-complete.png', { body: await page.screenshot(), contentType: 'image/png' });
});
