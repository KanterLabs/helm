import { createServer, request as httpRequest, type IncomingMessage } from 'node:http';
import { request as httpsRequest } from 'node:https';
import type { Socket } from 'node:net';
import { expect, test as base, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

/** Failure inventory, recorded before implementation (TC-188):
 * - Dirty task/comment/create/admin/checklist drafts can be discarded by a reload.
 * - A pending write can be cut off after consent but before SKIP_WAITING.
 * - Cancel can still send SKIP_WAITING; another tab can trigger an unwanted reload.
 * - Later can hide all future versions, or reappear for the same worker.
 * - Activation timeout can claim success, erase the draft, or hide Retry.
 * - A floating notice can cover a phone's save controls or escape modal focus.
 * These use the actual Helm UI/API and real disposable browser service workers.
 */
const backendOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
type WorkerEvent = { version: number; type: string };
type MutationHold = { observed: Promise<void>; responseStatus: Promise<number>; release: () => void };
type PendingMutationHold = MutationHold & { path: string; method: string; started: () => void; resumed: Promise<void>; finished: (status: number) => void; used: boolean };
type UpdateServer = {
  origin: string;
  version: number;
  allowActivation: boolean;
  events: WorkerEvent[];
  holdNextMutation: (path: string, method: string) => MutationHold;
  close: () => Promise<void>;
};

async function startUpdateServer(): Promise<UpdateServer> {
  const sockets = new Set<Socket>();
  const holds = new Set<PendingMutationHold>();
  const fixture: UpdateServer = {
    origin: '', version: 1, allowActivation: true, events: [], close: async () => {},
    holdNextMutation(path, method) {
      let started!: () => void;
      let release!: () => void;
      let finished!: (status: number) => void;
      const responseStatus = new Promise<number>((resolve) => { finished = resolve; });
      const observed = new Promise<void>((resolve) => { started = resolve; });
      const resumed = new Promise<void>((resolve) => { release = resolve; });
      const hold: PendingMutationHold = { path, method, observed, responseStatus, started, resumed, finished, used: false, release: () => { release(); holds.delete(hold); } };
      holds.add(hold);
      return hold;
    }
  };
  const server = createServer(async (incoming, outgoing) => {
    const path = incoming.url?.split('?')[0];
    if (path === '/sw.js') {
      outgoing.writeHead(200, { 'Content-Type': 'application/javascript', 'Cache-Control': 'no-store', 'Service-Worker-Allowed': '/' });
      outgoing.end(`
        const version = ${fixture.version};
        self.addEventListener('install', event => { if (version === 1) event.waitUntil(self.skipWaiting()); });
        self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
        self.addEventListener('message', event => {
          if (event.data?.type === 'TEST_VERSION') event.ports[0]?.postMessage(version);
          if (event.data?.type === 'PWA_STATUS') event.source?.postMessage({ type: 'PWA_CACHE_READY', shellCached: true });
          if (event.data?.type === 'SKIP_WAITING') event.waitUntil((async () => {
            const response = await fetch('/__pwa_activation?version=' + version, { cache: 'no-store' });
            if ((await response.json()).allow) await self.skipWaiting();
          })());
        });
      `);
      return;
    }
    if (path === '/__pwa_activation') {
      fixture.events.push({ version: Number(new URL(incoming.url!, 'http://fixture').searchParams.get('version')), type: 'SKIP_WAITING' });
      outgoing.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
      outgoing.end(JSON.stringify({ allow: fixture.allowActivation }));
      return;
    }
    // Hold the actual HTTP mutation before forwarding it. Browser-level
    // route interception can be bypassed by service-worker-controlled WebKit
    // pages, even though these disposable workers do not cache requests.
    const hold = [...holds].find((candidate) => !candidate.used && candidate.path === path && candidate.method === incoming.method);
    if (hold) {
      hold.used = true;
      hold.started();
      await hold.resumed;
      if (outgoing.destroyed) return;
    }
    const target = new URL(incoming.url || '/', backendOrigin);
    const forward = target.protocol === 'https:' ? httpsRequest : httpRequest;
    const headers = { ...incoming.headers, host: target.host };
    // The disposable browser origin terminates here. Translate its own
    // mutation Origin together with Host so real UI writes pass the backend's
    // same-origin check; retain any other Origin so rejection still applies.
    if (headers.origin === fixture.origin) headers.origin = backendOrigin;
    const upstream = forward(target, { method: incoming.method, headers }, (response: IncomingMessage) => {
      outgoing.writeHead(response.statusCode || 502, response.headers);
      response.once('end', () => hold?.finished(response.statusCode || 502));
      response.pipe(outgoing);
    });
    upstream.on('error', (error) => { hold?.finished(0); outgoing.destroy(error); });
    incoming.on('error', (error) => upstream.destroy(error));
    outgoing.on('close', () => upstream.destroy());
    incoming.pipe(upstream);
  });
  server.on('connection', (socket) => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)); });
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('PWA fixture did not listen');
  fixture.origin = `http://127.0.0.1:${address.port}`;
  fixture.close = async () => {
    holds.forEach((hold) => hold.release());
    sockets.forEach((socket) => socket.destroy());
    await new Promise<void>((resolve) => server.close(() => resolve()));
  };
  return fixture;
}
const test = base.extend<{ updateServer: UpdateServer }>({
  updateServer: async ({}, use) => { const server = await startUpdateServer(); try { await use(server); } finally { await server.close(); } },
  baseURL: async ({ updateServer }, use) => use(updateServer.origin)
});

