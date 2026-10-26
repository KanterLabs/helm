import { expect, test } from '@playwright/test';

// Readability failures: columns squeezed by six-lane boards, title gutters
// reserved for top-only controls, title/actions overlap, clipped bug badges,
// document overflow on phones, or expanded details losing their controls.
// Design failures: cards pushed off-centre by the scrollbar gutter, task keys
// cut off by bug badges, footer controls drawn as heavy boxes that compete
// with the title, and column names smaller than the card text they label.
test('project boards give bug titles space without hiding card controls', async ({ page, request }, info) => {
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const headers = () => ({ Origin: origin, 'Idempotency-Key': crypto.randomUUID() });
  const response = await request.post('/api/v1/projects', { headers: headers(), data: {
    key: `ROOM${crypto.randomUUID().slice(0, 7)}`.toUpperCase(), name: 'Roomier board demo'
  } });
  expect(response.ok()).toBeTruthy();
  const project = await response.json();
  const columns = (await (await request.get(`/api/v1/projects/${project.id}/columns`)).json()).data;
  const bugs = columns.find((column: { name: string }) => column.name === 'Bugs');
  const titles = [
    '[DEMO] Attachment disappears after reopening a task with a long description',
    '[DEMO] Phone keyboard covers the Add note button',
    '[DEMO] Save button stays disabled after reconnecting'
  ];
  const tasks = [];
  for (const [index, title] of titles.entries()) {
    const created = await request.post(`/api/v1/projects/${project.id}/tasks`, { headers: headers(), data: {
      title, kind: 'bug', column_id: bugs.id, priority: index === 2 ? 'urgent' : 'normal',
      description: 'Synthetic report used to verify readable cards and accessible details.',
      bug: { actual_behavior: 'Synthetic beta demonstration.', ...(index === 2 ? { severity: 's1' } : {}) }
    } });
    expect(created.ok()).toBeTruthy();
    tasks.push(await created.json());
  }
  await page.goto(`/p/${project.slug}`);
  const card = page.locator('.task-card').filter({ hasText: titles[0] });
  const measurements = [];
  for (const width of [1440, 1024, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    await card.scrollIntoViewIfNeeded();
    // Measure resting styles, not the hover state left by the previous click.
    await page.mouse.move(0, 0);
    const metrics = await card.evaluate(element => {
      const bounds = (selector: string) => element.querySelector(selector)!.getBoundingClientRect().toJSON();
      const style = (selector: string) => getComputedStyle(element.querySelector(selector)!);
      const title = element.querySelector('.task-title')!;
      const key = element.querySelector('.task-key')!;
      const column = element.closest('.board-column')!;
      return {
        columnWidth: column.getBoundingClientRect().width, column: column.getBoundingClientRect().toJSON(),
        columnNameSize: parseFloat(getComputedStyle(column.querySelector('.column-name h2')!).fontSize),
        keyClipped: key.scrollWidth > key.clientWidth,
        detailsBackground: style('.card-expand-toggle').backgroundColor,
        actionsBackground: style('[data-card-actions]').backgroundColor, actionsBorder: style('[data-card-actions]').borderTopColor,
        card: element.getBoundingClientRect().toJSON(), title: bounds('.task-title'),
        actions: bounds('[data-card-actions]'), details: bounds('.card-expand-toggle'),
        key: bounds('.task-key'), selection: bounds('.task-select'), drag: bounds('.task-drag-handle'),
        severity: bounds('.severity-badge'), fontSize: parseFloat(getComputedStyle(title).fontSize),
        lineHeight: parseFloat(getComputedStyle(title).lineHeight), pageWidth: document.documentElement.scrollWidth
      };
    });
    measurements.push({ width, ...metrics });
    await info.attach(`board-readability-${width}.png`, { body: await page.screenshot(), contentType: 'image/png' });
    await info.attach(`board-card-${width}.png`, { body: await card.screenshot(), contentType: 'image/png' });
    expect(metrics.columnWidth).toBeGreaterThanOrEqual(width > 600 ? 340 : Math.min(340, width - 32));
    expect(metrics.pageWidth).toBeLessThanOrEqual(width);
    expect(metrics.fontSize).toBeGreaterThanOrEqual(14);
    expect(metrics.lineHeight).toBeGreaterThanOrEqual(20);
    expect(metrics.title.width).toBeGreaterThanOrEqual(metrics.card.width - 32);
    expect(metrics.title.bottom).toBeLessThanOrEqual(metrics.actions.top + 1);
    expect(metrics.title.bottom).toBeLessThanOrEqual(metrics.details.top + 1);
    expect(metrics.details.right).toBeLessThanOrEqual(metrics.actions.left);
    expect(metrics.key.width).toBeGreaterThan(0);
    expect(metrics.key.left).toBeGreaterThanOrEqual(metrics.selection.right);
    expect(metrics.severity.right <= metrics.drag.left || metrics.severity.top >= metrics.drag.bottom).toBeTruthy();
    expect.soft(metrics.keyClipped, 'the task key must be readable in full').toBe(false);
    const inset = { left: metrics.card.left - metrics.column.left, right: metrics.column.right - metrics.card.right };
    expect.soft(Math.abs(inset.left - inset.right), 'cards sit centred in their column').toBeLessThanOrEqual(6);
    expect.soft(metrics.card.width).toBeGreaterThanOrEqual(metrics.columnWidth - 34);
    expect.soft(metrics.columnNameSize).toBeGreaterThanOrEqual(13);
    for (const color of [metrics.detailsBackground, metrics.actionsBackground, metrics.actionsBorder]) {
      expect.soft(color, 'resting footer controls stay quiet').toBe('rgba(0, 0, 0, 0)');
    }
    const details = card.getByRole('button', { name: `Show details for ${tasks[0].key}` });
    await details.click();
    await expect(card.locator('.task-excerpt')).toBeVisible();
    if (width === 1440 || width === 390) {
      await info.attach(`board-card-expanded-${width}.png`, { body: await card.screenshot(), contentType: 'image/png' });
    }
    await card.getByRole('button', { name: `Hide details for ${tasks[0].key}` }).click();
    await expect(details).toBeVisible();
  }
  await info.attach('board-readability.json', { body: JSON.stringify(measurements, null, 2), contentType: 'application/json' });
});
