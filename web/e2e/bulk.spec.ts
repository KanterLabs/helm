import { expect, test, type APIRequestContext, type APIResponse, type TestInfo } from '@playwright/test';

type Project = { id: string; key: string; slug: string };
type Column = { id: string; semantic_state: string };
type Task = { id: string; key: string; title: string; priority: string; version: number };

const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

function mutationHeaders(key = `bulk-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return { Origin: e2eOrigin, 'Content-Type': 'application/json', 'Idempotency-Key': key };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

async function createTask(request: APIRequestContext, project: Project, columnID: string, title: string): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: { title, column_id: columnID, priority: 'normal' },
    headers: mutationHeaders()
  }), `create ${title}`);
}

test('selects loaded tasks, preserves filtered selections, and reviews bulk results', async ({ page, request }) => {
  test.setTimeout(90_000);
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `BLK${runID}`.slice(0, 16), name: `Bulk E2E ${runID}` },
    headers: mutationHeaders()
  }), 'create bulk project');
  const columns = await json<Column[] | { data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`), 'list bulk columns');
  const columnList = Array.isArray(columns) ? columns : columns.data;
  const ready = columnList.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the bulk fixture should have a Ready column').toBeTruthy();
  const first = await createTask(request, project, ready!.id, `Bulk first ${runID}`);
  const second = await createTask(request, project, ready!.id, `Bulk second ${runID}`);

  await page.goto(`/p/${project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  const search = page.getByRole('textbox', { name: 'Search tasks' });
  const selectLoaded = page.getByRole('button', { name: 'Select all loaded filtered tasks', exact: true });
  const firstCard = board.locator('.task-card').filter({ hasText: first.title });
  const secondCard = board.locator('.task-card').filter({ hasText: second.title });
  await expect(selectLoaded).toBeVisible();
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toBeVisible();

  // A filtered selection remains selected when the filter changes.
  const filteredTasksResponse = page.waitForResponse(
    (response) => {
      const responseURL = new URL(response.url());
      return response.request().method() === 'GET'
        && responseURL.pathname === `/api/v1/projects/${project.id}/tasks`
        && responseURL.searchParams.get('column') === ready!.id
        && responseURL.searchParams.get('q') === first.title;
    },
    { timeout: 30_000 }
  );
  await search.fill(first.title);
  await filteredTasksResponse;
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toHaveCount(0);
  await selectLoaded.click();
  await expect(page.getByText('1 selected', { exact: true })).toBeVisible();

  const unfilteredTasksResponse = page.waitForResponse(
    (response) => {
      const responseURL = new URL(response.url());
      return response.request().method() === 'GET'
        && responseURL.pathname === `/api/v1/projects/${project.id}/tasks`
        && responseURL.searchParams.get('column') === ready!.id
        && !responseURL.searchParams.get('q');
    },
    { timeout: 30_000 }
  );
  await search.fill('');
  await unfilteredTasksResponse;
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toBeVisible();
  await expect(page.getByText('1 selected', { exact: true })).toBeVisible();

  await secondCard.getByRole('checkbox', { name: `Select ${second.key}` }).check();
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();

  const dialog = page.getByRole('dialog', { name: 'Review bulk changes' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(first.key, { exact: true })).toBeVisible();
  await expect(dialog.getByText(second.key, { exact: true })).toBeVisible();
  await dialog.getByLabel('Bulk change', { exact: true }).selectOption('priority');
  await dialog.getByLabel('Bulk priority', { exact: true }).selectOption('urgent');
  await dialog.getByRole('button', { name: 'Apply changes to 2 tasks', exact: true }).click();
  await expect(dialog.getByRole('heading', { name: 'Result summary', exact: true })).toBeVisible();
  await expect(dialog.getByRole('status')).toContainText('2 applied · 0 conflicts · 0 skipped');
  await expect(dialog.locator('.bulk-result-status.applied')).toHaveCount(2);

  const firstAfter = await json<Task>(await request.get(`/api/v1/tasks/${first.id}`), 'read first bulk task');
  const secondAfter = await json<Task>(await request.get(`/api/v1/tasks/${second.id}`), 'read second bulk task');
  expect(firstAfter.priority).toBe('urgent');
  expect(secondAfter.priority).toBe('urgent');

  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Select all loaded filtered tasks', exact: true }).click();
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Clear selection', exact: true }).click();
  await expect(page.locator('.bulk-selection-count')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Review bulk changes', exact: true })).toHaveCount(0);
});

test('ignores a delayed bulk response after closing and reopening review', async ({ page, request }) => {
  test.setTimeout(90_000);
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `BLK${runID}`.slice(0, 16), name: `Bulk stale E2E ${runID}` },
    headers: mutationHeaders()
  }), 'create delayed bulk project');
  const columns = await json<Column[] | { data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`), 'list delayed bulk columns');
  const columnList = Array.isArray(columns) ? columns : columns.data;
  const ready = columnList.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the delayed bulk fixture should have a Ready column').toBeTruthy();
  const first = await createTask(request, project, ready!.id, `Delayed first ${runID}`);
  const second = await createTask(request, project, ready!.id, `Delayed second ${runID}`);

  await page.goto(`/p/${project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  const firstCard = board.locator('.task-card').filter({ hasText: first.title });
  const secondCard = board.locator('.task-card').filter({ hasText: second.title });
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toBeVisible();
  await firstCard.getByRole('checkbox', { name: `Select ${first.key}` }).check();
  await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();
  const firstDialog = page.getByRole('dialog', { name: 'Review bulk changes' });
  await expect(firstDialog).toBeVisible();
  await firstDialog.getByLabel('Bulk priority', { exact: true }).selectOption('urgent');

  let bulkReleased = false;
  let releaseBulk!: () => void;
  const bulkHeld = new Promise<void>((resolve) => { releaseBulk = resolve; });
  let finishBulk!: () => void;
  const bulkFinished = new Promise<void>((resolve) => { finishBulk = resolve; });
  const bulkRoute = `**/api/v1/projects/${project.id}/tasks/bulk`;
  await page.route(bulkRoute, async (route) => {
    await bulkHeld;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        mode: 'partial',
        status: 'partial',
        requested: 1,
        applied: 1,
        skipped: 0,
        conflicts: 0,
        results: [{ reference: first.key, task_id: first.id, status: 'applied', version: first.version + 1 }]
      })
    });
    finishBulk();
  });

  try {
    const bulkRequest = page.waitForRequest(
      (browserRequest) => browserRequest.method() === 'POST' && browserRequest.url().endsWith(`/api/v1/projects/${project.id}/tasks/bulk`),
      { timeout: 30_000 }
    );
    await firstDialog.getByRole('button', { name: 'Apply changes to 1 tasks', exact: true }).click();
    await bulkRequest;

    // Closing invalidates the in-flight request. The new review deliberately
    // contains a different task, so an old response would be obvious in its
    // result summary or by changing the current selection.
    await firstDialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(firstDialog).toHaveCount(0);
    await page.getByRole('button', { name: 'Clear selection', exact: true }).click();
    await secondCard.getByRole('checkbox', { name: `Select ${second.key}` }).check();
    await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();
    const secondDialog = page.getByRole('dialog', { name: 'Review bulk changes' });
    await expect(secondDialog).toBeVisible();
    await expect(secondDialog.getByText(second.key, { exact: true })).toBeVisible();
    await expect(secondDialog.getByText(first.key, { exact: true })).toHaveCount(0);

    bulkReleased = true;
    releaseBulk();
    await bulkFinished;
    await expect(secondDialog.getByRole('heading', { name: 'Result summary', exact: true })).toHaveCount(0);
    await expect(secondDialog.getByRole('button', { name: 'Apply changes to 1 tasks', exact: true })).toBeVisible();
  } finally {
    if (!bulkReleased) releaseBulk();
    await page.unroute(bulkRoute);
  }
});

test('reviews every selected task while a filtered board page is reloading', async ({ page, request }, testInfo: TestInfo) => {
  test.setTimeout(90_000);
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    data: { key: `BLK${runID}`.slice(0, 16), name: `Bulk filtered reload E2E ${runID}` },
    headers: mutationHeaders()
  }), 'create filtered reload bulk project');
  const columns = await json<Column[] | { data: Column[] }>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`), 'list filtered reload bulk columns');
  const columnList = Array.isArray(columns) ? columns : columns.data;
  const ready = columnList.find((column) => column.semantic_state === 'ready');
  expect(ready, 'the filtered reload bulk fixture should have a Ready column').toBeTruthy();
  const first = await createTask(request, project, ready!.id, `Filtered first ${runID}`);
  const second = await createTask(request, project, ready!.id, `Filtered second ${runID}`);

  await page.goto(`/p/${project.slug}`);
  const board = page.locator('section.board');
  await expect(board).toBeVisible();
  const firstCard = board.locator('.task-card').filter({ hasText: first.title });
  const secondCard = board.locator('.task-card').filter({ hasText: second.title });
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toBeVisible();

  const search = page.getByRole('textbox', { name: 'Search tasks' });
  const selectLoaded = page.getByRole('button', { name: 'Select all loaded filtered tasks', exact: true });
  await expect(selectLoaded).toBeVisible();

  let releaseFilteredMetadataGate!: () => void;
  const filteredMetadataGate = new Promise<void>((resolve) => { releaseFilteredMetadataGate = resolve; });
  let filteredMetadataReleased = false;
  let filteredMetadataExpected = false;
  let filteredMetadataIntercepted = false;
  let filteredMetadataRequestSeen!: () => void;
  const filteredMetadataRequest = new Promise<void>((resolve) => { filteredMetadataRequestSeen = resolve; });
  let finishFilteredMetadata!: () => void;
  const filteredMetadataFinished = new Promise<void>((resolve) => { finishFilteredMetadata = resolve; });
  let filteredMetadataStatus = 0;
  const filteredMetadataRoute = `**/api/v1/projects/${project.id}/columns?**`;
  await page.route(filteredMetadataRoute, async (route) => {
    if (route.request().method() !== 'GET' || !filteredMetadataExpected || filteredMetadataIntercepted) {
      await route.continue();
      return;
    }
    filteredMetadataIntercepted = true;
    try {
      const response = await route.fetch();
      filteredMetadataStatus = response.status();
      filteredMetadataRequestSeen();
      await filteredMetadataGate;
      await route.fulfill({ response });
    } finally {
      finishFilteredMetadata();
    }
  });

  const releaseFilteredMetadata = () => {
    if (filteredMetadataReleased) return;
    filteredMetadataReleased = true;
    releaseFilteredMetadataGate();
  };

  let selectDisabledDuringMetadata = false;
  try {
    // Hold the real columns response for the first filter transition. The
    // prior task page remains rendered until metadata is accepted, so this
    // checks that selection is unavailable throughout the transition.
    filteredMetadataExpected = true;
    await search.fill(first.title);
    await filteredMetadataRequest;
    await expect(firstCard).toBeVisible();
    await expect(secondCard).toHaveCount(0);
    await expect(selectLoaded).toBeDisabled();
    selectDisabledDuringMetadata = true;

    releaseFilteredMetadata();
    await filteredMetadataFinished;
    filteredMetadataExpected = false;
    expect(filteredMetadataStatus).toBe(200);
    await expect(firstCard).toBeVisible();
    await expect(secondCard).toHaveCount(0);
    await expect(selectLoaded).toBeEnabled();
    await selectLoaded.click();
  } finally {
    releaseFilteredMetadata();
    if (filteredMetadataIntercepted) await filteredMetadataFinished;
    await page.unroute(filteredMetadataRoute);
  }
  await expect(page.getByText('1 selected', { exact: true })).toBeVisible();

  const unfilteredTasksResponse = page.waitForResponse(
    (response) => {
      const responseURL = new URL(response.url());
      return response.request().method() === 'GET'
        && responseURL.pathname === `/api/v1/projects/${project.id}/tasks`
        && responseURL.searchParams.get('column') === ready!.id
        && !responseURL.searchParams.get('q');
    },
    { timeout: 30_000 }
  );
  await search.fill('');
  await unfilteredTasksResponse;
  await expect(firstCard).toBeVisible();
  await expect(secondCard).toBeVisible();
  await secondCard.getByRole('checkbox', { name: `Select ${second.key}` }).check();
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible();

  let releaseFilteredBoardGate!: () => void;
  const filteredBoardGate = new Promise<void>((resolve) => { releaseFilteredBoardGate = resolve; });
  let filteredBoardReleased = false;
  let filteredBoardRequestSeen!: () => void;
  const filteredBoardRequest = new Promise<void>((resolve) => { filteredBoardRequestSeen = resolve; });
  let filteredBoardResponseCount = 0;
  const filteredBoardResponses: Promise<void>[] = [];
  const capturedTaskResponses: Array<{
    query: string;
    status: number;
    taskKeys: string[];
    versions: number[];
  }> = [];
  const boardTaskRoute = `**/api/v1/projects/${project.id}/tasks**`;

  await page.route(boardTaskRoute, async (route) => {
    const requestURL = new URL(route.request().url());
    if (route.request().method() !== 'GET' || requestURL.searchParams.get('q') !== first.title) {
      await route.continue();
      return;
    }
    const heldResponse = (async () => {
      const response = await route.fetch();
      const payload = await response.json() as { data?: Array<Pick<Task, 'key' | 'version'>> };
      capturedTaskResponses.push({
        query: requestURL.searchParams.get('q') || '',
        status: response.status(),
        taskKeys: (payload.data || []).map((task) => task.key),
        versions: (payload.data || []).map((task) => task.version)
      });
      filteredBoardResponseCount += 1;
      if (filteredBoardResponseCount === 1) filteredBoardRequestSeen();
      await filteredBoardGate;
      await route.fulfill({ response, json: payload });
    })();
    filteredBoardResponses.push(heldResponse);
    await heldResponse;
  });

  const releaseFilteredBoard = () => {
    if (filteredBoardReleased) return;
    filteredBoardReleased = true;
    releaseFilteredBoardGate();
  };

  try {
    // Reapplying the first title filter starts a real board reload. Its task
    // responses are fetched from Helm, then held after the UI has discarded
    // the replacement page's predecessor.
    await search.fill(first.title);
    await filteredBoardRequest;
    await expect(board.locator('.task-card')).toHaveCount(0);
    await expect(page.getByText('2 selected', { exact: true })).toBeVisible();

    await page.getByRole('button', { name: 'Review bulk changes', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Review bulk changes' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(first.key, { exact: true })).toBeVisible();
    await expect(dialog.getByText(second.key, { exact: true })).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Apply changes to 2 tasks', exact: true })).toBeVisible();

    releaseFilteredBoard();
    await Promise.all(filteredBoardResponses);
    await expect(firstCard).toBeVisible();
    await expect(secondCard).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Apply changes to 2 tasks', exact: true })).toBeVisible();

    await testInfo.attach('bulk-selection-filtered-task-responses.json', {
      body: JSON.stringify({
        scenario: 'filtered board replacement while two tasks remain selected',
        metadata_gate: {
          response_status: filteredMetadataStatus,
          select_disabled_during_transition: selectDisabledDuringMetadata
        },
        responses: capturedTaskResponses,
        review_keys: [first.key, second.key]
      }, null, 2),
      contentType: 'application/json'
    });
    await testInfo.attach('bulk-selection-filtered-review.png', {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png'
    });

    await dialog.getByLabel('Bulk change', { exact: true }).selectOption('priority');
    await dialog.getByLabel('Bulk priority', { exact: true }).selectOption('urgent');
    await dialog.getByRole('button', { name: 'Apply changes to 2 tasks', exact: true }).click();
    await expect(dialog.getByRole('heading', { name: 'Result summary', exact: true })).toBeVisible();
    await expect(dialog.getByRole('status')).toContainText('2 applied · 0 conflicts · 0 skipped');
    await expect(dialog.locator('.bulk-result-status.applied')).toHaveCount(2);
  } finally {
    releaseFilteredBoard();
    await Promise.all(filteredBoardResponses);
    await page.unroute(boardTaskRoute);
  }
});
