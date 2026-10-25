// Network proxy fixture and helpers for the offline PWA journeys.
import { request as httpRequest, createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { request as httpsRequest } from 'node:https';
import type { Socket } from 'node:net';
import { expect, test as base, type APIRequestContext, type APIResponse, type Locator, type Page } from '@playwright/test';

export type Project = { id: string; key: string; name: string; slug: string };
export type Column = { id: string; name: string; semantic_state: string; position: number };
export type Task = { id: string; key: string; title: string; column_id: string; position: number; version: number };
export type Collection<T> = { data: T[]; next_cursor?: string | null };
export type BoardFixture = { project: Project; column: Column; task: Task };
export type ManifestIcon = { src: string; sizes?: string; type?: string; purpose?: string };
export type NetworkProxy = {
  origin: string;
  setOffline: (offline: boolean) => void;
  setAuthRejected: (rejected: boolean) => void;
  close: () => Promise<void>;
};

export const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

/**
 * Keep the browser on a same-origin loopback origin while making a real
 * network outage independently controllable from Playwright's context-level
 * offline emulation. WebKit can throw before its service worker gets a chance
 * to answer a `goto()` while `context.setOffline(true)` is active; destroying
 * the proxy request lets the worker's network-first navigation fallback run in
 * all engines.
 */
export async function startNetworkProxy(): Promise<NetworkProxy> {
  const targetOrigin = new URL(e2eOrigin);
  let offline = false;
  let authRejected = false;
  let closed = false;
  const sockets = new Set<Socket>();

  const server: Server = createServer((incoming: IncomingMessage, outgoing: ServerResponse) => {
    if (offline || closed) {
      incoming.destroy();
      outgoing.destroy();
      return;
    }
    if (authRejected && incoming.url?.split('?')[0] === '/api/v1/auth/status') {
      outgoing.writeHead(403, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
      outgoing.end(JSON.stringify({ error: { code: 'auth_invalidated', message: 'Session expired' } }));
      return;
    }

    const target = new URL(incoming.url || '/', targetOrigin);
    const headers = { ...incoming.headers };
    // The backend receives the request through this proxy, but its Host header
    // still needs to identify the configured E2E origin.
    headers.host = target.host;
    const options = {
      protocol: target.protocol,
      hostname: target.hostname,
      port: target.port || undefined,
      method: incoming.method || 'GET',
      path: `${target.pathname}${target.search}`,
      headers
    };
    const forwardResponse = (response: IncomingMessage) => {
      outgoing.writeHead(response.statusCode || 502, response.headers);
      response.pipe(outgoing);
    };
    const upstream = target.protocol === 'https:'
      ? httpsRequest(options, forwardResponse)
      : httpRequest(options, forwardResponse);

    upstream.on('error', (error) => outgoing.destroy(error));
    incoming.on('error', (error) => upstream.destroy(error));
    outgoing.on('close', () => upstream.destroy());
    incoming.pipe(upstream);
  });

  server.on('connection', (socket) => {
    sockets.add(socket);
    socket.once('close', () => sockets.delete(socket));
  });

  await new Promise<void>((resolve, reject) => {
    const onError = (error: Error) => {
      server.off('listening', onListening);
      reject(error);
    };
    const onListening = () => {
      server.off('error', onError);
      resolve();
    };
    server.once('error', onError);
    server.once('listening', onListening);
    server.listen(0, '127.0.0.1');
  });

  const address = server.address();
  if (!address || typeof address === 'string') {
    await new Promise<void>((resolve) => server.close(() => resolve()));
    throw new Error('the local E2E network proxy did not expose a TCP address');
  }

  return {
    origin: `http://127.0.0.1:${address.port}`,
    setAuthRejected(value: boolean) { authRejected = value; },
    setOffline(value: boolean) {
      offline = value;
      if (value) sockets.forEach((socket) => socket.destroy());
    },
    async close() {
      if (closed) return;
      closed = true;
      sockets.forEach((socket) => socket.destroy());
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  };
}

export const test = base.extend<{ networkProxy: NetworkProxy }>({
  networkProxy: async ({}, use) => {
    const proxy = await startNetworkProxy();
    try {
      await use(proxy);
    } finally {
      await proxy.close();
    }
  },
  baseURL: async ({ networkProxy }, use) => {
    await use(networkProxy.origin);
  }
});

export function mutationHeaders(key: string): Record<string, string> {
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

export async function postJSON<T>(request: APIRequestContext, path: string, body: unknown, key: string): Promise<T> {
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
    key: `PWA${runID}`.slice(0, 16),
    name: `PWA offline ${runID}`,
    description: 'A small board used by the PWA offline browser checks.'
  }, `pwa-offline-${runID}-project`);

  let columns = collectionData(await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list PWA offline columns'
  ));
  if (!columns.length) {
    await postJSON<Column>(request, `/api/v1/projects/${project.id}/columns`, {
      name: 'Backlog',
      semantic_state: 'backlog',
      position: 0
    }, `pwa-offline-${runID}-column`);
    columns = collectionData(await json<Collection<Column> | Column[]>(
      await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
      'reload PWA offline columns'
    ));
  }
  const column = columns[0];
  expect(column, 'the PWA fixture needs one column').toBeTruthy();

  const task = await postJSON<Task>(request, `/api/v1/projects/${project.id}/tasks`, {
    title: `Cached PWA task ${runID}`,
    description: 'This title must remain available when the board is opened offline.',
    column_id: column.id,
    priority: 'normal',
    position: 0
  }, `pwa-offline-${runID}-task`);

  return { project, column: column as Column, task };
}

export async function authMode(request: APIRequestContext): Promise<string | undefined> {
  const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
  return status.mode;
}

export async function openOnlineBoard(page: Page, fixture: BoardFixture): Promise<void> {
  await page.goto(`/p/${fixture.project.slug}`, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: fixture.project.name, exact: true })).toBeVisible();
  await expect(page.getByText(fixture.task.title, { exact: true })).toBeVisible();
}

