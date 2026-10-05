import { expect, test, type APIRequestContext, type APIResponse, type TestInfo } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type LunaRun = {
  id: string;
  project_key?: string;
  outcome: string;
};
type LunaRunCollection = { data: LunaRun[] };
type LunaRunDetailPayload = {
  content_available?: boolean;
  input_text?: string;
  output_text?: string;
};
type DetailEvidence = {
  content_available: boolean;
  input_bytes: number;
  output_bytes: number;
};

const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

function runSuffix(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
}

function mutationHeaders(key: string): Record<string, string> {
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

async function createProject(request: APIRequestContext, suffix: string, label: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `LH${label}${suffix}`.slice(0, 16),
      name: `Luna history ${label} ${suffix}`,
      description: 'Concurrent persisted detail regression fixture.'
    },
    headers: mutationHeaders(`luna-history-stability-project-${label}-${suffix}`)
  }), `create Luna history ${label} project`);
}

test('keeps concurrent persisted Luna details visible after delayed responses', async ({ page, request }, testInfo: TestInfo) => {
  test.setTimeout(120_000);
  const auth = await request.get('/api/v1/auth/status');
  expect(auth.ok()).toBeTruthy();
  expect((await auth.json()).mode).toBe('disabled');

  const suffix = runSuffix();
  const projectA = await createProject(request, suffix, 'A');
  const projectB = await createProject(request, suffix, 'B');
  const queryA = `luna stability synthetic input A ${suffix}`;
  const queryB = `luna stability synthetic input B ${suffix}`;

  for (const [project, query, label] of [[projectA, queryA, 'A'], [projectB, queryB, 'B']] as const) {
    await json(
      await request.post(`/api/v1/projects/${project.key}/task-draft`, {
        data: { query },
        headers: mutationHeaders(`luna-history-stability-draft-${label}-${suffix}`)
      }),
      `create real Luna ${label} draft`
    );
  }

  const persistedRuns = await json<LunaRunCollection>(
    await request.get('/api/v1/codex/runs?limit=50'),
    'list persisted Luna runs'
  );
  const runA = persistedRuns.data.find((run) => run.project_key === projectA.key);
  const runB = persistedRuns.data.find((run) => run.project_key === projectB.key);
  expect(runA?.outcome).toBe('succeeded');
  expect(runB?.outcome).toBe('succeeded');
  expect(runA?.id).toBeTruthy();
  expect(runB?.id).toBeTruthy();
  const runAId = runA?.id || '';
  const runBId = runB?.id || '';

  let releaseA!: () => void;
  let releaseB!: () => void;
  const gateA = new Promise<void>((resolve) => { releaseA = resolve; });
  const gateB = new Promise<void>((resolve) => { releaseB = resolve; });
  let finishA!: () => void;
  let finishB!: () => void;
  const detailFinishedA = new Promise<void>((resolve) => { finishA = resolve; });
  const detailFinishedB = new Promise<void>((resolve) => { finishB = resolve; });
  const intercepted = new Set<string>();
  const detailEvidence = new Map<string, DetailEvidence>();
  let releasedA = false;
  let releasedB = false;
  const detailRoute = '**/api/v1/codex/runs/*';

  await page.route(detailRoute, async (route) => {
    const url = new URL(route.request().url());
    const id = decodeURIComponent(url.pathname.split('/').pop() || '');
    if (id !== runAId && id !== runBId) {
      await route.continue();
      return;
    }
    intercepted.add(id);
    const gate = id === runAId ? gateA : gateB;
    const finish = id === runAId ? finishA : finishB;
    try {
      const response = await route.fetch();
      const payload = await response.json() as LunaRunDetailPayload;
      detailEvidence.set(id, {
        content_available: payload.content_available === true,
        input_bytes: typeof payload.input_text === 'string' ? Buffer.byteLength(payload.input_text, 'utf8') : 0,
        output_bytes: typeof payload.output_text === 'string' ? Buffer.byteLength(payload.output_text, 'utf8') : 0
      });
      await gate;
      await route.fulfill({ response, json: payload });
    } finally {
      finish();
    }
  });

  const releaseDetailA = () => {
    if (!releasedA) {
      releasedA = true;
      releaseA();
    }
  };
  const releaseDetailB = () => {
    if (!releasedB) {
      releasedB = true;
      releaseB();
    }
  };

  try {
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Your Codex subscription' })).toBeVisible();
    const history = page.locator('.luna-history');
    await expect(history.getByRole('heading', { name: 'Recent Luna work' })).toBeVisible();
    const itemA = history.locator('.luna-run').filter({ hasText: projectA.key });
    const itemB = history.locator('.luna-run').filter({ hasText: projectB.key });
    await expect(itemA).toContainText('Succeeded');
    await expect(itemB).toContainText('Succeeded');

    await itemA.getByRole('button').click();
    await itemB.getByRole('button').click();
    await expect.poll(() => intercepted.size).toBe(2);

    releaseDetailA();
    await detailFinishedA;
    await expect(itemA.locator('.luna-exchange-part').first()).toContainText(queryA);
    await expect(itemA.locator('.luna-exchange-part').last()).toContainText('Verify persisted Luna history');
    await expect(itemB).toContainText('Loading input and output');

    releaseDetailB();
    await detailFinishedB;
    for (const [item, query] of [[itemA, queryA], [itemB, queryB]] as const) {
      await expect(item.locator('.luna-exchange-part').first()).toContainText(query);
      await expect(item.locator('.luna-exchange-part').last()).toContainText('Verify persisted Luna history');
    }
    const evidenceA = detailEvidence.get(runAId);
    const evidenceB = detailEvidence.get(runBId);
    expect(evidenceA).toMatchObject({ content_available: true });
    expect(evidenceB).toMatchObject({ content_available: true });
    expect(evidenceA?.input_bytes).toBeGreaterThan(0);
    expect(evidenceA?.output_bytes).toBeGreaterThan(0);
    expect(evidenceB?.input_bytes).toBeGreaterThan(0);
    expect(evidenceB?.output_bytes).toBeGreaterThan(0);
    if (!evidenceA || !evidenceB) throw new Error('detail evidence was not captured');

    await testInfo.attach('luna-history-stability-response.json', {
      body: Buffer.from(JSON.stringify({
        scenario: 'concurrent persisted Luna detail responses',
        details: [
          { run: 'A', ...evidenceA },
          { run: 'B', ...evidenceB }
        ],
        first_response_released_before_second: true,
        both_details_visible_after_second_response: true
      }, null, 2)),
      contentType: 'application/json'
    });
    await testInfo.attach('luna-history-stability-settings.png', {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png'
    });
  } finally {
    releaseDetailA();
    releaseDetailB();
    if (intercepted.has(runAId)) await detailFinishedA;
    if (intercepted.has(runBId)) await detailFinishedB;
    await page.unroute(detailRoute);
  }
});