type Project = { id: string; slug: string; name: string };
type Task = { id: string; key: string; title: string; description: string };
async function fixture(request: APIRequestContext) {
  const suffix = Date.now().toString(36).toUpperCase() + Math.random().toString(36).slice(2, 5).toUpperCase();
  const headers = () => ({ Origin: backendOrigin, 'Idempotency-Key': crypto.randomUUID() });
  const status = await request.get('/api/v1/auth/status');
  expect((await status.json()).mode, 'Use a disposable E2E server with disabled auth').toBe('disabled');
  const created = await request.post('/api/v1/projects', { data: { key: `PU${suffix}`.slice(0, 16), name: `PWA update ${suffix}` }, headers: headers() });
  expect(created.ok()).toBeTruthy();
  const project = await created.json() as Project;
  const columnsResponse = await request.get(`/api/v1/projects/${project.id}/columns`);
  const columns = await columnsResponse.json();
  const column = (Array.isArray(columns) ? columns : columns.data)[0];
  const response = await request.post(`/api/v1/projects/${project.id}/tasks`, { data: { title: `PWA task ${suffix}`, description: 'Persisted original', column_id: column.id, priority: 'normal' }, headers: headers() });
  expect(response.ok()).toBeTruthy();
  return { project, task: await response.json() as Task };
}
async function open(page: Page, project: Project) {
  await page.goto(`/p/${project.slug}`);
  await expect(page.getByRole('heading', { name: project.name, exact: true })).toBeVisible();
  await page.evaluate(async () => { await navigator.serviceWorker.ready; });
  await expect.poll(() => page.evaluate(() => Boolean(navigator.serviceWorker.controller))).toBeTruthy();
}
async function installUpdate(page: Page, server: UpdateServer, version: number) {
  server.version = version;
  await page.evaluate(async () => { const registration = await navigator.serviceWorker.getRegistration(); await registration!.update(); });
  await expect.poll(() => page.evaluate(async () => Boolean((await navigator.serviceWorker.getRegistration())?.waiting))).toBeTruthy();
  await expect(page.locator('.update-card')).toBeVisible();
}
async function workerVersion(page: Page): Promise<number> {
  return page.evaluate(() => new Promise<number>((resolve, reject) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => reject(new Error('Worker version response timed out')), 3000);
    channel.port1.onmessage = (event) => { clearTimeout(timer); resolve(event.data); };
    navigator.serviceWorker.controller!.postMessage({ type: 'TEST_VERSION' }, [channel.port2]);
  }));
}
async function evidence(page: Page, testInfo: TestInfo, server: UpdateServer, name: string) {
  await testInfo.attach(`${name}.png`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
  await testInfo.attach(`${name}-workers.json`, { body: JSON.stringify({ controller: await workerVersion(page), activationMessages: server.events }, null, 2), contentType: 'application/json' });
}
async function openDrawer(page: Page, task: Task) {
  await page.locator('.task-card').filter({ hasText: task.key }).locator('[data-task-trigger]').click();
  const drawer = page.locator('.task-drawer');
  await expect(drawer).toBeVisible();
  return drawer;
}

test('Later hides only the current version; another tab activates without reloading drafts', async ({ page, context, request, updateServer }, testInfo) => {
  const { project, task } = await fixture(request);
  await open(page, project);
  await installUpdate(page, updateServer, 2);
  await page.locator('.update-card').getByRole('button', { name: 'Later', exact: true }).click();
  await expect(page.locator('.update-card')).toBeHidden();
  await page.evaluate(async () => { await (await navigator.serviceWorker.getRegistration())!.update(); });
  await expect(page.locator('.update-card')).toBeHidden();
  expect(updateServer.events).toEqual([]);
  await openDrawer(page, task);
  await expect(page.locator('.update-card')).toBeHidden();
  await page.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page.locator('.update-card')).toBeHidden();
  await installUpdate(page, updateServer, 3);
  const drawer = await openDrawer(page, task);
  await drawer.getByLabel('Task title').fill('Draft stays in this tab');
  const originalNavigation = await page.evaluate(() => performance.timeOrigin);
  const other = await context.newPage();
  await open(other, project);
  await expect(other.locator('.update-card')).toBeVisible();
  const otherReload = other.waitForEvent('domcontentloaded');
  await other.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await otherReload;
  await expect.poll(() => workerVersion(other)).toBe(3);
  await expect.poll(() => workerVersion(page)).toBe(3);
  await expect(drawer.getByLabel('Task title')).toHaveValue('Draft stays in this tab');
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(originalNavigation);
  expect(updateServer.events).toEqual([{ version: 3, type: 'SKIP_WAITING' }]);
  await evidence(page, testInfo, updateServer, 'other-tab-preserves-draft');
});

