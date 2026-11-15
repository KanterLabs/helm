import { expect, type APIRequestContext, type Browser, type BrowserContext, type Locator, type Page, type TestInfo } from '@playwright/test';

// Device-flow sign-in against the real binary (test/githubbeta, device-flow
// mode). Every login, code, token and repository here is invented.
export const baseURL = process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18082';
export const ADMIN_ONE = 'admin-one@synthetic.invalid';
export const ADMIN_TWO = 'admin-two@synthetic.invalid';
const PASSWORD = 'synthetic-admin-password-0001';
export const LOGIN = 'synthetic-octo';
export const BETA = 'synthetic/helm-beta';
export const DOCS = 'synthetic/helm-docs';
export const PR_TITLE = 'Synthetic merged implementation';
export const INSTALL_URL = 'https://github.com/apps/synthetic-helm-beta/installations/new';
export const AUTHORIZATIONS_URL = 'https://github.com/settings/apps/authorizations';
export const USER_CODE = /^WPFV-\d{4}$/;
// Split so Playwright's bundled source snapshots never hold a whole sentinel.
export const SENTINELS = [
  ['HELM_DEVICE_ACCESS_', 'SENTINEL_DO_NOT_EXPOSE_', '9d41e2'],
  ['HELM_DEVICE_REFRESH_', 'SENTINEL_DO_NOT_EXPOSE_', '5ac07b'],
  ['HELM_DEVICE_CODE_', 'SENTINEL_DO_NOT_EXPOSE_', 'e37f10']
].map((parts) => parts.join(''));
export const REFRESH_DUE = 8 * 3600 - 5 * 60; // inside the 10-minute refresh margin

export type Credential = { exists: boolean; regular: boolean; file_mode: string; dir_mode: string };
export type Upstream = {
  attempts: number; pending: boolean; token_seq: number; refreshes: number; token_polls: number;
  installed: string[]; granted: boolean; upstream_requests: number; clock_offset_seconds: number; credential: Credential;
};
export type Control = {
  action?: 'approve' | 'deny' | 'expire'; user_code?: string; installed?: string[];
  reject_access?: boolean; fail_next_refresh?: boolean; advance_seconds?: number; reset_clock?: boolean;
};

const headers = (version?: number) => ({ Origin: new URL(baseURL).origin, ...(version === undefined ? {} : { 'If-Match': `"v${version}"` }) });

export function leaked(text: string): boolean {
  return SENTINELS.some((sentinel) => text.includes(sentinel));
}

