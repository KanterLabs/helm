import { expect, test, type Page } from '@playwright/test';
import { baseConnection, installConnectionMocks, type Connection } from './support/githubConnectionFixture';

// The connection panel's not-set-up wording and its links to the in-app
// guide. Covers web/src/lib/components/github/failures.md entries 24 and 25.
test.use({ serviceWorkers: 'block' });

const panelOf = (page: Page) => page.locator('[data-github-connection]');
const drawer = (page: Page) => page.locator('[data-help-drawer]');

async function open(page: Page, connection: Connection, enabled = true) {
  await installConnectionMocks(page, connection);
  if (!enabled) {
    // What a server answers with HELM_GITHUB_ENABLED unset.
    await page.route(/\/api\/v1\/integrations\/github$/, (route) => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({ enabled: false, configured: false, state: 'not_configured', message: '', allowed_repositories: [], checked_at: null, connection })
    }));
  }
  await page.route(/\/api\/v1\/docs\/github-device-flow$/, (route) => route.fulfill({
    status: 200, contentType: 'text/markdown', body: '# Connect GitHub by signing in\n\nSynthetic guide body.\n\n## Settings\n\nSynthetic settings table.\n'
  }));
  await page.goto('/settings');
  await expect(panelOf(page)).toBeVisible();
}

test('says GitHub is off, not that a client ID is missing', async ({ page }, testInfo) => {
  const off: Connection = { mode: 'device_flow', signin_available: false, state: 'disconnected' };
  await open(page, off, false);
  const panel = panelOf(page);
  await expect(panel).toHaveAttribute('data-connection-view', 'unavailable');
  await expect(panel.getByRole('heading', { name: 'GitHub is turned off on this Helm' })).toBeVisible();
  await expect(panel).toContainText('HELM_GITHUB_ENABLED=true');
  await expect(panel).not.toContainText('client ID');
  await expect(panel.getByRole('button', { name: /Connect GitHub/ })).toHaveCount(0);
  await testInfo.attach('github-off.png', { body: await page.locator('[data-github-settings]').screenshot(), contentType: 'image/png' });
});

test('the setup guide and how-this-works open in-app help', async ({ page }) => {
  await open(page, baseConnection('disconnected', { signin_available: false }));
  const panel = panelOf(page);
  await expect(panel.getByRole('heading', { name: 'GitHub sign-in is not set up' })).toBeVisible();
  await expect(panel).toContainText('HELM_GITHUB_APP_CLIENT_ID');
  await panel.getByRole('button', { name: 'Setup guide' }).click();
  await expect(drawer(page)).toBeVisible();
  await expect(drawer(page).getByRole('heading', { name: 'Connect GitHub by signing in' })).toBeVisible();
  await expect(drawer(page).getByRole('heading', { name: 'Settings' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(drawer(page)).toHaveCount(0);

  await open(page, baseConnection('disconnected'));
  await expect(panel.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
  await panel.getByRole('button', { name: 'How this works' }).click();
  await expect(drawer(page).getByRole('heading', { name: 'Connect GitHub by signing in' })).toBeVisible();
});
