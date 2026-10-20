import { expect } from '@playwright/test';
import { authMode, awaitServiceWorkerControl, cacheUrls, createFixture, decodedImageDimensions, goOfflineToCachedBoard, json, offlinePage, offlineStoreContains, openOnlineBoard, reconnectButton, test, trackApiMutations } from './support/pwaOffline';
import type { ManifestIcon } from './support/pwaOffline';

test('serves a standalone install shell with static-only service-worker cache keys', async ({ page, request, networkProxy }) => {
  test.skip(await authMode(request) !== 'disabled', 'requires the built E2E server in disabled-auth mode');
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  const manifestHref = await page.locator('link[rel="manifest"]').getAttribute('href');
  expect(manifestHref, 'the app shell should link a web manifest').toBeTruthy();

  const manifestResponse = await request.get(new URL(manifestHref as string, networkProxy.origin).toString());
  expect(manifestResponse.headers()['content-type']).toContain('application/manifest+json');
  const manifest = await json<{
    name?: string;
    display?: string;
    start_url?: string;
    scope?: string;
    icons?: ManifestIcon[];
  }>(manifestResponse, 'read PWA manifest');
  expect(manifest.name).toMatch(/helm/i);
  expect(manifest.display).toBe('standalone');
  expect(typeof manifest.start_url).toBe('string');
  expect(typeof manifest.scope).toBe('string');

  const start = new URL(manifest.start_url as string, networkProxy.origin);
  const scope = new URL(manifest.scope as string, networkProxy.origin);
  expect(start.origin).toBe(scope.origin);
  expect(start.pathname.startsWith(scope.pathname)).toBeTruthy();

  const icons = manifest.icons || [];
  const declaredSizes = new Set(icons.flatMap((icon) => (icon.sizes || '').split(/\s+/).filter(Boolean)));
  for (const size of ['180x180', '192x192', '512x512']) {
    expect(declaredSizes, `manifest should declare a ${size} icon`).toContain(size);
    const icon = icons.find((candidate) => (candidate.sizes || '').split(/\s+/).includes(size));
    expect(icon?.src, `${size} icon should have a source URL`).toBeTruthy();
    const iconURL = new URL(icon?.src as string, networkProxy.origin).toString();
    const iconResponse = await request.get(iconURL);
    expect(iconResponse.ok(), `${size} icon should be fetchable`).toBeTruthy();
    expect((await iconResponse.body()).byteLength, `${size} icon should contain decoded bytes`).toBeGreaterThan(0);
    const dimensions = await decodedImageDimensions(page, iconURL);
    const [width, height] = size.split('x').map(Number);
    expect(dimensions, `${size} icon should decode at its declared dimensions`).toEqual({ width, height });
  }

  const fixture = await createFixture(request);
  await openOnlineBoard(page, fixture);
  await awaitServiceWorkerControl(page);
  const urls = await cacheUrls(page);
  expect(urls.length, 'the service worker should cache the application shell').toBeGreaterThan(0);
  const forbidden = urls.filter((url) => /\/(?:api|auth)(?:\/|$)|\/cdn-cgi(?:\/|$)/i.test(new URL(url).pathname));
  expect(forbidden, 'service-worker cache keys must never include API, auth, or Cloudflare control paths').toEqual([]);
});

test('renders a saved board read-only offline, blocks writes, and re-authenticates on reconnect', async ({ page, request, context, networkProxy }) => {
  test.skip(await authMode(request) !== 'disabled', 'requires the built E2E server in disabled-auth mode');
  const fixture = await createFixture(request);
  const mutations = trackApiMutations(page);
  let authStatusReads = 0;
  page.on('request', (requestEvent) => {
    const path = new URL(requestEvent.url()).pathname;
    if (path === '/api/v1/auth/status') authStatusReads += 1;
  });

  await openOnlineBoard(page, fixture);
  await awaitServiceWorkerControl(page);
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), {
    timeout: 15_000,
    message: 'authenticated board load should persist a snapshot in helm-offline-boards/snapshots/current'
  }).toBeTruthy();
  mutations.length = 0;

  await page.setViewportSize({ width: 390, height: 844 });
  // Without navigation, native online/offline events also revalidate auth.
  await context.setOffline(true);
  await expect(page.getByTestId('offline-board-view')).toBeVisible();
  await context.setOffline(false);
  await expect(page.locator('.app-shell')).toBeVisible();
  await expect(page.getByText(fixture.task.title, { exact: true })).toBeVisible();
  const root = await goOfflineToCachedBoard(page, networkProxy, fixture);
  await expect(root.getByRole('button', { name: /add task/i })).toHaveCount(0);
  await expect(root.locator('[draggable="true"]')).toHaveCount(0);
  await expect(root.locator('textarea, [contenteditable="true"], input:not([type="search"])')).toHaveCount(0);
  await expect(root.getByTestId('offline-search')).toBeVisible();

  // Exercise focus/keyboard handling and every available non-network control;
  // the read-only surface must not leak a mutation request while disconnected.
  await page.keyboard.press('Tab');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Escape');
  await (await reconnectButton(root)).click();
  await expect(root).toBeVisible();
  await expect.poll(() => mutations.length, { timeout: 1_000 }).toBe(0);

  networkProxy.setOffline(false);
  // Chromium's offline emulation can lose navigator.onLine across a cached
  // navigation. Exercise the user reconnect action as well as the event path.
  if (await root.isVisible()) {
    await expect(root.getByTestId('offline-reconnect')).toBeEnabled();
    await root.getByTestId('offline-reconnect').click();
  }
  await expect(page.locator('.app-shell')).toBeVisible();
  await expect(page.getByRole('heading', { name: fixture.project.name, exact: true })).toBeVisible();
  await expect(page.getByText(fixture.task.title, { exact: true })).toBeVisible();
  expect(authStatusReads, 'reconnect should perform a fresh auth/status read').toBeGreaterThanOrEqual(2);
  expect(mutations, 'offline interactions must not issue API writes').toEqual([]);
});

