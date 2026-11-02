import { expect, test, type Locator } from '@playwright/test';
import { DatabaseSync } from 'node:sqlite';
import { attachMaskedScreenshot, hasDatabase, requireDisabledAuth } from './support/agentTokens';

// Failure cases recorded before the fix: a long account name/email expands
// the footer's implicit grid track, pushing Settings/Metrics and the account
// chevron into the workspace. Constraining the footer must still allow its
// account popup to open, including in the collapsed rail and mobile drawer.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

async function expectFooterContained(sidebar: Locator) {
  const overflow = await sidebar.evaluate((nav) => {
    const edge = nav.getBoundingClientRect();
    return Array.from(nav.querySelectorAll('.sidebar-bottom > *, .user-chip')).map((element) => {
      const box = element.getBoundingClientRect();
      return Math.max(0, edge.left - box.left, box.right - edge.right);
    });
  });
  expect(Math.max(...overflow), 'footer controls extend beyond the navigation sidebar').toBeLessThanOrEqual(1);
}

test('keeps long account identities and footer controls inside desktop, rail and mobile navigation', async ({ page, request }, testInfo) => {
  test.skip(!hasDatabase, 'requires the disposable real-server database');
  await requireDisabledAuth(request);
  const response = await request.get('/api/v1/auth/me');
  expect(response.ok()).toBeTruthy();
  const actor = await response.json() as { id: string };
  const db = new DatabaseSync(process.env.HELM_E2E_DB!);
  const original = db.prepare('SELECT name, email FROM actors WHERE id = ?').get(actor.id)!;
  const name = 'Navigation Account With A Very Long Display Name';
  const email = 'navigation.account.with.a.long.address@example.test';
  db.prepare('UPDATE actors SET name = ?, email = ? WHERE id = ?').run(name, email, actor.id);
  try {
    await page.goto('/settings');
    const sidebar = page.locator('nav.sidebar');
    const account = sidebar.getByRole('button', { name: 'Account menu', exact: true });
    await expect(sidebar.locator('.user-copy small')).toHaveText(email);
    for (const theme of ['light', 'dark']) {
      await account.click();
      await page.getByRole('menuitemradio', { name: `${theme === 'light' ? 'Light' : 'Dark'} theme`, exact: true }).click();
      await page.keyboard.press('Escape');
      for (const width of [1920, 1280, 1024]) {
        await page.setViewportSize({ width, height: 900 });
        await expect(sidebar.getByRole('button', { name: 'Settings', exact: true })).toHaveAttribute('aria-current', 'page');
        await attachMaskedScreenshot(page, testInfo, `sidebar-${width}-${theme}.png`);
        await expectFooterContained(sidebar);
      }
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    await sidebar.getByRole('button', { name: 'Collapse sidebar', exact: true }).click();
    await expect(sidebar).toHaveClass(/\brail\b/);
    // Wait for the width transition before measuring the footer.
    await expect(sidebar).toHaveCSS('width', '64px');
    await expectFooterContained(sidebar);
    await account.click();
    await expect(page.getByRole('menu', { name: 'Account', exact: true })).toBeVisible();
    await attachMaskedScreenshot(page, testInfo, 'sidebar-rail-account-menu.png');
    await page.keyboard.press('Escape');
    await expect(account).toBeFocused();

    await page.setViewportSize({ width: 320, height: 800 });
    await page.locator('.mobile-nav').getByRole('button', { name: 'More', exact: true }).click();
    await expect(sidebar).toBeVisible();
    await expectFooterContained(sidebar);
    await account.click();
    await expect(page.getByRole('menu', { name: 'Account', exact: true })).toBeVisible();
    await attachMaskedScreenshot(page, testInfo, 'sidebar-mobile-account-menu.png');
    await page.keyboard.press('Escape');
    await expect(account).toBeFocused();
  } finally {
    db.prepare('UPDATE actors SET name = ?, email = ? WHERE id = ?').run(original.name, original.email, actor.id);
    db.close();
  }
});
