import { expect, test, type Page, type Route } from '@playwright/test';
import {
  A3_MESSAGE, AUTHORIZATIONS_URL, baseConnection, CODES, DEVICE_URL, installConnectionMocks, leaveSettings, LOGIN,
  pendingAttempt, setHidden, type Connection
} from './support/githubConnectionFixture';
import { project } from './support/githubShared';

// Device-flow connection panel against synthetic routes (contracts C5/C6).
// Covers web/src/lib/components/github/failures.md; every value is invented.
test.use({ serviceWorkers: 'block' });

const panelOf = (page: Page) => page.locator('[data-github-connection]');
const liveOf = (page: Page) => page.locator('[data-github-connection-live]');
const posts = (calls: string[], path: string) => calls.filter((call) => call === `POST /integrations/github${path}`).length;

async function open(page: Page, connection: Connection | undefined, repositories: string[] = []) {
  await page.clock.install();
  const mock = await installConnectionMocks(page, connection, repositories);
  await page.goto('/settings');
  if (connection?.mode === 'device_flow') await expect(panelOf(page)).toBeVisible();
  return mock;
}

/** Let the panel's 3-second poll timer fire `times` times. */
async function pollTicks(page: Page, times = 1) {
  for (let i = 0; i < times; i += 1) await page.clock.runFor(3_100);
}

test('connects with one press, a copied code and no refresh', async ({ page, context }, testInfo) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const mock = await open(page, baseConnection('disconnected'));
  const panel = panelOf(page);
  await expect(liveOf(page)).toHaveAttribute('aria-live', 'polite');
  await expect(panel.getByRole('textbox')).toHaveCount(0);
  const connect = panel.getByRole('button', { name: 'Connect GitHub' });
  await connect.focus();
  await page.keyboard.press('Enter');
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[1]);
  await expect(panel.getByRole('heading', { name: 'Enter this code on GitHub' })).toBeFocused();
  await expect(liveOf(page)).toContainText('Enter it on GitHub');
  await expect(panel.locator('[data-github-time-left]')).toContainText(/1[45]:\d\d/);
  const link = panel.getByRole('link', { name: /Open GitHub/ });
  await expect(link).toHaveAttribute('href', DEVICE_URL);
  await expect(link).toHaveAttribute('target', '_blank');
  await expect(link).toHaveAttribute('rel', /noopener/);
  await page.keyboard.press('Tab');
  await expect(panel.getByRole('button', { name: 'Copy code' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(panel.locator('[data-github-copy-status]')).toHaveText('Copied. Paste it on GitHub.');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(CODES[1]);
  await page.keyboard.press('Tab');
  await expect(link).toBeFocused();
  await testInfo.attach('pending-code.png', { body: await page.locator('[data-github-settings]').screenshot(), contentType: 'image/png' });

  mock.approve();
  await pollTicks(page);
  await expect(panel).toContainText(`Connected as @${LOGIN}`);
  await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
  await expect(liveOf(page)).toContainText(`GitHub is connected as @${LOGIN}`);
  await expect(page.locator('[data-github-settings] [data-github-repositories]')).toBeVisible();
  expect(posts(mock.calls, '/connect')).toBe(1);
  await testInfo.attach('calls.json', { body: JSON.stringify(mock.calls, null, 2), contentType: 'application/json' });
});

test('a double click starts one attempt', async ({ page }) => {
  const mock = await open(page, baseConnection('disconnected'));
  await panelOf(page).getByRole('button', { name: 'Connect GitHub' }).evaluate((button: HTMLButtonElement) => { button.click(); button.click(); });
  await expect(panelOf(page).locator('[data-github-user-code]')).toHaveText(CODES[1]);
  expect(posts(mock.calls, '/connect')).toBe(1);
  mock.set({ attempt: undefined });
  await page.reload();
  await panelOf(page).getByRole('button', { name: 'Connect GitHub' }).dblclick();
  await expect(panelOf(page).locator('[data-github-user-code]')).toHaveText(CODES[2]);
  expect(posts(mock.calls, '/connect')).toBe(2);
});

test('never shows an old code once a new attempt starts', async ({ page }, testInfo) => {
  const mock = await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0) }));
  const panel = panelOf(page);
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[0]);
  // Another administrator replaces the attempt: the old code must go.
  mock.otherAdminConnects();
  await pollTicks(page);
  await expect(panel).toContainText('Another administrator is connecting GitHub');
  await expect(panel).not.toContainText(CODES[0]);
  // Taking over: while the request is in flight, no code at all.
  let releaseConnect = () => undefined as void;
  const gate = new Promise<void>((resolve) => { releaseConnect = resolve; });
  await page.route('**/api/v1/integrations/github/connect', async (route: Route) => { await gate; await route.fallback(); });
  await panel.getByRole('button', { name: 'Connect it myself instead' }).click();
  await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
  await expect(panel.getByRole('button', { name: /Connect/ })).toHaveCount(0);
  await expect(panel).toContainText('Getting a code from GitHub');
  releaseConnect();
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[2]);
  await expect(panel).not.toContainText(CODES[0]);
  await expect(panel).not.toContainText(CODES[1]);
  await testInfo.attach('replaced-code.png', { body: await page.locator('[data-github-settings]').screenshot(), contentType: 'image/png' });
});

