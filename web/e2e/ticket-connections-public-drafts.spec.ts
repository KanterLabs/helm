import { expect, test } from '@playwright/test';

// Synthetic Cloudflare responses isolate draft handling. The surrounding
// Tickets page, browser navigation guard and dialog use the real Helm server.
// No credentials or provisioning requests are used by these tests.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

test('Public setup drafts survive tabs and require an explicit discard', async ({ page }, info) => {
  await page.route('**/api/v1/public-endpoints', route => route.fulfill({ json: { connector: { state: 'stopped' }, data: [] } }));
  await page.route('**/api/v1/cloudflare', route => route.fulfill({ json: {
    connected: true, connected_via: 'cloudflare', oauth_available: true, token_link: '', token_permissions: []
  } }));
  await page.route('**/api/v1/cloudflare/zones', route => route.fulfill({ json: { data: [{
    id: 'draft-zone', name: 'example.test', usable: true, email_routing: 'ready',
    plus_addressing: false, suggested_hostname: 'hooks.example.test'
  }] } }));
  await page.goto('/tickets?connect=1');
  const dialog = page.getByRole('dialog', { name: 'Connect apps' });
  await expect(dialog.getByLabel('Guided inbox name')).toHaveValue('Alerts');
  // Loading defaults is not user work and must not trigger a discard prompt.
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await page.getByRole('button', { name: /Connect apps/ }).click();
  await dialog.getByLabel('Guided inbox name').fill('Draft alert inbox');
  await dialog.getByRole('button', { name: 'Change', exact: true }).click();
  await dialog.getByLabel('Guided public hostname').fill('draft.example.test');
  await dialog.locator('.cf-email-options > summary').click();
  await dialog.getByLabel('Guided fallback email address').fill('operator@example.test');
  await dialog.getByRole('tab', { name: /^Webhooks/ }).click();
  await dialog.getByRole('tab', { name: /^Public access/ }).click();
  await expect(dialog.getByLabel('Guided inbox name')).toHaveValue('Draft alert inbox');
  await expect(dialog.getByLabel('Guided public hostname')).toHaveValue('draft.example.test');
  await expect(dialog.getByLabel('Guided fallback email address')).toHaveValue('operator@example.test');
  await expect.poll(() => page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  }), { message: 'Public setup drafts join the workspace navigation guard' }).toBe(true);
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  const confirm = page.getByRole('alertdialog', { name: 'Discard unsaved changes?' });
  await expect(confirm).toBeVisible();
  await info.attach('public-draft-discard.png', { contentType: 'image/png', body: await page.screenshot() });
  await confirm.getByRole('button', { name: 'Keep editing' }).click();
  await expect(dialog.getByLabel('Guided inbox name')).toHaveValue('Draft alert inbox');
  await page.keyboard.press('Escape');
  await confirm.getByRole('button', { name: 'Discard', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Connect apps/ })).toBeFocused();
});

test('A hidden token draft still participates in the workspace navigation guard', async ({ page }, info) => {
  await page.route('**/api/v1/public-endpoints', route => route.fulfill({ json: { connector: { state: 'stopped' }, data: [] } }));
  await page.route('**/api/v1/cloudflare', route => route.fulfill({ json: {
    connected: false, oauth_available: true, token_link: '', token_permissions: []
  } }));
  await page.goto('/tickets?connect=1');
  const dialog = page.getByRole('dialog', { name: 'Connect apps' });
  await dialog.getByRole('button', { name: 'Use an API token instead' }).click();
  await dialog.getByLabel('Cloudflare API token', { exact: true }).fill('synthetic-draft-only');
  await dialog.getByRole('button', { name: 'Hide the token option' }).click();
  await dialog.getByRole('tab', { name: /^Email/ }).click();
  await expect.poll(() => page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  }), { message: 'Hidden token remains protected without reading the DOM' }).toBe(true);
  await page.keyboard.press('Escape');
  const confirm = page.getByRole('alertdialog', { name: 'Discard unsaved changes?' });
  await expect(confirm).toBeVisible();
  await info.attach('hidden-token-discard.png', { contentType: 'image/png', body: await page.screenshot() });
  await confirm.getByRole('button', { name: 'Discard', exact: true }).click();
  await expect(dialog).toHaveCount(0);
});
