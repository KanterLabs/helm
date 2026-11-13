import { existsSync } from 'node:fs';
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test';
import { addressField, clientField, liveRegion, openSettings, panel, snap } from './support/giteaFixture';
import {
  captured, control, expectPrivateStateFile, forge, forgeURL, helmOrigin, helmStatus, leakScan, resetAll, sentinel, stateFile, watch
} from './support/giteaForge';

// WP5 part 1 of docs/GITEA_OAUTH_PLAN.md: the real Helm binary, a real
// browser and test/e2e/fake-gitea (a synthetic Gitea 28.3.0 on loopback),
// through the whole journey of docs/GITEA_OAUTH_UX.md. Failure list:
// test/e2e/GITEA_FAILURES.md. Tests share one Helm and one forge, in order.
test.describe.configure({ mode: 'serial' });
test.use({ trace: 'on' });

const callbackPath = '/api/v1/integrations/gitea/oauth/callback';
const stepOne = (page: Page) => panel(page).getByRole('heading', { name: 'Where is your Gitea?' });
const stepTwo = (page: Page) => panel(page).getByRole('heading', { name: "Create Helm's application in Gitea" });
const found = (page: Page) => panel(page).locator('[data-gitea-found]');
const signIn = (page: Page) => panel(page).getByRole('button', { name: 'Sign in with Gitea' });
const connected = (page: Page) => panel(page).locator('[data-gitea-connected-heading]');
const testButton = (page: Page) => panel(page).getByRole('button', { name: 'Test connection' });
const problem = (page: Page) => panel(page).locator('[data-gitea-check-problem]');
let clientId = '';

async function paste(field: Locator, text: string): Promise<void> {
  await field.focus();
  await field.evaluate((node, value) => {
    const data = new DataTransfer();
    data.setData('text/plain', value);
    node.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
  }, text);
}

