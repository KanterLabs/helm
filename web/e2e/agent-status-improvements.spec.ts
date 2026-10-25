/**
 * Failure scenarios established before implementation (TC-270):
 * - A stale check-in must never replace Working/Waiting/Verifying/Handoff.
 * - Freshness and claim expiry are independent; an expired claim must not
 *   erase the reported state or invent a lifecycle mutation.
 * - A heartbeat advances Last check-in, never the durable Last progress.
 * - Missing, failed, or bounded-out timeline evidence reports unavailable;
 *   progress from a different task/operation must never fill that timestamp.
 * - A delayed response must not overwrite a switched task/operation.
 * - Board cards must not issue per-task timeline reads.
 * - Visible and accessible text must retain both state and freshness.
 *
 * Fixtures are created through a disposable real server. Clock adjustments
 * and failures are applied only to synthetic fixture read responses because
 * the public API intentionally offers no backdating endpoint.
 */
import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

// This fixture rewrites clock reads and injects history failures. A service
// worker can forward API requests outside page.route (observed in WebKit),
// bypassing those adjustments. The separate PWA suite exercises real workers.
test.use({ serviceWorkers: 'block' });

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; semantic_state: string };
type Work = {
  operation_id: string;
  actor_id: string;
  state: 'working' | 'waiting' | 'verifying' | 'handoff';
  updated_at: string;
};
type Task = {
  id: string; key: string; title: string; version: number;
  claimed_by?: unknown; claim_expires_at?: string; completed_at?: string | null;
  agent_work?: Work;
};
type Timeline = {
  data: Array<{ id: string; task_id: string; kind: string; created_at: string; progress?: { operation_id: string } }>;
  next_cursor?: string | null;
};
const origin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

async function json<T>(response: APIResponse): Promise<T> {
  expect(response.ok(), `HTTP ${response.status()} for ${new URL(response.url()).pathname}`).toBeTruthy();
  return response.json() as Promise<T>;
}

async function post<T>(request: APIRequestContext, path: string, data: unknown, task?: Task): Promise<T> {
  return json<T>(await request.post(path, {
    data,
    headers: {
      Origin: origin,
      'Idempotency-Key': `agent-status-${crypto.randomUUID()}`,
      ...(task ? { 'If-Match': `"v${task.version}"` } : {})
    }
  }));
}

