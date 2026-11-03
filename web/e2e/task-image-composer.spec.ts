import { expect, test } from '@playwright/test';

// Written before UI implementation. Failure scenarios: batch uploads fail
// independently; publish loses draft/captions on error; images leak across tasks
// or sessions; image-only comments cannot send; viewer traps or loses focus;
// older evidence is inaccessible; mobile controls overflow the drawer.
test('stages an image, preserves its caption on failed publish, and opens its original', async ({ page, request }, info) => {
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const headers = { Origin: origin };
  const suffix = Date.now().toString(36).toUpperCase();
  const project = await (await request.post('/api/v1/projects', { headers, data: { key: `UIC${suffix}`.slice(0, 16), name: 'Screenshot composer check' } })).json();
  const columns = await (await request.get(`/api/v1/projects/${project.id}/columns`)).json();
  const task = await (await request.post(`/api/v1/projects/${project.id}/tasks`, { headers, data: { title: 'Check screenshot composer', column_id: columns.data[0].id } })).json();
  await page.goto(`/p/${project.slug}/tasks/${task.key}?view=activity`);
  await page.getByRole('tab', { name: 'Activity', exact: true }).click();
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNwaDgAAAKEAYEml6crAAAAAElFTkSuQmCC', 'base64');
  await expect(page.getByRole('button', { name: 'Add screenshots', exact: true })).toBeEnabled();
  await page.locator('input[data-screenshot-picker]').setInputFiles({ name: 'inventory.png', mimeType: 'image/png', buffer: png });
  await expect(page.getByText('Ready to send', { exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: /Caption for inventory.png/ }).fill('Inventory after rearrangement');
  await page.route(`**/api/v1/tasks/${task.id}/comments`, async (route) => {
    if (route.request().method() === 'POST') await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { message: 'Please retry this update' } }) });
    else await route.continue();
  });
  await page.getByRole('button', { name: 'Add comment', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Please retry this update' })).toBeVisible();
  await expect(page.getByRole('textbox', { name: /Caption for inventory.png/ })).toHaveValue('Inventory after rearrangement');
  await page.unroute(`**/api/v1/tasks/${task.id}/comments`);
  await page.getByRole('button', { name: 'Add comment', exact: true }).click();
  const image = page.getByRole('button', { name: /Open screenshot: Inventory after rearrangement/ }).first();
  await image.click();
  await expect(page.getByRole('dialog', { name: 'Screenshot viewer', exact: true })).toBeVisible();
  const downloadEvent = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download original', exact: true }).click();
  expect((await downloadEvent).suggestedFilename()).toBe('inventory.png');
  await page.keyboard.press('Escape');
  await expect(image).toBeFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(image).toBeVisible();
  await info.attach('screenshot-composer-mobile.png', { contentType: 'image/png', body: await page.screenshot() });
});
