import { expect, type APIRequestContext, type APIResponse, type Locator, type Page } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; name: string; semantic_state: string; position: number };
type Task = { id: string; key: string; title: string; column_id: string; position: number; version: number };
type Collection<T> = { data: T[]; next_cursor?: string | null };
type BoardFixture = { project: Project; columns: Column[]; manyColumn: Column; manyTaskCount: number };
export type Viewport = { width: number; height: number; name: string };

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

export async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

async function postJSON<T>(request: APIRequestContext, path: string, body: unknown, key: string): Promise<T> {
  return json<T>(await request.post(path, {
    data: body,
    headers: mutationHeaders(key)
  }), `POST ${path}`);
}

export function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

export async function createFixture(request: APIRequestContext): Promise<BoardFixture> {
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await postJSON<Project>(request, '/api/v1/projects', {
    key: `COL${runID}`.slice(0, 16),
    name: `Board column scroll ${runID}`,
    description: 'Viewport-bounded board column regression fixture.'
  }, `board-column-scroll-${runID}-project`);

  let columns = collectionData(await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list board column scroll columns'
  ));

  // Keep eight columns in the fixture so horizontal board scrolling remains
  // exercised even on a wide desktop viewport. The first five are created by
  // the project endpoint; the extra columns use the public column API.
  for (let index = columns.length; index < 8; index += 1) {
    await postJSON<Column>(request, `/api/v1/projects/${project.id}/columns`, {
      name: `Review ${index + 1}`,
      semantic_state: 'ready',
      position: index
    }, `board-column-scroll-${runID}-column-${index + 1}`);
  }
  columns = collectionData(await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'reload board column scroll columns'
  ));
  expect(columns.length, 'the fixture should have enough columns to overflow the board').toBe(8);

  // Empty, few-card, and 100-card columns are deliberately adjacent in the
  // same board. The many-card column is paged once in the browser so all 100
  // cards participate in the flex/overflow regression.
  const counts = [0, 1, 0, 0, 0, 0, 0, 100];
  const manyColumn = columns[columns.length - 1];
  expect(manyColumn).toBeTruthy();
  for (const [columnIndex, count] of counts.entries()) {
    const column = columns[columnIndex];
    for (let taskIndex = 0; taskIndex < count; taskIndex += 1) {
      const longCard = column.id === manyColumn.id && taskIndex === 0;
      await postJSON<Task>(request, `/api/v1/projects/${project.id}/tasks`, {
        title: longCard
          ? `Viewport card ${String(taskIndex + 1).padStart(3, '0')} keeps a longer title measurable in a bounded card`
          : `Viewport card ${String(taskIndex + 1).padStart(3, '0')}`,
        column_id: column.id,
        priority: 'normal',
        ...(longCard ? {
          description: 'This deliberately verbose card description makes the fixture exercise intrinsic card sizing before the column body starts scrolling.'
        } : {})
      }, `board-column-scroll-${runID}-task-${columnIndex + 1}-${taskIndex + 1}`);
    }
  }

  return { project, columns, manyColumn: manyColumn as Column, manyTaskCount: counts[counts.length - 1] };
}

export async function requireBoundingBox(locator: Locator, description: string) {
  const box = await locator.boundingBox();
  if (!box) throw new Error(description);
  return box;
}

export async function measureColumnCards(column: Locator) {
  return await column.locator('.column-cards').evaluate((element) => {
    const body = element.getBoundingClientRect();
    const cards = Array.from(element.querySelectorAll<HTMLElement>(':scope > .task-card')).map((card) => {
      const rect = card.getBoundingClientRect();
      return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right, width: rect.width, height: rect.height };
    });
    return {
      body: { top: body.top, bottom: body.bottom, left: body.left, right: body.right, width: body.width, height: body.height },
      cards,
      cardsClientHeight: element.clientHeight,
      cardsScrollHeight: element.scrollHeight,
      cardsClientWidth: element.clientWidth,
      cardsScrollWidth: element.scrollWidth,
      cardsScrollTop: element.scrollTop
    };
  });
}

export function boardColumn(board: Locator, column: Column): Locator {
  return board.getByRole('article', { name: `${column.name} column`, exact: true });
}

export async function settleLayout(page: Page): Promise<void> {
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  }));
}

export async function openBoard(page: Page, fixture: BoardFixture, viewport: Viewport): Promise<Locator> {
  await page.setViewportSize({ width: viewport.width, height: viewport.height });
  await page.goto(`/p/${fixture.project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  await expect(board.locator('.board-column')).toHaveCount(fixture.columns.length);
  // Initial event reconciliation may replace the loading board while WebKit
  // resolves this action. Reacquire the locator after that legitimate remount.
  await expect(async () => { await board.scrollIntoViewIfNeeded(); }).toPass({ timeout: 10_000 });
  await settleLayout(page);
  return board;
}

export async function loadAllCards(column: Locator, expectedCount: number): Promise<void> {
  await expect(column.locator('.task-card')).toHaveCount(Math.min(50, expectedCount));
  if (expectedCount <= 50) return;

  const loadMore = column.getByRole('button', { name: 'Load more tasks', exact: true });
  await expect(loadMore).toBeVisible();
  await loadMore.click();
  await expect(column.locator('.task-card')).toHaveCount(expectedCount);
}

