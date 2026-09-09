export type BoardOverflowDirection = 'previous' | 'next';

export type BoardOverflowState = {
  hasOverflow: boolean;
  canPrevious: boolean;
  canNext: boolean;
  atStart: boolean;
  atEnd: boolean;
  scrollLeft: number;
  maxScrollLeft: number;
};

export type BoardOverflowNavigationOptions = {
  /** The scroll container inside the action node. */
  selector?: string;
  /** Called after the initial measurement and each real scroll/layout change. */
  onChange?: (state: BoardOverflowState) => void;
};

export const defaultBoardOverflowState: BoardOverflowState = {
  hasOverflow: false,
  canPrevious: false,
  canNext: false,
  atStart: true,
  atEnd: true,
  scrollLeft: 0,
  maxScrollLeft: 0
};

// Layout values can be fractional in browsers, especially when a board is
// resized between device-pixel ratios. Treat the final pixel as an edge so a
// disabled button does not remain active because of a sub-pixel remainder.
export const BOARD_OVERFLOW_EDGE_TOLERANCE = 1;

function readNumber(value: number): number {
  return Number.isFinite(value) ? Math.max(0, value) : 0;
}

export function measureBoardOverflow(
  node: Pick<HTMLElement, 'scrollLeft' | 'scrollWidth' | 'clientWidth'>,
  tolerance = BOARD_OVERFLOW_EDGE_TOLERANCE
): BoardOverflowState {
  const scrollLeft = readNumber(node.scrollLeft);
  const scrollWidth = readNumber(node.scrollWidth);
  const clientWidth = readNumber(node.clientWidth);
  const maxScrollLeft = Math.max(0, scrollWidth - clientWidth);
  const hasOverflow = maxScrollLeft > tolerance;
  const atStart = scrollLeft <= tolerance;
  const atEnd = scrollLeft >= maxScrollLeft - tolerance;

  return {
    hasOverflow,
    canPrevious: hasOverflow && !atStart,
    canNext: hasOverflow && !atEnd,
    atStart,
    atEnd,
    scrollLeft,
    maxScrollLeft
  };
}

export function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

function resolveScroller(node: HTMLElement, selector = '[data-board-overflow-scroll]'): HTMLElement | null {
  if (node.matches(selector)) return node;
  return node.querySelector<HTMLElement>(selector);
}

function directColumns(node: HTMLElement): HTMLElement[] {
  return Array.from(node.children).filter((child): child is HTMLElement =>
    child instanceof HTMLElement && child.classList.contains('board-column')
  );
}

function columnOffset(node: HTMLElement, scroller: HTMLElement, index: number): number {
  // A column's offsetLeft can be relative to an ancestor other than the
  // scroller. Prefer the two rects, which remain in the same coordinate space
  // after the board is wrapped; offsetLeft is a useful fallback for test DOMs
  // and embedded web views where layout rects are not populated.
  const scrollerRect = scroller.getBoundingClientRect();
  const columnRect = node.getBoundingClientRect();
  if (scrollerRect.left !== 0 || columnRect.left !== 0 || scrollerRect.width !== 0 || columnRect.width !== 0) {
    return Math.max(0, columnRect.left - scrollerRect.left + scroller.scrollLeft);
  }
  const offset = Number(node.offsetLeft);
  if (offset > 0 || index === 0) return Math.max(0, offset);
  return Math.max(0, columnRect.left - scrollerRect.left + scroller.scrollLeft);
}

export function boardColumnScrollTarget(
  node: HTMLElement,
  direction: BoardOverflowDirection,
  tolerance = BOARD_OVERFLOW_EDGE_TOLERANCE
): number | null {
  const scroller = resolveScroller(node);
  if (!scroller) return null;

  const maxScrollLeft = Math.max(0, readNumber(scroller.scrollWidth) - readNumber(scroller.clientWidth));
  if (maxScrollLeft <= tolerance) return null;

  const current = readNumber(scroller.scrollLeft);
  const columns = directColumns(scroller);
  const offsets = columns.map((column, index) => columnOffset(column, scroller, index));
  const candidate = direction === 'next'
    ? offsets.find((offset) => offset > current + tolerance)
    : offsets.slice().reverse().find((offset) => offset < current - tolerance);
  const target = candidate === undefined
    ? direction === 'next' ? maxScrollLeft : 0
    : candidate;
  return Math.min(maxScrollLeft, Math.max(0, target));
}