test('dirty task and comment cancellation sends no activation; pending save defers update', async ({ page, request, updateServer }, testInfo) => {
  test.setTimeout(75_000);
  const { project, task } = await fixture(request);
  await open(page, project);
  await installUpdate(page, updateServer, 2);
  const drawer = await openDrawer(page, task);
  const title = drawer.getByLabel('Task title');
  await title.fill('Unsaved PWA title');
  await page.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(title).toHaveValue('Unsaved PWA title');
  expect(updateServer.events).toEqual([]);
  expect((await (await request.get(`/api/v1/tasks/${task.id}`)).json()).title).toBe(task.title);
  await drawer.getByRole('tab', { name: /Activity/ }).click();
  await drawer.getByLabel('Comment', { exact: true }).fill('Unsaved PWA comment');
  await page.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(drawer.getByLabel('Comment', { exact: true })).toHaveValue('Unsaved PWA comment');
  const persistedComments = await (await request.get(`/api/v1/tasks/${task.id}/comments`)).json();
  expect((Array.isArray(persistedComments) ? persistedComments : persistedComments.data).length).toBe(0);
  expect(updateServer.events).toEqual([]);
  await testInfo.attach('cancellation-persisted-state.json', { body: JSON.stringify({ title: task.title, comments: [] }), contentType: 'application/json' });
  await drawer.getByRole('tab', { name: /Details/ }).click();
  const patch = updateServer.holdNextMutation(`/api/v1/tasks/${task.id}`, 'PATCH');
  try {
    await drawer.getByRole('button', { name: 'Save changes', exact: true }).click();
    await patch.observed;
    await expect(page.locator('.update-card').getByRole('button', { name: 'Reload', exact: true })).toBeDisabled();
    expect(updateServer.events).toEqual([]);
    await evidence(page, testInfo, updateServer, 'pending-save-defers-update');
  } finally { patch.release(); }
  const savedStatus = await patch.responseStatus;
  expect(savedStatus, `task PATCH returned HTTP ${savedStatus}`).toBeGreaterThanOrEqual(200);
  expect(savedStatus).toBeLessThan(300);
  await expect(drawer.getByRole('button', { name: 'Saved', exact: true })).toBeVisible();
  expect((await (await request.get(`/api/v1/tasks/${task.id}`)).json()).title).toBe('Unsaved PWA title');
});

