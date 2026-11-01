import type { Page, Route } from '@playwright/test';

const pendingByPage = new WeakMap<Page, Set<Promise<void>>>();

export async function routeAgentWork(page: Page, pattern: string, handler: (route: Route) => Promise<void>) {
  let pending = pendingByPage.get(page);
  if (!pending) {
    pending = new Set<Promise<void>>();
    pendingByPage.set(page, pending);
  }
  const callbacks = pending;
  await page.route(pattern, (route) => {
    const callback = handler(route);
    callbacks.add(callback);
    return callback.finally(() => callbacks.delete(callback));
  });
}

export async function drainAgentWorkRoutes(page: Page) {
  const pending = pendingByPage.get(page);
  // Playwright clears its route list before unrouteAll(wait) drains callbacks.
  // One completed callback can then remove interception and continue another
  // pending route before its fetch/fulfill finishes. Keep routes registered
  // until every callback settles, including reads started during the drain.
  while (pending?.size) await Promise.all([...pending]);
  await page.unrouteAll({ behavior: 'wait' });
  pendingByPage.delete(page);
}