test('keeps state, freshness, claim validity, and durable progress independent', async ({ page, request }, testInfo) => {
  test.setTimeout(150_000);
  expect((await json<{ mode: string }>(await request.get('/api/v1/auth/status'))).mode).toBe('disabled');
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
  const project = await post<Project>(request, '/api/v1/projects', {
    key: `AS${suffix}`.slice(0, 16), name: `Agent status ${suffix}`
  });
  const columnResponse = await json<{ data: Column[] } | Column[]>(await request.get(`/api/v1/projects/${project.id}/columns`));
  const columns = Array.isArray(columnResponse) ? columnResponse : columnResponse.data;
  const column = columns.find((item) => item.semantic_state === 'active');
  expect(column).toBeTruthy();
  const tasks: Task[] = [];
  for (const state of ['working', 'waiting', 'verifying', 'handoff'] as const) {
    let task = await post<Task>(request, `/api/v1/projects/${project.id}/tasks`, {
      title: `${state} status ${suffix}`, column_id: column!.id, priority: 'normal'
    });
    task = await post<Task>(request, `/api/v1/tasks/${task.id}/claim`, { lease_seconds: 3600 }, task);
    task = await post<Task>(request, `/api/v1/tasks/${task.id}/progress`, {
      operation_id: `agent-status/${suffix}/${state}`, state,
      phase: 'Synthetic status acceptance', summary: `${state} milestone`,
      next_action: 'Review the synthetic result', checkpoint_refs: ['acceptance'],
      checkpoint_completed: 0, checkpoint_total: 1
    }, task);
    tasks.push(task);
  }
  const missing = await post<Task>(request, `/api/v1/projects/${project.id}/tasks`, {
    title: `Missing status ${suffix}`, column_id: column!.id, priority: 'normal'
  });
  const [working, waiting, verifying, handoff] = tasks;
  const staleIDs = new Set<string>();
  const expiredIDs = new Set([handoff.id]);
  const readCounts = new Map<string, number>();
  let taskTimelineReads = 0;
  const eventReadScopes: string[] = [];
  let timelineMode: 'normal' | 'mismatch' | 'error' | 'hold' = 'normal';
  let releaseHeld: (() => void) | undefined;
  let heldStarted: (() => void) | undefined;
  let heldFinished: (() => void) | undefined;
  const staleAt = new Date(Date.now() - 16 * 60 * 1000).toISOString();
  const expiredAt = new Date(Date.now() - 60 * 1000).toISOString();
  function adjust(value: unknown): void {
    if (Array.isArray(value)) { value.forEach(adjust); return; }
    if (!value || typeof value !== 'object') return;
    const record = value as Record<string, unknown>;
    if (typeof record.id === 'string') {
      if (staleIDs.has(record.id) && record.agent_work) {
        Object.assign(record.agent_work, { updated_at: staleAt, stale: true, action_needed: true });
      }
      if (expiredIDs.has(record.id)) record.claim_expires_at = expiredAt;
    }
    if (record.data) adjust(record.data);
  }
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    if (route.request().method() !== 'GET') { await route.continue(); return; }
    if (url.pathname === '/api/v1/events') {
      // Browser projects share the disposable server. Read its real events
      // for this fixture's project so the operation race does not depend on
      // draining every earlier test's global 50-event pages at 15s intervals.
      const scopedURL = new URL(url);
      scopedURL.searchParams.set('project', project.id);
      const response = await route.fetch({ url: scopedURL.toString() });
      const payload = await response.json() as { data: Array<{ project_id?: string }> };
      expect(payload.data.every((event) => event.project_id === project.id),
        'operation-switch evidence must come from the real project-scoped feed').toBe(true);
      eventReadScopes.push(scopedURL.searchParams.get('project')!);
      await route.fulfill({ response, json: payload });
      return;
    }
    const timelineTask = url.pathname.match(/^\/api\/v1\/tasks\/([^/]+)\/timeline$/)?.[1];
    if (timelineTask) taskTimelineReads += 1;
    if (timelineTask && url.searchParams.get('kind') === 'agent_progress') {
      readCounts.set(timelineTask, (readCounts.get(timelineTask) || 0) + 1);
      expect(url.searchParams.get('limit')).toBe('50');
      if (timelineMode === 'error' && timelineTask === waiting.id) {
        await route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'Synthetic timeline failure' } } });
        return;
      }
      const upstreamURL = new URL(url);
      // Synthetic cursors exercise the UI bound; keep them away from the
      // real API's opaque-cursor validator when repeating fixture history.
      if (timelineMode === 'mismatch' && timelineTask === verifying.id) upstreamURL.searchParams.delete('before');
      const response = await route.fetch({ url: upstreamURL.toString() });
      const payload = await response.json() as Timeline;
      if (timelineMode === 'mismatch' && timelineTask === verifying.id) {
        for (const item of payload.data) if (item.progress) item.progress.operation_id = 'older/unrelated-operation';
        payload.next_cursor = `synthetic-page-${readCounts.get(timelineTask)}`;
      }
      if (timelineMode === 'hold' && timelineTask === working.id) {
        timelineMode = 'normal';
        heldStarted?.();
        await new Promise<void>((resolve) => { releaseHeld = resolve; });
        await route.fulfill({ response, json: payload });
        heldFinished?.();
        return;
      }
      await route.fulfill({ response, json: payload });
      return;
    }
    if (url.pathname === '/api/v1/my-work' || /\/tasks(?:\/[^/]+)?$/.test(url.pathname)) {
      const response = await route.fetch();
      if (!response.ok()) { await route.fulfill({ response }); return; }
      const payload = await response.json();
      adjust(payload);
      await route.fulfill({ response, json: payload });
      return;
    }
    await route.continue();
  });

  const boardURL = `/p/${encodeURIComponent(project.slug)}`;
  const drawer = page.locator('.task-drawer');
  const panel = drawer.locator('.agent-work-panel');
  const checkIn = panel.locator('[data-agent-last-check-in] time');
  const progress = panel.locator('[data-agent-last-progress]');
  const card = (task: Task) => page.locator('.task-card').filter({ hasText: task.title });
  const open = async (task: Task) => {
    await card(task).locator('[data-task-trigger]').click();
    await expect(drawer.getByLabel('Task title')).toHaveValue(task.title);
  };
  const close = async () => {
    await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
    await expect(drawer).toBeHidden();
  };
  await page.goto(boardURL);
  for (const task of tasks) {
    const label = task.agent_work!.state.replace(/^./, (first) => first.toUpperCase());
    await card(task).getByRole('button', { name: `Show details for ${task.key}` }).click();
    await expect(card(task).locator('.agent-pulse-badge-label')).toHaveText(label);
    await expect(card(task).locator('.agent-pulse')).toHaveAccessibleName(`${label} Fresh check-in`);
  }
  expect(taskTimelineReads, 'board cards never fetch per-task history').toBe(0);

  const expiredClaimRead = page.waitForResponse((response) => response.request().method() === 'GET'
    && new URL(response.url()).pathname === `/api/v1/tasks/${handoff.id}`);
  await open(handoff);
  expect((await (await expiredClaimRead).json()).claim_expires_at,
    'the browser must receive the synthetic expired lease before expiry rendering is assessed').toBe(expiredAt);
  await expect(panel.getByRole('heading')).toContainText('Handoff');
  await expect(panel).toContainText('Fresh check-in');
  await expect(panel.locator('.agent-pulse-claim')).toContainText('Expired');
  await close();

  await open(working);
  await expect(progress.locator('time')).toHaveCount(1);
  const milestoneAt = await progress.locator('time').getAttribute('datetime');
  const firstCheckIn = await checkIn.getAttribute('datetime');
  const timelineBefore = await json<Timeline>(await request.get(`/api/v1/tasks/${working.id}/timeline?kind=agent_progress`));
  const heartbeat = await json<Task>(await request.post(`/api/v1/tasks/${working.id}/heartbeat`, {
    data: { operation_id: working.agent_work!.operation_id }, headers: { Origin: origin }
  }));
  expect(Date.parse(heartbeat.agent_work!.updated_at)).toBeGreaterThan(Date.parse(firstCheckIn!));
  expect(heartbeat.version).toBe(working.version);
  expect(heartbeat.claim_expires_at).toBe(working.claim_expires_at);
  expect(heartbeat.completed_at).toBe(working.completed_at);
  await page.reload();
  await expect(checkIn).toHaveAttribute('datetime', heartbeat.agent_work!.updated_at);
  await expect(progress.locator('time')).toHaveAttribute('datetime', milestoneAt!);
  const timelineAfter = await json<Timeline>(await request.get(`/api/v1/tasks/${working.id}/timeline?kind=agent_progress`));
  expect(timelineAfter).toEqual(timelineBefore);
  await testInfo.attach('agent-status-independent-clocks.json', {
    body: JSON.stringify({ state: heartbeat.agent_work!.state, milestoneAt, firstCheckIn,
      heartbeatCheckIn: heartbeat.agent_work!.updated_at, versionUnchanged: true, timelineUnchanged: true }, null, 2),
    contentType: 'application/json'
  });
  await close();

  tasks.forEach((task) => staleIDs.add(task.id));
  await page.reload();
  for (const task of tasks) {
    const label = task.agent_work!.state.replace(/^./, (first) => first.toUpperCase());
    await card(task).getByRole('button', { name: `Show details for ${task.key}` }).click();
    await expect(card(task).locator('.agent-pulse-badge-label')).toHaveText(label);
    await expect(card(task).locator('.agent-pulse')).toHaveAccessibleName(`${label} Stale check-in`);
    await open(task);
    await expect(panel.getByRole('heading')).toContainText(label);
    await expect(panel).toContainText('Stale check-in');
    await expect(panel).toContainText('Review the synthetic result');
    await expect(progress.locator('time')).toHaveCount(1);
    if (task.id === handoff.id) {
      await testInfo.attach('agent-status-desktop.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
    }
    await close();
  }
  await open(missing);
  await expect(panel.getByRole('heading')).toHaveText('No live pulse yet');
  await close();

  timelineMode = 'mismatch';
  const priorReads = readCounts.get(verifying.id) || 0;
  await open(verifying);
  await expect(progress).toHaveText('Unavailable');
  expect(readCounts.get(verifying.id)! - priorReads).toBe(3);
  await close();
  timelineMode = 'error';
  await open(waiting);
  await expect(progress).toHaveText('Unavailable');
  await close();

  // Hold an old operation's response while a real progress event switches
  // the mounted drawer to the next operation. Release the old result last.
  staleIDs.delete(working.id);
  timelineMode = 'hold';
  const started = new Promise<void>((resolve) => { heldStarted = resolve; });
  const finished = new Promise<void>((resolve) => { heldFinished = resolve; });
  await open(working);
  await started;
  const operationEventRead = page.waitForResponse(async (response) => {
    if (new URL(response.url()).pathname !== '/api/v1/events' || !response.ok()) return false;
    const payload = await response.json() as {
      data: Array<{ type: string; task_id?: string; project_id?: string; payload?: { state?: string } }>
    };
    return payload.data.some((event) => event.type === 'task.progressed'
      && event.task_id === working.id && event.project_id === project.id
      && event.payload?.state === 'handoff');
  });
  const newOperation = await post<Task>(request, `/api/v1/tasks/${working.id}/progress`, {
    operation_id: `agent-status/${suffix}/next-operation`, state: 'handoff',
    summary: 'New operation after delayed history', next_action: 'Review the new operation'
  }, heartbeat);
  const operationEvents = await (await operationEventRead).json() as {
    data: Array<{ cursor: number; type: string; task_id?: string; project_id?: string; payload?: { state?: string; version?: number } }>
  };
  const operationEvent = operationEvents.data.find((event) => event.type === 'task.progressed'
    && event.task_id === working.id && event.payload?.version === newOperation.version);
  expect(operationEvent?.project_id).toBe(project.id);
  expect(eventReadScopes.length).toBeGreaterThan(0);
  expect(eventReadScopes.every((scope) => scope === project.id)).toBe(true);
  await expect(panel.getByRole('heading')).toContainText('Handoff', { timeout: 30_000 });
  await expect(progress.locator('time')).toHaveAttribute('datetime', newOperation.agent_work!.updated_at);
  releaseHeld!();
  await finished;
  await expect(progress.locator('time')).toHaveAttribute('datetime', newOperation.agent_work!.updated_at);
  await testInfo.attach('agent-status-operation-switch.json', {
    body: JSON.stringify({ feedScope: 'fixture project', projectID: project.id,
      eventCursor: operationEvent?.cursor, taskVersion: newOperation.version,
      state: newOperation.agent_work!.state, operationID: newOperation.agent_work!.operation_id,
      staleResponseReleasedAfterCurrentProgress: true }, null, 2),
    contentType: 'application/json'
  });
  await close();

  await page.goto('/my-work');
  await expect(page.getByRole('heading', { name: 'Live work', exact: true })).toBeVisible();
  const liveFilters = page.getByRole('group', { name: 'Filter live work' });
  await liveFilters.getByRole('button', { name: 'Stale', exact: true }).click();
  for (const task of [waiting, verifying, handoff]) {
    const row = page.locator('section[aria-labelledby="stale-heading"] .live-work-row').filter({ hasText: task.title });
    await expect(row.locator('.agent-pulse')).toContainText(task.agent_work!.state.replace(/^./, (first) => first.toUpperCase()));
    await expect(row.locator('.agent-pulse')).toContainText('Stale check-in');
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('section[aria-labelledby="stale-heading"] .live-work-row').filter({ hasText: handoff.title }).click();
  await expect(panel.getByRole('heading')).toContainText('Handoff');
  await expect(panel).toContainText('Stale check-in');
  await testInfo.attach('agent-status-phone.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
  const viewport = await page.evaluate(() => ({ width: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(viewport.document).toBeLessThanOrEqual(viewport.width);
  // Polling can still be inside route.fetch/json when the assertions finish.
  // Drain those handlers before Playwright disposes their response bodies.
  await page.unrouteAll({ behavior: 'wait' });
});