test('repairs an evicted shell cache while retaining the service worker registration', async ({ page, request, networkProxy }) => {
  test.skip(await authMode(request) !== 'disabled', 'requires the built E2E server in disabled-auth mode');
  const fixture = await createFixture(request);
  await openOnlineBoard(page, fixture);
  await awaitServiceWorkerControl(page);
  const originalUrls = await cacheUrls(page);
  await page.evaluate(async () => {
    for (const name of await caches.keys()) if (name.startsWith('helm-static-v')) await caches.delete(name);
  });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect.poll(async () => (await cacheUrls(page)).length).toBe(originalUrls.length);
  await expect.poll(() => offlineStoreContains(page, fixture.task.title)).toBeTruthy();
  await goOfflineToCachedBoard(page, networkProxy, fixture);
});

test('clears offline snapshots from the UI and after sign-out', async ({ page, request, networkProxy }) => {
  test.skip(await authMode(request) !== 'disabled', 'requires the built E2E server in disabled-auth mode');
  const fixture = await createFixture(request);
  await openOnlineBoard(page, fixture);
  await awaitServiceWorkerControl(page);
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), { timeout: 15_000 }).toBeTruthy();

  const root = await goOfflineToCachedBoard(page, networkProxy, fixture);
  const clear = root.getByTestId('offline-clear');
  await expect(clear, 'offline mode should expose a clear saved boards control').toBeVisible();
  page.once('dialog', (dialog) => dialog.accept());
  await clear.click();
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), { timeout: 10_000 }).toBeFalsy();
  await expect(root.getByText(fixture.task.title, { exact: true })).toHaveCount(0);
  await expect(root.getByTestId('offline-empty')).toBeVisible();

  // Start a second saved session in the same context to verify the logout path
  // clears storage too. Going online first is required for the real auth/logout
  // request; no route mocks disable or bypass it.
  networkProxy.setOffline(false);
  await page.goto(`/p/${fixture.project.slug}`, { waitUntil: 'domcontentloaded' });
  await expect(page.getByText(fixture.task.title, { exact: true })).toBeVisible();
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), { timeout: 15_000 }).toBeTruthy();
  await page.getByRole('button', { name: 'Account menu', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
  await expect(page.locator('.auth-page')).toBeVisible();
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), { timeout: 10_000 }).toBeFalsy();

  // Keep this a genuine outage navigation as well. Push the deep-link URL
  // without loading it while online, then let the service worker serve the
  // cached shell after the proxy starts destroying requests.
  await page.evaluate((path) => window.history.replaceState({}, '', path), `/p/${fixture.project.slug}`);
  networkProxy.setOffline(true);
  await page.reload({ waitUntil: 'domcontentloaded' });
  const emptyRoot = await offlinePage(page);
  await expect(emptyRoot.getByText(fixture.task.title, { exact: true })).toHaveCount(0);
  await expect(emptyRoot.getByTestId('offline-empty')).toBeVisible();
});

test('drops a cached snapshot when a fresh auth request is rejected', async ({ page, request, networkProxy }) => {
  test.skip(await authMode(request) !== 'disabled', 'requires the built E2E server in disabled-auth mode');
  const fixture = await createFixture(request);
  await openOnlineBoard(page, fixture);
  await awaitServiceWorkerControl(page);
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), { timeout: 15_000 }).toBeTruthy();

  const root = await goOfflineToCachedBoard(page, networkProxy, fixture);
  // Reject at the network boundary: WebKit request routing can miss requests
  // from service-worker-controlled pages, even for uncached API routes.
  networkProxy.setAuthRejected(true);
  networkProxy.setOffline(false);
  await root.getByTestId('offline-reconnect').click();
  await expect.poll(() => offlineStoreContains(page, fixture.task.title), {
    timeout: 10_000,
    message: 'a rejected auth response should clear the offline snapshot'
  }).toBeFalsy();
  await expect(root.getByText(fixture.task.title, { exact: true })).toHaveCount(0);

  networkProxy.setAuthRejected(false);
  await (await reconnectButton(root)).click();
  await expect(page.locator('.app-shell')).toBeVisible();
  await expect(page.getByRole('heading', { name: fixture.project.name, exact: true })).toBeVisible();
  await expect(page.getByText(fixture.task.title, { exact: true })).toBeVisible();
});
