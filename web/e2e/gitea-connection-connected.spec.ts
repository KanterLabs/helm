import { expect, test, type Page } from '@playwright/test';
import {
  BASE, CLIENT_ID, clientField, connectedStatus, disconnectedStatus, DRAFT_KEY, expectNoIdempotencyKeys, installGiteaMocks,
  liveRegion, openSettings, panel, repositories, signInAsMember, snap
} from './support/giteaFixture';

// WP4 of docs/GITEA_OAUTH_PLAN.md: step 3 of docs/GITEA_OAUTH_UX.md (back in
// Helm), the connected panel, reconnect, unavailable and access control.
test.use({ serviceWorkers: 'block' });

const draft = JSON.stringify({ base_url: BASE, client_id: CLIENT_ID, allow_insecure_http: false });
const seedDraft = (page: Page) => page.evaluate(([key, value]) => sessionStorage.setItem(key, value), [DRAFT_KEY, draft]);
const readDraft = (page: Page) => page.evaluate((key) => sessionStorage.getItem(key), DRAFT_KEY);
const connectedHeading = (page: Page, host = 'gitea.example.com', product = 'Gitea') =>
  panel(page).getByRole('heading', { name: new RegExp(`Connected to ${host.replace(/\./g, '\\.')} as @helm-reader`) }).and(panel(page).locator(`[data-product="${product}"]`));

test('returning with each sign-in error keeps the values and offers the fix', async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  await installGiteaMocks(page);
  await openSettings(page);
  const cases: Array<[string, string, string | null]> = [
    ['state_invalid', 'That sign-in expired or was started somewhere else.', null],
    ['scope_conflict', 'Gitea still holds an older authorization for Helm. Remove Helm under Authorized OAuth2 Applications, then sign in again.', 'link'],
    ['authorize_failed', 'Gitea refused the sign-in. Check the Client ID.', 'client'],
    ['client_confidential', 'The application is set as a Confidential Client. Edit it in Gitea, untick Confidential Client, save, then sign in again.', 'link'],
    ['client_unknown', 'Gitea does not recognise that Client ID. Copy it again from the application page.', 'client'],
    ['exchange_failed', 'Gitea did not complete the sign-in.', null],
    ['storage_failed', 'Helm could not save the connection. Check that its data directory is writable.', null],
    ['already_connected', 'Helm is already connected. Disconnect first.', 'show'],
    ['mystery_code', 'Something went wrong connecting to Gitea.', null]
  ];
  for (const [code, message, action] of cases) {
    await seedDraft(page);
    await page.goto(`/settings?gitea_error=${code}`);
    await expect(page).toHaveURL(/\/settings$/);
    const result = panel(page).locator('[data-gitea-result]');
    await expect(result).toContainText(message);
    await expect(result).toBeFocused();
    await expect(clientField(page)).toHaveValue(CLIENT_ID);
    await expect(panel(page)).not.toContainText(code);
    if (action === 'link') await expect(result.getByRole('link')).toHaveAttribute('href', `${BASE}/user/settings/applications`);
    if (action === 'show') await expect(result.getByRole('button', { name: 'Show the connection' })).toBeVisible();
    if (action === 'client') {
      await result.getByRole('button', { name: 'Edit the Client ID' }).click();
      await expect(clientField(page)).toBeFocused();
    }
    await snap(page, testInfo, `returned-${code}`);
  }
  // Without saved values the message still shows, above step 1.
  await page.evaluate((key) => sessionStorage.removeItem(key), DRAFT_KEY);
  await page.goto('/settings?gitea_error=exchange_failed');
  await expect(panel(page).locator('[data-gitea-result]')).toContainText('Gitea did not complete the sign-in.');
  await expect(panel(page).getByRole('textbox', { name: /Server address/ })).toHaveValue('');
  await snap(page, testInfo, 'returned-without-draft');
});

