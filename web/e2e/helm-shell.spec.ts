import { expect, test } from '@playwright/test';

test.describe('Helm workspace', () => {
  test('reveals the workspace shell while project data is still loading', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    let releaseAuth = () => {};
    let releaseProjects = () => {};
    const authGate = new Promise<void>((resolve) => { releaseAuth = resolve; });
    const projectsGate = new Promise<void>((resolve) => { releaseProjects = resolve; });
    await page.route(/\/api\/v1\/auth\/status$/, async (route) => {
      await authGate;
      await route.continue();
    });
    await page.route(/\/api\/v1\/projects(?:\?.*)?$/, async (route) => {
      await projectsGate;
      await route.continue();
    });

    await page.goto('/');
    const splashSpinner = page.locator('.splash .spinner');
    await expect(splashSpinner).toBeVisible();
    expect(await splashSpinner.evaluate((element) => getComputedStyle(element).borderTopColor)).toBe('rgb(109, 94, 252)');

    releaseAuth();
    await expect(page.locator('.app-shell')).toBeVisible();
    await expect(page.locator('.workspace-loading')).toBeVisible();
    await expect(page.locator('.splash')).toBeHidden();

    releaseProjects();
    await expect(page.locator('.workspace-loading')).toBeHidden();
  });

  test('offers a retry when the bootstrap connection fails', async ({ page }) => {
    let attempts = 0;
    await page.route(/\/api\/v1\/auth\/status$/, async (route) => {
      attempts += 1;
      if (attempts === 1) await route.abort('connectionfailed');
      else await route.continue();
    });

    await page.goto('/');
    const retry = page.getByRole('button', { name: 'Retry', exact: true });
    await expect(retry).toBeVisible();
    await retry.click();
    await expect(page.locator('.app-shell')).toBeVisible();
    expect(attempts).toBe(2);
  });

  test('keeps the primary navigation and board usable on small screens', async ({ page }) => {
    const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
    const projectName = `Mobile layout ${runId}`;
    const projectKey = `MOB${runId}`.slice(0, 16);

    await page.goto('/');
    const origin = new URL(page.url()).origin;
    const response = await page.request.post('/api/v1/projects', {
      data: { key: projectKey, name: projectName, description: 'Phone-sized board acceptance fixture.' },
      headers: {
        Origin: origin,
        'Content-Type': 'application/json',
        'Idempotency-Key': `mobile-layout-${crypto.randomUUID()}`
      }
    });
    expect(response.ok()).toBeTruthy();
    const project = await response.json() as { slug: string };

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/p/${project.slug}`);
    const navigation = page.locator('.mobile-nav');
    await expect(navigation).toBeVisible();
    for (const label of ['My work', 'Issues', 'Board', 'Tickets', 'More']) {
      await expect(navigation.getByRole('button', { name: label, exact: true })).toBeVisible();
    }

    const board = page.locator('section.board');
    await expect(board).toBeVisible();
    await expect(board.locator('.board-column')).toHaveCount(6);
    const layout = await page.evaluate(() => {
      const nav = document.querySelector('.mobile-nav')?.getBoundingClientRect();
      const columns = Array.from(document.querySelectorAll<HTMLElement>('section.board .board-column'))
        .slice(0, 2)
        .map((column) => column.getBoundingClientRect());
      const search = document.querySelector('.filter-search')?.getBoundingClientRect();
      const newTask = document.querySelector<HTMLElement>('.board-heading .button.primary')?.getBoundingClientRect();
      const board = document.querySelector<HTMLElement>('section.board');
      return {
        viewportWidth: window.innerWidth,
        documentWidth: document.documentElement.scrollWidth,
        bodyWidth: document.body.scrollWidth,
        boardClientWidth: board?.clientWidth,
        boardScrollWidth: board?.scrollWidth,
        boardSnapType: board ? getComputedStyle(board).scrollSnapType : '',
        navBottom: nav?.bottom,
        navButtonHeight: document.querySelector('.mobile-nav button')?.getBoundingClientRect().height,
        searchHeight: search?.height,
        newTaskHeight: newTask?.height,
        firstColumn: columns[0] ? { top: columns[0].top, bottom: columns[0].bottom, left: columns[0].left, right: columns[0].right } : null,
        secondColumn: columns[1] ? { top: columns[1].top, left: columns[1].left, right: columns[1].right } : null
      };
    });
    expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth);
    expect(layout.bodyWidth).toBeLessThanOrEqual(layout.viewportWidth);
    expect(layout.navBottom).toBeCloseTo(844, 0);
    expect(layout.navButtonHeight).toBeGreaterThanOrEqual(44);
    expect(layout.searchHeight).toBeGreaterThanOrEqual(44);
    expect(layout.newTaskHeight).toBeGreaterThanOrEqual(44);
    expect(layout.boardScrollWidth).toBeGreaterThan(layout.boardClientWidth!);
    expect(layout.boardSnapType).toMatch(/x/);
    expect(layout.boardSnapType).toMatch(/mandatory/);
    expect(layout.firstColumn).not.toBeNull();
    expect(layout.secondColumn).not.toBeNull();
    expect(layout.secondColumn!.top).toBeCloseTo(layout.firstColumn!.top, 0);
    expect(layout.secondColumn!.left).toBeGreaterThan(layout.firstColumn!.right);
  });
});
