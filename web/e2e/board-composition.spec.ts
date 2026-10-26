import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// Composition failures that made boards feel displaced down and to the right:
// page actions floating between the title and the toolbar, column arrows on a
// row of their own above the board, a toolbar whose search box and buttons
// disagree on height, quick-add links pinned to the bottom of empty columns,
// more empty space beneath the board than beside it, and phone rows that hold
// only right-aligned controls.
const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
const headers = () => ({ Origin: origin, 'Idempotency-Key': crypto.randomUUID() });

async function fixture(request: APIRequestContext) {
  const suffix = crypto.randomUUID().slice(0, 6).toUpperCase();
  const response = await request.post('/api/v1/projects', { headers: headers(), data: {
    key: `CMP${suffix}`, name: `Composition demo ${suffix}`,
    description: 'Synthetic board used to check page balance and spacing.'
  } });
  expect(response.ok()).toBeTruthy();
  const project = await response.json();
  const columns = (await (await request.get(`/api/v1/projects/${project.id}/columns`)).json()).data as { id: string; name: string }[];
  const seed: [string, string, boolean][] = [
    ['Backlog', 'Draft the onboarding checklist', false], ['Backlog', 'Collect feedback from the pilot team', false],
    ['Bugs', '[DEMO] Save button stays disabled after reconnecting', true],
    ['Ready', 'Write release notes for the board refresh', false], ['Ready', 'Review keyboard shortcuts', false],
    ['Ready', 'Tidy column descriptions', false]
  ];
  for (const [name, title, bug] of seed) {
    const column = columns.find((item) => item.name === name)!;
    const created = await request.post(`/api/v1/projects/${project.id}/tasks`, { headers: headers(), data: {
      title, column_id: column.id, description: 'Synthetic composition task.',
      ...(bug ? { kind: 'bug', bug: { actual_behavior: 'Synthetic beta demonstration.' } } : {})
    } });
    expect(created.ok()).toBeTruthy();
  }
  return { ...project, columns } as { id: string; slug: string; columns: { id: string; name: string }[] };
}

async function measure(page: Page) {
  return page.evaluate(() => {
    const box = (selector: string) => {
      const rect = document.querySelector(selector)?.getBoundingClientRect();
      return rect && rect.height ? rect.toJSON() as DOMRect : null;
    };
    const columns = Array.from(document.querySelectorAll('section.board > .board-column'));
    const empty = columns.find((column) => column.querySelector('.column-empty'));
    return {
      viewport: { width: innerWidth, height: innerHeight },
      scrollHeight: document.documentElement.scrollHeight,
      shellLeft: box('.main-shell')!.left, topbar: box('.topbar')!, mobileNav: box('.mobile-nav'),
      titleRow: box('.board-heading .heading-title-row')!, actions: box('.board-heading .heading-actions')!,
      firstAction: box('.board-heading .heading-actions .button.primary')!,
      toolbar: box('.board-toolbar')!, search: box('.board-toolbar .filter-search')!,
      filters: box('[data-board-filter-trigger]')!, total: box('.task-total')!,
      arrows: box('.board-overflow-controls'), column: columns[0].getBoundingClientRect().toJSON() as DOMRect,
      // A column body scrolls only when the shared height hit the viewport cap.
      capped: columns.some((column) => {
        const body = column.querySelector('.column-cards')!;
        return body.scrollHeight > body.clientHeight + 1;
      }),
      emptyState: empty?.querySelector('.column-empty')?.getBoundingClientRect().toJSON() as DOMRect,
      emptyAdd: empty?.querySelector('.quick-add-wrap')?.getBoundingClientRect().toJSON() as DOMRect
    };
  });
}

const settle = (page: Page) => page.evaluate(() => new Promise<void>((resolve) => {
  let frames = 4;
  const next = () => (frames-- ? requestAnimationFrame(next) : resolve());
  requestAnimationFrame(next);
}));
const centre = (rect: { top: number; height: number }) => rect.top + rect.height / 2;
const views = [
  { name: 'desktop-1440', width: 1440, height: 900 }, { name: 'short-1440', width: 1440, height: 720 },
  { name: 'desktop-1024', width: 1024, height: 768 }, { name: 'tablet-768', width: 768, height: 1024 },
  { name: 'phone-390', width: 390, height: 844 }, { name: 'phone-320', width: 320, height: 640 },
  { name: 'dark-1440', width: 1440, height: 900, theme: 'dark' },
  { name: 'rail-1440', width: 1440, height: 900, rail: true }, { name: 'dark-390', width: 390, height: 844, theme: 'dark' }
];

