import { expect, test } from '@playwright/test';

// Exercise the browser UI at an HTTPS legacy origin without creating DNS,
// certificates, provider resources or a second database. Service-worker
// lifecycle itself is covered separately by pwa-offline.spec.ts.
test('legacy HTTPS client shows a consent-gated migration notice on mobile', async ({ browser, baseURL }) => {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    serviceWorkers: 'block'
  });
  const oldOrigin = 'https://legacy.helm.test';
  const newOrigin = 'https://canonical.helm.test';
  const destination = baseURL || 'http://127.0.0.1:18080';
  try {
    await context.route(`${oldOrigin}/**`, async (route) => {
      const incoming = new URL(route.request().url());
      const response = await route.fetch({
        url: `${destination}${incoming.pathname}${incoming.search}`,
        headers: { ...route.request().headers(), host: new URL(destination).host }
      });
      if (incoming.pathname === '/api/v1/auth/status') {
        await route.fulfill({ response, json: {
          ...await response.json(), canonical_origin: newOrigin, legacy_origin: oldOrigin
        } });
      } else {
        await route.fulfill({ response });
      }
    });
    const page = await context.newPage();
    await page.goto(oldOrigin);
    const notice = page.getByRole('alert').filter({ hasText: 'This Helm workspace moved' });
    await expect(notice).toBeVisible();
    await expect(notice).toContainText('Changes are disabled');
    await expect(notice).toContainText('Home Screen');
    await expect(notice.getByRole('link', { name: 'Open the new Helm address' }))
      .toHaveAttribute('href', `${newOrigin}/`);
    await expect(page).toHaveURL(`${oldOrigin}/`);
    const bounds = await notice.boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
    await expect(notice.getByRole('group', { name: 'Confirm old-address cleanup' })).toHaveCount(0);
    await notice.getByRole('button', { name: 'Clear old saved boards & cache' }).click();
    await expect(notice.getByRole('group', { name: 'Confirm old-address cleanup' })).toBeVisible();
    await notice.getByRole('button', { name: 'Keep it', exact: true }).click();
    await expect(notice.getByRole('group', { name: 'Confirm old-address cleanup' })).toHaveCount(0);
  } finally {
    await context.close();
  }
});