test('says another administrator is connecting, without a code', async ({ page }) => {
  const mock = await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0, { user_code: undefined }) }));
  const panel = panelOf(page);
  await expect(panel).toContainText('Another administrator is connecting GitHub');
  await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Copy code' })).toHaveCount(0);
  await expect(panel.getByRole('link', { name: /Open GitHub/ })).toHaveCount(0);
  mock.approve();
  await pollTicks(page);
  await expect(panel).toContainText(`Connected as @${LOGIN}`);
});

test('removes an expired code and offers Try again', async ({ page }, testInfo) => {
  const mock = await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0, { expires_at: new Date(Date.now() + 70_000).toISOString() }) }));
  const panel = panelOf(page);
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[0]);
  await expect(panel.locator('[data-github-time-left]')).toContainText(/1:(0\d|1\d)/);
  await page.clock.runFor(72_000);
  await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
  await expect(panel.getByRole('link', { name: /Open GitHub/ })).toHaveCount(0);
  await expect(panel).toContainText('The code expired before it was entered on GitHub.');
  await expect(liveOf(page)).toContainText('The code expired');
  await testInfo.attach('expired.png', { body: await page.locator('[data-github-settings]').screenshot(), contentType: 'image/png' });
  // The server still says "pending" with a past expiry: the panel keeps it expired and stops polling.
  const gets = mock.gets();
  await pollTicks(page, 3);
  expect(mock.gets()).toBe(gets);
  // By now the server has marked it expired too, so Begin makes a new attempt.
  mock.set({ attempt: { ...mock.connection!.attempt!, state: 'expired', user_code: undefined } });
  await panel.getByRole('button', { name: 'Try again' }).click();
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[1]);
  expect(posts(mock.calls, '/connect')).toBe(1);
});

test('reports a blocked clipboard honestly and selects the code', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: () => Promise.reject(new DOMException('Synthetic denial', 'NotAllowedError')) } });
  });
  await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0) }));
  const panel = panelOf(page);
  await panel.getByRole('button', { name: 'Copy code' }).click();
  await expect(panel.locator('[data-github-copy-status]')).toHaveText('Your browser blocked copying. The code is selected: press Ctrl+C (⌘C on a Mac) to copy it.');
  await expect(panel.locator('[data-github-copy-status]')).not.toContainText('Copied.');
  expect(await page.evaluate(() => window.getSelection()?.toString())).toBe(CODES[0]);
});

