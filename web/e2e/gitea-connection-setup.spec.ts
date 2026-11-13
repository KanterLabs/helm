import { expect, test, type Locator, type Page } from '@playwright/test';
import {
  addressField, BASE, CLIENT_ID, clientField, connectedStatus, DRAFT_KEY, expectNoIdempotencyKeys, installGiteaMocks,
  liveRegion, openSettings, panel, probeFor, REDIRECT_URI, snap
} from './support/giteaFixture';

// WP4 of docs/GITEA_OAUTH_PLAN.md: steps 1 and 2 of docs/GITEA_OAUTH_UX.md
// against synthetic routes. Failure list: src/lib/components/gitea/failures.md.
test.use({ serviceWorkers: 'block' });

async function paste(field: Locator, text: string): Promise<void> {
  await field.focus();
  await field.evaluate((node, value) => {
    const data = new DataTransfer();
    data.setData('text/plain', value);
    node.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
  }, text);
}

function gate(): { promise: Promise<void>; release: () => void } {
  let release = () => undefined as void;
  const promise = new Promise<void>((resolve) => { release = resolve; });
  return { promise, release };
}

const stepTwo = (page: Page) => panel(page).getByRole('heading', { name: /Create Helm's application in/ });
const signIn = (page: Page) => panel(page).getByRole('button', { name: /Sign in with (Gitea|Forgejo)/ });

test('an address, a pasted client ID and one press reach the forge, and a failed sign-in keeps the values', async ({ page, context }, testInfo) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const mock = await installGiteaMocks(page);
  const held = gate();
  mock.probe = async (body) => { await held.promise; return probeFor(body); };
  await openSettings(page);
  await expect(panel(page).getByRole('heading', { name: 'Gitea', exact: true })).toBeVisible();
  const field = addressField(page);
  await expect(field).toHaveAttribute('placeholder', 'gitea.example.com');
  for (const [name, value] of [['inputmode', 'url'], ['autocapitalize', 'off'], ['autocomplete', 'off'], ['spellcheck', 'false']]) await expect(field).toHaveAttribute(name, value);
  await expect(stepTwo(page)).toHaveCount(0);
  await snap(page, testInfo, 'empty');

  // Typing sends one check after the pause, not one per key.
  await field.pressSequentially('gitea.example.com', { delay: 40 });
  await expect(panel(page).locator('[data-gitea-checking]')).toContainText('Checking gitea.example.com');
  await snap(page, testInfo, 'checking');
  held.release();
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('Found Gitea 28.3.0 at gitea.example.com');
  await expect(stepTwo(page)).toBeFocused();
  await expect(liveRegion(page)).toContainText('Found Gitea 28.3.0 at gitea.example.com');
  expect(mock.count('POST', '/probe')).toBe(1);
  mock.probe = undefined;

  // Copy, don't type.
  await expect(panel(page).getByRole('link', { name: /Open Gitea's application page/ })).toHaveAttribute('href', `${BASE}/user/settings/applications`);
  await expect(panel(page).getByRole('link', { name: /Open Gitea's application page/ })).toHaveAttribute('target', '_blank');
  await expect(panel(page).locator('[data-gitea-redirect-uri]')).toHaveText(REDIRECT_URI);
  await panel(page).getByRole('button', { name: 'Copy redirect URI' }).click();
  await expect(liveRegion(page)).toContainText('Copied redirect URI');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(REDIRECT_URI);
  await panel(page).getByRole('button', { name: 'Copy application name' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('Helm');
  await expect(panel(page)).toContainText('Untick "Confidential Client"');
  await expect(panel(page)).toContainText('Unregistered Redirect URI');

  // The ID is extracted from a pasted line; a wrong shape keeps the button off.
  await expect(signIn(page)).toBeDisabled();
  await clientField(page).fill('not a client id!');
  await clientField(page).blur();
  await expect(panel(page).locator('#gitea-client-error')).toContainText('does not look like a Client ID');
  await expect(clientField(page)).toHaveAttribute('aria-describedby', /gitea-client-error/);
  await expect(signIn(page)).toBeDisabled();
  await clientField(page).fill('');
  await paste(clientField(page), `  Client ID: ${CLIENT_ID}\n`);
  await expect(clientField(page)).toHaveValue(CLIENT_ID);
  await paste(clientField(page), `   ${CLIENT_ID}   `);
  await expect(clientField(page)).toHaveValue(CLIENT_ID);
  await expect(signIn(page)).toBeEnabled();
  await snap(page, testInfo, 'step-2');

  // One sign-in, however fast the clicks.
  const start = gate();
  mock.start = async () => { await start.promise; return undefined; };
  await signIn(page).dblclick();
  await expect(signIn(page)).toBeDisabled();
  await expect(signIn(page).locator('.button-spinner')).toBeVisible();
  await snap(page, testInfo, 'signing-in');
  start.release();
  await page.waitForURL(/^https:\/\/gitea\.example\.com\/login\/oauth\/authorize/);
  expect(mock.count('POST', '/oauth/start')).toBe(1);
  expect(mock.requests.find((item) => item.path === '/oauth/start')?.body).toEqual({ base_url: BASE, client_id: CLIENT_ID, allow_insecure_http: false });

  // Back from the forge with an error: values are intact, the URL is clean.
  await page.goto('/settings?gitea_error=denied');
  await expect(page).toHaveURL(/\/settings$/);
  const result = panel(page).locator('[data-gitea-result]');
  await expect(result).toContainText('Sign-in was cancelled on Gitea. Nothing was changed.');
  await expect(result).toBeFocused();
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('gitea.example.com');
  await expect(clientField(page)).toHaveValue(CLIENT_ID);
  await expect(signIn(page)).toBeEnabled();
  await snap(page, testInfo, 'returned-denied');
  expect(await page.evaluate((key) => sessionStorage.getItem(key), DRAFT_KEY)).toContain(CLIENT_ID);
  expect(await page.evaluate((key) => sessionStorage.getItem(key), DRAFT_KEY)).not.toContain('token');
  expectNoIdempotencyKeys(mock);
});

test('an http address needs explicit consent before anything proceeds', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page);
  await openSettings(page);
  await addressField(page).fill('http://gitea.example.com');
  await addressField(page).press('Enter');
  const warning = panel(page).locator('[data-gitea-http-warning]');
  await expect(warning).toContainText('This address uses http, so sign-in details could be read on the network.');
  await expect(stepTwo(page)).toHaveCount(0);
  await snap(page, testInfo, 'http-warning');
  await panel(page).getByRole('button', { name: 'Continue' }).click();
  await expect(stepTwo(page)).toHaveCount(0);
  expect(mock.count('POST', '/probe')).toBe(1);
  await warning.getByRole('checkbox', { name: 'Continue without encryption' }).click();
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('Found Gitea 28.3.0 at http://gitea.example.com');
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('without encryption');
  expect(mock.requests.filter((item) => item.path === '/probe').map((item) => item.body?.allow_insecure_http)).toEqual([false, true]);
  await snap(page, testInfo, 'http-found');
});

test('every check failure names its fix in place', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page);
  await openSettings(page);
  const field = addressField(page);
  const error = panel(page).locator('#gitea-address-error');
  const cases: Array<[string, string, string]> = [
    ['invalid_server_url', 'not a host', 'That does not look like a server address. Try gitea.example.com.'],
    ['server_unreachable', 'unreachable.example.com', 'Helm could not reach that server. Check the address, and that the Helm server can reach it on your network.'],
    ['tls_error', 'badcert.example.com', "Helm does not trust that server's certificate. If it uses a private certificate authority, see the setup guide."],
    ['not_a_forge', 'notaforge.example.com/some/page', 'That server did not answer like Gitea or Forgejo. If Gitea lives under a path, include it.'],
    ['issuer_mismatch', 'moved.example.com', 'This server says its address is https://git.example.com.'],
    ['other', 'broken.example.com', 'Something went wrong connecting to Gitea.']
  ];
  for (const [code, input, message] of cases) {
    await field.fill(input);
    await field.press('Enter');
    await expect(error).toHaveText(message);
    await expect(field).toBeFocused();
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(field).toHaveAttribute('aria-describedby', /gitea-address-error/);
    await expect(panel(page)).not.toContainText('synthetic-leak');
    await snap(page, testInfo, `error-${code}`);
  }
  // Actions offered with the message.
  await field.fill('unreachable.example.com');
  await field.press('Enter');
  const before = mock.count('POST', '/probe');
  await panel(page).getByRole('button', { name: 'Try again' }).click();
  await expect.poll(() => mock.count('POST', '/probe')).toBe(before + 1);
  await field.fill('badcert.example.com');
  await field.press('Enter');
  await expect(panel(page).getByRole('button', { name: 'Open the setup guide' })).toBeVisible();
  await field.fill('moved.example.com');
  await field.press('Enter');
  await panel(page).getByRole('button', { name: 'Use that address' }).click();
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('Found Gitea 28.3.0 at git.example.com');
  await panel(page).getByRole('button', { name: 'Change server address' }).click();
  await expect(field).toBeFocused();
  await expect(field).toHaveValue('https://git.example.com');
  await field.fill('busy.example.com');
  await field.press('Enter');
  await expect(error).toHaveText('Helm is already connected. Disconnect first.');
  await snap(page, testInfo, 'error-already_connected');
  mock.status = connectedStatus();
  await panel(page).getByRole('button', { name: 'Show the connection' }).click();
  await expect(panel(page).getByRole('heading', { name: /Connected to gitea\.example\.com as @helm-reader/ })).toBeVisible();
  expectNoIdempotencyKeys(mock);
});

test('a newer check wins, Enter then Tab checks once, and Forgejo is named Forgejo', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page);
  const slow = gate();
  mock.probe = async (body) => { if (String(body.base_url).startsWith('slow')) await slow.promise; return probeFor(body); };
  await openSettings(page);
  const field = addressField(page);
  await field.fill('slow.example.com');
  await field.press('Enter');
  await expect(panel(page).locator('[data-gitea-checking]')).toBeVisible();
  await field.fill('gitea.example.com');
  await field.press('Enter');
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('at gitea.example.com');
  slow.release();
  await page.waitForTimeout(200);
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('at gitea.example.com');

  await panel(page).getByRole('button', { name: 'Change server address' }).click();
  await field.fill('forgejo.example.com');
  await field.press('Enter');
  await page.keyboard.press('Tab');
  await expect(panel(page).locator('[data-gitea-found]')).toContainText('Found Forgejo 11.0.1+gitea-1.22.0 at forgejo.example.com');
  expect(mock.requests.filter((item) => item.path === '/probe' && item.body?.base_url === 'forgejo.example.com')).toHaveLength(1);
  await expect(panel(page).getByRole('heading', { name: 'Forgejo', exact: true })).toBeVisible();
  await expect(stepTwo(page)).toHaveText("Create Helm's application in Forgejo");
  await expect(panel(page).getByRole('link', { name: /Open Forgejo's application page/ })).toBeVisible();
  await expect(signIn(page)).toHaveText(/Sign in with Forgejo/);
  await expect(panel(page)).not.toContainText('Gitea shows you');
  await snap(page, testInfo, 'forgejo-step-2');
});

test('a sign-in address outside the probed server is never opened', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page);
  await openSettings(page);
  await addressField(page).fill('gitea.example.com');
  await addressField(page).press('Enter');
  await clientField(page).fill(CLIENT_ID);
  for (const target of ['https://evil.example.net/login/oauth/authorize?x=1', 'https://gitea.example.com.evil.example/login/oauth/authorize']) {
    mock.start = () => ({ status: 200, body: { authorize_url: target } });
    await signIn(page).click();
    await expect(panel(page).locator('[data-gitea-result]')).toContainText('Helm did not open the sign-in page');
    await expect(page).toHaveURL(/\/settings$/);
    await expect(signIn(page)).toBeEnabled();
  }
  await snap(page, testInfo, 'authorize-refused');
});

test('copying reports a denied clipboard and selects the value instead', async ({ page }, testInfo) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: () => Promise.reject(new DOMException('Denied', 'NotAllowedError')) } });
  });
  await installGiteaMocks(page);
  await openSettings(page);
  await addressField(page).fill('gitea.example.com');
  await addressField(page).press('Enter');
  await panel(page).getByRole('button', { name: 'Copy redirect URI' }).click();
  await expect(liveRegion(page)).toContainText('Copying did not work');
  await expect(panel(page).locator('[data-gitea-copy-failed]')).toBeVisible();
  expect(await page.evaluate(() => String(window.getSelection()))).toBe(REDIRECT_URI);
  await snap(page, testInfo, 'copy-denied');
});

test('the first load shows a skeleton and a failed load offers a retry', async ({ page }, testInfo) => {
  const mock = await installGiteaMocks(page);
  const held = gate();
  mock.statusGate = held.promise;
  mock.statusFails = 1;
  await page.goto('/settings');
  await expect(panel(page).locator('[data-gitea-loading]')).toBeVisible();
  await snap(page, testInfo, 'loading');
  held.release();
  mock.statusGate = undefined;
  await expect(panel(page).locator('[data-gitea-load-error]')).toContainText('Helm could not load the Gitea connection.');
  await snap(page, testInfo, 'load-failed');
  await panel(page).getByRole('button', { name: 'Retry' }).click();
  await expect(addressField(page)).toBeVisible();
});