/** Calls Helm's API as the context's administrator; fails on any sentinel. */
export async function api(request: APIRequestContext, method: string, path: string, body?: unknown, version?: number) {
  const response = await request.fetch(`/api/v1${path}`, { method, data: body, headers: headers(version) });
  const text = await response.text();
  expect(leaked(text), `${method} ${path} must not carry a synthetic secret`).toBeFalsy();
  expect(response.ok(), `${method} ${path}: ${response.status()} ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : null;
}

/** The status of an API call that is expected to be refused. */
export async function refused(request: APIRequestContext, method: string, path: string, body?: unknown, version?: number) {
  const response = await request.fetch(`/api/v1${path}`, { method, data: body, headers: headers(version) });
  expect(leaked(await response.text())).toBeFalsy();
  return response.status();
}

/** Drives the synthetic GitHub; with no body it only reads its counters. */
export async function upstream(request: APIRequestContext, body?: Control): Promise<Upstream> {
  const response = body ? await request.post('/__githubbeta/device', { data: body }) : await request.get('/__githubbeta/device');
  expect(response.ok(), `control ${JSON.stringify(body)}: ${response.status()}`).toBeTruthy();
  return response.json();
}

export async function signIn(context: BrowserContext, email: string): Promise<void> {
  const response = await context.request.post('/api/v1/auth/login', { headers: headers(), data: { email, password: PASSWORD } });
  expect(response.status(), `local login for ${email}`).toBe(200);
}

export async function adminContext(browser: Browser, email: string): Promise<BrowserContext> {
  const context = await browser.newContext({ baseURL });
  await signIn(context, email);
  return context;
}

/** Fails the test if any API answer the page receives carries a secret. */
export function watchResponses(page: Page, extra: () => string[] = () => []): string[] {
  const leaks: string[] = [];
  page.on('response', async (response) => {
    if (!response.url().includes('/api/')) return;
    try {
      const text = await response.text();
      if (leaked(text) || extra().some((value) => value && text.includes(value))) leaks.push(new URL(response.url()).pathname);
    } catch { /* a navigation can close a response */ }
  });
  return leaks;
}

/** A fresh project with one task, created by the signed-in administrator. */
export async function seed(request: APIRequestContext) {
  const suffix = Date.now().toString(36).slice(-6).toUpperCase();
  const project = await api(request, 'POST', '/projects', { key: `DF${suffix}`, name: `Device flow ${suffix}` });
  const task = await api(request, 'POST', `/projects/${project.id}/tasks`, { title: 'Ship the device-flow proof' });
  return { project, task };
}

export const panelOf = (page: Page) => page.locator('[data-github-connection]');
export const settingsOf = (page: Page) => page.locator('[data-github-settings]');

export async function readCode(page: Page): Promise<string> {
  const code = panelOf(page).locator('[data-github-user-code]');
  await expect(code).toHaveText(USER_CODE);
  return (await code.innerText()).trim();
}

/**
 * Screenshots at 1280 and 390 px wide (amendment A5), proving no sideways
 * scroll. The settings section is captured, or the viewport scrolled to
 * `focus`. Phone shots are of the viewport: an element shot there is
 * overlaid by the fixed app bars.
 */
export async function shoot(page: Page, info: TestInfo, name: string, focus?: Locator): Promise<void> {
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width === 1280 ? 900 : 844 });
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), { message: `${name} overflows at ${width}px` }).toBeTruthy();
    const path = info.outputPath(`${name}-${width}.png`);
    const target = focus || settingsOf(page);
    if (width === 1280 && !focus) await target.screenshot({ path });
    else {
      await target.evaluate((element) => element.scrollIntoView({ block: 'center' }));
      await page.screenshot({ path });
    }
  }
  await page.setViewportSize({ width: 1280, height: 900 });
}

/** A sign-in that has not been checked yet is not reported as a failure. */
export async function expectHealthy(page: Page): Promise<void> {
  await expect(settingsOf(page).locator('.github-state')).toHaveText('Connected');
  await expect(panelOf(page).locator('[data-github-check-note]')).toHaveCount(0);
  await expect(panelOf(page)).not.toContainText('could not reach GitHub');
}

/** Connect GitHub, then approve on the synthetic GitHub with the code shown. */
export async function connect(page: Page, installed: string[]): Promise<void> {
  await upstream(page.request, { installed });
  await panelOf(page).getByRole('button', { name: /^(Connect GitHub|Reconnect GitHub|Try again)$/ }).click();
  await upstream(page.request, { action: 'approve', user_code: await readCode(page) });
  const done = installed.length ? `Connected as @${LOGIN}` : 'One more step: choose repositories';
  await expect(panelOf(page)).toContainText(done, { timeout: 25_000 });
}

/** Test connection and wait for its result to settle. */
export async function testConnection(page: Page, label = 'Test connection'): Promise<void> {
  const answered = page.waitForResponse((response) => new URL(response.url()).pathname === '/api/v1/integrations/github/check');
  await panelOf(page).getByRole('button', { name: label }).click();
  await answered;
}

export async function artifact(request: APIRequestContext, taskID: string) {
  const list = (await api(request, 'GET', `/tasks/${taskID}/artifacts`)).data;
  expect(list).toHaveLength(1);
  return list[0];
}