test('never links anywhere but https://github.com/', async ({ page }) => {
  const mock = await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0, { verification_uri: 'https://github.com.example/login/device' }) }));
  const panel = panelOf(page);
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[0]);
  await expect(panel.getByRole('link', { name: /Open GitHub/ })).toHaveCount(0);
  await expect(panel).toContainText('github.com/login/device');
  mock.set({ state: 'connected', login: LOGIN, attempt: undefined, install_url: 'https://evil.example/apps/x/installations/new', authorizations_url: 'http://github.com/settings/apps/authorizations' });
  mock.repositories = ['demo-org/helm-web'];
  await page.reload();
  await expect(panel).toContainText(`Connected as @${LOGIN}`);
  await expect(panel.getByRole('link', { name: /Choose repositories on GitHub/ })).toHaveCount(0);
  await panel.getByRole('button', { name: 'Disconnect' }).click();
  await expect(panel).toContainText('Helm cannot remove its access on GitHub by itself');
  for (const href of await panel.locator('a').evaluateAll((links) => links.map((a) => a.getAttribute('href') || ''))) {
    expect(href.startsWith('https://github.com/')).toBe(true);
  }
  expect(await panel.locator('a').count()).toBe(0);
});

test('disconnect asks first and explains GitHub-side revocation', async ({ page }, testInfo) => {
  const mock = await open(page, baseConnection('connected'), ['demo-org/helm-web', 'demo-org/helm-api']);
  const panel = panelOf(page);
  await expect(panel).toContainText(`Connected as @${LOGIN}`);
  await expect(panel).toContainText('2 repositories');
  await panel.getByRole('button', { name: 'Disconnect' }).click();
  const confirm = panel.locator('[data-github-disconnect-confirm]');
  await expect(confirm.getByRole('heading', { name: 'Disconnect GitHub?' })).toBeFocused();
  await expect(confirm).toContainText('Helm cannot remove its access on GitHub by itself');
  const revoke = confirm.getByRole('link', { name: /authorized GitHub Apps/ });
  await expect(revoke).toHaveAttribute('href', AUTHORIZATIONS_URL);
  await expect(revoke).toHaveAttribute('target', '_blank');
  await testInfo.attach('disconnect-confirm.png', { body: await page.locator('[data-github-settings]').screenshot(), contentType: 'image/png' });
  await confirm.getByRole('button', { name: 'Keep connected' }).click();
  await expect(confirm).toHaveCount(0);
  await panel.getByRole('button', { name: 'Disconnect' }).click();
  await page.keyboard.press('Escape');
  await expect(confirm).toHaveCount(0);
  expect(posts(mock.calls, '/disconnect')).toBe(0);
  await panel.getByRole('button', { name: 'Disconnect' }).click();
  await confirm.getByRole('button', { name: 'Disconnect GitHub' }).click();
  await expect(panel.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
  await expect(liveOf(page)).toContainText('GitHub is disconnected');
  await expect(page.locator('[data-github-settings] [data-github-repositories]')).toHaveCount(0);
  expect(posts(mock.calls, '/disconnect')).toBe(1);
});

test('an HTML 502 from a proxy is not blamed on GitHub', async ({ page }, testInfo) => {
  const mock = await open(page, baseConnection('disconnected'));
  const panel = panelOf(page);
  const alert = panel.getByRole('alert').locator('[data-github-error]');
  const connect = panel.getByRole('button', { name: 'Connect GitHub' });
  const clean = async () => {
    for (const raw of ['502', 'Bad gateway', 'DOCTYPE', 'github_', 'Synthetic', 'request_failed']) await expect(panel).not.toContainText(raw);
  };
  mock.failNext('POST /integrations/github/connect', 'html502');
  await connect.click();
  await expect(alert).toHaveText(A3_MESSAGE);
  await clean();
  await testInfo.attach('proxy-502.png', { body: await page.locator('[data-github-settings]').screenshot(), contentType: 'image/png' });
  // A network failure (no answer at all) switches the whole app to its offline
  // viewer before the panel can speak, so it is not driven here.
  mock.failNext('POST /integrations/github/connect', { status: 424, code: 'github_unreachable', message: 'Synthetic GitHub said bad_verification_code' });
  await connect.click();
  await expect(alert).toHaveText('Helm could not reach GitHub. Wait a minute, then try again.');
  await clean();
  mock.failNext('POST /integrations/github/connect', { status: 409, code: 'github_signin_unavailable', message: 'Synthetic unavailable' });
  await connect.click();
  await expect(alert).toContainText('GitHub sign-in is not set up on this Helm');
  await clean();
  // A failed poll keeps the code and says so plainly.
  await connect.click();
  await expect(panel.locator('[data-github-user-code]')).toBeVisible();
  await expect(alert).toHaveCount(0);
  mock.failNext('GET /integrations/github', 'html502');
  await pollTicks(page);
  await expect(panel.locator('[data-github-poll-note]')).toHaveText('Helm did not get a proper answer from its own server. It will check again in a few seconds.');
  await expect(panel.locator('[data-github-user-code]')).toBeVisible();
  await clean();
  await pollTicks(page);
  await expect(panel.locator('[data-github-poll-note]')).toHaveCount(0);
  // The first load of the panel follows the same rule.
  mock.failNext('GET /integrations/github', 'html502');
  await page.reload();
  await expect(page.locator('[data-github-settings]').getByRole('alert')).toContainText(A3_MESSAGE);
  await expect(page.locator('[data-github-settings]')).not.toContainText('DOCTYPE');
});

test('never overlaps polls and ignores an answer older than an action', async ({ page }) => {
  const mock = await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0) }));
  const panel = panelOf(page);
  await expect(panel.locator('[data-github-user-code]')).toHaveText(CODES[0]);
  const start = mock.gets();
  mock.holdGets = true;
  await pollTicks(page);
  await expect.poll(() => mock.gets()).toBe(start + 1);
  await pollTicks(page, 4);
  expect(mock.gets()).toBe(start + 1);
  await panel.getByRole('button', { name: 'Cancel' }).click();
  await expect(panel.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
  await mock.release();
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
  mock.holdGets = false;
  const after = mock.gets();
  await pollTicks(page, 4);
  expect(mock.gets()).toBe(after);
});

