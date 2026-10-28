import { expect, test, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

// Failure inventory references (F1-F28) live in ../FAILURE_INVENTORY.md.
const root = path.resolve(__dirname, '..');
const appURL = pathToFileURL(path.join(root, 'index.html')).href;
const shots = path.join(root, 'out', 'shots');
fs.mkdirSync(shots, { recursive: true });

function guardPage(page: Page) {
  const offline: string[] = [];
  page.on('request', (r) => { if (!r.url().startsWith('file://')) offline.push(r.url()); });
  page.on('dialog', (d) => { offline.push(`dialog:${d.message()}`); void d.dismiss(); });
  page.on('pageerror', (e) => offline.push(`pageerror:${e.message}`));
  return offline;
}

// Fixed overlays (evidence panel/sheet) are captured at viewport size.
async function shot(page: Page, name: string, fullPage = !name.includes('evidence')) {
  await page.screenshot({ path: path.join(shots, `${name}.png`), fullPage, animations: 'disabled' });
}

async function expectNoHorizontalOverflow(page: Page, where: string) {
  const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  expect(o.sw, `horizontal overflow on ${where}`).toBeLessThanOrEqual(o.cw);
}

const row = (page: Page, key: string) => page.locator(`[data-card="${key}"]`);

test('audit columns: scope → run → review evidence → preview → apply receipt', async ({ page }) => {
  const offline = guardPage(page);
  await page.goto(appURL);
  await expect(page.getByTestId('prototype-banner')).toContainText('synthetic');

  // F1-F3, F5: scope panel shows baseline, Done coverage and a real count.
  await page.getByRole('button', { name: 'Audit columns' }).click();
  const scope = page.getByRole('region', { name: 'Audit columns' });
  await expect(scope).toContainText('acme-synthetic/harbor');
  await expect(scope.getByLabel('Branch or ref')).toHaveValue('beta');
  await expect(scope).toContainText('Cards are judged against committed work here.');
  await expect(scope.getByLabel('All cards')).toBeChecked();
  await expect(scope).toContainText('Includes 6 Done cards');
  await expect(scope).toContainText('QA hold');
  const start = scope.getByRole('button', { name: /^Audit \d+ cards$/ });
  await expect(start).toHaveText('Audit 18 cards');
  await scope.getByLabel('Specific columns').check();
  await scope.getByRole('checkbox', { name: /^Done/ }).check();
  await expect(start).toHaveText('Audit 6 cards');
  await scope.getByLabel('All cards').check();
  await expect(start).toHaveText('Audit 18 cards');
  await expect(start).not.toContainText('%');
  await shot(page, 'desktop-light-1-scope');

  // F6-F7: running stages, cancel produces an honest canceled state.
  await start.click();
  const progress = page.getByRole('status', { name: 'Audit progress' });
  await expect(progress).toContainText('Reading committed work');
  await expect(page.getByText('Leaving this page is safe')).toBeVisible();
  await shot(page, 'desktop-light-2-running');
  await page.getByRole('button', { name: 'Cancel audit' }).click();
  await expect(page.getByRole('heading', { name: 'Audit canceled' })).toBeVisible();
  await expect(page.getByText('No cards were changed')).toBeVisible();
  await expect(page.getByText(/corrections? suggested/)).toHaveCount(0);
  await page.getByRole('button', { name: 'Retry with the same scope' }).click();
  await expect(progress).toContainText('Checking card criteria');

  // F8, F9, F13, F14: answer first, counts balance, nothing preselected.
  await expect(page.getByRole('heading', { name: '8 column corrections suggested' })).toBeVisible({ timeout: 10_000 });
  const summary = page.getByTestId('result-summary');
  await expect(summary).toContainText('18 cards audited');
  await expect(summary).toContainText('6 already correct');
  await expect(summary).toContainText("4 couldn't be verified");
  await expect(page.getByTestId('baseline-chip')).toContainText('acme-synthetic/harbor · beta');
  await expect(page.getByRole('tab', { name: /Corrections/ })).toHaveAttribute('aria-selected', 'true');
  const list = page.getByTestId('result-list');
  await expect(list.locator('input[type="checkbox"]:checked')).toHaveCount(0);
  expect(await list.innerText()).not.toMatch(/\d+\s?%|\b[0-9a-f]{40}\b/);
  expect(page.url()).toContain('#run=');

  await expect(row(page, 'HB-12')).toContainText('In progress');
  await expect(row(page, 'HB-12').getByTestId('recommended')).toHaveText('Done');
  await expect(row(page, 'HB-12')).toContainText('3 of 3 conditions supported');
  // F11: destination and blocker both visible, no selection.
  await expect(row(page, 'HB-21')).toContainText('Done recommended');
  await expect(row(page, 'HB-21')).toContainText('Cannot apply while this card is claimed');
  await expect(row(page, 'HB-21').getByRole('checkbox')).toHaveCount(0);
  await expect(row(page, 'HB-21').getByRole('button', { name: 'Open card' })).toBeVisible();
  // F12: stale assessment cannot be selected.
  await expect(row(page, 'HB-15')).toContainText('Card changed after this audit');
  await expect(row(page, 'HB-15').getByRole('checkbox')).toHaveCount(0);
  await expect(row(page, 'HB-15').getByRole('button', { name: 'Re-audit this card' })).toBeVisible();
  await shot(page, 'desktop-light-3-corrections');

  // F10: unknown evidence is not a placement error.
  await page.getByRole('tab', { name: /Couldn.t verify/ }).click();
  for (const key of ['HB-18', 'HB-26', 'HB-29', 'HB-24']) {
    await expect(row(page, key)).toContainText("Couldn't verify");
    await expect(row(page, key).getByTestId('recommended')).toHaveCount(0);
    await expect(row(page, key).getByRole('checkbox')).toHaveCount(0);
  }
  await expect(row(page, 'HB-18').getByRole('button', { name: 'Re-audit this card' })).toBeVisible();
  await expect(row(page, 'HB-29').getByRole('button', { name: 'Choose column meaning' })).toBeVisible();
  await expect(row(page, 'HB-24').getByRole('button', { name: 'Reconnect repository' })).toBeVisible();
  await shot(page, 'desktop-light-4-unverified');

  // F4: PR-head-only work is labeled and kept separate from beta.
  await page.getByRole('tab', { name: /Already correct/ }).click();
  await expect(row(page, 'HB-27')).toContainText('PR #52 head');
  await row(page, 'HB-27').getByRole('button', { name: 'Review evidence' }).click();
  const prPanel = page.getByRole('dialog');
  await expect(prPanel).toContainText('PR head · not on beta');
  await page.keyboard.press('Escape');
  await page.getByRole('tab', { name: /Corrections/ }).click();

  // F15-F17: criterion evidence, safe excerpts, Escape returns focus.
  const reviewHB7 = row(page, 'HB-7').getByRole('button', { name: 'Review evidence' });
  await reviewHB7.click();
  const panel = page.getByRole('dialog', { name: /HB-7/ });
  await expect(panel).toBeVisible();
  for (const word of ['Supported', 'Contradicted', 'Not verified']) await expect(panel).toContainText(word);
  await expect(panel).toContainText('src/export/csv.ts');
  await expect(panel.getByText(/^beta @ [0-9a-f]{7}$/).first()).toBeVisible();
  await expect(panel).toContainText('<script>');
  expect(await page.evaluate(() => (window as unknown as { __xss?: boolean }).__xss)).toBeUndefined();
  await expect(panel.getByText(/[0-9a-f]{40}/)).toBeHidden();
  await shot(page, 'desktop-light-5-evidence');
  await panel.getByRole('checkbox', { name: 'Select: Reopen' }).check();
  await page.keyboard.press('Escape');
  await expect(panel).toBeHidden();
  await expect(reviewHB7).toBeFocused();

  // Keyboard selection from a row.
  const pick = row(page, 'HB-12').getByRole('checkbox', { name: 'Select: Mark done' });
  await pick.focus();
  await page.keyboard.press('Space');
  await expect(pick).toBeChecked();
  for (const key of ['HB-31', 'HB-34', 'HB-9', 'HB-22']) await row(page, key).getByRole('checkbox').check();
  const reviewBar = page.getByRole('button', { name: 'Review 6 corrections' });
  await expect(reviewBar).toBeVisible();
  // F18: selection does not move cards.
  await expect(row(page, 'HB-12').getByTestId('current')).toHaveText('In progress');

  // F19-F21: the read-only preview is the single confirmation.
  await reviewBar.click();
  const preview = page.getByRole('region', { name: 'Review corrections' });
  await expect(preview.getByTestId('preview-row')).toHaveCount(6);
  await expect(preview.locator('[data-preview="HB-22"]')).toContainText("Can't apply");
  await expect(preview.locator('[data-preview="HB-22"]')).toContainText('beta advanced');
  await expect(preview.locator('[data-preview="HB-12"]')).toContainText('Mark done');
  await expect(preview.locator('[data-preview="HB-7"]')).toContainText('Reopen');
  await expect(preview.locator('[data-preview="HB-31"]')).toContainText('Resolve as fixed');
  await expect(preview.locator('[data-preview="HB-31"]').getByLabel('Resolution')).toHaveValue('fixed');
  await expect(preview.locator('[data-preview="HB-34"]')).toContainText('Reopen bug');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  const apply = preview.getByRole('button', { name: /^Apply \d+ corrections$/ });
  await expect(apply).toHaveText('Apply 5 corrections');
  await expect(preview.getByRole('button', { name: /Approve|Continue|Confirm/ })).toHaveCount(0);
  await shot(page, 'desktop-light-6-preview');

  // F22-F23: honest per-card receipt and idempotent retry.
  await apply.click();
  const receipt = page.getByRole('region', { name: 'Apply receipt' });
  await expect(receipt.getByRole('heading')).toHaveText('2 of 5 corrections applied');
  await expect(receipt.locator('[data-receipt="HB-12"]')).toContainText('Applied');
  await expect(receipt.locator('[data-receipt="HB-7"]')).toContainText('Applied');
  await expect(receipt.locator('[data-receipt="HB-31"]')).toContainText('Already applied');
  await expect(receipt.locator('[data-receipt="HB-9"]')).toContainText('Skipped');
  await expect(receipt.locator('[data-receipt="HB-34"]')).toContainText('Failed');
  const response = await page.evaluate(() => (window as unknown as { helmAuditPrototype: { lastApplyResponse: unknown } }).helmAuditPrototype.lastApplyResponse);
  fs.writeFileSync(path.join(root, 'out', 'apply-response.json'), JSON.stringify(response, null, 2) + '\n');
  await shot(page, 'desktop-light-7-receipt');
  await receipt.locator('[data-receipt="HB-34"]').getByRole('button', { name: 'Retry' }).click();
  await expect(receipt.locator('[data-receipt="HB-34"]')).toContainText('Applied');
  await expect(receipt.getByRole('heading')).toHaveText('3 of 5 corrections applied');
  await expect(receipt.getByRole('button', { name: 'Retry' })).toHaveCount(0);

  // F24: rows show the applied transition; skipped cards stay put.
  await receipt.getByRole('button', { name: 'Back to results' }).click();
  await expect(row(page, 'HB-12')).toContainText('Applied: In progress → Done');
  await expect(row(page, 'HB-9')).toContainText('Skipped');
  await expect(row(page, 'HB-9').getByTestId('current')).toHaveText('Backlog');

  // F28: the deep link restores the same run after leaving.
  await page.reload();
  await expect(page.getByRole('heading', { name: '8 column corrections suggested' })).toBeVisible();
  await expect(row(page, 'HB-12')).toContainText('Applied: In progress → Done');
  await shot(page, 'desktop-light-8-restored');

  expect(offline, 'network requests, dialogs or page errors').toEqual([]); // F27
});

const layouts = [
  { name: 'mobile320-light', width: 320, theme: 'light' },
  { name: 'mobile320-dark', width: 320, theme: 'dark' },
  { name: 'mobile390-light', width: 390, theme: 'light' },
  { name: 'mobile390-dark', width: 390, theme: 'dark' },
  { name: 'desktop-dark', width: 1280, theme: 'dark' }
];

test('layouts: 320/390/desktop in light and dark stay readable without overflow', async ({ page }) => {
  const offline = guardPage(page);
  for (const l of layouts) {
    await page.setViewportSize({ width: l.width, height: l.width < 600 ? 780 : 860 });
    await page.goto(`${appURL}?theme=${l.theme}#scope`);
    await page.evaluate(() => sessionStorage.clear());
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('data-theme', l.theme);
    await expectNoHorizontalOverflow(page, `${l.name} scope`);
    await shot(page, `${l.name}-1-scope`);

    await page.getByRole('button', { name: /^Audit \d+ cards$/ }).click();
    await expect(page.getByRole('heading', { name: '8 column corrections suggested' })).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('baseline-chip')).toBeVisible();
    await expectNoHorizontalOverflow(page, `${l.name} results`);
    await shot(page, `${l.name}-2-results`);

    const open = row(page, 'HB-7').getByRole('button', { name: 'Review evidence' });
    await open.click();
    await expect(page.getByRole('dialog', { name: /HB-7/ })).toBeVisible();
    await expectNoHorizontalOverflow(page, `${l.name} evidence`);
    await shot(page, `${l.name}-3-evidence`);
    await page.keyboard.press('Escape');
    await expect(open).toBeFocused();

    const pick = row(page, 'HB-12').getByRole('checkbox');
    await pick.focus();
    await page.keyboard.press('Space');
    await row(page, 'HB-21').scrollIntoViewIfNeeded();
    await expectNoHorizontalOverflow(page, `${l.name} selected`);
    await page.getByRole('button', { name: 'Review 1 correction' }).click();
    await expect(page.getByRole('button', { name: 'Apply 1 correction' })).toBeVisible();
    await expectNoHorizontalOverflow(page, `${l.name} preview`);
    await shot(page, `${l.name}-4-preview`);
    await page.getByRole('button', { name: 'Apply 1 correction' }).click();
    await expect(page.getByRole('region', { name: 'Apply receipt' }).getByRole('heading')).toHaveText('1 of 1 correction applied');
    await expectNoHorizontalOverflow(page, `${l.name} receipt`);
    await shot(page, `${l.name}-5-receipt`);
  }
  expect(offline).toEqual([]);
});