test('timeout preserves work and offers Retry; discard consent activates and reloads', async ({ page, request, updateServer }, testInfo) => {
  test.setTimeout(75_000);
  await page.setViewportSize({ width: 390, height: 844 });
  const { project, task } = await fixture(request);
  await open(page, project);
  await installUpdate(page, updateServer, 2);
  const drawer = await openDrawer(page, task);
  await drawer.getByLabel('Task title').fill('Draft survives timeout');
  updateServer.allowActivation = false;
  await page.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await page.getByRole('button', { name: 'Discard and update', exact: true }).click();
  await expect(page.locator('.update-card').getByRole('button', { name: 'Retry', exact: true })).toBeEnabled({ timeout: 15_000 });
  await expect(page.locator('.update-card')).toContainText('did not become active');
  await expect(drawer.getByLabel('Task title')).toHaveValue('Draft survives timeout');
  expect(await workerVersion(page)).toBe(1);
  expect((await (await request.get(`/api/v1/tasks/${task.id}`)).json()).title).toBe(task.title);
  const save = drawer.getByRole('button', { name: 'Save changes', exact: true });
  await save.scrollIntoViewIfNeeded();
  await expect(save).toBeInViewport();
  const hit = await save.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return element.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2));
  });
  expect(hit, 'the update notice must not cover the phone save control').toBeTruthy();
  await evidence(page, testInfo, updateServer, 'activation-timeout-preserves-draft');
  const originalNavigation = await page.evaluate(() => performance.timeOrigin);
  updateServer.allowActivation = true;
  await page.locator('.update-card').getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Keep editing', exact: true })).toBeFocused();
  const reloaded = page.waitForEvent('domcontentloaded');
  await page.getByRole('button', { name: 'Discard and update', exact: true }).click();
  await reloaded;
  await expect.poll(() => workerVersion(page)).toBe(2);
  await expect.poll(() => page.evaluate(() => performance.timeOrigin)).not.toBe(originalNavigation);
  await expect(page.locator('.update-card')).toBeHidden();
  await expect(page.getByRole('heading', { name: project.name, exact: true })).toBeVisible();
  expect(updateServer.events).toEqual([{ version: 2, type: 'SKIP_WAITING' }, { version: 2, type: 'SKIP_WAITING' }]);
  await evidence(page, testInfo, updateServer, 'retry-activated-version-two');
});

for (const draft of [
  { kind: 'task', trigger: 'New task', dialog: 'Create a task', field: 'Task title' },
  { kind: 'report', trigger: 'Report bug', dialog: 'Report a bug', field: 'Bug title' },
  { kind: 'project', trigger: 'New project', dialog: 'Create a project', field: 'Project name' },
  { kind: 'focus', trigger: /New focus$/, dialog: 'Create a focus', field: 'Focus name' }
]) {
  test(`${draft.kind} form stays intact when update is canceled`, async ({ page, request, updateServer }, testInfo) => {
    const { project } = await fixture(request);
    await open(page, project);
    await installUpdate(page, updateServer, 2);
    if (draft.kind === 'focus') await page.getByRole('button', { name: 'Focus', exact: true }).click();
    await page.getByRole('button', { name: draft.trigger, exact: true }).first().click();
    const form = page.getByRole('dialog', { name: draft.dialog, exact: true });
    const input = form.getByLabel(draft.field, { exact: true });
    const value = `Unsaved ${draft.kind} stays here`;
    await input.fill(value);
    await form.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Keep editing', exact: true })).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(input).toHaveValue(value);
    expect(await workerVersion(page)).toBe(1);
    expect(updateServer.events).toEqual([]);
    await evidence(page, testInfo, updateServer, `${draft.kind}-draft-canceled`);
  });
}

test('admin settings remain dirty and unpersisted after update cancellation', async ({ page, request, updateServer }, testInfo) => {
  const { project } = await fixture(request);
  await open(page, project);
  await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  const settings = page.getByRole('region', { name: 'Projects & columns', exact: true });
  await expect(settings.getByRole('combobox', { name: 'Project', exact: true })).toBeVisible();
  await settings.getByRole('combobox', { name: 'Project', exact: true }).selectOption(project.id);
  await expect(settings.getByRole('textbox', { name: 'Project name', exact: true })).toHaveValue(project.name);
  await installUpdate(page, updateServer, 2);
  await settings.getByRole('textbox', { name: 'Project name', exact: true }).fill('Unsaved admin project name');
  await page.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(settings.getByRole('textbox', { name: 'Project name', exact: true })).toHaveValue('Unsaved admin project name');
  const persisted = await (await request.get(`/api/v1/projects/${project.id}`)).json();
  expect(persisted.name).toBe(project.name);
  expect(updateServer.events).toEqual([]);
  await testInfo.attach('admin-persisted-state.json', { body: JSON.stringify({ name: persisted.name }), contentType: 'application/json' });
  await evidence(page, testInfo, updateServer, 'admin-draft-canceled');
});

