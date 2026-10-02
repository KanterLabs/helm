import { expect, test, type APIRequestContext, type APIResponse, type Page, type TestInfo } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; semantic_state: string };
type Label = { id: string; name: string };
type Task = {
  id: string;
  key: string;
  title: string;
  description?: string | null;
  project_id: string;
  column_id: string;
  priority: string;
  version: number;
  labels?: Label[];
};
type Collection<T> = { data: T[]; next_cursor?: string | null };

const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

function mutationHeaders(key = `task-save-stability-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return {
    Origin: e2eOrigin,
    'Content-Type': 'application/json',
    'Idempotency-Key': key
  };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

function runSuffix(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
}

async function createProject(request: APIRequestContext, suffix: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `TS${suffix}`.slice(0, 16),
      name: `Task save stability ${suffix}`,
      description: 'Task save race regression fixture.'
    },
    headers: mutationHeaders()
  }), 'create task save stability project');
}

async function readyColumn(request: APIRequestContext, project: Project): Promise<Column> {
  const columns = collectionData(await json<Collection<Column> | Column[]>(
    await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
    'list task save stability columns'
  ));
  const ready = columns.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the task save stability project should have a Ready column').toBeTruthy();
  return ready as Column;
}

async function createTask(
  request: APIRequestContext,
  project: Project,
  column: Column,
  title: string,
  description: string
): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: {
      title,
      description,
      column_id: column.id,
      priority: 'normal'
    },
    headers: mutationHeaders()
  }), `create ${title}`);
}

function labelNames(task: Task): string[] {
  return (task.labels || []).map((label) => label.name).sort();
}

function sanitizedTask(task: Task): Record<string, unknown> {
  return {
    key: task.key,
    title: task.title,
    description: task.description || '',
    labels: labelNames(task),
    version: task.version
  };
}

async function attachEvidence(
  page: Page,
  testInfo: TestInfo,
  name: string,
  persisted: Record<string, unknown>
): Promise<void> {
  await testInfo.attach(`${name}-persisted.json`, {
    body: JSON.stringify(persisted, null, 2),
    contentType: 'application/json'
  });
  await testInfo.attach(`${name}.png`, {
    body: await page.screenshot({ fullPage: true }),
    contentType: 'image/png'
  });
}

async function openTaskDrawer(page: Page, task: Task, expectedTitle = task.title) {
  const board = page.locator('section.board');
  const card = board.locator('.task-card').filter({ hasText: task.key });
  await expect(card).toBeVisible();
  await card.locator('[data-task-trigger]').click();
  const drawer = page.locator('.task-drawer');
  await expect(drawer).toBeVisible();
  await expect(drawer.getByLabel('Task title')).toHaveValue(expectedTitle);
  return drawer;
}

test.describe('task save stability', () => {
  test('preserves edits made while a PATCH response is delayed', async ({ page, request }, testInfo) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = runSuffix();
    const project = await createProject(request, suffix);
    const column = await readyColumn(request, project);
    const task = await createTask(
      request,
      project,
      column,
      `Patch race original ${suffix}`,
      `Patch race original description ${suffix}`
    );
    const drawer = await (async () => {
      await page.goto(`/p/${project.slug}`);
      return openTaskDrawer(page, task);
    })();

    const submittedTitle = `Patch race submitted ${suffix}`;
    const submittedDescription = `Patch race submitted description ${suffix}`;
    const newerTitle = `Patch race newer ${suffix}`;
    const newerDescription = `Patch race newer description ${suffix}`;
    await drawer.getByLabel('Task title').fill(submittedTitle);
    await drawer.locator('textarea.description-input').fill(submittedDescription);

    let releasePatch!: () => void;
    const patchGate = new Promise<void>((resolve) => { releasePatch = resolve; });
    let patchRequestSeen!: (request: import('@playwright/test').Request) => void;
    const firstPatchRequest = new Promise<import('@playwright/test').Request>((resolve) => { patchRequestSeen = resolve; });
    let finishPatch!: () => void;
    const patchFinished = new Promise<void>((resolve) => { finishPatch = resolve; });
    let firstPatchIntercepted = false;
    let patchReleased = false;
    const patchRoute = `**/api/v1/tasks/${task.id}`;
    await page.route(patchRoute, async (route) => {
      if (route.request().method() !== 'PATCH' || firstPatchIntercepted) {
        await route.continue();
        return;
      }
      firstPatchIntercepted = true;
      patchRequestSeen(route.request());
      try {
        const response = await route.fetch();
        await patchGate;
        await route.fulfill({ response });
      } finally {
        finishPatch();
      }
    });

    try {
      await drawer.getByRole('button', { name: 'Save changes', exact: true }).click();
      const submittedRequest = await firstPatchRequest;
      expect(submittedRequest.postDataJSON()).toMatchObject({
        title: submittedTitle,
        description: submittedDescription
      });

      await drawer.getByLabel('Task title').fill(newerTitle);
      await drawer.locator('textarea.description-input').fill(newerDescription);
      await expect(drawer.locator('.drawer-save-bar')).toContainText('Saving changes…');

      patchReleased = true;
      releasePatch();
      await patchFinished;
      await expect(drawer.getByLabel('Task title')).toHaveValue(newerTitle);
      await expect(drawer.locator('textarea.description-input')).toHaveValue(newerDescription);
      await expect(drawer.locator('.drawer-save-bar')).toContainText('Unsaved changes');

      const persistedAfterFirstSave = await json<Task>(
        await request.get(`/api/v1/tasks/${task.id}`),
        'read task after delayed first save'
      );
      expect(persistedAfterFirstSave.title).toBe(submittedTitle);
      expect(persistedAfterFirstSave.description).toBe(submittedDescription);

      const secondSave = page.waitForResponse((response) =>
        response.request().method() === 'PATCH'
        && response.url().endsWith(`/api/v1/tasks/${task.id}`)
        && response.ok()
      );
      await drawer.getByRole('button', { name: 'Save changes', exact: true }).click();
      await secondSave;
      await expect(drawer.locator('.drawer-save-bar')).toContainText('All changes saved');

      const persisted = await json<Task>(
        await request.get(`/api/v1/tasks/${task.id}`),
        'read task after saving newer draft'
      );
      expect(persisted.title).toBe(newerTitle);
      expect(persisted.description).toBe(newerDescription);
      await attachEvidence(page, testInfo, 'task-save-patch-race', {
        task: sanitizedTask(persisted),
        firstSubmittedSnapshot: {
          title: submittedTitle,
          description: submittedDescription
        },
        secondSubmittedSnapshot: {
          title: newerTitle,
          description: newerDescription
        }
      });
    } finally {
      if (!patchReleased) releasePatch();
      if (firstPatchIntercepted) await patchFinished;
      await page.unroute(patchRoute);
    }
  });

  test('keeps a delayed label save attached to task A after switching to task B', async ({ page, request }, testInfo) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = runSuffix();
    const project = await createProject(request, suffix);
    const column = await readyColumn(request, project);
    const taskA = await createTask(
      request,
      project,
      column,
      `Label race original A ${suffix}`,
      `Label race original A description ${suffix}`
    );
    const taskB = await createTask(
      request,
      project,
      column,
      `Label race original B ${suffix}`,
      `Label race original B description ${suffix}`
    );
    await page.goto(`/p/${project.slug}`);
    const drawer = await openTaskDrawer(page, taskA);

    const submittedATitle = `Label race submitted A ${suffix}`;
    const submittedADescription = `Label race submitted A description ${suffix}`;
    const labelName = `label-race-${suffix.toLowerCase()}`;
    await drawer.getByLabel('Task title').fill(submittedATitle);
    await drawer.locator('textarea.description-input').fill(submittedADescription);
    await drawer.getByLabel(/Labels/).fill(labelName);

    let releaseLabel!: () => void;
    const labelGate = new Promise<void>((resolve) => { releaseLabel = resolve; });
    let labelRequestSeen!: (request: import('@playwright/test').Request) => void;
    const firstLabelRequest = new Promise<import('@playwright/test').Request>((resolve) => { labelRequestSeen = resolve; });
    let finishLabel!: () => void;
    const labelFinished = new Promise<void>((resolve) => { finishLabel = resolve; });
    let labelIntercepted = false;
    let labelReleased = false;
    const labelRoute = `**/api/v1/projects/${project.id}/labels`;
    await page.route(labelRoute, async (route) => {
      if (route.request().method() !== 'POST' || labelIntercepted) {
        await route.continue();
        return;
      }
      labelIntercepted = true;
      labelRequestSeen(route.request());
      try {
        const response = await route.fetch();
        await labelGate;
        await route.fulfill({ response });
      } finally {
        finishLabel();
      }
    });

    try {
      await drawer.getByRole('button', { name: 'Save changes', exact: true }).click();
      const labelRequest = await firstLabelRequest;
      expect(labelRequest.postDataJSON()).toMatchObject({ name: labelName });

      const discardDialog = page.waitForEvent('dialog').then(async (dialog) => {
        expect(dialog.type()).toBe('confirm');
        expect(dialog.message()).toContain('unsaved task details');
        await dialog.accept();
      });
      await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
      await discardDialog;
      await expect(page.locator('.task-drawer')).toHaveCount(0);

      const drawerB = await openTaskDrawer(page, taskB);
      const draftBTitle = `Label race draft B ${suffix}`;
      const draftBDescription = `Label race draft B description ${suffix}`;
      await drawerB.getByLabel('Task title').fill(draftBTitle);
      await drawerB.locator('textarea.description-input').fill(draftBDescription);
      await expect(drawerB.locator('.drawer-save-bar')).toContainText('Saving changes…');

      const resumedSave = page.waitForResponse((response) =>
        response.request().method() === 'PATCH'
        && (response.url().endsWith(`/api/v1/tasks/${taskA.id}`) || response.url().endsWith(`/api/v1/tasks/${taskB.id}`))
        && response.ok()
      );
      labelReleased = true;
      releaseLabel();
      await labelFinished;
      await resumedSave;

      const persistedA = await json<Task>(
        await request.get(`/api/v1/tasks/${taskA.id}`),
        'read task A after delayed label save'
      );
      const persistedB = await json<Task>(
        await request.get(`/api/v1/tasks/${taskB.id}`),
        'read task B after delayed label save'
      );
      expect(persistedA.title).toBe(submittedATitle);
      expect(persistedA.description).toBe(submittedADescription);
      expect(labelNames(persistedA)).toContain(labelName);
      expect(persistedB.title).toBe(taskB.title);
      expect(persistedB.description).toBe(taskB.description);
      expect(labelNames(persistedB)).toEqual(labelNames(taskB));
      await expect(drawerB.getByLabel('Task title')).toHaveValue(draftBTitle);
      await expect(drawerB.locator('textarea.description-input')).toHaveValue(draftBDescription);
      await expect(drawerB.locator('.drawer-save-bar')).toContainText('Unsaved changes');
      await expect(drawerB.locator('.drawer-alert')).toHaveCount(0);

      await attachEvidence(page, testInfo, 'task-save-label-switch-race', {
        taskA: sanitizedTask(persistedA),
        taskB: sanitizedTask(persistedB),
        taskASubmittedSnapshot: {
          title: submittedATitle,
          description: submittedADescription,
          labels: [labelName]
        },
        taskBDrawerDraft: {
          title: draftBTitle,
          description: draftBDescription
        }
      });
    } finally {
      if (!labelReleased) releaseLabel();
      if (labelIntercepted) await labelFinished;
      await page.unroute(labelRoute);
    }
  });

  test('preserves a reopened drawer draft when the old PATCH response arrives', async ({ page, request }, testInfo) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = runSuffix();
    const project = await createProject(request, suffix);
    const column = await readyColumn(request, project);
    const task = await createTask(
      request,
      project,
      column,
      `Reopen race original ${suffix}`,
      `Reopen race original description ${suffix}`
    );
    await page.goto(`/p/${project.slug}`);
    const drawer = await openTaskDrawer(page, task);

    const submittedTitle = `Reopen race submitted ${suffix}`;
    const submittedDescription = `Reopen race submitted description ${suffix}`;
    const reopenedTitle = `Reopen race newer ${suffix}`;
    const reopenedDescription = `Reopen race newer description ${suffix}`;
    await drawer.getByLabel('Task title').fill(submittedTitle);
    await drawer.locator('textarea.description-input').fill(submittedDescription);

    let releasePatch!: () => void;
    const patchGate = new Promise<void>((resolve) => { releasePatch = resolve; });
    let patchRequestSeen!: (request: import('@playwright/test').Request) => void;
    const firstPatchRequest = new Promise<import('@playwright/test').Request>((resolve) => { patchRequestSeen = resolve; });
    let finishPatch!: () => void;
    const patchFinished = new Promise<void>((resolve) => { finishPatch = resolve; });
    let patchIntercepted = false;
    let patchReleased = false;
    const patchRoute = `**/api/v1/tasks/${task.id}`;
    await page.route(patchRoute, async (route) => {
      if (route.request().method() !== 'PATCH' || patchIntercepted) {
        await route.continue();
        return;
      }
      patchIntercepted = true;
      patchRequestSeen(route.request());
      try {
        const response = await route.fetch();
        await patchGate;
        await route.fulfill({ response });
      } finally {
        finishPatch();
      }
    });

    try {
      await drawer.getByRole('button', { name: 'Save changes', exact: true }).click();
      const submittedRequest = await firstPatchRequest;
      expect(submittedRequest.postDataJSON()).toMatchObject({
        title: submittedTitle,
        description: submittedDescription
      });

      await expect.poll(async () => {
        const persisted = await json<Task>(
          await request.get(`/api/v1/tasks/${task.id}`),
          'poll reopened task save'
        );
        return { title: persisted.title, description: persisted.description || '' };
      }).toEqual({ title: submittedTitle, description: submittedDescription });

      const discardDialog = page.waitForEvent('dialog').then(async (dialog) => {
        expect(dialog.type()).toBe('confirm');
        expect(dialog.message()).toContain('unsaved task details');
        await dialog.accept();
      });
      await drawer.getByRole('button', { name: 'Close task details', exact: true }).click();
      await discardDialog;
      await expect(page.locator('.task-drawer')).toHaveCount(0);

      const reopenedDrawer = await openTaskDrawer(page, task, submittedTitle);
      await expect(reopenedDrawer.getByLabel('Task title')).toHaveValue(submittedTitle);
      await reopenedDrawer.getByLabel('Task title').fill(reopenedTitle);
      await reopenedDrawer.locator('textarea.description-input').fill(reopenedDescription);

      patchReleased = true;
      releasePatch();
      await patchFinished;
      await expect(reopenedDrawer.getByLabel('Task title')).toHaveValue(reopenedTitle);
      await expect(reopenedDrawer.locator('textarea.description-input')).toHaveValue(reopenedDescription);
      await expect(reopenedDrawer.locator('.drawer-save-bar')).toContainText('Unsaved changes');
      await expect(reopenedDrawer.locator('.drawer-alert')).toHaveCount(0);

      const persisted = await json<Task>(
        await request.get(`/api/v1/tasks/${task.id}`),
        'read task after reopened delayed save'
      );
      expect(persisted.title).toBe(submittedTitle);
      expect(persisted.description).toBe(submittedDescription);
      await attachEvidence(page, testInfo, 'task-save-reopen-race', {
        task: sanitizedTask(persisted),
        firstSubmittedSnapshot: {
          title: submittedTitle,
          description: submittedDescription
        },
        reopenedDrawerDraft: {
          title: reopenedTitle,
          description: reopenedDescription
        }
      });
    } finally {
      if (!patchReleased) releasePatch();
      if (patchIntercepted) await patchFinished;
      await page.unroute(patchRoute);
    }
  });
});
