import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  boardColumnScrollTarget,
  boardOverflowNavigation,
  measureBoardOverflow,
  scrollBoardColumn
} from './boardOverflowNavigation';

type ResizeCallback = (entries: ResizeObserverEntry[], observer: ResizeObserver) => void;

class TestResizeObserver {
  static instances: TestResizeObserver[] = [];
  readonly callback: ResizeCallback;
  readonly observed = new Set<Element>();
  disconnected = false;

  constructor(callback: ResizeCallback) {
    this.callback = callback;
    TestResizeObserver.instances.push(this);
  }

  observe(target: Element): void {
    this.observed.add(target);
  }

  disconnect(): void {
    this.disconnected = true;
    this.observed.clear();
  }

  trigger(): void {
    this.callback([], this as unknown as ResizeObserver);
  }
}

function dimensions(
  node: HTMLElement,
  values: { scrollWidth: number; clientWidth: number; scrollLeft?: number }
): void {
  Object.defineProperties(node, {
    scrollWidth: { configurable: true, value: values.scrollWidth, writable: true },
    clientWidth: { configurable: true, value: values.clientWidth, writable: true },
    scrollLeft: { configurable: true, value: values.scrollLeft || 0, writable: true }
  });
}

function boardFixture(): { root: HTMLElement; board: HTMLElement; columns: HTMLElement[] } {
  const root = document.createElement('div');
  const board = document.createElement('section');
  board.dataset.boardOverflowScroll = 'true';
  const columns = [0, 1, 2, 3].map((index) => {
    const column = document.createElement('article');
    column.className = 'board-column';
    Object.defineProperty(column, 'offsetLeft', { configurable: true, value: index * 220 });
    board.append(column);
    return column;
  });
  root.append(board);
  dimensions(board, { scrollWidth: 880, clientWidth: 440 });
  return { root, board, columns };
}

afterEach(() => {
  vi.unstubAllGlobals();
  TestResizeObserver.instances = [];
  document.body.replaceChildren();
});

describe('board overflow measurements', () => {
  it('derives overflow and edge button state from the real scroll metrics', () => {
    const node = { scrollWidth: 1000, clientWidth: 400, scrollLeft: 0 } as HTMLElement;
    expect(measureBoardOverflow(node)).toMatchObject({
      hasOverflow: true,
      canPrevious: false,
      canNext: true,
      atStart: true,
      atEnd: false,
      maxScrollLeft: 600
    });
    node.scrollLeft = 600;
    expect(measureBoardOverflow(node)).toMatchObject({
      canPrevious: true,
      canNext: false,
      atStart: false,
      atEnd: true
    });
    expect(measureBoardOverflow({ scrollWidth: 400, clientWidth: 400, scrollLeft: 0 } as HTMLElement).hasOverflow).toBe(false);
  });

  it('targets adjacent column starts and clamps the final target to the scroll edge', () => {
    const { root, board } = boardFixture();
    expect(boardColumnScrollTarget(root, 'next')).toBe(220);
    board.scrollLeft = 220;
    expect(boardColumnScrollTarget(root, 'next')).toBe(440);
    expect(boardColumnScrollTarget(root, 'previous')).toBe(0);
    board.scrollLeft = 430;
    expect(boardColumnScrollTarget(root, 'next')).toBe(440);
    expect(boardColumnScrollTarget(root, 'previous')).toBe(220);
  });
});

describe('board overflow action lifecycle', () => {
  it('updates on scroll and ResizeObserver callbacks, and tears down listeners', async () => {
    vi.stubGlobal('ResizeObserver', TestResizeObserver);
    const { root, board } = boardFixture();
    const states = [] as ReturnType<typeof measureBoardOverflow>[];
    const mounted = boardOverflowNavigation(root, { onChange: (state) => states.push(state) });
    const resizeObserver = TestResizeObserver.instances[0];
    expect(states[states.length - 1]).toMatchObject({ hasOverflow: true, canPrevious: false, canNext: true });

    board.scrollLeft = 220;
    board.dispatchEvent(new Event('scroll'));
    expect(states[states.length - 1]).toMatchObject({ canPrevious: true, canNext: true, scrollLeft: 220 });

    board.scrollLeft = 440;
    resizeObserver.trigger();
    await vi.waitFor(() => expect(states[states.length - 1]).toMatchObject({ canPrevious: true, canNext: false, scrollLeft: 440 }));

    mounted.destroy();
    expect(resizeObserver.disconnected).toBe(true);
    const count = states.length;
    board.scrollLeft = 0;
    board.dispatchEvent(new Event('scroll'));
    expect(states).toHaveLength(count);
  });

  it('re-observes columns when the board grows or shrinks without polling', async () => {
    vi.stubGlobal('ResizeObserver', TestResizeObserver);
    const { root, board } = boardFixture();
    const states = [] as ReturnType<typeof measureBoardOverflow>[];
    const mounted = boardOverflowNavigation(root, { onChange: (state) => states.push(state) });
    expect(TestResizeObserver.instances[0].observed.size).toBe(5);

    const added = document.createElement('article');
    added.className = 'board-column';
    Object.defineProperty(added, 'offsetLeft', { configurable: true, value: 880 });
    board.append(added);
    dimensions(board, { scrollWidth: 1100, clientWidth: 440, scrollLeft: 440 });
    await Promise.resolve();
    expect(states[states.length - 1]).toMatchObject({ hasOverflow: true, canNext: true, maxScrollLeft: 660 });
    expect(TestResizeObserver.instances[0].observed.has(added)).toBe(true);

    board.removeChild(added);
    dimensions(board, { scrollWidth: 880, clientWidth: 440, scrollLeft: 440 });
    await Promise.resolve();
    expect(states[states.length - 1]).toMatchObject({ hasOverflow: true, canNext: false, maxScrollLeft: 440 });

    mounted.destroy();
  });

  it('uses instant scrolling for reduced motion and smooth scrolling otherwise', () => {
    const { root, board } = boardFixture();
    const scrollTo = vi.fn(({ left, behavior }: { left: number; behavior: ScrollBehavior }) => {
      board.scrollLeft = left;
      expect(behavior).toBeDefined();
    });
    Object.defineProperty(board, 'scrollTo', { configurable: true, value: scrollTo });
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })));
    expect(scrollBoardColumn(root, 'next')).toBe(220);
    expect(scrollTo).toHaveBeenLastCalledWith({ left: 220, behavior: 'smooth' });

    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true })));
    expect(scrollBoardColumn(root, 'next')).toBe(440);
    expect(scrollTo).toHaveBeenLastCalledWith({ left: 440, behavior: 'auto' });
  });
});