async function consentPage(page: Page, testInfo: TestInfo, name: string): Promise<void> {
  await page.waitForURL((url) => url.origin === forgeURL && url.pathname === '/login/oauth/authorize');
  await expect(page.getByRole('heading', { name: 'Authorize Application' })).toBeVisible();
  await expect(page.locator('[data-scopes] li')).toHaveText(['read:repository', 'read:user']);
  await testInfo.attach(name, { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
}

async function backInHelm(page: Page): Promise<void> {
  await page.waitForURL((url) => url.origin === helmOrigin && url.pathname === '/settings');
  await expect(panel(page).locator('[data-gitea-loading]')).toHaveCount(0);
}

/** Presses Test connection and waits for its answer. */
async function testConnection(page: Page, staysConnected = true): Promise<void> {
  const answered = page.waitForResponse((response) => new URL(response.url()).pathname === '/api/v1/integrations/gitea/check');
  await testButton(page).click();
  expect((await answered).status()).toBe(200);
  if (staysConnected) await expect(testButton(page)).toBeEnabled();
}

test.beforeAll(async ({ request }) => {
  for (const [name, value] of Object.entries({ forgeURL, sentinel, db: process.env.HELM_E2E_DB, log: process.env.HELM_E2E_SERVER_LOG })) {
    expect(value, `run-worker.sh must provide ${name}`).toBeTruthy();
  }
  await resetAll(request);
});

test.afterAll(async ({ request }) => {
  await resetAll(request);
});

test.beforeEach(async ({ page }) => watch(page));

test('an empty panel reaches Connected with the address, the client ID and the forge\'s Approve', async ({ page, request }, testInfo) => {
  test.setTimeout(120_000);
  // What the person types or presses in Helm and on the consent page.
  const entries: string[] = [];
  // What they do on the forge's own application form (outside Helm).
  const registration: string[] = [];
  await openSettings(page);
  await expect(stepOne(page)).toBeVisible();
  expect((await helmStatus(request)).state).toBe('disconnected');
  await snap(page, testInfo, 'step1-empty');

  // Entry 1: the address. The check starts by itself; no Continue press.
  entries.push('address');
  await addressField(page).fill(forgeURL);
  const warning = panel(page).locator('[data-gitea-http-warning]');
  await expect(warning).toContainText('This address uses http, so sign-in details could be read on the network.');
  await expect(stepTwo(page)).toHaveCount(0);
  await snap(page, testInfo, 'step1-http-warning');
  // Only because the fake is plain http on loopback.
  entries.push('http-consent');
  // click, not check(): ticking it starts the check and the box leaves the page at once.
  await warning.getByRole('checkbox', { name: 'Continue without encryption' }).click();
  // The exact separator matters: Svelte drops a leading space at the start of an
  // {#if} block, which once rendered "…PORT· without encryption".
  await expect(found(page)).toContainText(`Found Gitea 28.3.0 at ${forgeURL} · without encryption`);
  await expect(stepTwo(page)).toBeVisible();
  const redirectURI = (await panel(page).locator('[data-gitea-redirect-uri]').innerText()).trim();
  expect(redirectURI).toBe(`${helmOrigin}${callbackPath}`);
  await expect(signIn(page)).toBeDisabled();
  await snap(page, testInfo, 'step2');

  // On the forge: create the application with the values Helm shows.
  const [forgePage] = await Promise.all([page.context().waitForEvent('page'), panel(page).getByRole('link', { name: /Open Gitea's application page/ }).click()]);
  await forgePage.waitForLoadState();
  expect(forgePage.url()).toBe(`${forgeURL}/user/settings/applications`);
  registration.push('application name', 'redirect URI', 'untick Confidential Client', 'create');
  await forgePage.getByLabel('Application Name').fill('Helm');
  await forgePage.getByLabel('Redirect URIs (one per line)').fill(redirectURI);
  await forgePage.getByRole('checkbox', { name: 'Confidential Client' }).uncheck();
  await forgePage.getByRole('button', { name: 'Create Application' }).click();
  const line = (await forgePage.locator('[data-client-id-line]').innerText()).trim();
  clientId = (await forgePage.locator('[data-client-id]').innerText()).trim();
  await testInfo.attach('forge-application-created.png', { contentType: 'image/png', body: await forgePage.screenshot() });
  await forgePage.close();
  expect((await forge(request)).apps).toEqual([{ name: 'Helm', client_id: clientId, confidential: false, redirect_uris: [redirectURI] }]);

  // Entry 2: the client ID, pasted as the whole line the forge shows.
  entries.push('client-id');
  await paste(clientField(page), line);
  await expect(clientField(page)).toHaveValue(clientId);
  entries.push('sign-in');
  await signIn(page).click();

  await consentPage(page, testInfo, 'forge-consent.png');
  expect((await forge(request)).last_authorize).toEqual({
    client_id: clientId, redirect_uri: redirectURI, response_type: 'code', scope: 'read:repository read:user',
    code_challenge_method: 'S256', has_state: true, has_code_challenge: true
  });
  // Entry 3: the forge's consent button.
  entries.push('approve');
  await page.getByRole('button', { name: 'Approve' }).click();
  await backInHelm(page);

  await expect(connected(page)).toHaveText(`Connected to ${forgeURL} as @helm-reader`);
  await expect(connected(page)).toBeFocused();
  await expect(liveRegion(page)).toHaveText(`Connected to ${forgeURL} as @helm-reader.`);
  expect(new URL(page.url()).search, 'gitea parameters removed from the address bar').toBe('');
  const callback = captured.responses.find((item) => item.url === callbackPath);
  expect(callback?.status).toBe(303);
  expect(callback?.headers['referrer-policy']).toBe('no-referrer');
  expect(callback?.headers.location).toBe('/settings?gitea=connected');

  await testInfo.attach('entries.json', { contentType: 'application/json', body: JSON.stringify({ entries, registration }, null, 2) });
  expect(entries, 'everything the person typed or pressed').toEqual(['address', 'http-consent', 'client-id', 'sign-in', 'approve']);
  expect(entries.filter((entry) => entry !== 'http-consent' && entry !== 'sign-in'), 'the three entries').toEqual(['address', 'client-id', 'approve']);
  const counters = (await forge(request)).counters;
  expect(counters).toMatchObject({ consent_shown: 1, approvals: 1, exchanges: 1, exchange_failures: 0, refreshes: 0 });
  await snap(page, testInfo, 'connected');
});

test('Connected shows the account, read-only access and repositories, and Test connection asks the forge', async ({ page, request }, testInfo) => {
  await openSettings(page);
  await expect(connected(page)).toHaveText(`Connected to ${forgeURL} as @helm-reader`);
  await expect(panel(page)).toContainText('Gitea 28.3.0 · without encryption · connected just now');
  await expect(panel(page).locator('[data-gitea-access="scoped"]')).toHaveText('Read-only access');
  await expect(panel(page).locator('[data-gitea-access="full"]')).toHaveCount(0);
  await expect(panel(page).locator('.gitea-repos-head')).toContainText('Helm can read 57 repositories');
  expect(await helmStatus(request)).toMatchObject({
    state: 'connected', access: 'scoped', repository_count: 57, client_id: clientId, account: { login: 'helm-reader', id: 7 },
    server: { base_url: forgeURL, product: 'gitea', version: '28.3.0', insecure_http: true }, check: { state: 'ok', code: '' }
  });
  expectPrivateStateFile();

  await panel(page).getByRole('button', { name: 'View' }).click();
  const list = panel(page).locator('[data-gitea-repositories] li');
  await expect(list).toHaveCount(57);
  await expect(list.first()).toContainText('helm-reader/repo-01');
  await expect(panel(page).locator('[data-gitea-repositories]')).toContainText('e2e-team/repo-57');
  const filter = panel(page).getByRole('searchbox', { name: 'Filter repositories' });
  await filter.fill('repo-40');
  await expect(list).toHaveCount(1);
  await expect(list).toContainText('Private');
  await expect(list).toContainText('Archived');
  await filter.fill('');
  await expect(list).toHaveCount(57);
  await snap(page, testInfo, 'connected-repositories');

  const before = (await forge(request)).counters;
  await testConnection(page);
  await expect(liveRegion(page)).toHaveText('The connection works.');
  await expect(problem(page)).toHaveCount(0);
  const after = (await forge(request)).counters;
  expect(after.user_calls).toBe(before.user_calls + 1);
  expect(after.limit_checks).toBe(before.limit_checks + 1);
  expect(after.refreshes).toBe(before.refreshes);
});

test('revoking on the forge shows Sign in again, which reconnects with the stored server and client ID', async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);
  await openSettings(page);
  await expect(connected(page)).toBeVisible();
  const before = (await forge(request)).counters;
  await control(request, { revoke: true });
  await testConnection(page, false);
  const again = panel(page).getByRole('button', { name: 'Sign in again' });
  await expect(again).toBeVisible();
  await expect(panel(page)).toContainText("Helm's sign-in to Gitea has ended.");
  await expect(panel(page).locator('.gitea-state')).toHaveText('Signed out');
  const forgeAfter = await forge(request);
  expect(forgeAfter.counters.api_unauthorized).toBe(before.api_unauthorized + 1);
  expect(forgeAfter.counters.refresh_failures).toBe(before.refresh_failures + 1);
  expect(forgeAfter.last_refresh_error).toBe('invalid_grant');
  expect(await helmStatus(request)).toMatchObject({ state: 'reconnect_required', client_id: clientId, server: { base_url: forgeURL } });
  await snap(page, testInfo, 'reconnect-required');

  // Nothing is typed: one press, then the forge's consent.
  await again.click();
  await consentPage(page, testInfo, 'forge-consent-again.png');
  expect((await forge(request)).last_authorize).toMatchObject({ client_id: clientId, redirect_uri: `${helmOrigin}${callbackPath}`, code_challenge_method: 'S256' });
  await page.getByRole('button', { name: 'Approve' }).click();
  await backInHelm(page);
  await expect(connected(page)).toHaveText(`Connected to ${forgeURL} as @helm-reader`);
  expect(await helmStatus(request)).toMatchObject({ state: 'connected', access: 'scoped', repository_count: 57, check: { state: 'ok' } });
  expectPrivateStateFile();
});

test('an expired access token is refreshed without the person noticing, also in one-time refresh mode', async ({ page, request }) => {
  await openSettings(page);
  await expect(connected(page)).toBeVisible();
  // Short lifetimes make every later call refresh first; no forced expiry
  // follows a refresh within the 60-second rejection window.
  await control(request, { token_lifetime: 120 });
  let before = (await control(request, { expire_access: true })).counters;
  await testConnection(page);
  await expect(liveRegion(page)).toHaveText('The connection works.');
  await expect(connected(page)).toHaveText(`Connected to ${forgeURL} as @helm-reader`);
  await expect(problem(page)).toHaveCount(0);
  let after = (await forge(request)).counters;
  expect(after.api_unauthorized, 'the expired token was refused once').toBe(before.api_unauthorized + 1);
  expect(after.refreshes, 'the refresh counter moved').toBeGreaterThan(before.refreshes);
  expect(after.refresh_failures).toBe(before.refresh_failures);

  // Forgejo's default: a spent refresh token is refused. Helm must always use the newest.
  before = (await control(request, { one_time_refresh: true })).counters;
  await testConnection(page);
  await testConnection(page);
  after = (await forge(request)).counters;
  expect(after.refreshes - before.refreshes, 'refreshes in one-time mode').toBeGreaterThanOrEqual(2);
  expect(after.refresh_reuse_refused).toBe(0);
  expect(after.refresh_failures).toBe(before.refresh_failures);
  await expect(connected(page)).toBeVisible();
  await expect(problem(page)).toHaveCount(0);
  expect((await helmStatus(request)).state).toBe('connected');

  // Back to the forge's defaults; the next due refresh brings a 1-hour token.
  await control(request, { one_time_refresh: false, token_lifetime: 3600 });
  await testConnection(page);
  expect((await helmStatus(request)).check.state).toBe('ok');
  expectPrivateStateFile();
});

test('a full-access grant shows the full-access warning', async ({ page, request }, testInfo) => {
  await openSettings(page);
  await control(request, { full_access: true });
  await testConnection(page);
  const full = panel(page).locator('[data-gitea-access="full"]');
  await expect(full).toContainText('This server gave Helm full access to this account.');
  await expect(panel(page).locator('[data-gitea-access="scoped"]')).toHaveCount(0);
  expect((await helmStatus(request)).access).toBe('full');
  await snap(page, testInfo, 'full-access');
  await control(request, { full_access: false });
  await testConnection(page);
  await expect(panel(page).locator('[data-gitea-access="scoped"]')).toHaveText('Read-only access');
});

test('the connection lives on the server: a reload and a new browser session still show it', async ({ page, browser }, testInfo) => {
  await openSettings(page);
  await page.reload();
  await expect(connected(page)).toHaveText(`Connected to ${forgeURL} as @helm-reader`);
  await expect(panel(page).locator('.gitea-repos-head')).toContainText('Helm can read 57 repositories');
  expect(await page.evaluate(() => sessionStorage.getItem('helm.gitea.draft'))).toBeNull();
  const fresh = await browser.newContext({ baseURL: helmOrigin });
  try {
    const other = await fresh.newPage();
    watch(other);
    await openSettings(other);
    await expect(connected(other)).toHaveText(`Connected to ${forgeURL} as @helm-reader`);
    await expect(panel(other).locator('.gitea-repos-head')).toContainText('Helm can read 57 repositories');
    await testInfo.attach('new-session.png', { contentType: 'image/png', body: await panel(other).screenshot() });
  } finally {
    await fresh.close();
  }
});

test('Disconnect asks first, deletes the connection file and returns the panel to step 1', async ({ page, request }, testInfo) => {
  await openSettings(page);
  await panel(page).getByRole('button', { name: 'Disconnect' }).click();
  const confirm = panel(page).locator('[data-gitea-disconnect-confirm]');
  await expect(confirm.getByRole('link', { name: 'Authorized OAuth2 Applications' })).toHaveAttribute('href', `${forgeURL}/user/settings/applications`);
  await snap(page, testInfo, 'disconnect-confirm');
  await confirm.getByRole('button', { name: 'Yes, disconnect' }).click();
  await expect(stepOne(page)).toBeVisible();
  await expect(addressField(page)).toHaveValue('');
  await expect(liveRegion(page)).toHaveText('Disconnected from Gitea.');
  expect((await helmStatus(request)).state).toBe('disconnected');
  expect(existsSync(stateFile()), 'gitea.json removed').toBe(false);
});

test('Deny on the forge keeps the typed values, and a confidential application is named', async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);
  await openSettings(page);
  await addressField(page).fill(forgeURL);
  await panel(page).locator('[data-gitea-http-warning]').getByRole('checkbox', { name: 'Continue without encryption' }).click();
  await expect(stepTwo(page)).toBeVisible();
  await paste(clientField(page), clientId);
  await signIn(page).click();
  await consentPage(page, testInfo, 'forge-consent-deny.png');
  await page.getByRole('button', { name: 'Deny' }).click();
  await backInHelm(page);
  const denied = panel(page).locator('[data-gitea-result="denied"]');
  await expect(denied).toContainText('Sign-in was cancelled on Gitea. Nothing was changed.');
  await expect(panel(page)).not.toContainText('fake-gitea');
  await expect(found(page)).toContainText(`Found Gitea 28.3.0 at ${forgeURL}`);
  await expect(clientField(page)).toHaveValue(clientId);
  expect(new URL(page.url()).search).toBe('');
  expect((await forge(request)).counters.denials).toBe(1);
  expect((await helmStatus(request)).state).toBe('disconnected');
  await snap(page, testInfo, 'denied');

  await control(request, { confidential: true });
  await signIn(page).click();
  await consentPage(page, testInfo, 'forge-consent-confidential.png');
  await page.getByRole('button', { name: 'Approve' }).click();
  await backInHelm(page);
  const confidential = panel(page).locator('[data-gitea-result="client_confidential"]');
  await expect(confidential).toContainText('The application is set as a Confidential Client. Edit it in Gitea, untick Confidential Client, save, then sign in again.');
  await expect(confidential.getByRole('link', { name: /Edit the application in Gitea/ })).toHaveAttribute('href', `${forgeURL}/user/settings/applications`);
  await expect(clientField(page)).toHaveValue(clientId);
  expect((await forge(request)).last_exchange_error).toBe('unauthorized_client');
  expect((await helmStatus(request)).state).toBe('disconnected');
  await snap(page, testInfo, 'client-confidential');
  await control(request, { confidential: false });
});

test('no forge secret reaches the server output, the database, Helm responses or the browser console', async ({}, testInfo) => {
  await leakScan(testInfo);
});
