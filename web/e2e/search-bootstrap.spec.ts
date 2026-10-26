import { expect, test } from '@playwright/test';

test.use({ serviceWorkers: 'block' });

// Failure scenario: startup finishes after command-palette navigation and
// reapplies the URL's empty query over text already entered in Search.
test('preserves a search draft when a delayed workspace bootstrap finishes', async ({ page, request }, info) => {
  const suffix = Date.now().toString(36).toUpperCase();
  const response = await request.post('/api/v1/projects', {
    headers: { Origin: new URL(test.info().project.use.baseURL!).origin, 'Idempotency-Key': crypto.randomUUID() },
    data: { key: `SB${suffix}`, name: `Search bootstrap ${suffix}` }
  });
  expect(response.ok()).toBeTruthy();
  const project = await response.json();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let started!: () => void;
  const held = new Promise<void>((resolve) => { started = resolve; });
  await page.route('**/api/v1/sidebar-counts?*', async (route) => {
    const upstream = await route.fetch();
    started();
    await gate;
    await route.fulfill({ response: upstream });
  });
  try {
    await page.goto(`/p/${project.slug}`);
    await held;
    await page.getByRole('button', { name: 'Search anything', exact: true }).click();
    await page.getByRole('option', { name: /^Search everything\./ }).click();
    const input = page.getByRole('textbox', { name: 'Search all tasks', exact: true });
    const query = `Keep this draft ${suffix}`;
    await input.fill(query);
    // Event polling starts only after workspace bootstrap has settled.
    const bootstrapped = page.waitForResponse((result) => new URL(result.url()).pathname === '/api/v1/events');
    release();
    await bootstrapped;
    await expect(input).toHaveValue(query);
    await input.press('Enter');
    await expect(page).toHaveURL((url) => url.searchParams.get('q') === query);
    await info.attach('search-draft-after-bootstrap.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});