test('back from the forge connected: a scoped connection, its repositories and a filter', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page, connectedStatus());
  await page.goto('/settings');
  await seedDraft(page);
  await openSettings(page, '?gitea=connected');
  await expect(page).toHaveURL(/\/settings$/);
  await expect(connectedHeading(page)).toBeFocused();
  await expect(liveRegion(page)).toContainText('Connected to gitea.example.com as @helm-reader');
  expect(await readDraft(page)).toBeNull();
  await expect(panel(page)).toContainText('Gitea 28.3.0');
  await expect(panel(page).locator('[data-gitea-access="scoped"]')).toHaveText('Read-only access');
  await expect(panel(page)).toContainText('Helm can read 12 repositories');
  expect(mock.count('GET', '/repositories')).toBe(0);
  await snap(page, testInfo, 'connected-scoped');

  await panel(page).getByRole('button', { name: 'View' }).click();
  const list = panel(page).locator('[data-gitea-repositories] li');
  await expect(list).toHaveCount(12);
  await expect(panel(page).locator('[data-gitea-repositories]')).toContainText('platform/infra-terraform-modules-shared-with-everyone');
  await snap(page, testInfo, 'repositories');
  const filter = panel(page).getByRole('searchbox', { name: 'Filter repositories' });
  await filter.fill('docs/');
  await expect(list).toHaveCount(2);
  await snap(page, testInfo, 'repositories-filtered');
  await filter.fill('zzz');
  await expect(panel(page)).toContainText('No repository matches “zzz”.');
  expect(mock.count('GET', '/repositories')).toBe(1);

  mock.status = connectedStatus({ repository_count: 3 });
  mock.repositories = { list: repositories(3), truncated: true };
  await openSettings(page);
  await panel(page).getByRole('button', { name: 'View' }).click();
  await expect(list).toHaveCount(3);
  await expect(panel(page).getByRole('searchbox', { name: 'Filter repositories' })).toHaveCount(0);
  await expect(panel(page)).toContainText('Showing the first 3.');
  await snap(page, testInfo, 'repositories-truncated');
  expectNoIdempotencyKeys(mock);
});

test('full access is a warning, not a badge', async ({ page }, testInfo) => {
  await installGiteaMocks(page, connectedStatus({ access: 'full' }));
  await openSettings(page);
  await expect(panel(page).locator('[data-gitea-access="full"]')).toContainText('This server gave Helm full access to this account. Helm only reads, but use a dedicated account with access to just the repositories Helm needs.');
  await expect(panel(page).getByText('Read-only access', { exact: true })).toHaveCount(0);
  await snap(page, testInfo, 'connected-full');
});

test('test connection reports problems and disconnect asks first', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page, connectedStatus());
  await openSettings(page);
  await panel(page).getByRole('button', { name: 'Test connection' }).click();
  await expect(liveRegion(page)).toContainText('The connection works.');
  mock.check = () => connectedStatus({ check: { state: 'unreachable', code: 'server_unreachable' }, checked_at: new Date().toISOString() });
  await panel(page).getByRole('button', { name: 'Test connection' }).click();
  await expect(panel(page).locator('[data-gitea-check-problem]')).toContainText('Helm could not reach gitea.example.com');
  await expect(liveRegion(page)).toContainText('Helm could not reach gitea.example.com');
  await snap(page, testInfo, 'check-unreachable');

  await panel(page).getByRole('button', { name: 'Disconnect' }).click();
  const confirm = panel(page).locator('[data-gitea-disconnect-confirm]');
  await expect(confirm).toContainText("To remove Helm's authorization on Gitea as well, delete it under Authorized OAuth2 Applications.");
  await expect(confirm.getByRole('link', { name: /Authorized OAuth2 Applications/ })).toHaveAttribute('href', `${BASE}/user/settings/applications`);
  await snap(page, testInfo, 'disconnect-confirm');
  await confirm.getByRole('button', { name: 'Cancel' }).click();
  await expect(confirm).toHaveCount(0);
  expect(mock.count('POST', '/disconnect')).toBe(0);
  await seedDraft(page);
  await panel(page).getByRole('button', { name: 'Disconnect' }).click();
  await confirm.getByRole('button', { name: 'Yes, disconnect' }).click();
  await expect(panel(page).getByRole('textbox', { name: /Server address/ })).toBeVisible();
  await expect(liveRegion(page)).toContainText('Disconnected from Gitea.');
  expect(mock.count('POST', '/disconnect')).toBe(1);
  expect(await readDraft(page)).toBeNull();
  expectNoIdempotencyKeys(mock);
});

