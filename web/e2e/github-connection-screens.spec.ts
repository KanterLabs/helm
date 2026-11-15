import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { expect, test, type BrowserContext, type Page, type TestInfo } from '@playwright/test';
import { API, WEB } from './support/githubShared';
import {
  baseConnection, installConnectionMocks, pendingAttempt, type Attempt, type Connection, type ConnectionMock
} from './support/githubConnectionFixture';

// Screenshots of every C6 row (plus the confirmation and proxy-error states)
// at 1280 and 390 px, light and dark, and a 320 px overflow check.
// HELM_GITHUB_SCREENSHOT_DIR, when set, also receives a copy of each image.
test.use({ serviceWorkers: 'block' });

type Screen = {
  name: string;
  connection: Connection | undefined;
  repositories?: string[];
  /** Ready check once the panel has loaded, plus any interaction. */
  prepare: (page: Page, mock: ConnectionMock) => Promise<void>;
};

const ended = (state: string): Attempt => pendingAttempt(0, { state, user_code: undefined, expires_at: new Date(Date.now() - 60_000).toISOString() });
const panel = (page: Page) => page.locator('[data-github-connection]');
const view = (name: string) => async (page: Page) => { await expect(panel(page)).toHaveAttribute('data-connection-view', name); };

const screens: Screen[] = [
  { name: 'operator-token', connection: { mode: 'operator_token', signin_available: false, state: 'disconnected' },
    prepare: async (page) => { await expect(page.locator('[data-github-settings] [data-github-repositories]')).toBeVisible(); } },
  { name: 'signin-unavailable', connection: baseConnection('disconnected', { signin_available: false }), prepare: view('unavailable') },
  { name: 'disconnected', connection: baseConnection('disconnected'), prepare: view('disconnected') },
  { name: 'pending', connection: baseConnection('disconnected', { attempt: pendingAttempt(0) }), prepare: view('pending') },
  { name: 'pending-other-admin', connection: baseConnection('disconnected', { attempt: pendingAttempt(0, { user_code: undefined }) }), prepare: view('other_pending') },
  { name: 'expired', connection: baseConnection('disconnected', { attempt: ended('expired') }), prepare: view('expired') },
  { name: 'denied', connection: baseConnection('disconnected', { attempt: ended('denied') }), prepare: view('denied') },
  { name: 'failed', connection: baseConnection('disconnected', { attempt: ended('failed') }), prepare: view('failed') },
  { name: 'no-repositories', connection: baseConnection('no_repositories'), prepare: view('no_repositories') },
  { name: 'connected', connection: baseConnection('connected'), repositories: [WEB, API], prepare: view('connected') },
  { name: 'connected-truncated', connection: baseConnection('connected', { repositories_truncated: true }), repositories: Array.from({ length: 50 }, (_, i) => `demo-org/synthetic-repo-${i + 1}`), prepare: view('connected') },
  { name: 'disconnect-confirm', connection: baseConnection('connected'), repositories: [WEB, API],
    prepare: async (page) => {
      await view('connected')(page);
      await panel(page).getByRole('button', { name: 'Disconnect' }).click();
      await expect(panel(page).locator('[data-github-disconnect-confirm]')).toBeVisible();
    } },
  { name: 'reconnect-required', connection: baseConnection('reconnect_required', { login: 'synthetic-admin' }), prepare: view('reconnect_required') },
  { name: 'proxy-error', connection: baseConnection('disconnected'),
    prepare: async (page, mock) => {
      await view('disconnected')(page);
      mock.failNext('POST /integrations/github/connect', 'html502');
      await panel(page).getByRole('button', { name: 'Connect GitHub' }).click();
      await expect(panel(page).getByRole('alert')).toBeVisible();
    } }
];

const darkScreens = new Set(['disconnected', 'pending', 'pending-other-admin', 'expired', 'no-repositories', 'connected', 'disconnect-confirm', 'reconnect-required', 'proxy-error', 'signin-unavailable']);

/** Each state gets a fresh page, so init scripts and the clock never stack. */
async function show(context: BrowserContext, screen: Screen, width: number, theme: 'light' | 'dark'): Promise<Page> {
  const page = await context.newPage();
  await page.addInitScript((value) => { localStorage.setItem('helm.theme', value); }, theme);
  await page.setViewportSize({ width, height: width < 400 ? 844 : 900 });
  // The test clock lets an expired attempt read as expired without waiting.
  await page.clock.install();
  const mock = await installConnectionMocks(page, screen.connection, screen.repositories || []);
  await page.goto('/settings');
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
  await screen.prepare(page, mock);
  await page.locator('[data-github-settings]').scrollIntoViewIfNeeded();
  await page.mouse.move(0, 0);
  return page;
}

async function keep(page: Page, info: TestInfo, name: string): Promise<void> {
  // The phone app bars float over whatever is scrolled under them; hide them for the capture only.
  const body = await page.locator('[data-github-settings]').screenshot({ animations: 'disabled', style: '.topbar, .mobile-nav { visibility: hidden !important; }' });
  await info.attach(`${name}.png`, { body, contentType: 'image/png' });
  const dir = process.env.HELM_GITHUB_SCREENSHOT_DIR;
  if (dir) {
    await mkdir(dir, { recursive: true });
    await writeFile(path.join(dir, `${name}.png`), body);
  }
}

for (const width of [1280, 390]) {
  for (const theme of ['light', 'dark'] as const) {
    test(`screenshots of every state at ${width} px, ${theme}`, async ({ context }, info) => {
      test.setTimeout(180_000);
      for (const screen of screens) {
        if (theme === 'dark' && !darkScreens.has(screen.name)) continue;
        await test.step(screen.name, async () => {
          const page = await show(context, screen, width, theme);
          await keep(page, info, `${screen.name}-${width}-${theme}`);
          await page.close();
        });
      }
    });
  }
}

test('no sideways scrolling at 320 px in any state', async ({ context }, info) => {
  test.setTimeout(180_000);
  const report: Array<{ name: string; page: number; section: number; viewport: number; outside: string[] }> = [];
  for (const screen of screens) {
    await test.step(screen.name, async () => {
      const page = await show(context, screen, 320, 'light');
      const measured = await page.evaluate(() => {
        const section = document.querySelector('[data-github-settings]') as HTMLElement;
        const box = section.getBoundingClientRect();
        const outside = Array.from(section.querySelectorAll<HTMLElement>('button, a, code, [data-github-user-code], p, h3'))
          .filter((el) => { const r = el.getBoundingClientRect(); return r.width > 0 && (r.left < box.left - 0.5 || r.right > box.right + 0.5); })
          .map((el) => el.textContent?.trim().slice(0, 40) || el.tagName);
        return { page: document.documentElement.scrollWidth, section: section.scrollWidth - section.clientWidth, viewport: document.documentElement.clientWidth, outside };
      });
      report.push({ name: screen.name, ...measured });
      expect(measured.page, `${screen.name}: page scroll width`).toBeLessThanOrEqual(measured.viewport);
      expect(measured.section, `${screen.name}: section overflow`).toBeLessThanOrEqual(0);
      expect(measured.outside, `${screen.name}: controls outside the section`).toEqual([]);
      if (['pending', 'connected', 'disconnect-confirm', 'proxy-error'].includes(screen.name)) await keep(page, info, `${screen.name}-320-light`);
      await page.close();
    });
  }
  await info.attach('overflow-320.json', { body: JSON.stringify(report, null, 2), contentType: 'application/json' });
});
