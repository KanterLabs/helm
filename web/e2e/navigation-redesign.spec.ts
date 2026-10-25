import { expect, test, type Page } from '@playwright/test';

async function createProject(page: Page, label: string) {
  const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  await page.goto('/');
  const origin = new URL(page.url()).origin;
  const response = await page.request.post('/api/v1/projects', {
    data: { key: `NAV${runId}`.slice(0, 16), name: `${label} ${runId}`, description: 'Navigation redesign fixture.' },
    headers: { Origin: origin, 'Content-Type': 'application/json', 'Idempotency-Key': `nav-redesign-${crypto.randomUUID()}` }
  });
  expect(response.ok()).toBeTruthy();
  return await response.json() as { slug: string; name: string };
}

test.describe('Sidebar and site navigation', () => {
  test('nests project views under the active project and mirrors them in the breadcrumb', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 860 });
    const project = await createProject(page, 'Nav tree');
    await page.goto(`/p/${project.slug}`);

    const sidebar = page.locator('nav.sidebar');
    await expect(sidebar.locator('.helm-mark path')).toHaveCSS('fill', 'rgb(255, 255, 255)');
    const projectRow = sidebar.getByRole('button', { name: project.name, exact: true });
    await expect(projectRow).toHaveAttribute('aria-expanded', 'true');
    const views = sidebar.getByRole('group', { name: `${project.name} views` });
    for (const label of ['Board', 'Timeline', 'Focus', 'Progress', 'Audits']) {
      await expect(views.getByRole('button', { name: label, exact: true })).toBeVisible();
    }
    await expect(views.getByRole('button', { name: 'Board', exact: true })).toHaveAttribute('aria-current', 'page');

    const breadcrumb = page.getByRole('navigation', { name: 'Breadcrumb' });
    await expect(breadcrumb.locator('[aria-current="page"]')).toHaveText('Board');

    await views.getByRole('button', { name: 'Timeline', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/timeline$`));
    await expect(breadcrumb.locator('[aria-current="page"]')).toHaveText('Timeline');

    await views.getByRole('button', { name: 'Progress', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/roadmap$`));
    await expect(views.getByRole('button', { name: 'Progress', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(sidebar.getByRole('navigation', { name: 'Workspace views' }).getByRole('button', { name: 'Roadmap', exact: true })).not.toHaveAttribute('aria-current', 'page');

    await sidebar.getByRole('navigation', { name: 'Workspace views' }).getByRole('button', { name: 'Issues', exact: true }).click();
    await expect(breadcrumb.locator('[aria-current="page"]')).toHaveText('Issues');
    await expect(views.getByRole('button', { name: 'Progress', exact: true })).not.toHaveAttribute('aria-current', 'page');

    await views.getByRole('button', { name: 'Board', exact: true }).click();
    await expect(page.locator('section.board')).toBeVisible();
    await projectRow.click();
    await expect(projectRow).toHaveAttribute('aria-expanded', 'false');
    await expect(views).toBeHidden();
    await projectRow.click();
    await expect(views).toBeVisible();

    await testInfo.attach('navigation-desktop.png', { body: await page.screenshot(), contentType: 'image/png' });
  });

  test('collapses to a remembered rail with tooltips and keyboard toggle', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 860 });
    await page.goto('/');
    await page.evaluate(() => localStorage.removeItem('helm.sidebar-collapsed'));
    await page.reload();
    const sidebar = page.locator('nav.sidebar');
    await expect(sidebar).not.toHaveClass(/\brail\b/);

    await sidebar.getByRole('button', { name: 'Collapse sidebar', exact: true }).click();
    await expect(sidebar).toHaveClass(/\brail\b/);
    expect(await page.evaluate(() => localStorage.getItem('helm.sidebar-collapsed'))).toBe('true');

    await sidebar.getByRole('navigation', { name: 'Workspace views' }).getByRole('button', { name: 'Issues', exact: true }).hover();
    await expect(page.getByRole('tooltip')).toHaveText('Issues');
    await testInfo.attach('navigation-rail.png', { body: await page.screenshot(), contentType: 'image/png' });

    await page.reload();
    await expect(sidebar).toHaveClass(/\brail\b/);
    await page.locator('main.content').click({ position: { x: 4, y: 4 } });
    await page.keyboard.press('[');
    await expect(sidebar).not.toHaveClass(/\brail\b/);
    expect(await page.evaluate(() => localStorage.getItem('helm.sidebar-collapsed'))).toBe('false');

    await page.getByRole('textbox', { name: 'Search tasks' }).focus();
    await page.keyboard.press('[');
    await expect(sidebar).not.toHaveClass(/\brail\b/);
  });

  test('keeps account, theme, and create actions in single menus', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 860 });
    await page.goto('/');
    await page.evaluate(() => localStorage.setItem('helm.theme', 'light'));
    await page.reload();

    const account = page.getByRole('button', { name: 'Account menu', exact: true });
    await account.click();
    const accountMenu = page.getByRole('menu', { name: 'Account' });
    await expect(accountMenu.getByRole('menuitem', { name: 'Sign out', exact: true })).toBeVisible();
    await accountMenu.getByRole('menuitemradio', { name: 'Dark theme', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expect(page.locator('nav.sidebar .helm-mark path')).toHaveCSS('fill', 'rgb(255, 255, 255)');
    expect(await page.evaluate(() => localStorage.getItem('helm.theme'))).toBe('dark');
    await page.keyboard.press('Escape');
    await expect(accountMenu).toBeHidden();
    await expect(account).toBeFocused();

    const create = page.getByRole('button', { name: 'Create new', exact: true });
    await create.click();
    const createMenu = page.getByRole('menu', { name: 'Create' });
    for (const label of ['Task', 'Bug report', 'Project']) {
      await expect(createMenu.getByRole('menuitem', { name: label, exact: true })).toBeVisible();
    }
    await createMenu.getByRole('menuitem', { name: 'Project', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Create a project' })).toBeVisible();
  });

  test('uses five mobile tabs and opens the full navigation from More', async ({ page }, testInfo) => {
    const project = await createProject(page, 'Nav mobile');
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/p/${project.slug}`);

    const tabs = page.locator('.mobile-nav');
    await expect(tabs.getByRole('button')).toHaveCount(5);
    for (const label of ['My work', 'Issues', 'Board', 'Tickets', 'More']) {
      await expect(tabs.getByRole('button', { name: label, exact: true })).toBeVisible();
    }
    await expect(page.locator('nav.sidebar')).toBeHidden();

    const more = tabs.getByRole('button', { name: 'More', exact: true });
    await more.click();
    await expect(more).toHaveAttribute('aria-expanded', 'true');
    const drawer = page.locator('nav.sidebar');
    await expect(drawer).toBeVisible();
    await expect(drawer.getByRole('button', { name: 'Settings', exact: true })).toBeVisible();
    const layout = await page.evaluate(() => ({ width: window.innerWidth, documentWidth: document.documentElement.scrollWidth }));
    expect(layout.documentWidth).toBeLessThanOrEqual(layout.width);
    await testInfo.attach('navigation-mobile-drawer.png', { body: await page.screenshot(), contentType: 'image/png' });

    await drawer.getByRole('group', { name: `${project.name} views` }).getByRole('button', { name: 'Timeline', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/p/${project.slug}/timeline$`));
    await expect(drawer).toBeHidden();

    await more.click();
    await expect(drawer).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(drawer).toBeHidden();
    await expect(more).toBeFocused();
  });
});