test('checklist drafts cancel updates and pending checklist writes defer activation', async ({ page, request, updateServer }, testInfo) => {
  test.setTimeout(75_000);
  const { project, task } = await fixture(request);
  await open(page, project);
  await installUpdate(page, updateServer, 2);
  const drawer = await openDrawer(page, task);
  const checklist = drawer.getByRole('region', { name: 'Checklist', exact: true });
  const newItem = checklist.getByRole('textbox', { name: 'Add acceptance criterion', exact: true });
  const original = 'PWA criterion stays persisted';
  await newItem.fill(original);
  await drawer.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(newItem).toHaveValue(original);
  expect(updateServer.events).toEqual([]);
  const before = await (await request.get(`/api/v1/tasks/${task.id}`)).json();
  expect(before.checklist || []).toEqual([]);
  await evidence(page, testInfo, updateServer, 'checklist-new-draft-canceled');

  const checklistPost = updateServer.holdNextMutation(`/api/v1/tasks/${task.id}/checklist`, 'POST');
  try {
    await checklist.getByRole('button', { name: 'Add', exact: true }).click();
    await checklistPost.observed;
    await expect(drawer.locator('.update-card').getByRole('button', { name: 'Reload', exact: true })).toBeDisabled();
    await expect(checklist).toHaveAttribute('aria-busy', 'true');
    expect(updateServer.events).toEqual([]);
    expect(await workerVersion(page)).toBe(1);
    const pending = await (await request.get(`/api/v1/tasks/${task.id}`)).json();
    expect(pending.checklist || []).toEqual([]);
    await evidence(page, testInfo, updateServer, 'checklist-pending-write-defers-update');
  } finally { checklistPost.release(); }
  const addedStatus = await checklistPost.responseStatus;
  expect(addedStatus, `checklist POST returned HTTP ${addedStatus}`).toBeGreaterThanOrEqual(200);
  expect(addedStatus).toBeLessThan(300);
  const item = checklist.getByRole('textbox', { name: `Edit checklist item: ${original}`, exact: true });
  await expect(item).toHaveValue(original);
  await expect(newItem).toHaveValue('');
  await expect(checklist).toHaveAttribute('aria-busy', 'false');
  await expect(drawer.locator('.update-card').getByRole('button', { name: 'Reload', exact: true })).toBeEnabled();

  await item.fill('Unsubmitted checklist edit');
  await drawer.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(item).toHaveValue('Unsubmitted checklist edit');
  const persisted = await (await request.get(`/api/v1/tasks/${task.id}`)).json();
  expect(persisted.checklist.map((entry: { text: string }) => entry.text)).toEqual([original]);
  expect(updateServer.events).toEqual([]);
  await testInfo.attach('checklist-persisted-state.json', { body: JSON.stringify({ texts: persisted.checklist.map((entry: { text: string }) => entry.text) }), contentType: 'application/json' });
  await evidence(page, testInfo, updateServer, 'checklist-existing-edit-canceled');

  await item.fill(original);
  const reloaded = page.waitForEvent('domcontentloaded');
  await drawer.locator('.update-card').getByRole('button', { name: 'Reload', exact: true }).click();
  await reloaded;
  await expect.poll(() => workerVersion(page)).toBe(2);
  await expect(page.locator('.update-card')).toBeHidden();
  expect(updateServer.events).toEqual([{ version: 2, type: 'SKIP_WAITING' }]);
  await evidence(page, testInfo, updateServer, 'checklist-saved-clean-update');
});

