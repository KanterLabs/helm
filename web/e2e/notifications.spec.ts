import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test';

type Project = {
  id: string;
  key: string;
  name: string;
  slug: string;
};

type Column = {
  id: string;
  semantic_state: string;
};

type Task = {
  id: string;
  key: string;
  title: string;
  project_id: string;
  column_id: string;
  version: number;
};

type Actor = {
  id: string;
  kind?: string;
};

type Agent = {
  id: string;
};

type TokenIssue = {
  token: string;
};

type Watch = {
  id: string;
  actor_id: string;
  project_id: string;
  task_id?: string | null;
};

type Notification = {
  id: string;
  title: string;
  task_id?: string | null;
  read_at?: string | null;
};

type NotificationPreferences = {
  actor_id: string;
  assignments: boolean;
  mentions: boolean;
  blockers: boolean;
  state_changes: boolean;
};

type Collection<T> = {
  data: T[];
  next_cursor?: string | null;
};

const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

async function getJSON<T>(request: APIRequestContext, path: string): Promise<T> {
  return json<T>(await request.get(path), `GET ${path}`);
}

async function postJSON<T>(
  request: APIRequestContext,
  path: string,
  body: unknown,
  idempotencyKey?: string,
  headers: Record<string, string> = {}
): Promise<T> {
  return json<T>(await request.post(path, {
    data: body,
    headers: {
      'Content-Type': 'application/json',
      Origin: e2eOrigin,
      ...(idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {}),
      ...headers
    }
  }), `POST ${path}`);
}

async function patchJSON<T>(
  request: APIRequestContext,
  path: string,
  body: unknown,
  idempotencyKey: string,
  headers: Record<string, string> = {}
): Promise<T> {
  return json<T>(await request.patch(path, {
    data: body,
    headers: {
      'Content-Type': 'application/json',
      Origin: e2eOrigin,
      'Idempotency-Key': idempotencyKey,
      ...headers
    }
  }), `PATCH ${path}`);
}

function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

