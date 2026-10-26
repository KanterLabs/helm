import { tick } from 'svelte';

/** Shortest shared column height, so cramped viewports keep a usable card body. */
// Keep a full readable card visible beneath the column header and intake hint.
export const minBoardColumnHeight = 340;

/**
 * Columns share the tallest natural stack, capped to the viewport space below the
 * board so the page itself stays still and only column bodies scroll.
 */
export function boardColumnHeight(natural: number[], available: number, minimum = minBoardColumnHeight) {
  const tallest = natural.length ? Math.max(...natural) : 0;
  return Math.ceil(Math.min(Math.max(tallest, minimum), Math.max(available, minimum)));
}

/** Natural height of a column: fixed chrome plus every card-body child at full size. */
function naturalColumnHeight(column: HTMLElement, body: HTMLElement) {
  const style = getComputedStyle(body);
  const children = Array.from(body.children) as HTMLElement[];
  const gap = parseFloat(style.rowGap) || 0;
  const content = children.reduce((sum, child) => sum + child.offsetHeight, 0) + gap * Math.max(0, children.length - 1);
  const padding = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
  return column.offsetHeight - body.offsetHeight + padding + content;
}

/** Space reserved beneath the columns: board padding, its scrollbar, and ancestor bottom padding. */
function trailingSpace(node: HTMLElement) {
  const own = getComputedStyle(node);
  let space = parseFloat(own.paddingBottom) + parseFloat(own.borderBottomWidth) + (node.offsetHeight - node.clientHeight
    - parseFloat(own.borderTopWidth) - parseFloat(own.borderBottomWidth));
  for (let parent = node.parentElement; parent && parent !== document.documentElement; parent = parent.parentElement) {
    const style = getComputedStyle(parent);
    space += parseFloat(style.paddingBottom) + parseFloat(style.borderBottomWidth);
  }
  return space;
}

/** Keep columns aligned at one height that fits the viewport, so the page does not scroll. */
export function boardCardHeight(node: HTMLElement) {
  let frame = 0;
  const measure = () => {
    frame = 0;
    const natural: number[] = [];
    let top = Infinity;
    let gutter = 0;
    for (const column of node.querySelectorAll<HTMLElement>(':scope > .board-column')) {
      const body = column.querySelector<HTMLElement>('.column-cards');
      if (!body) continue;
      gutter = Math.max(gutter, body.offsetWidth - body.clientWidth);
      natural.push(naturalColumnHeight(column, body));
      top = Math.min(top, column.getBoundingClientRect().top + window.scrollY);
    }
    if (!natural.length) return;
    const available = window.innerHeight - top - trailingSpace(node);
    const value = `${boardColumnHeight(natural, available)}px`;
    if (node.style.getPropertyValue('--board-column-height') !== value) {
      node.style.setProperty('--board-column-height', value);
    }
    // Overlay scrollbars reserve nothing; classic ones reserve a stable gutter
    // that the card padding gives back so cards stay centred in the column.
    if (node.style.getPropertyValue('--column-gutter') !== `${gutter}px`) {
      node.style.setProperty('--column-gutter', `${gutter}px`);
    }
  };
  const schedule = () => {
    if (!frame) frame = window.requestAnimationFrame(measure);
  };
  const resize = new ResizeObserver(schedule);
  const observeLayout = () => {
    resize.disconnect();
    resize.observe(node);
    // Alerts or toolbar wrapping above the board move it without resizing it.
    const content = node.closest('.content');
    if (content) resize.observe(content);
    for (const column of node.querySelectorAll<HTMLElement>(':scope > .board-column')) {
      for (const child of column.children) {
        if (!child.classList.contains('column-cards')) resize.observe(child);
      }
      for (const child of column.querySelectorAll(':scope > .column-cards > *')) resize.observe(child);
    }
    schedule();
  };
  const mutations = new MutationObserver(observeLayout);
  mutations.observe(node, { childList: true, subtree: true });
  observeLayout();
  measure();
  window.addEventListener('resize', schedule);
  return {
    destroy() {
      resize.disconnect();
      mutations.disconnect();
      window.cancelAnimationFrame(frame);
      window.removeEventListener('resize', schedule);
    }
  };
}

type ScrollContext = { scope: string; column: string; page: number; ready?: boolean };

/** Per-app cache survives a refresh's loading placeholder, not project/filter changes. */
export function createColumnScroll() {
  let scope = '';
  const positions = new Map<string, { page: number; top: number }>();
  return (node: HTMLElement, context: ScrollContext) => {
    let alive = true;
    const restore = () => {
      if (scope !== context.scope) {
        scope = context.scope;
        positions.clear();
      }
      // Other columns can finish first and remount this body while it is still empty.
      // Restoring then would clamp the saved offset to zero before its cards arrive.
      if (context.ready === false) return;
      const saved = positions.get(context.column);
      const top = saved?.page === context.page ? saved.top : 0;
      positions.set(context.column, { page: context.page, top });
      void tick().then(() => {
        if (alive && context.ready !== false) node.scrollTop = top;
      });
    };
    const save = () => {
      if (scope === context.scope && context.ready !== false) {
        positions.set(context.column, { page: context.page, top: node.scrollTop });
      }
    };
    restore();
    node.addEventListener('scroll', save, { passive: true });
    return {
      update(next: ScrollContext) {
        const changed = next.scope !== context.scope || next.column !== context.column || next.page !== context.page
          || (context.ready === false && next.ready !== false);
        context = next;
        if (changed) restore();
      },
      destroy() {
        alive = false;
        node.removeEventListener('scroll', save);
      }
    };
  };
}