export async function awaitServiceWorkerControl(page: Page): Promise<void> {
  await page.evaluate(async () => {
    if (!('serviceWorker' in navigator)) throw new Error('Helm did not expose navigator.serviceWorker');
    await navigator.serviceWorker.ready;
  });

  // The first navigation registers the worker but may not yet be controlled;
  // one reload is the browser-standard transition into a controlled client.
  if (!await page.evaluate(() => Boolean(navigator.serviceWorker.controller))) {
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
  await expect.poll(
    () => page.evaluate(() => Boolean(navigator.serviceWorker?.controller)),
    { timeout: 15_000, message: 'the board page should be controlled by the PWA service worker' }
  ).toBeTruthy();
}

export async function offlinePage(page: Page) {
  const root = page.getByTestId('offline-board-view');
  await expect(root, 'offline mode should render its dedicated root').toBeVisible();
  await expect(root.getByText('Read-only', { exact: true })).toBeVisible();
  await expect(root.getByText('Offline', { exact: true })).toBeVisible();
  return root;
}

export async function reconnectButton(root: Locator) {
  const button = root.getByRole('button', { name: /reconnect|retry|try again/i }).first();
  await expect(button, 'offline mode should offer a reconnect control').toBeVisible();
  return button;
}

/**
 * Read the production store without importing an application-only module.
 * The DB/store/key are part of the PWA contract. Reading only the current
 * record keeps the assertion tied to the persisted snapshot, not implementation
 * details of the store's normalization helpers.
 */
export async function offlineStoreContains(page: Page, needle: string): Promise<boolean> {
  return await page.evaluate(async (value) => {
    if (typeof indexedDB.databases !== 'function') return false;
    const databases = await indexedDB.databases();
    if (!databases.some((database) => database.name === 'helm-offline-boards')) return false;

    const database = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open('helm-offline-boards');
      request.onerror = () => reject(request.error || new Error('could not open helm-offline-boards'));
      request.onsuccess = () => resolve(request.result);
    });
    try {
      if (!database.objectStoreNames.contains('snapshots')) return false;
      const record = await new Promise<unknown>((resolve) => {
        try {
          const request = database.transaction('snapshots', 'readonly').objectStore('snapshots').get('current');
          request.onerror = () => resolve(undefined);
          request.onsuccess = () => resolve(request.result);
        } catch {
          resolve(undefined);
        }
      });
      return JSON.stringify(record ?? '').includes(value);
    } finally {
      database.close();
    }
  }, needle);
}

export async function decodedImageDimensions(page: Page, source: string): Promise<{ width: number; height: number }> {
  return await page.evaluate(async (url) => {
    const image = new Image();
    await new Promise<void>((resolve, reject) => {
      image.onload = () => resolve();
      image.onerror = () => reject(new Error(`could not decode ${url}`));
      image.src = url;
    });
    if (typeof image.decode === 'function') await image.decode();
    return { width: image.naturalWidth, height: image.naturalHeight };
  }, source);
}

export async function cacheUrls(page: Page): Promise<string[]> {
  return await page.evaluate(async () => {
    const names = await caches.keys();
    const entries = await Promise.all(names.map(async (name) => {
      const cache = await caches.open(name);
      return (await cache.keys()).map((request) => request.url);
    }));
    return entries.flat();
  });
}

export function trackApiMutations(page: Page): string[] {
  const mutations: string[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (!url.pathname.startsWith('/api/')) return;
    if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method().toUpperCase())) {
      mutations.push(`${request.method()} ${url.pathname}`);
    }
  });
  return mutations;
}

export async function goOfflineToCachedBoard(page: Page, networkProxy: NetworkProxy, fixture: BoardFixture): Promise<Locator> {
  networkProxy.setOffline(true);
  // Reload the already-open deep link. The proxy destroys this navigation's
  // network request, so the service worker must return the cached shell.
  await page.reload({ waitUntil: 'domcontentloaded' });
  const root = await offlinePage(page);
  await expect(root.getByText(fixture.task.title, { exact: true })).toBeVisible();
  return root;
}