test('stops polling when hidden and after leaving Settings', async ({ page }) => {
  const mock = await open(page, baseConnection('disconnected', { attempt: pendingAttempt(0) }));
  await expect(panelOf(page).locator('[data-github-user-code]')).toHaveText(CODES[0]);
  await pollTicks(page, 2);
  await expect.poll(() => mock.gets()).toBeGreaterThanOrEqual(3);
  await setHidden(page, true);
  const hidden = mock.gets();
  await pollTicks(page, 4);
  expect(mock.gets()).toBe(hidden);
  await setHidden(page, false);
  await expect.poll(() => mock.gets()).toBe(hidden + 1);
  await leaveSettings(page, project.slug);
  await expect(panelOf(page)).toHaveCount(0);
  const left = mock.gets();
  await pollTicks(page, 4);
  expect(mock.gets()).toBe(left);
});

test('operator_token and an older server render today\'s panel unchanged', async ({ page }, testInfo) => {
  const mock = await open(page, undefined);
  const settings = page.locator('[data-github-settings]');
  await expect(settings.locator('[data-github-repositories]')).toBeVisible();
  await expect(settings.getByRole('button', { name: 'Test connection' })).toBeEnabled();
  const older = await settings.innerText();
  mock.connection = { mode: 'operator_token', signin_available: false, state: 'disconnected' };
  await page.reload();
  await expect(settings.locator('[data-github-repositories]')).toBeVisible();
  expect(await settings.innerText()).toBe(older);
  await expect(panelOf(page)).toHaveCount(0);
  await expect(settings.getByRole('button', { name: /Connect GitHub|Disconnect/ })).toHaveCount(0);
  await expect(settings.locator('.github-state')).toHaveText('Connected');
  await testInfo.attach('operator-token.png', { body: await settings.screenshot(), contentType: 'image/png' });
});