test('keeps project and task watches, preferences, and notification navigation useful', async ({ page, request }) => {
  test.setTimeout(90_000);

  const status = await getJSON<{ mode?: string }>(request, '/api/v1/auth/status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const projectKey = `NFX${runID}`.slice(0, 16);
  const projectName = `Notification inbox ${runID}`;
  const taskTitle = `Assignment notification ${runID}`;
  let mutationNumber = 0;
  const mutationKey = () => `notification-e2e-${runID}-${++mutationNumber}`;

  const project = await postJSON<Project>(request, '/api/v1/projects', {
    key: projectKey,
    name: projectName,
    description: 'Real API fixture for notification inbox acceptance.'
  }, mutationKey());
  const actor = await getJSON<Actor>(request, '/api/v1/auth/me');
  const columns = collectionData(await getJSON<Collection<Column> | Column[]>(
    request,
    `/api/v1/projects/${project.id}/columns?limit=20`
  ));
  const column = columns.find((item) => item.semantic_state === 'ready') || columns[0];
  expect(column, 'the project should have a board column').toBeTruthy();

  const task = await postJSON<Task>(request, `/api/v1/projects/${project.id}/tasks`, {
    title: taskTitle,
    column_id: (column as Column).id,
    priority: 'normal'
  }, mutationKey());

  // Restore the assignment preference before generating the notification so a
  // rerun against a retained disposable database remains deterministic.
  const currentPreferences = await getJSON<NotificationPreferences>(request, '/api/v1/notification-preferences');
  if (!currentPreferences.assignments) {
    await patchJSON<NotificationPreferences>(request, '/api/v1/notification-preferences', { assignments: true }, mutationKey());
  }

  const agent = await postJSON<Agent>(request, '/api/v1/agents', {
    name: `Notification actor ${runID}`,
    description: 'Scoped actor used to generate a real assignment event.',
    project_ids: [project.id]
  }, mutationKey());
  // The plaintext is intentionally kept in memory only; it is never logged.
  const issued = await postJSON<TokenIssue>(request, `/api/v1/agents/${agent.id}/tokens`, {
    name: `Assignment event ${runID}`,
    scopes: ['tasks:write'],
    project_ids: [project.id]
  });

  let documentNavigations = 0;
  page.on('request', (browserRequest) => {
    if (browserRequest.isNavigationRequest() && browserRequest.frame() === page.mainFrame()) documentNavigations += 1;
  });
  await page.goto(`/p/${encodeURIComponent(project.slug)}`);
  const initialDocumentNavigations = documentNavigations;
  await expect(page.getByRole('heading', { name: projectName, exact: true })).toBeVisible();

  const taskCard = page.locator('.task-card').filter({ hasText: taskTitle });
  await expect(taskCard).toBeVisible();

  const inboxTrigger = page.locator('.notifications-trigger');
  await inboxTrigger.click();
  const inboxPanel = page.locator('#notifications-panel');
  await expect(inboxPanel).toBeVisible();

  // The global inbox owns project watching. The generic watch route must
  // receive exactly one target field; inspect the outgoing request as well as
  // the resulting UI state.
  const projectWatchResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === 'POST' && url.pathname === '/api/v1/watches';
  });
  await inboxPanel.getByRole('button', { name: 'Watch project', exact: true }).click();
  const projectWatchResponse = await projectWatchResponsePromise;
  const projectWatch = await json<Watch>(projectWatchResponse, 'POST project watch');
  expect(projectWatchResponse.request().postDataJSON()).toEqual({ project_id: project.id });
  await expect(inboxPanel.getByRole('button', { name: 'Unwatch project', exact: true })).toBeVisible();

  const projectDeletePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === 'DELETE' && url.pathname === `/api/v1/watches/${projectWatch.id}`;
  });
  await inboxPanel.getByRole('button', { name: 'Unwatch project', exact: true }).click();
  await projectDeletePromise;
  await expect(inboxPanel.getByRole('button', { name: 'Watch project', exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Close notifications', exact: true }).click();

  // The task drawer owns task watching so its modal boundary stays intact;
  // the topbar inbox is intentionally covered by the drawer backdrop.
  await taskCard.locator('[data-task-trigger]').click();
  const taskDrawer = page.locator('.task-drawer');
  await expect(taskDrawer).toBeVisible();

  const taskWatchResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === 'POST' && url.pathname === '/api/v1/watches';
  });
  await taskDrawer.getByRole('button', { name: /^Watch task(?: |$)/ }).click();
  const taskWatchResponse = await taskWatchResponsePromise;
  const taskWatch = await json<Watch>(taskWatchResponse, 'POST task watch');
  expect(taskWatchResponse.request().postDataJSON()).toEqual({ task_id: task.id });
  await expect(taskDrawer.getByRole('button', { name: /^Unwatch task(?: |$)/ })).toBeVisible();
  const taskWatches = collectionData(await getJSON<Collection<Watch> | Watch[]>(
    request,
    `/api/v1/watches?project=${encodeURIComponent(project.id)}&task=${encodeURIComponent(task.id)}`
  ));
  expect(taskWatches.filter((watch) => watch.project_id === project.id && !watch.task_id)).toHaveLength(0);
  expect(taskWatches.filter((watch) => watch.project_id === project.id && watch.task_id === task.id)).toHaveLength(1);

  const taskDeletePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === 'DELETE' && url.pathname === `/api/v1/watches/${taskWatch.id}`;
  });
  await taskDrawer.getByRole('button', { name: /^Unwatch task(?: |$)/ }).click();
  await taskDeletePromise;
  await expect(taskDrawer.getByRole('button', { name: /^Watch task(?: |$)/ })).toBeVisible();

  await page.getByRole('button', { name: 'Close task details', exact: true }).click();
  await expect(page.locator('.task-drawer')).toBeHidden();

  // A different actor's bearer mutation generates the inbox item. Assignment
  // notifications exclude the event actor, so this recipient is the disabled
  // human actor currently driving the browser.
  const assignmentResponse = await request.patch(`/api/v1/tasks/${task.id}`, {
    data: { assignee: actor.id },
    headers: {
      Authorization: `Bearer ${issued.token}`,
      'Content-Type': 'application/json',
      Origin: e2eOrigin,
      'If-Match': `"v${task.version}"`,
      'Idempotency-Key': mutationKey()
    }
  });
  expect(assignmentResponse.ok(), `agent assignment returned HTTP ${assignmentResponse.status()}`).toBeTruthy();

  const generatedNotifications = collectionData(await getJSON<Collection<Notification> | Notification[]>(
    request,
    '/api/v1/notifications?limit=25'
  ));
  const assignmentNotification = generatedNotifications.find((notification) =>
    notification.task_id === task.id && notification.title === 'Task assigned to you'
  );
  expect(assignmentNotification, 'the agent assignment should create a notification for the browser actor').toBeTruthy();
  if (!assignmentNotification) throw new Error('the assignment notification fixture was not returned');

  await inboxTrigger.click();
  await expect(inboxPanel).toBeVisible();
  const notificationItem = inboxPanel.locator(`.notification-item:has([data-notification-open="${assignmentNotification.id}"])`);
  await expect(notificationItem).toBeVisible();
  await expect(notificationItem).toHaveClass(/unread/);
  const notificationState = notificationItem.locator('[data-notification-read-toggle]');
  await expect(notificationState).toHaveText('Read');

  await notificationState.click();
  await expect(notificationItem).not.toHaveClass(/unread/);
  await expect(notificationState).toHaveText('Unread');
  await notificationState.click();
  await expect(notificationItem).toHaveClass(/unread/);
  await expect(notificationState).toHaveText('Read');

  const preferencesTrigger = inboxPanel.getByRole('button', { name: /Notification preferences/ });
  await preferencesTrigger.click();
  const preferencesPanel = inboxPanel.locator('#notifications-preferences-panel');
  await expect(preferencesPanel).toBeVisible();
  const assignments = preferencesPanel.getByRole('checkbox', { name: 'Assignments', exact: true });
  await expect(assignments).toBeChecked();
  const preferencePatchPromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === 'PATCH' && url.pathname === '/api/v1/notification-preferences';
  });
  await assignments.uncheck();
  const preferencePatch = await preferencePatchPromise;
  expect(preferencePatch.request().postDataJSON()).toEqual({ assignments: false });
  await expect(assignments).not.toBeChecked();
  const persistedPreferences = await getJSON<NotificationPreferences>(request, '/api/v1/notification-preferences');
  expect(persistedPreferences.assignments).toBe(false);
  await preferencesTrigger.click();
  await expect(preferencesPanel).toBeHidden();

  const notificationOpen = notificationItem.locator('[data-notification-open]');
  await notificationOpen.click();
  await expect(page.locator('.task-drawer')).toBeVisible();
  await expect(page.locator('.task-drawer')).toHaveAttribute('aria-label', `${task.key}: ${taskTitle}`);
  await expect(page).toHaveURL(new RegExp(`/p/${escapeRegExp(project.slug)}/tasks/${escapeRegExp(task.key)}$`));
  expect(documentNavigations, 'opening a notification should use SPA navigation').toBe(initialDocumentNavigations);
  await expect(notificationItem).not.toHaveClass(/unread/);
});