export function scrollBoardColumn(
  node: HTMLElement,
  direction: BoardOverflowDirection,
  options: { behavior?: ScrollBehavior } = {}
): number | null {
  const scroller = resolveScroller(node);
  if (!scroller) return null;

  const target = boardColumnScrollTarget(scroller, direction);
  if (target === null) return null;

  const behavior = options.behavior || (prefersReducedMotion() ? 'auto' : 'smooth');
  if (typeof scroller.scrollTo === 'function') {
    scroller.scrollTo({ left: target, behavior });
  } else {
    scroller.scrollLeft = target;
  }
  return target;
}

function sameState(left: BoardOverflowState, right: BoardOverflowState): boolean {
  return left.hasOverflow === right.hasOverflow
    && left.canPrevious === right.canPrevious
    && left.canNext === right.canNext
    && left.atStart === right.atStart
    && left.atEnd === right.atEnd
    && left.scrollLeft === right.scrollLeft
    && left.maxScrollLeft === right.maxScrollLeft;
}

/**
 * Svelte action for a wrapper around the board's horizontal scroll container.
 * The action intentionally listens to scroll and layout observers instead of
 * polling, and disconnects every observer/listener when the wrapper unmounts.
 */
export function boardOverflowNavigation(
  node: HTMLElement,
  initialOptions: BoardOverflowNavigationOptions = {}
) {
  let options = initialOptions;
  let scroller: HTMLElement | null = null;
  let state = defaultBoardOverflowState;
  let hasEmitted = false;
  let frame = 0;

  const emit = (next: BoardOverflowState) => {
    if (hasEmitted && sameState(state, next)) return;
    state = next;
    hasEmitted = true;
    options.onChange?.(state);
  };

  const measure = () => {
    if (!scroller) {
      emit(defaultBoardOverflowState);
      return;
    }
    emit(measureBoardOverflow(scroller));
  };

  const scheduleMeasure = () => {
    if (frame || typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
      if (!frame) measure();
      return;
    }
    frame = window.requestAnimationFrame(() => {
      frame = 0;
      measure();
    });
  };

  const onScroll = () => measure();

  const resizeObserver = typeof ResizeObserver !== 'undefined'
    ? new ResizeObserver(scheduleMeasure)
    : null;

  const observeLayout = () => {
    resizeObserver?.disconnect();
    if (!scroller) return;
    resizeObserver?.observe(scroller);
    for (const child of directColumns(scroller)) resizeObserver?.observe(child);
  };

  const observeScroller = (next: HTMLElement | null) => {
    if (next === scroller) {
      observeLayout();
      measure();
      return;
    }
    if (scroller) scroller.removeEventListener('scroll', onScroll);
    scroller = next;
    if (!scroller) {
      resizeObserver?.disconnect();
      measure();
      return;
    }
    scroller.addEventListener('scroll', onScroll, { passive: true });
    observeLayout();
    measure();
  };

  const mutationObserver = typeof MutationObserver !== 'undefined'
    ? new MutationObserver(() => {
      observeScroller(resolveScroller(node, options.selector));
      scheduleMeasure();
    })
    : null;

  observeScroller(resolveScroller(node, options.selector));
  mutationObserver?.observe(node, { childList: true, subtree: true });
  if (typeof window !== 'undefined') window.addEventListener('resize', scheduleMeasure);

  return {
    update(nextOptions: BoardOverflowNavigationOptions = {}) {
      options = nextOptions;
      observeScroller(resolveScroller(node, options.selector));
      scheduleMeasure();
    },
    destroy() {
      if (scroller) scroller.removeEventListener('scroll', onScroll);
      resizeObserver?.disconnect();
      mutationObserver?.disconnect();
      if (typeof window !== 'undefined') {
        window.removeEventListener('resize', scheduleMeasure);
        if (frame && typeof window.cancelAnimationFrame === 'function') window.cancelAnimationFrame(frame);
      }
      frame = 0;
    }
  };
}
