import { expect, test } from '@playwright/test';
import { createFixture, json, requireBoundingBox, measureColumnCards, boardColumn, settleLayout, openBoard, loadAllCards, type Viewport } from './support/boardColumnScrollFixture';

test('fits equal-height columns to the viewport so only board and column bodies scroll', async ({ page, request }) => {
  test.setTimeout(120_000);

  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const fixture = await createFixture(request);
  const viewports: Viewport[] = [
    { name: 'desktop', width: 1280, height: 900 },
    { name: 'phone portrait', width: 390, height: 844 },
    { name: 'short landscape', width: 900, height: 480 }
  ];

  // The fixture creates a large mutation-event backlog. Layout assertions do
  // not need live reconciliation, and an empty feed keeps the board DOM
  // attached while each viewport is measured.
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

  for (const viewport of viewports) {
    const board = await openBoard(page, fixture, viewport);
    const emptyColumn = boardColumn(board, fixture.columns[0]);
    const fewColumn = boardColumn(board, fixture.columns[1]);
    const manyColumn = boardColumn(board, fixture.manyColumn);
    await expect(emptyColumn.locator('.column-empty')).toBeVisible();
    await expect(fewColumn.locator('.task-card')).toHaveCount(1);
    await loadAllCards(manyColumn, fixture.manyTaskCount);

    // Loading the final page must not alter the board's horizontal overflow;
    // only each column's card body should own vertical overflow.
    await board.scrollIntoViewIfNeeded();
    await settleLayout(page);
    const boardMetrics = await board.evaluate((element) => {
      const box = (target: Element) => {
        const rect = (target as HTMLElement).getBoundingClientRect();
        return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right, height: rect.height };
      };
      const root = document.scrollingElement || document.documentElement;
      const style = getComputedStyle(element);
      const columns = Array.from(element.querySelectorAll<HTMLElement>('.board-column')).map((column) => ({
        ...box(column),
        cards: (() => {
          const body = column.querySelector<HTMLElement>('.column-cards');
          if (!body) throw new Error('board column is missing its card body');
          return box(body);
        })()
      }));
      return {
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
        boardClientWidth: element.clientWidth,
        boardScrollWidth: element.scrollWidth,
        boardOverflowX: style.overflowX,
        documentWidth: document.documentElement.scrollWidth,
        bodyWidth: document.body.scrollWidth,
        bodyScrollHeight: root.scrollHeight,
        bodyClientHeight: root.clientHeight,
        columns
      };
    });
    const columnHeights = boardMetrics.columns.map((column) => column.height);
    expect(Math.max(...columnHeights) - Math.min(...columnHeights), `${viewport.name} columns should share one height`).toBeLessThanOrEqual(2);
    expect(Math.min(...columnHeights), `${viewport.name} columns should retain a usable minimum height`).toBeGreaterThanOrEqual(220);
    expect(boardMetrics.boardScrollWidth, `${viewport.name} board should retain horizontal scrolling`).toBeGreaterThan(boardMetrics.boardClientWidth + 10);
    expect(boardMetrics.boardOverflowX, `${viewport.name} board should own horizontal overflow`).toMatch(/auto|scroll/);
    expect(boardMetrics.documentWidth, `${viewport.name} board overflow must not widen the document`).toBeLessThanOrEqual(boardMetrics.viewportWidth + 2);
    expect(boardMetrics.bodyWidth, `${viewport.name} board overflow must not widen the body`).toBeLessThanOrEqual(boardMetrics.viewportWidth + 2);

    const manyBody = manyColumn.locator('.column-cards');
    await manyBody.evaluate((element) => {
      element.scrollTop = 0;
      element.dispatchEvent(new Event('scroll'));
    });
    await settleLayout(page);
    const manyAtTop = await measureColumnCards(manyColumn);
    expect(manyAtTop.cards, `${viewport.name} should render all cards in the 100-card column`).toHaveLength(fixture.manyTaskCount);
    expect(manyAtTop.cardsScrollTop, `${viewport.name} card body should start at scrollTop 0`).toBe(0);
    expect(manyAtTop.cards[0].top, `${viewport.name} first card should start inside the card body`).toBeGreaterThanOrEqual(manyAtTop.body.top - 2);
    expect(Math.min(...manyAtTop.cards.map((card) => card.height)), `${viewport.name} cards must keep their natural height`).toBeGreaterThan(24);
    expect(manyAtTop.cardsScrollHeight, `${viewport.name} more than ten cards should overflow inside the card body`).toBeGreaterThan(manyAtTop.cardsClientHeight + 100);
    expect(manyAtTop.cardsScrollWidth, `${viewport.name} card content should not create horizontal overflow`).toBeLessThanOrEqual(manyAtTop.cardsClientWidth + 2);
    expect(Math.max(...manyAtTop.cards.map((card) => card.right)), `${viewport.name} cards should remain contained by the card body width`).toBeLessThanOrEqual(manyAtTop.body.right + 2);

    const fewMetrics = await measureColumnCards(fewColumn);
    expect(fewMetrics.cards, `${viewport.name} few-card column should retain its sample cards`).toHaveLength(1);
    expect(fewMetrics.cardsScrollHeight, `${viewport.name} a short column should not need inner scrolling`).toBeLessThanOrEqual(fewMetrics.cardsClientHeight + 2);

    const emptyMetrics = await measureColumnCards(emptyColumn);
    expect(Math.abs(emptyMetrics.body.height - manyAtTop.body.height), `${viewport.name} empty columns should share the populated column height`).toBeLessThanOrEqual(2);

    const columnBefore = await requireBoundingBox(manyColumn, `${viewport.name} many-card column should have a bounding box`);
    const header = manyColumn.locator('.column-header');
    const footer = manyColumn.locator('.quick-add-wrap');
    const headerBefore = await requireBoundingBox(header, `${viewport.name} column header should have a bounding box`);
    const footerBefore = await requireBoundingBox(footer, `${viewport.name} Add task footer should have a bounding box`);
    await manyBody.evaluate((element) => { element.scrollTop = element.scrollHeight; });
    await expect.poll(() => manyBody.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
    const manyAtEnd = await measureColumnCards(manyColumn);
    const columnAfter = await requireBoundingBox(manyColumn, `${viewport.name} many-card column should remain measurable after scrolling`);
    const headerAfter = await requireBoundingBox(header, `${viewport.name} column header should remain measurable after scrolling`);
    const footerAfter = await requireBoundingBox(footer, `${viewport.name} Add task footer should remain measurable after scrolling`);
    expect(manyAtEnd.cardsScrollTop, `${viewport.name} inner scroll should reach the card body end`).toBeGreaterThan(0);
    expect(Math.abs(headerAfter.y - headerBefore.y), `${viewport.name} column header should remain stationary`).toBeLessThanOrEqual(1);
    expect(Math.abs(footerAfter.y - footerBefore.y), `${viewport.name} Add task footer should remain stationary`).toBeLessThanOrEqual(1);
    expect(Math.abs((headerAfter.y - columnAfter.y) - (headerBefore.y - columnBefore.y)), `${viewport.name} column header should stay fixed relative to its column`).toBeLessThanOrEqual(1);
    expect(Math.abs((footerAfter.y - columnAfter.y) - (footerBefore.y - columnBefore.y)), `${viewport.name} Add task footer should stay fixed relative to its column`).toBeLessThanOrEqual(1);
    const cardHeightDelta = Math.max(...manyAtTop.cards.map((card, index) => Math.abs(card.height - manyAtEnd.cards[index].height)));
    expect(cardHeightDelta, `${viewport.name} card heights must not shrink while the body scrolls`).toBeLessThanOrEqual(1);
    expect(await emptyColumn.locator('.column-cards').evaluate((element) => element.scrollTop), `${viewport.name} empty column should keep its own scroll position`).toBe(0);

    // Columns never exceed the viewport. On desktop they also end inside it,
    // so the page stays still and only column bodies own vertical overflow.
    // Phones and short landscapes spend most of the height on the heading and
    // toolbar, so columns keep their minimum height and the page scrolls.
    expect(Math.max(...columnHeights), `${viewport.name} columns should fit within the viewport height`).toBeLessThanOrEqual(boardMetrics.viewportHeight);
    if (viewport.name === 'desktop') {
      expect(boardMetrics.bodyScrollHeight, 'desktop page should not scroll vertically').toBeLessThanOrEqual(boardMetrics.bodyClientHeight + 1);
      expect(Math.max(...boardMetrics.columns.map((column) => column.bottom)), 'desktop columns should end inside the viewport').toBeLessThanOrEqual(boardMetrics.viewportHeight + 1);

      // A shorter viewport shrinks the shared column height rather than
      // pushing the board below the fold.
      const heightBeforeResize = (await requireBoundingBox(manyColumn, 'desktop column should have a bounding box before height resize')).height;
      await page.setViewportSize({ width: viewport.width, height: viewport.height - 100 });
      await settleLayout(page);
      const heightAfterResize = (await requireBoundingBox(manyColumn, 'desktop column should have a bounding box after height resize')).height;
      expect(Math.abs(heightBeforeResize - heightAfterResize - 100), 'a shorter viewport should shrink the column by the same amount').toBeLessThanOrEqual(2);
      expect(await page.evaluate(() => {
        const root = document.scrollingElement || document.documentElement;
        return root.scrollHeight - root.clientHeight;
      }), 'the shorter desktop page should still not scroll').toBeLessThanOrEqual(1);
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await settleLayout(page);
    }

    if (viewport.name === 'phone portrait') {
      await manyBody.evaluate((element) => { element.scrollTop = 0; });
      const beforeResize = await measureColumnCards(manyColumn);
      // Narrow below the readable 340px column minimum to exercise responsive sizing.
      await page.setViewportSize({ width: 320, height: viewport.height });
      await settleLayout(page);
      const afterResize = await measureColumnCards(manyColumn);
      expect(afterResize.body.width, 'resizing the viewport should resize the scoped column').not.toBe(beforeResize.body.width);
      expect(afterResize.body.height, 'resized columns should still fit within the viewport height').toBeLessThanOrEqual(viewport.height);
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await settleLayout(page);
    }

    // A short landscape viewport leaves too little room below the heading and
    // toolbar, so columns keep their minimum height and the page scrolls. Body
    // scrolling must not consume or reset the column's independent scroll.
    if (viewport.name === 'short landscape') {
      expect(boardMetrics.bodyScrollHeight).toBeGreaterThan(boardMetrics.bodyClientHeight);
      const columnScrollBeforePage = await manyBody.evaluate((element) => element.scrollTop);
      const bodyScroll = await page.evaluate(() => {
        const root = document.scrollingElement || document.documentElement;
        // The board was aligned into view above, which may already place the
        // document at its maximum scroll position in a short viewport. Start
        // from the top so this check proves the page itself still scrolls.
        root.scrollTop = 0;
        const before = root.scrollTop;
        root.scrollTop = root.scrollHeight;
        return { before, after: root.scrollTop };
      });
      expect(bodyScroll.after, `${viewport.name} page should scroll independently of column bodies`).toBeGreaterThan(bodyScroll.before);
      expect(await manyBody.evaluate((element) => element.scrollTop), `${viewport.name} page scrolling must not consume the column body scroll`).toBe(columnScrollBeforePage);
    }

    // Changing board criteria is a navigation within the board. It should
    // reset the current column's vertical page to the top before new cards
    // replace the old result set.
    if (viewport.name === 'phone portrait') {
      await manyColumn.locator('.column-cards').evaluate((element) => {
        element.scrollTop = 120;
        element.dispatchEvent(new Event('scroll'));
      });
      const boardActions = page.locator('[data-board-actions-trigger]');
      await boardActions.click();
      await page.getByRole('button', { name: 'Refresh board', exact: true }).click();
      if (await boardActions.evaluate((element) => Boolean(element.closest('details')?.open))) await boardActions.click();
      await expect(manyColumn.locator('.task-card')).toHaveCount(50);
      await expect.poll(() => manyColumn.locator('.column-cards').evaluate((element) => element.scrollTop)).toBe(120);
      await page.locator('[data-board-filter-trigger]').click();
      const filterPanel = page.getByRole('dialog', { name: 'Board filters', exact: true });
      const priorityFilter = filterPanel.getByLabel('Filter by priority');
      await priorityFilter.selectOption('high');
      await filterPanel.getByRole('button', { name: 'Show results', exact: true }).click();
      await expect.poll(() => manyColumn.locator('.column-cards').evaluate((element) => element.scrollTop)).toBe(0);
    }
  }
});