test('reconnect required signs in again with the stored server, once', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page, connectedStatus({ state: 'reconnect_required', access: 'unknown' }));
  await openSettings(page);
  await expect(panel(page)).toContainText("Helm's sign-in to Gitea has ended.");
  await snap(page, testInfo, 'reconnect-required');
  mock.start = () => ({ status: 200, body: { authorize_url: 'https://evil.example.net/login/oauth/authorize' } });
  await panel(page).getByRole('button', { name: 'Sign in again' }).click();
  await expect(panel(page).locator('[data-gitea-result]')).toContainText('Helm did not open the sign-in page');
  let release = () => undefined as void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  mock.start = async () => { await held; return undefined; };
  await panel(page).getByRole('button', { name: 'Sign in again' }).dblclick();
  await expect(panel(page).getByRole('button', { name: 'Sign in again' })).toBeDisabled();
  release();
  await page.waitForURL(/^https:\/\/gitea\.example\.com\/login\/oauth\/authorize/);
  const starts = mock.requests.filter((item) => item.path === '/oauth/start');
  expect(starts).toHaveLength(2);
  expect(starts[1].body).toEqual({ base_url: '', client_id: '', allow_insecure_http: false });

  await page.goto('/settings?gitea_error=denied');
  await expect(page).toHaveURL(/\/settings$/);
  await expect(panel(page).locator('[data-gitea-result]')).toContainText('Sign-in was cancelled on Gitea. Nothing was changed.');
  await expect(panel(page).getByRole('button', { name: 'Sign in again' })).toBeVisible();
  await snap(page, testInfo, 'reconnect-returned-denied');
});

test('an unreadable saved connection starts over at step 1', async ({ page }, testInfo) => {
  // The server reports reconnect_required with no server when its saved file
  // cannot be trusted. "Sign in again" would have nothing to reuse.
  await installGiteaMocks(page, connectedStatus({
    state: 'reconnect_required', access: 'unknown', server: null, client_id: '', account: null, register_url: '', revoke_url: ''
  }));
  await openSettings(page);
  await expect(panel(page).locator('[data-gitea-lost]')).toContainText("Helm's saved connection could not be read");
  await expect(panel(page).getByRole('textbox', { name: /Server address/ })).toBeVisible();
  await expect(panel(page).getByRole('button', { name: 'Sign in again' })).toHaveCount(0);
  await expect(panel(page)).not.toContainText('Helm was connected to');
  await expect(panel(page).locator('.gitea-state')).toHaveText('Not connected');
  await snap(page, testInfo, 'reconnect-unreadable');
});

test('unavailable sign-in says why and offers nothing to fill in', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page, disconnectedStatus({ available: false, unavailable_reason: 'public_origin_missing' }));
  await openSettings(page);
  await expect(panel(page)).toContainText("Sign-in needs Helm's public address. An operator sets HELM_PUBLIC_ORIGIN.");
  await expect(panel(page).getByRole('textbox')).toHaveCount(0);
  await expect(panel(page).getByRole('button')).toHaveCount(0);
  await snap(page, testInfo, 'unavailable-public-origin');
  mock.status = disconnectedStatus({ available: false, unavailable_reason: 'disabled' });
  await openSettings(page);
  await expect(panel(page)).toContainText('Gitea sign-in is turned off on this server.');
  await expect(panel(page).getByRole('textbox')).toHaveCount(0);
  await snap(page, testInfo, 'unavailable-disabled');
});

test('a Forgejo connection is labelled Forgejo throughout', async ({ page }, testInfo) => {
  const base = 'https://forgejo.example.com';
  await installGiteaMocks(page, connectedStatus({ server: { base_url: base, product: 'forgejo', version: '11.0.1+gitea-1.22.0', insecure_http: false } }));
  await openSettings(page);
  await expect(panel(page).getByRole('heading', { name: 'Forgejo', exact: true })).toBeVisible();
  await expect(connectedHeading(page, 'forgejo.example.com', 'Forgejo')).toBeVisible();
  await expect(panel(page)).toContainText('Forgejo 11.0.1+gitea-1.22.0');
  await expect(panel(page)).not.toContainText('Gitea ');
  await snap(page, testInfo, 'connected-forgejo');
});

test('members who are not administrators never see or load the panel', async ({ page }) => {
  const mock = await installGiteaMocks(page);
  await signInAsMember(page);
  await page.goto('/settings');
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Appearance' })).toBeVisible();
  await expect(panel(page)).toHaveCount(0);
  expect(mock.requests).toEqual([]);
});