test('board pages keep a balanced frame from heading to columns', async ({ page, request }, info) => {
  test.setTimeout(120_000);
  const project = await fixture(request);
  const measurements = [];
  for (const view of views) {
    await page.addInitScript(({ theme, rail }) => {
      localStorage.setItem('helm.theme', theme);
      localStorage.setItem('helm.sidebar-collapsed', rail ? 'true' : 'false');
    }, { theme: view.theme || 'light', rail: Boolean(view.rail) });
    await page.setViewportSize({ width: view.width, height: view.height });
    await page.goto(`/p/${project.slug}`);
    await expect(page.locator('section.board .task-card')).toHaveCount(6);
    await page.mouse.move(0, 0);
    // Transient notes above the board must not leave a stale column height
    // behind: measure a few frames after the note goes, before any later
    // re-render could hide a missed re-fit.
    await expect(page.locator('.board-loading-note')).toHaveCount(0);
    await settle(page);
    const m = await measure(page);
    measurements.push({ view: view.name, ...m });
    await info.attach(`composition-${view.name}.png`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
    const phone = view.width <= 600;
    const stacked = view.width <= 820;
    const chrome = m.column.top - m.topbar.bottom;
    const bottom = (m.mobileNav ? m.mobileNav.top : view.height) - m.column.bottom;
    const gutter = m.column.left - m.shellLeft;

    // The page itself never scrolls once the 340px minimum column fits.
    if (view.height >= 700) expect.soft(m.scrollHeight, `${view.name}: page fits the viewport`).toBeLessThanOrEqual(view.height + 1);
    // Search and toolbar buttons line up as one control row.
    expect.soft(Math.abs(m.search.height - m.filters.height), `${view.name}: search matches button height`).toBeLessThanOrEqual(1);
    // Column arrows join an existing row instead of adding their own.
    if (m.arrows) {
      expect.soft(Math.abs(centre(m.arrows) - centre(m.total)), `${view.name}: arrows share the task-count row`).toBeLessThanOrEqual(4);
      expect.soft(m.arrows.bottom, `${view.name}: arrows sit inside the toolbar`).toBeLessThanOrEqual(m.toolbar.bottom + 1);
    }
    // Empty columns offer Add task beneath their placeholder, not at the floor.
    expect.soft(m.emptyAdd.top - m.emptyState.bottom, `${view.name}: Add task follows the empty state`).toBeLessThanOrEqual(16);
    if (!stacked) {
      expect.soft(Math.abs(centre(m.actions) - centre(m.titleRow)), `${view.name}: page actions share the title row`).toBeLessThanOrEqual(4);
      expect.soft(Math.abs(centre(m.search) - centre(m.total)), `${view.name}: task count shares the toolbar row`).toBeLessThanOrEqual(4);
      expect.soft(chrome, `${view.name}: header chrome above the columns`).toBeLessThanOrEqual(176);
    } else {
      // Stacked rows start at the content edge rather than collecting on the right.
      expect.soft(Math.abs(m.firstAction.left - m.search.left), `${view.name}: heading actions start at the content edge`).toBeLessThanOrEqual(2);
    }
    if (phone) {
      expect.soft(Math.abs(m.total.left - m.search.left), `${view.name}: task count starts at the content edge`).toBeLessThanOrEqual(2);
      expect.soft(bottom, `${view.name}: columns end close to the navigation bar`).toBeLessThanOrEqual(32);
      expect.soft(chrome, `${view.name}: header chrome above the columns`).toBeLessThanOrEqual(view.width < 360 ? 360 : 340);
    } else if (m.capped) {
      // The board is framed evenly: the space below matches the side gutter.
      expect.soft(Math.abs(bottom - gutter), `${view.name}: bottom margin matches side gutter`).toBeLessThanOrEqual(10);
    } else {
      expect.soft(bottom, `${view.name}: short boards stay clear of the viewport floor`).toBeGreaterThanOrEqual(gutter - 10);
    }
  }
  // The timeline shares the heading; its actions stay on the title row too.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/p/${project.slug}/timeline`);
  await expect(page.locator('.board-heading h1')).toBeVisible();
  const timeline = await page.evaluate(() => ({
    title: document.querySelector('.board-heading .heading-title-row')!.getBoundingClientRect().toJSON() as DOMRect,
    actions: document.querySelector('.board-heading .heading-actions')!.getBoundingClientRect().toJSON() as DOMRect
  }));
  expect.soft(Math.abs(centre(timeline.actions) - centre(timeline.title)), 'timeline actions share the title row').toBeLessThanOrEqual(4);
  await info.attach('composition-timeline-1440.png', { body: await page.screenshot(), contentType: 'image/png' });
  await info.attach('board-composition.json', { body: JSON.stringify(measurements, null, 2), contentType: 'application/json' });
});

// Collapsing details must release space after a taller card stack was measured.
// Content-hugging card bodies leave unused column space that is not chrome.
test('columns shrink again after expanded card details are collapsed', async ({ page, request }, info) => {
  const project = await fixture(request);
  await page.setViewportSize({ width: 1440, height: 1400 });
  await page.goto(`/p/${project.slug}`);
  const ready = page.locator('.board-column').filter({ has: page.getByRole('heading', { name: 'Ready', exact: true }) });
  await expect(ready.locator('.task-card')).toHaveCount(3);
  await settle(page);
  const before = (await ready.boundingBox())!.height;
  expect(before, 'three collapsed cards should fit to their content').toBeLessThan(700);
  for (let i = 0; i < 3; i++) await ready.getByRole('button', { name: /^Show details for/ }).first().click();
  await expect.poll(async () => (await ready.boundingBox())!.height).toBeGreaterThan(before + 200);
  for (let i = 0; i < 3; i++) await ready.getByRole('button', { name: /^Hide details for/ }).first().click();
  await expect.poll(async () => (await ready.boundingBox())!.height).toBeLessThanOrEqual(before + 2);
  await info.attach('composition-collapsed-again.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
});
