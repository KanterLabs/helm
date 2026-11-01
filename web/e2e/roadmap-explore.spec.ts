import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// Explore mode shows the roadmap on its own, enlarged. Ways it can fail:
// - the sidebar, top bar, phone navigation or page heading stay on screen;
// - the roadmap is not actually enlarged, or zoom steps do nothing or run past
//   their bounds;
// - a reload, or returning from a task opened inside explore, loses the mode;
// - Escape exits explore while a task drawer is still open, or does not exit
//   at all, or leaves focus nowhere;
// - leaving the roadmap (back button) strands another page without chrome;
// - the roadmap, explored or not, pushes the page sideways on a phone.
// Panels can re-render during measurement, and retries share the fixture DB:
// poll geometry and uniquely identify each attempt's task.
const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
const headers = () => ({ Origin: origin, 'Idempotency-Key': crypto.randomUUID() });
const fixtureTasks = new Set<string>();

test.afterEach(async ({ request }) => {
  // The workspace preview shows only five due tasks. Keep earlier attempts'
  // synthetic tasks from hiding the next attempt's task in that preview.
  // Remove only IDs created by this attempt, using their current versions.
  for (const id of fixtureTasks) {
    const current = await request.get(`/api/v1/tasks/${id}`);
    expect(current.ok()).toBeTruthy();
    const task = await current.json();
    const response = await request.delete(`/api/v1/tasks/${id}`, {
      headers: { ...headers(), 'If-Match': `"v${task.version}"` }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    fixtureTasks.delete(id);
  }
});

async function fixture(request: APIRequestContext) {
  const suffix = crypto.randomUUID().slice(0, 6).toUpperCase();
  const response = await request.post('/api/v1/projects', { headers: headers(), data: {
    key: `EXP${suffix}`, name: `Explore demo ${suffix}`, description: 'Synthetic roadmap used to check explore mode.'
  } });
  expect(response.ok()).toBeTruthy();
  const project = await response.json();
  const columns = (await (await request.get(`/api/v1/projects/${project.id}/columns`)).json()).data as { id: string; name: string }[];
  const due = new Date(Date.now() - 86_400_000).toISOString();
  const walkthroughTitle = `Plan the explore walkthrough ${suffix}`;
  for (const title of [walkthroughTitle, `Check roadmap zoom on a wall display ${suffix}`]) {
    const created = await request.post(`/api/v1/projects/${project.id}/tasks`, { headers: headers(), data: {
      title, column_id: columns[0].id, description: 'Synthetic explore task.', due_at: due
    } });
    expect(created.ok()).toBeTruthy();
    fixtureTasks.add((await created.json()).id);
  }
  return { ...project, walkthroughTitle } as { id: string; slug: string; name: string; walkthroughTitle: string };
}

const chrome = (page: Page) => ({
  sidebar: page.locator('#primary-sidebar'), topbar: page.locator('.topbar'), heading: page.locator('.content > .page-heading'),
  bar: page.getByRole('toolbar', { name: 'Roadmap explore controls' })
});
// The roadmap re-renders while its panels load; retry until the title settles.
async function heroTitle(page: Page) {
  let height = 0;
  await expect.poll(async () => (height = (await page.locator('.roadmap-hero h2').boundingBox())?.height ?? 0)).toBeGreaterThan(0);
  return height;
}
const overflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - innerWidth);

test('explore hides the rest of Helm and enlarges the roadmap', async ({ page, request }, info) => {
  const project = await fixture(request);
  // Start from the default zoom once; later reloads must keep the chosen one.
  await page.addInitScript(() => {
    if (sessionStorage.getItem('explore-e2e-reset')) return;
    sessionStorage.setItem('explore-e2e-reset', '1');
    localStorage.removeItem('helm.roadmap-explore-zoom');
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/my-work');
  await page.goto('/roadmap');
  const { sidebar, topbar, heading, bar } = chrome(page);
  const walkthrough = page.locator('.upcoming-row').filter({ hasText: project.walkthroughTitle });
  await expect(walkthrough).toBeVisible();
  const normal = await heroTitle(page);
  let normalWidth = 0;
  await expect.poll(async () => (normalWidth = (await page.locator('.roadmap-content').boundingBox())?.width ?? 0)).toBeGreaterThan(0);

  const enter = page.getByRole('button', { name: 'Explore roadmap' });
  await enter.click();
  await expect(page).toHaveURL(/\/roadmap\?explore=1$/);
  await expect(bar).toBeVisible();
  for (const hidden of [sidebar, topbar, heading]) await expect(hidden).toBeHidden();
  await expect(bar.getByRole('button', { name: 'Exit explore' })).toBeFocused();
  const explored = await heroTitle(page);
  expect(explored, 'default explore zoom enlarges the roadmap').toBeGreaterThan(normal * 1.1);
  await expect.poll(async () => (await page.locator('.roadmap-content').boundingBox())?.width ?? 0,
    { message: 'roadmap uses the freed width' }).toBeGreaterThan(normalWidth);
  expect(await overflow(page)).toBeLessThanOrEqual(0);
  await info.attach('explore-1440.png', { body: await page.screenshot(), contentType: 'image/png' });

  // Zoom steps change scale and stop at their bounds.
  const zoomIn = bar.getByRole('button', { name: 'Zoom in' });
  const zoomOut = bar.getByRole('button', { name: 'Zoom out' });
  const level = bar.locator('[data-explore-zoom]');
  const start = await level.textContent();
  await zoomIn.click();
  await expect(level).not.toHaveText(start!);
  expect(await heroTitle(page)).toBeGreaterThan(explored);
  while (await zoomIn.isEnabled()) await zoomIn.click();
  const max = await level.textContent();
  await page.keyboard.press('=');
  await expect(level).toHaveText(max!);
  while (await zoomOut.isEnabled()) await zoomOut.click();
  await expect(level).toHaveText('100%');
  expect(Math.abs(await heroTitle(page) - normal), '100% matches the normal roadmap').toBeLessThanOrEqual(1);
  await page.keyboard.press('=');
  await page.keyboard.press('=');
  const chosen = await level.textContent();

  // A reload keeps explore and the chosen zoom.
  await page.reload();
  await expect(bar).toBeVisible();
  await expect(level).toHaveText(chosen!);
  await expect(sidebar).toBeHidden();

  // A task opened from explore shows in its board; Escape closes the drawer
  // first and returns to the roadmap still in explore.
  await walkthrough.click();
  await expect(page.locator('.task-drawer')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('.task-drawer')).toBeHidden();
  await expect(page).toHaveURL(/\/roadmap\?explore=1$/);
  await expect(bar).toBeVisible();
  await expect(sidebar).toBeHidden();

  // Escape exits and returns focus to the Explore control.
  await page.keyboard.press('Escape');
  await expect(bar).toBeHidden();
  for (const shown of [sidebar, topbar, heading]) await expect(shown).toBeVisible();
  await expect(page).toHaveURL(/\/roadmap$/);
  await expect(enter).toBeFocused();

  // Back navigation out of explore never strands another page without chrome.
  await enter.click();
  await expect(bar).toBeVisible();
  await page.goBack();
  await expect(page).not.toHaveURL(/\/roadmap/);
  await expect(sidebar).toBeVisible();
  await expect(topbar).toBeVisible();

  // Project roadmaps explore the same way.
  await page.goto(`/p/${project.slug}/roadmap`);
  await page.getByRole('button', { name: 'Explore roadmap' }).click();
  await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/roadmap\\?explore=1$`));
  await expect(bar).toContainText(project.name);
  await expect(sidebar).toBeHidden();
  await info.attach('explore-project-1440.png', { body: await page.screenshot(), contentType: 'image/png' });
  await bar.getByRole('button', { name: 'Exit explore' }).click();
  await expect(sidebar).toBeVisible();
});

test('explore fits a phone without sideways scrolling', async ({ page, request }, info) => {
  await fixture(request);
  await page.addInitScript(() => localStorage.setItem('helm.roadmap-explore-zoom', '1.5'));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/roadmap');
  await expect(page.locator('.upcoming-row').first()).toBeVisible();
  expect(await overflow(page), 'roadmap fits a phone').toBeLessThanOrEqual(0);
  await page.getByRole('button', { name: 'Explore roadmap' }).click();
  const { topbar, bar } = chrome(page);
  await expect(bar).toBeVisible();
  await expect(topbar).toBeHidden();
  await expect(page.locator('.mobile-nav')).toBeHidden();
  await expect(page.locator('.roadmap-hero')).toBeVisible();
  expect(await overflow(page)).toBeLessThanOrEqual(0);
  await info.attach('explore-390.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
  await bar.getByRole('button', { name: 'Exit explore' }).click();
  await expect(page.locator('.mobile-nav')).toBeVisible();
});
