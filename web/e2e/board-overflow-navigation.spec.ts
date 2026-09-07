import { expect, test, type APIRequestContext } from '@playwright/test';

type Project = { id: string; slug: string };
type Column = { id: string; name: string; position: number };

const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

function mutationHeaders(key: string): Record<string, string> {
  return {
    'Content-Type': 'application/json',
    Origin: e2eOrigin,
    'Idempotency-Key': key
  };
}

async function createOverflowProject(request: APIRequestContext): Promise<{ project: Project; columns: Column[] }> {
  const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const projectResponse = await request.post('/api/v1/projects', {
    data: { key: `OVF${runId}`.slice(0, 16), name: `Board overflow ${runId}` },
    headers: mutationHeaders(`board-overflow-${runId}-project`)
  });
  expect(projectResponse.ok()).toBeTruthy();
  const project = await projectResponse.json() as Project;

  const columnsResponse = await request.get(`/api/v1/projects/${project.id}/columns?limit=20`);
  expect(columnsResponse.ok()).toBeTruthy();
  let columns = (await columnsResponse.json() as { data?: Column[] } | Column[]);
  let data = Array.isArray(columns) ? columns : columns.data || [];
  for (let index = data.length; index < 8; index += 1) {
    const response = await request.post(`/api/v1/projects/${project.id}/columns`, {
      data: { name: `Review ${index + 1}`, semantic_state: 'ready', position: index },
      headers: mutationHeaders(`board-overflow-${runId}-column-${index + 1}`)
    });
    expect(response.ok()).toBeTruthy();
  }

  columns = await (await request.get(`/api/v1/projects/${project.id}/columns?limit=20`)).json() as { data?: Column[] } | Column[];
  data = Array.isArray(columns) ? columns : columns.data || [];
  expect(data).toHaveLength(8);
  return { project, columns: data };
}

test('reveals offscreen board columns with edge-aware keyboard navigation', async ({ page, request }) => {
  test.setTimeout(90_000);

  const status = await request.get('/api/v1/auth/status');
  expect(status.ok()).toBeTruthy();
  expect((await status.json() as { mode?: string }).mode).toBe('disabled');

  const fixture = await createOverflowProject(request);
  await page.route('**/api/v1/events**', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ data: [], next_cursor: null })
    });
  });

  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/p/${fixture.project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  await expect(board.locator('.board-column')).toHaveCount(fixture.columns.length);

  const overflow = page.locator('.board-overflow-navigation');
  const previous = page.getByRole('button', { name: 'Show previous columns', exact: true });
  const next = page.getByRole('button', { name: 'Show next columns', exact: true });
  await expect(overflow).toHaveAttribute('data-board-overflow', 'true');
  await expect(previous).toBeDisabled();
  await expect(next).toBeEnabled();
  const horizontalOverflow = await board.evaluate((element) => element.scrollWidth > element.clientWidth + 10);
  expect(horizontalOverflow).toBeTruthy();

  await next.focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => board.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await expect(previous).toBeEnabled();
  await expect(overflow).toHaveAttribute('data-board-overflow-at-start', 'false');

  await board.evaluate((element) => {
    element.scrollLeft = element.scrollWidth;
    element.dispatchEvent(new Event('scroll'));
  });
  await expect(next).toBeDisabled();
  await expect(previous).toBeEnabled();
  await expect(overflow).toHaveAttribute('data-board-overflow-at-end', 'true');
  await previous.focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => board.evaluate((element) => element.scrollLeft)).toBeLessThan(
    await board.evaluate((element) => element.scrollWidth - element.clientWidth)
  );

  // A narrower board still exposes the same controls, and the action follows
  // the new client width without a timer or a full board reload.
  await page.setViewportSize({ width: 520, height: 900 });
  await expect(overflow).toHaveAttribute('data-board-overflow', 'true');
  await expect(next).toBeEnabled();

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await board.evaluate((element) => {
    const original = element.scrollTo.bind(element);
    (element as HTMLElement & { lastBoardScrollBehavior?: ScrollBehavior }).scrollTo = (options) => {
      (element as HTMLElement & { lastBoardScrollBehavior?: ScrollBehavior }).lastBoardScrollBehavior =
        typeof options === 'object' ? options.behavior : 'auto';
      original(options);
    };
    element.scrollLeft = 0;
    element.dispatchEvent(new Event('scroll'));
  });
  await next.click();
  await expect.poll(() => board.evaluate((element) => (
    element as HTMLElement & { lastBoardScrollBehavior?: ScrollBehavior }
  ).lastBoardScrollBehavior)).toBe('auto');
});