test('ticket note, waiting reason and create drafts survive canceled updates without persistence', async ({ page, request, updateServer }, testInfo) => {
  test.setTimeout(75_000);
  const { project } = await fixture(request);
  const title = `Ticket update safety ${project.name}`;
  const created = await request.post('/api/v1/tickets', {
    data: { title, description: 'Persisted ticket details', priority: 'normal', assignee: null },
    headers: { Origin: backendOrigin, 'Idempotency-Key': crypto.randomUUID() }
  });
  expect(created.ok(), `create ticket returned HTTP ${created.status()}`).toBeTruthy();
  const ticket = await created.json() as Task & { version: number; ticket: { status: string } };
  await open(page, project);
  await page.goto(`/tickets?ticket=${encodeURIComponent(ticket.key)}&q=${encodeURIComponent(title)}`);
  const detail = page.locator('.ticket-detail');
  await expect(detail.getByRole('heading', { name: title, exact: true })).toBeVisible();
  await installUpdate(page, updateServer, 2);
  const reload = page.locator('.update-card').getByRole('button', { name: 'Reload', exact: true });
  const note = detail.getByRole('textbox', { name: 'Add internal note', exact: true });
  await note.fill('Unsaved ticket note');
  await reload.click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(note).toHaveValue('Unsaved ticket note');
  const comments = await (await request.get(`/api/v1/tasks/${ticket.id}/comments`)).json();
  expect(Array.isArray(comments) ? comments : comments.data).toEqual([]);
  expect(updateServer.events).toEqual([]);
  await evidence(page, testInfo, updateServer, 'ticket-note-draft-canceled');

  await note.fill('');
  await detail.getByRole('button', { name: 'Waiting…', exact: true }).click();
  const reason = detail.getByRole('textbox', { name: 'What is this waiting on?', exact: true });
  await reason.fill('Unsaved ticket waiting reason');
  await reload.click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(reason).toHaveValue('Unsaved ticket waiting reason');
  const unchanged = await (await request.get(`/api/v1/tasks/${ticket.id}`)).json();
  expect({ title: unchanged.title, description: unchanged.description, version: unchanged.version, status: unchanged.ticket.status })
    .toEqual({ title: ticket.title, description: ticket.description, version: ticket.version, status: ticket.ticket.status });
  expect(updateServer.events).toEqual([]);
  await detail.locator('.ticket-wait').getByRole('button', { name: 'Cancel', exact: true }).click();

  await page.getByRole('button', { name: '＋ New ticket', exact: true }).click();
  const form = page.getByRole('form', { name: 'New ticket', exact: true });
  const draftTitle = `Unsubmitted ticket ${project.name}`;
  await form.getByRole('textbox', { name: 'Title', exact: true }).fill(draftTitle);
  await form.getByRole('textbox', { name: /Details/ }).fill('Unsubmitted ticket details');
  await form.getByRole('combobox', { name: 'Priority', exact: true }).selectOption('high');
  await form.getByRole('checkbox', { name: 'Assign to me', exact: true }).uncheck();
  await reload.click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(form.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue(draftTitle);
  await expect(form.getByRole('textbox', { name: /Details/ })).toHaveValue('Unsubmitted ticket details');
  await expect(form.getByRole('combobox', { name: 'Priority', exact: true })).toHaveValue('high');
  await expect(form.getByRole('checkbox', { name: 'Assign to me', exact: true })).not.toBeChecked();
  const uncreated = await (await request.get(`/api/v1/tickets?q=${encodeURIComponent(draftTitle)}`)).json();
  expect(uncreated.data).toEqual([]);
  expect(updateServer.events).toEqual([]);
  await testInfo.attach('ticket-persisted-state.json', {
    body: JSON.stringify({ title: unchanged.title, version: unchanged.version, status: unchanged.ticket.status, comments: [], unsubmittedMatches: uncreated.data.length }),
    contentType: 'application/json'
  });
  await evidence(page, testInfo, updateServer, 'ticket-create-draft-canceled');

  // Changed options are drafts even when the text inputs are empty.
  await form.getByRole('textbox', { name: 'Title', exact: true }).fill('');
  await form.getByRole('textbox', { name: /Details/ }).fill('');
  await reload.click();
  await page.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(form.getByRole('combobox', { name: 'Priority', exact: true })).toHaveValue('high');
  await expect(form.getByRole('checkbox', { name: 'Assign to me', exact: true })).not.toBeChecked();
  expect(updateServer.events).toEqual([]);
  await evidence(page, testInfo, updateServer, 'ticket-create-options-canceled');
});
